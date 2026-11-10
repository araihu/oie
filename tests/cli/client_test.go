package cli_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/avatarfiles"
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
	engine.SetAvatarFiles(avatarfiles.New(t.TempDir()))
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
	output, err := run(body, "accounts", "register", "--idempotency-key", string(key), "--body-stdin")
	if err != nil {
		t.Fatalf("CLI register: %v %s", err, output)
	}
	var account engineapi.Account
	if err := json.Unmarshal(output, &account); err != nil {
		t.Fatalf("JSON: %v %s", err, output)
	}
	registration := append([]byte(nil), output...)
	output, err = run("", "accounts", "capabilities", "--account-id", account.Id)
	var capabilities engineapi.Capabilities
	if err != nil || json.Unmarshal(output, &capabilities) != nil || capabilities.Discovery.State != "not_connected" || len(capabilities.Features) == 0 || len(capabilities.Discovery.Services) != 0 {
		t.Fatalf("CLI capability diagnostics: %v %s", err, output)
	}
	if err = engine.ContactsReceived(t.Context(), domain.ID(account.Id), []application.ContactInput{{Address: "bob@localhost", DisplayName: "Generated Café", Subscription: "none"}}); err != nil {
		t.Fatal(err)
	}
	output, err = run("", "accounts", "contacts", "--q", "cafe", "--account-id", account.Id)
	var contacts engineapi.ContactPage
	if err != nil || json.Unmarshal(output, &contacts) != nil || len(contacts.Items) != 1 || contacts.Items[0].Address != "bob@localhost" || contacts.Items[0].AccountId != account.Id || len(contacts.Sources) != 1 || contacts.Sources[0].State != "known" {
		t.Fatalf("CLI saved contacts: %v %s", err, output)
	}
	output = registration
	replay, err := run(body, "accounts", "register", "--idempotency-key", string(key), "--body-stdin")
	if err != nil || !bytes.Equal(output, replay) {
		t.Fatalf("CLI replay changed receipt: %v %s", err, replay)
	}
	output, err = run("", "operations", "get", "--operation-id", string(key))
	if err != nil || !bytes.Contains(output, []byte(account.Id)) {
		t.Fatalf("CLI reconciliation: %v %s", err, output)
	}
	_, err = run(`{"protocol":"xmpp","address":"alice@localhost","label":"Changed"}`,
		"accounts", "register", "--idempotency-key", string(key), "--body-stdin")
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 4 {
		t.Fatalf("expected HTTP failure exit 4: %v", err)
	}
	_, seq, err := store.Snapshot(t.Context())
	if err != nil || seq != 1 {
		t.Fatal("generated CLI retries created extra state")
	}

	var avatarPNG bytes.Buffer
	avatarBounds := image.Rect(0, 0, 8, 8)
	if err := png.Encode(&avatarPNG, image.NewGray(avatarBounds)); err != nil {
		t.Fatal(err)
	}
	if err := engine.StoreAvatar(t.Context(), domain.ID(account.Id), "bob@localhost", "cli-fixture", avatarPNG.Bytes()); err != nil {
		t.Fatal(err)
	}
	output, err = run("", "accounts", "avatars", "list", "--account-id", account.Id)
	var avatars engineapi.AvatarPage
	if err != nil || json.Unmarshal(output, &avatars) != nil || len(avatars.Items) != 1 || avatars.Items[0].Address != "bob@localhost" {
		t.Fatalf("CLI avatar metadata: %v %s", err, output)
	}
	output, err = run("", "accounts", "avatars", "download", "--hash", avatars.Items[0].ImageHash, "--output", "raw")
	if err != nil {
		t.Fatalf("CLI avatar download: %v %s", err, output)
	}
	avatar, err := png.Decode(bytes.NewReader(output))
	if err != nil || avatar.Bounds() != avatarBounds {
		t.Fatalf("CLI avatar download did not return the expected PNG: %v", err)
	}

	// Exercise the newly generated messaging commands against the real store.
	// The adapter is deliberately disconnected: this tests durable acceptance,
	// reconciliation and cancellation, not a simulated protocol delivery.
	openKey, _ := ids()
	output, err = run(`{"accountId":"`+account.Id+`","address":"bob@localhost"}`, "conversations", "open", "--idempotency-key", string(openKey), "--body-stdin")
	if err != nil {
		t.Fatalf("CLI conversation: %v %s", err, output)
	}
	var conversation engineapi.Conversation
	if err = json.Unmarshal(output, &conversation); err != nil {
		t.Fatal(err)
	}
	sendKey, _ := ids()
	text := `{"originalText":"Olá from the generated CLI"}`
	output, err = run(text, "messages", "send", "--conversation-id", conversation.Id, "--idempotency-key", string(sendKey), "--body-stdin")
	if err != nil {
		t.Fatalf("CLI accept: %v %s", err, output)
	}
	var message engineapi.Message
	if err = json.Unmarshal(output, &message); err != nil || message.SendState != "queued" {
		t.Fatalf("CLI queued evidence: %v %s", err, output)
	}
	replay, err = run(text, "messages", "send", "--conversation-id", conversation.Id, "--idempotency-key", string(sendKey), "--body-stdin")
	if err != nil || !bytes.Equal(output, replay) {
		t.Fatal("CLI message replay changed")
	}
	cancelKey, _ := ids()
	output, err = run(`{"expectedRevisionId":"`+message.RevisionId+`"}`, "messages", "outbox", "cancel", "--message-id", message.Id, "--idempotency-key", string(cancelKey), "--body-stdin")
	if err != nil || !bytes.Contains(output, []byte(`"sendState": "cancelled"`)) {
		t.Fatalf("CLI cancel: %v %s", err, output)
	}
	output, err = run("", "operations", "get", "--operation-id", string(sendKey))
	if err != nil || !bytes.Contains(output, []byte(message.Id)) {
		t.Fatal("CLI send receipt reconciliation failed")
	}
	output, err = run("", "messages", "list", "--conversation-id", conversation.Id)
	if err != nil || !bytes.Contains(output, []byte(`"sendState": "cancelled"`)) {
		t.Fatalf("CLI retained timeline: %v %s", err, output)
	}
	output, err = run("", "messages", "search", "--q", "olá generated", "--account-id", account.Id, "--conversation-id", conversation.Id, "--include-edit-history", "--limit", "1")
	var search engineapi.MessageSearchPage
	if err != nil || json.Unmarshal(output, &search) != nil || len(search.Items) != 1 || search.Items[0].Message.Id != message.Id || search.Items[0].Historical || search.Items[0].Message.SendState != "cancelled" || !search.IncludeEditHistory {
		t.Fatalf("CLI retained content search: %v %s", err, output)
	}
	output, err = run("", "messages", "context", "--conversation-id", conversation.Id, "--message-id", message.Id, "--limit", "1")
	var window engineapi.MessageContext
	if err != nil || json.Unmarshal(output, &window) != nil || window.TargetId != message.Id || len(window.Items) != 1 || window.HasOlder || window.HasNewer {
		t.Fatalf("CLI bounded source context: %v %s", err, output)
	}
	output, err = run("", "conversations", "details", "--conversation-id", conversation.Id)
	var details engineapi.ConversationDetails
	if err != nil || json.Unmarshal(output, &details) != nil || details.ConversationId != conversation.Id || details.AccountId != account.Id || details.State != "not_connected" {
		t.Fatalf("CLI scoped details: %v %s", err, output)
	}
	output, err = run("", "shared", "content", "list", "--type", "documents", "--account-id", account.Id)
	var library engineapi.SharedContentPage
	if err != nil || json.Unmarshal(output, &library) != nil || library.Indexing.TotalMessages != 1 || library.Indexing.State != "pending" {
		t.Fatalf("CLI shared-content query: %v %s", err, output)
	}
	// Star metadata is independent of the already-cancelled message and uses an
	// explicit null precondition on the first write through the generated CLI.
	starKey, _ := ids()
	starBody := `{"starred":true,"expectedRevisionId":null}`
	output, err = run(starBody, "messages", "stars", "set", "--message-id", message.Id, "--idempotency-key", string(starKey), "--body-stdin")
	if err != nil {
		t.Fatalf("CLI star acceptance: %v %s", err, output)
	}
	var state engineapi.MessageStar
	if err = json.Unmarshal(output, &state); err != nil || !state.Starred || state.RevisionId == nil {
		t.Fatalf("CLI star state: %v %s", err, output)
	}
	replay, err = run(starBody, "messages", "stars", "set", "--message-id", message.Id, "--idempotency-key", string(starKey), "--body-stdin")
	if err != nil || !bytes.Equal(output, replay) {
		t.Fatalf("CLI star replay changed receipt: %v %s", err, replay)
	}
	output, err = run("", "messages", "stars", "list", "--account-id", account.Id)
	if err != nil || !bytes.Contains(output, []byte(message.Id)) || !bytes.Contains(output, []byte(`"sendState": "cancelled"`)) {
		t.Fatalf("CLI starred source projection: %v %s", err, output)
	}
	output, err = run("", "operations", "get", "--operation-id", string(starKey))
	if err != nil || !bytes.Contains(output, []byte(`"kind": "set_message_star"`)) {
		t.Fatalf("CLI star receipt: %v %s", err, output)
	}
	// Reply composition freezes the authored body and source UUID in acceptance.
	replyKey, _ := ids()
	replyBody := `{"originalText":"Resposta CLI 😀","replyToMessageId":"` + message.Id + `"}`
	output, err = run(replyBody, "messages", "send", "--conversation-id", conversation.Id, "--idempotency-key", string(replyKey), "--body-stdin")
	var reply engineapi.Message
	if err != nil || json.Unmarshal(output, &reply) != nil || reply.ReplyToMessageId == nil || *reply.ReplyToMessageId != message.Id || reply.Reply == nil || reply.Reply.BodyText != "Resposta CLI 😀" {
		t.Fatalf("CLI reply composition: %v %s", err, output)
	}
	replay, err = run(replyBody, "messages", "send", "--conversation-id", conversation.Id, "--idempotency-key", string(replyKey), "--body-stdin")
	if err != nil || !bytes.Equal(output, replay) {
		t.Fatalf("CLI reply changed frozen receipt: %v %s", err, replay)
	}
	// Room commands stay local while the native adapter is disconnected.
	if err = engine.Receive(t.Context(), application.Incoming{AccountID: domain.ID(account.Id), Address: "room@rooms.localhost", Kind: "room", ProviderID: "cli-room-source", OriginalText: "Saved room source", Historical: true, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	chats, err := engine.Conversations(t.Context(), "", application.ConversationQuery{AccountID: domain.ID(account.Id), Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var roomID string
	for _, chat := range chats {
		if chat.Kind == "room" {
			roomID = string(chat.ID)
		}
	}
	output, err = run("", "conversations", "rooms", "status", "--conversation-id", roomID)
	var session engineapi.RoomSession
	if err != nil || json.Unmarshal(output, &session) != nil || session.Active {
		t.Fatalf("CLI room read: %v %s", err, output)
	}
	joinKey, _ := ids()
	output, err = run(`{"expectedRevisionId":null}`, "conversations", "rooms", "join", "--conversation-id", roomID, "--idempotency-key", string(joinKey), "--body-stdin")
	if err != nil || json.Unmarshal(output, &session) != nil || !session.Active || session.IntentRevisionId == nil || session.State == "joined" {
		t.Fatalf("CLI explicit room intent: %v %s", err, output)
	}
	leaveKey, _ := ids()
	output, err = run(`{"expectedRevisionId":"`+*session.IntentRevisionId+`"}`, "conversations", "rooms", "leave", "--conversation-id", roomID, "--idempotency-key", string(leaveKey), "--body-stdin")
	if err != nil || json.Unmarshal(output, &session) != nil || session.Active {
		t.Fatalf("CLI explicit room leave: %v %s", err, output)
	}
	output, err = run(`{"expectedRevisionId":null}`, "conversations", "rooms", "join", "--conversation-id", roomID, "--idempotency-key", string(joinKey), "--body-stdin")
	if err != nil || json.Unmarshal(output, &session) != nil || session.Active {
		t.Fatalf("CLI old join replay reactivated room: %v %s", err, output)
	}
	output, err = run("", "operations", "get", "--operation-id", string(joinKey))
	if err != nil || !bytes.Contains(output, []byte(`"kind": "join_room"`)) {
		t.Fatalf("CLI room operation receipt: %v %s", err, output)
	}

}
