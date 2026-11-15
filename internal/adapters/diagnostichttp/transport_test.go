package diagnostichttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/araihu/oie/internal/diagnostics"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTimeoutReportsOnlySafeContext(t *testing.T) {
	var output bytes.Buffer
	logger := diagnostics.New("", &output, diagnostics.Desktop, false)
	transport := Transport(logger, transportFunc(func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("private-query %s: %w", r.URL, context.DeadlineExceeded)
	}))
	request := httptest.NewRequest(http.MethodPost, "https://private.example/api/v1/messages?query=private-query", strings.NewReader("private-message"))
	request.Header.Set("Authorization", "Bearer private-token")
	_, _ = transport.RoundTrip(request)
	record := output.String()
	if !strings.Contains(record, string(diagnostics.EngineTimeout)) || !strings.Contains(record, "request_ref") || strings.Contains(record, "private") {
		t.Fatal("timeout diagnostics lost the safe evidence or exposed request data")
	}
}

func TestFailedCallsCanBeCorrelatedAcrossProcesses(t *testing.T) {
	var engineOut, webOut bytes.Buffer
	engineLog := diagnostics.New("", &engineOut, diagnostics.Hosted, true)
	webLog := diagnostics.New("", &webOut, diagnostics.Desktop, true)
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		HTTPProblem(WithLogger(r, engineLog), http.StatusServiceUnavailable)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer engine.Close()
	client := &http.Client{Transport: Transport(webLog, http.DefaultTransport)}
	response, err := client.Get(engine.URL + "/api/v1/engine?private-query=private-value")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	ref := func(out *bytes.Buffer) string {
		t.Helper()
		var r struct {
			Reference string `json:"request_ref"`
		}
		if err := json.Unmarshal(out.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		return r.Reference
	}
	if engineRef, webRef := ref(&engineOut), ref(&webOut); engineRef == "" || engineRef != webRef {
		t.Fatal("failed request does not share a correlation reference")
	}
}
