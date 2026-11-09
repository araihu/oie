package web

import (
	"bytes"
	"context"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"strings"
	"testing"
)

func TestDocumentCardPreviewIsScopedAndEscapesMetadata(t *testing.T) {
	catalogs, err := loadCatalogs()
	if err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"en", "pt-BR"} {
		t.Run(locale, func(t *testing.T) {
			item := engineapi.SharedContentItem{Id: testMessage, Kind: "document", Url: "https://files.example.test/report.PDF?token=opaque", FileName: "<script>report.pdf", Message: engineapi.Message{Id: testRevision, AccountId: testAccount, ConversationId: testChat}}
			v := view{EngineID: testEngine, Locale: locale, Text: catalogs[locale]}
			var out bytes.Buffer
			if err := documentCard(v, item).Render(context.Background(), &out); err != nil {
				t.Fatal(err)
			}
			html := out.String()
			for _, want := range []string{`data-document-item="` + testMessage + `"`, `data-document-engine="` + testEngine + `"`, `data-document-account="` + testAccount + `"`, `data-document-conversation="` + testChat + `"`, `data-document-message="` + testRevision + `"`, `data-document-format="pdf"`, `data-document-source="/ui/shared-content/` + testMessage + `/content?engineId=` + testEngine + `"`, catalogs[locale].DocumentOpen, `data-document-preview`, `data-document-open`, `oie-document-name`, "&lt;script&gt;report.pdf"} {
				if !strings.Contains(html, want) {
					t.Errorf("missing safe document preview markup %q", want)
				}
			}
			for _, forbidden := range []string{"<script>", "<iframe", "<object", "<embed", "<canvas", "pdf.mjs", "pdf.worker.mjs", "<pre"} {
				if strings.Contains(html, forbidden) {
					t.Errorf("document content/library loaded into card: %s", forbidden)
				}
			}
		})
	}
}
func TestDocumentFormatAndSourceDoNotUseRemoteURLAsProxyInput(t *testing.T) {
	for _, tc := range []struct{ address, want string }{{"https://files.example.test/file.pdf", "pdf"}, {"https://files.example.test/file.XML?query=.pdf", "text"}, {"https://files.example.test/file.md", "markdown"}, {"https://files.example.test/file.txt", "text"}, {"https://files.example.test/file.log", "text"}, {"https://files.example.test/file.json", "text"}, {"https://files.example.test/file.csv", "text"}, {"https://files.example.test/file.docx", "unsupported"}} {
		if got := documentFormat(engineapi.SharedContentItem{Url: tc.address}); got != tc.want {
			t.Errorf("format %s: %s", tc.address, got)
		}
	}
	if got := documentSource(view{EngineID: testEngine}, engineapi.SharedContentItem{Id: "https://provider.invalid/file.pdf"}); got != "" {
		t.Fatal("raw URL accepted as source identity")
	}
}

func TestDocumentCaptionUsesOnlySourceContextBeyondTheReference(t *testing.T) {
	address := "https://files.example.test/report.pdf"
	for _, tc := range []struct{ source, want string }{
		{address, ""}, {"report.pdf\n" + address, ""},
		{"[report.pdf](" + address + ")", ""},
		{"Proposta para discussão\n" + address, "Proposta para discussão"},
		{"Veja: <" + address + ">", "Veja:"},
		{"A função foo() compara x<>y\n" + address, "A função foo() compara x<>y"},
		{"Um outro arquivo: " + address + "?v=2\n" + address, "Um outro arquivo: " + address + "?v=2"},
		{"<script>alert(1)</script>\n" + address, "<script>alert(1)</script>"},
	} {
		item := engineapi.SharedContentItem{Url: address, FileName: "report.pdf", Message: engineapi.Message{OriginalText: tc.source}}
		if got := documentCaption(item); got != tc.want {
			t.Errorf("source %q got %q want %q", tc.source, got, tc.want)
		}
		if item.Message.OriginalText != tc.source {
			t.Fatal("source changed")
		}
	}
	item := engineapi.SharedContentItem{Url: address, Message: engineapi.Message{OriginalText: strings.Repeat("😀", 601)}}
	if got := documentCaption(item); len([]rune(got)) != 601 || !strings.HasSuffix(got, "…") {
		t.Fatal("excerpt is not bounded by Unicode characters")
	}
}
