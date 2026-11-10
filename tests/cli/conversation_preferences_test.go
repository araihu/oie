package cli_test

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/adapters/sqlite"
	xmppadapter "github.com/araihu/oie/internal/adapters/xmpp"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"github.com/google/uuid"
)

func TestGeneratedCLIPreferencesPreserveOmittedAndFalse(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "oiectl")
	build := exec.CommandContext(t.Context(), "go", "-C", "../../cli", "build", "-o", binary, "./cmd/oiectl")
	build.Env = append(os.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v %s", err, output)
	}
	ids := func() (domain.ID, error) { id, err := uuid.NewV7(); return domain.ID(id.String()), err }
	nextID := func() domain.ID {
		id, err := ids()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	store, err := sqlite.Open(t.Context(), t.TempDir(), nextID())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	engine, err := application.New(store, "local", ids, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	engine.SetProtocols(xmppadapter.New(t.TempDir(), nil))
	registered, err := engine.Register(t.Context(), nextID(), application.RegisterInput{Protocol: "xmpp", Address: "alice@localhost", Label: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := engine.OpenConversation(t.Context(), nextID(), registered.Account.ID, "bob@localhost")
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	seeded, err := engine.UpdateConversationPreferences(t.Context(), nextID(), opened.Conversation.ID, application.ConversationPreferencesInput{AccountID: registered.Account.ID, ExpectedRevisionID: opened.Conversation.RevisionID, Favorite: &yes})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := httpapi.New(engine)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	run := func(key domain.ID, body string, extra ...string) []byte {
		t.Helper()
		args := []string{"--server", server.URL, "--output", "json", "conversations", "set-preferences", "--conversation-id", string(opened.Conversation.ID), "--idempotency-key", string(key), "--body-stdin"}
		args = append(args, extra...)
		command := exec.CommandContext(t.Context(), binary, args...)
		command.Stdin = bytes.NewBufferString(body)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("CLI preferences: %v %s", err, output)
		}
		return output
	}
	archiveBody := `{"accountId":"` + string(registered.Account.ID) + `","expectedRevisionId":"` + string(seeded.Conversation.RevisionID) + `","archived":true}`
	var archived engineapi.Conversation
	if err := json.Unmarshal(run(nextID(), archiveBody), &archived); err != nil || !archived.Archived || !archived.Favorite {
		t.Fatal("omitted favorite changed", archived, err)
	}
	unarchiveBody := `{"accountId":"` + string(registered.Account.ID) + `","expectedRevisionId":"` + archived.RevisionId + `","archived":false}`
	key := nextID()
	original := run(key, unarchiveBody)
	var restored engineapi.Conversation
	if err := json.Unmarshal(original, &restored); err != nil || restored.Archived || !restored.Favorite {
		t.Fatal("false was dropped or favorite reset", restored, err)
	}
	if replay := run(key, unarchiveBody); !bytes.Equal(replay, original) {
		t.Fatal("CLI replay changed original snapshot")
	}
	if header := strings.TrimSpace(string(run(key, unarchiveBody, "--response-header", "Idempotency-Replayed"))); header != "true" {
		t.Fatal("CLI replay header", header)
	}
}
