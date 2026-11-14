package search

import (
	"slices"
	"strings"
	"testing"
)

func TestSharedMetadataSearchPolicy(t *testing.T) {
	for _, tc := range []struct {
		text, query string
		want        bool
	}{
		{"anything", "", true},
		{"Relatório de ORÇAMENTO", "relatorio orcamento", true},
		{"A cafe\u0301 receipt Straße", "CAFÉ STRASSE", true},
		{"Relatório de orçamento", "orcamento relatorio", true},
		{"A video recording", "viedo", true},
		{"A document attached", "docun", true},
		{"faturamento", "faturamneto", true},
		{"faturamento", "fatxramenxo", true},
		{"faturamento", "fatxramynxo", false},
		{"A video recording", "vxdxo", false},
		{"A cat", "cta", false},
		{"invoice 2026", "2025", false},
		{"invoice 12345678", "12345679", false},
		{"A café 😀 receipt", "cafe 😀", true},
		{"A café receipt", "cafe 😀", false},
		{"A café 😀 receipt", "😢", false},
		{"A report.pdf", "pdf", true},
		{"abx", "abcd", false},
		{"anything", "...", false},
		{"anything", "\u0301", false},
		{"anything", strings.Repeat("a", 201), false},
	} {
		t.Run(tc.query+"/"+tc.text, func(t *testing.T) {
			if got := Match(tc.text, tc.query); got != tc.want {
				t.Errorf("Match(%q,%q)=%v, want %v", tc.text, tc.query, got, tc.want)
			}
		})
	}
}

func BenchmarkSharedMetadataSearch(b *testing.B) {
	text := "https://example.org/media/arquivo-2026.pdf Relatório de orçamento e faturamento, enviado por Ana na conversa Família, conta Pessoal"
	for b.Loop() {
		if !Match(text, "orcamento faturamneto") {
			b.Fatal("expected fuzzy match")
		}
	}
}

func TestHighlightSpans(t *testing.T) {
	for _, tc := range []struct {
		text, query string
		want        []string
	}{
		{"😀 Café e vi\u0301deo", "viedo", []string{"vi\u0301deo"}},
		{"documento", "docun", []string{"docum"}},
		{"Straße e ﬃ", "ss ffi", []string{"ß", "ﬃ"}},
		{"o arqiuvo e arquivo", "arquivo", []string{"arquivo"}},
		{"anxarquivo", "arquivo", []string{"arquivo"}},
		{"漢字左右", "漢字右左", []string{"漢字左右"}},
		{"1024", "1204", nil},
		{"a\u302e\u1715", "\u1715\u302e", nil},
	} {
		t.Run(tc.query, func(t *testing.T) {
			var got []string
			for _, span := range NewHighlight(tc.query).Spans(tc.text) {
				got = append(got, tc.text[span.Start:span.End])
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
