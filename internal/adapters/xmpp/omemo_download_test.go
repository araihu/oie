package xmpp

import (
	"bytes"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/araihu/oie/internal/adapters/xmpp/omemo"
	"github.com/araihu/oie/internal/application"
)

func TestOMEMOAttachmentAuthenticatesBeforeDownload(t *testing.T) {
	clear := []byte("private attachment bytes")
	sealed, file, err := omemo.SealMedia(clear)
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Clone(sealed)
	tampered[len(tampered)-1] ^= 1
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" || r.Header.Get("Referer") != "" {
			t.Error("attachment secret reached the HTTP upload host")
		}
		switch r.URL.Path {
		case "/tampered.txt":
			_, _ = w.Write(tampered)
		case "/redirect.txt":
			http.Redirect(w, r, "/correct.txt", http.StatusFound)
		default:
			_, _ = w.Write(sealed)
		}
	}))
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	m := New(t.TempDir(), roots)
	file.URL = server.URL + "/correct.txt"
	if _, err := m.OpenOMEMOAttachment(t.Context(), file); !errors.Is(err, application.MediaFailure{Code: "media_blocked"}) {
		t.Fatalf("expected default private network denial, got %v", err)
	}
	if err := m.SetPrivateOMEMOUploadOrigin(server.URL); err != nil {
		t.Fatal(err)
	}
	if !readOMEMOPayloadTest(t, omemo.MediaURI(file.URL, file)) {
		t.Fatal("missing attachment")
	}
	out, err := m.OpenOMEMOAttachment(t.Context(), file)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(out.Body)
	_ = out.Body.Close()
	if err != nil || !bytes.Equal(got, clear) {
		t.Fatalf("download should equal original: %q %v", got, err)
	}
	for _, suffix := range []string{"/tampered.txt", "/redirect.txt"} {
		file.URL = server.URL + suffix
		if out, err := m.OpenOMEMOAttachment(t.Context(), file); err == nil || out.Body != nil {
			t.Fatalf("released data from %q before successful authentication", suffix)
		}
	}
}

func readOMEMOPayloadTest(t *testing.T, uri string) bool {
	t.Helper()
	in := application.Incoming{}
	if !readOMEMOPayload(&in, "Caption\n"+uri+"\nuntrusted URL: "+uri) {
		return false
	}
	if in.OriginalText != "Caption" || len(in.EncryptedAttachments) != 1 || strings.Contains(in.EncryptedAttachments[0].URL, "#") {
		t.Fatalf("attachment URI or key leaked into visible text")
	}
	return true
}
