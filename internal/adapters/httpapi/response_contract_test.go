package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
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
			if json.Unmarshal(data, &problem) != nil || problem.Status != res.StatusCode || problem.Type == "" || strings.Contains(string(data), "secret") {
				t.Fatalf("problem status/leak: %s", data)
			}
			validateResponseContract(t, req, res, data)
		})
	}
	_, seq, err := store.Snapshot(t.Context())
	if err != nil || seq != 0 {
		t.Fatal("boundary rejection mutated engine", seq, err)
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
