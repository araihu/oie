package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
)

var responseContract = sync.OnceValues(func() (routers.Router, error) {
	spec, err := engineapi.GetSpec()
	if err != nil {
		return nil, err
	}
	spec.Servers = nil
	if err := spec.Validate(context.Background()); err != nil {
		return nil, err
	}
	return legacy.NewRouter(spec)
})

// Validate the bytes actually emitted by HTTP, not a Go projection or example.
// Unmatched paths/methods have no operation schema; their problem contract is
// tested separately. SSE and binary streaming have dedicated framing tests.
func responseContractError(req *http.Request, res *http.Response, data []byte) error {
	router, err := responseContract()
	if err != nil {
		return err
	}
	route, params, err := router.FindRoute(req)
	if err != nil {
		return nil
	}
	if !strings.Contains(res.Header.Get("Content-Type"), "json") {
		return nil
	}
	input := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: route},
		Status:                 res.StatusCode, Header: res.Header,
		Options: &openapi3filter.Options{IncludeResponseStatus: true},
	}
	input.SetBodyBytes(data)
	return openapi3filter.ValidateResponse(context.Background(), input)
}

func validateResponseContract(t *testing.T, req *http.Request, res *http.Response, data []byte) {
	t.Helper()
	if err := responseContractError(req, res, data); err != nil {
		t.Fatalf("response violates %s %s (%d): %v\n%s", req.Method, req.URL.Path, res.StatusCode, err, data)
	}
}

func TestResponseValidatorRejectsRuntimeSchemaDrift(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://engine/api/v1/engine", nil)
	res := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}}
	good := []byte(`{"id":"019a5df0-0000-7000-8000-000000000003","role":"local","apiVersion":"0.27.0","eventCursor":"019a5df0-0000-7000-8000-000000000003:0"}`)
	validateResponseContract(t, req, res, good)
	for _, bad := range [][]byte{
		bytes.ReplaceAll(good, []byte(`"0.27.0"`), []byte(`"not-a-version"`)),
		bytes.ReplaceAll(good, []byte(`"role":"local",`), nil),
		bytes.ReplaceAll(good, []byte(`"role":"local"`), []byte(`"role":"bff"`)),
	} {
		if responseContractError(req, res, bad) == nil {
			t.Fatalf("validator accepted invalid runtime response: %s", bad)
		}
	}
}

func TestProblemDocumentationCoversContractCodes(t *testing.T) {
	spec, err := engineapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../../../docs/engine-api-problems.md")
	if err != nil {
		t.Fatal(err)
	}
	codes := spec.Components.Schemas["Problem"].Value.Properties["code"].Value.Enum
	for _, code := range codes {
		if !bytes.Contains(data, []byte(fmt.Sprintf("\n## %s\n", code))) {
			t.Errorf("problem documentation is missing the heading for %s", code)
		}
	}
}

func TestHTTPBoundaryProblemsAndAllow(t *testing.T) {
	server, store := start(t, t.TempDir())
	for _, tc := range []struct {
		method, path, content, body, allow string
		status                             int
	}{
		{"DELETE", "/api/v1/health", "", "", "GET", 405},
		{"TRACE", "/api/v1/accounts", "", "", "GET, POST", 405},
		{"PUT", "/api/v1/accounts/" + string(id()) + "/connection", "text/plain", `{"password":"secret"}`, "", 415},
		{"POST", "/api/v1/accounts", "application/json", `{"protocol":"xmpp","address":"alice@localhost","label":"` + strings.Repeat("x", 1<<20) + `"}`, "", 413},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			req, _ := http.NewRequest(tc.method, server.URL+tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", tc.content)
			req.Header.Set("Idempotency-Key", string(id()))
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(res.Body)
			_ = res.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != tc.status || res.Header.Get("Allow") != tc.allow || res.Header.Get("Cache-Control") != "no-store" {
				t.Fatalf("boundary status=%d allow=%q cache=%q body=%s", res.StatusCode, res.Header.Get("Allow"), res.Header.Get("Cache-Control"), data)
			}
			var problem engineapi.Problem
			if res.Header.Get("Content-Type") != "application/problem+json" ||
				json.Unmarshal(data, &problem) != nil || problem.Status != res.StatusCode ||
				problem.Type != "urn:oie:problem:invalid_request" || problem.Title != "Invalid request" ||
				problem.Code != "invalid_request" || strings.Contains(string(data), "secret") {
				t.Fatalf("problem status/leak: %s", data)
			}
			const documentation = "https://github.com/araihu/oie/blob/main/docs/engine-api-problems.md#invalid_request"
			if problem.Documentation != nil || res.Header.Get("Link") != "<"+documentation+`>; rel="help"` ||
				res.Header.Get("Vary") != "Prefer" || res.Header.Get("Preference-Applied") != "" {
				t.Fatalf("incorrect legacy problem response: %v %s", res.Header, data)
			}
			validateResponseContract(t, req, res, data)
		})
	}
	_, seq, err := store.Snapshot(t.Context())
	if err != nil || seq != 0 {
		t.Fatal("boundary rejection mutated engine", seq, err)
	}
}

