package cli_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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

func TestGeneratedCLIExercisesRealContract(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "oiectl")
	build := exec.Command("go", "-C", "../../cli", "build", "-o", binary, "./cmd/oiectl")
	build.Env = append(os.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("generated CLI build: %v\n%s", err, output)
	}
	ids := func() (domain.ID, error) { id, err := uuid.NewV7(); return domain.ID(id.String()), err }
	id, _ := ids()
	store, err := sqlite.Open(t.Context(), t.TempDir(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	engine, _ := application.New(store, "hosted", ids, time.Now)
	engine.SetProtocols(xmppadapter.New(t.TempDir(), nil))
	handler, err := httpapi.New(engine)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	key, _ := ids()
	body := `{"protocol":"xmpp","address":"alice@localhost","label":"Personal"}`
	run := func(input string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(t.Context(), binary, append([]string{"--server", server.URL, "--output", "json"}, args...)...)
		cmd.Stdin = bytes.NewBufferString(input)
		return cmd.CombinedOutput()
	}
	output, err := run(body, "accounts", "register-account", "--idempotency-key", string(key), "--body-stdin")
	if err != nil {
		t.Fatalf("CLI register: %v %s", err, output)
	}
	var account engineapi.Account
	if err := json.Unmarshal(output, &account); err != nil {
		t.Fatalf("JSON: %v %s", err, output)
	}
	registration := append([]byte(nil), output...)
	output, err = run("", "accounts", "get-account-capabilities", "--account-id", account.Id)
	var capabilities engineapi.Capabilities
	if err != nil || json.Unmarshal(output, &capabilities) != nil || capabilities.Discovery.State != "not_connected" || len(capabilities.Features) == 0 || len(capabilities.Discovery.Services) != 0 {
		t.Fatalf("CLI capability diagnostics: %v %s", err, output)
	}
	output = registration
	replay, err := run(body, "accounts", "register-account", "--idempotency-key", string(key), "--body-stdin")
	if err != nil || !bytes.Equal(output, replay) {
		t.Fatalf("CLI replay changed receipt: %v %s", err, replay)
	}
	output, err = run("", "operations", "get-operation", "--operation-id", string(key))
	if err != nil || !bytes.Contains(output, []byte(account.Id)) {
		t.Fatalf("CLI reconciliation: %v %s", err, output)
	}
	_, err = run(`{"protocol":"xmpp","address":"alice@localhost","label":"Changed"}`,
		"accounts", "register-account", "--idempotency-key", string(key), "--body-stdin")
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 4 {
		t.Fatalf("expected HTTP failure exit 4: %v", err)
	}
	_, seq, err := store.Snapshot(t.Context())
	if err != nil || seq != 1 {
		t.Fatal("generated CLI retries created extra state")
	}
	// Exercise the newly generated messaging commands against the real store.
	// The adapter is deliberately disconnected: this tests durable acceptance,
	// reconciliation and cancellation, not a simulated protocol delivery.
	openKey, _ := ids()
	output, err = run(`{"accountId":"`+account.Id+`","address":"bob@localhost"}`, "conversations", "open-conversation", "--idempotency-key", string(openKey), "--body-stdin")
	if err != nil {
		t.Fatalf("CLI conversation: %v %s", err, output)
	}
	var conversation engineapi.Conversation
	if err = json.Unmarshal(output, &conversation); err != nil {
		t.Fatal(err)
	}
	sendKey, _ := ids()
	text := `{"originalText":"Olá from the generated CLI"}`
	output, err = run(text, "messages", "send-message", "--conversation-id", conversation.Id, "--idempotency-key", string(sendKey), "--body-stdin")
	if err != nil {
		t.Fatalf("CLI accept: %v %s", err, output)
	}
	var message engineapi.Message
	if err = json.Unmarshal(output, &message); err != nil || message.SendState != "queued" {
		t.Fatalf("CLI queued evidence: %v %s", err, output)
	}
	replay, err = run(text, "messages", "send-message", "--conversation-id", conversation.Id, "--idempotency-key", string(sendKey), "--body-stdin")
	if err != nil || !bytes.Equal(output, replay) {
		t.Fatal("CLI message replay changed")
	}
	cancelKey, _ := ids()
	output, err = run(`{"expectedRevisionId":"`+message.RevisionId+`"}`, "messages", "cancel-message", "--message-id", message.Id, "--idempotency-key", string(cancelKey), "--body-stdin")
	if err != nil || !bytes.Contains(output, []byte(`"sendState": "cancelled"`)) {
		t.Fatalf("CLI cancel: %v %s", err, output)
	}
	output, err = run("", "operations", "get-operation", "--operation-id", string(sendKey))
	if err != nil || !bytes.Contains(output, []byte(message.Id)) {
		t.Fatal("CLI send receipt reconciliation failed")
	}
	output, err = run("", "messages", "list-messages", "--conversation-id", conversation.Id)
	if err != nil || !bytes.Contains(output, []byte(`"sendState": "cancelled"`)) {
		t.Fatalf("CLI retained timeline: %v %s", err, output)
	}
	output, err = run("", "messages", "get-message-context", "--conversation-id", conversation.Id, "--message-id", message.Id, "--limit", "1")
	var window engineapi.MessageContext
	if err != nil || json.Unmarshal(output, &window) != nil || window.TargetId != message.Id || len(window.Items) != 1 || window.HasOlder || window.HasNewer {
		t.Fatalf("CLI bounded source context: %v %s", err, output)
	}
	output, err = run("", "conversations", "get-conversation-details", "--conversation-id", conversation.Id)
	var details engineapi.ConversationDetails
	if err != nil || json.Unmarshal(output, &details) != nil || details.ConversationId != conversation.Id || details.AccountId != account.Id || details.State != "not_connected" {
		t.Fatalf("CLI scoped details: %v %s", err, output)
	}
	output, err = run("", "shared-content", "list-shared-content", "--type", "documents", "--account-id", account.Id)
	var library engineapi.SharedContentPage
	if err != nil || json.Unmarshal(output, &library) != nil || library.Indexing.TotalMessages != 1 || library.Indexing.State != "pending" {
		t.Fatalf("CLI shared-content query: %v %s", err, output)
	}
	// Star metadata is independent of the already-cancelled message and uses an
	// explicit null precondition on the first write through the generated CLI.
	starKey, _ := ids()
	starBody := `{"starred":true,"expectedRevisionId":null}`
	output, err = run(starBody, "messages", "set-message-star", "--message-id", message.Id, "--idempotency-key", string(starKey), "--body-stdin")
	if err != nil {
		t.Fatalf("CLI star acceptance: %v %s", err, output)
	}
	var state engineapi.MessageStar
	if err = json.Unmarshal(output, &state); err != nil || !state.Starred || state.RevisionId == nil {
		t.Fatalf("CLI star state: %v %s", err, output)
	}
	replay, err = run(starBody, "messages", "set-message-star", "--message-id", message.Id, "--idempotency-key", string(starKey), "--body-stdin")
	if err != nil || !bytes.Equal(output, replay) {
		t.Fatalf("CLI star replay changed receipt: %v %s", err, replay)
	}
	output, err = run("", "messages", "list-starred-messages", "--account-id", account.Id)
	if err != nil || !bytes.Contains(output, []byte(message.Id)) || !bytes.Contains(output, []byte(`"sendState": "cancelled"`)) {
		t.Fatalf("CLI starred source projection: %v %s", err, output)
	}
	output, err = run("", "operations", "get-operation", "--operation-id", string(starKey))
	if err != nil || !bytes.Contains(output, []byte(`"kind": "set_message_star"`)) {
		t.Fatalf("CLI star receipt: %v %s", err, output)
	}
}
