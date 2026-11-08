package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func TestRuntimeEngineVersionMatchesReviewedContract(t *testing.T) {
	server, _ := start(t, t.TempDir())
	status, body := request(t, server.URL, http.MethodGet, "/api/v1/engine", "", "")
	if status != http.StatusOK {
		t.Fatalf("engine status %d", status)
	}
	var metadata struct {
		Version string `json:"apiVersion"`
	}
	if err := json.Unmarshal(body, &metadata); err != nil {
		t.Fatal(err)
	}
	contract, err := engineapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Version != contract.Info.Version {
		t.Fatalf("runtime API version %q differs from contract %q", metadata.Version, contract.Info.Version)
	}
}