func TestProblemDocumentationPreservesPublishedDefaultSchema(t *testing.T) {
	server, _ := start(t, t.TempDir())
	baseline, err := openapi3.NewLoader().LoadFromFile("../../../api/compatibility/engine-0.27.0.json")
	if err != nil {
		t.Fatal(err)
	}
	schema := baseline.Components.Schemas["Problem"].Value
	for _, tc := range []struct {
		prefer            string
		wantDocumentation bool
	}{
		{"", false},
		{`other="before, oie-problem-documentation, after"`, false},
		{"respond-async, oie-problem-documentation", true},
		{`oie-problem-documentation=""`, true},
		{`respond-async, OIE-PROBLEM-DOCUMENTATION=""; optional="a,b"`, true},
		{`oie-problem-documentation="nonempty", oie-problem-documentation=""`, false},
	} {
		req, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/accounts/not-a-uuid", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Prefer", tc.prefer)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		var problem map[string]any
		if err := json.Unmarshal(data, &problem); err != nil || res.StatusCode != 400 {
			t.Fatalf("Prefer %q: status=%d body=%s err=%v", tc.prefer, res.StatusCode, data, err)
		}
		_, hasDocumentation := problem["documentation"]
		if hasDocumentation != tc.wantDocumentation || (schema.VisitJSON(problem) != nil) != tc.wantDocumentation {
			t.Fatalf("Prefer %q: unexpected compatibility for %s", tc.prefer, data)
		}
		if tc.wantDocumentation && (res.Header.Get("Preference-Applied") != "oie-problem-documentation" ||
			res.Header.Get("Link") != fmt.Sprintf(`<%s>; rel="help"`, problem["documentation"])) {
			t.Fatalf("Prefer %q: headers=%v body=%s", tc.prefer, res.Header, data)
		}
	}
}

func TestCreationAndAcceptanceIdentifyPollableResources(t *testing.T) {
	_, _, server := newMessagingEngine(t, t.TempDir(), &protocolStub{})
	a := register(t, server.URL, string(id()), "alice@localhost")
	call := func(method, path, body string, status int) []byte {
		t.Helper()
		req, _ := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", string(id()))
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != status {
			t.Fatalf("acceptance %s: %d %s", path, res.StatusCode, data)
		}
		validateResponseContract(t, req, res, data)
		location := res.Header.Get("Location")
		if !strings.HasPrefix(location, "/api/v1/") {
			t.Fatalf("missing pollable Location for %s: %q", path, location)
		}
		poll, _ := http.NewRequest("GET", server.URL+location, nil)
		current, err := http.DefaultClient.Do(poll)
		if err != nil {
			t.Fatal(err)
		}
		currentData, err := io.ReadAll(current.Body)
		_ = current.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if current.StatusCode != 200 {
			t.Fatalf("cannot poll Location: %d %s", current.StatusCode, currentData)
		}
		validateResponseContract(t, poll, current, currentData)
		return data
	}
	data := call("POST", "/api/v1/conversations", `{"accountId":"`+a.Id+`","address":"bob@localhost"}`, 201)
	var c engineapi.Conversation
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	call("PUT", "/api/v1/accounts/"+a.Id+"/connection", `{"password":"write-only"}`, 202)
	call("POST", "/api/v1/conversations/"+c.Id+"/messages", `{"originalText":"accepted is not delivered"}`, 202)
}
