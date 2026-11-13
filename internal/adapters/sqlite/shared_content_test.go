package sqlite

import (
	"context"
	"errors"
	"fmt"
	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"reflect"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func startCatalog(t *testing.T, e *application.Engine) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx) }()
	return func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
}
func waitCatalog(t *testing.T, s *Store, total int64) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		page, err := s.SharedContent(t.Context(), domain.SharedContentQuery{Type: "links", Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		if page.Indexing.TotalMessages == total && page.Indexing.State == "complete" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("local catalog did not catch up")
}
func catalogConversation(t *testing.T, s *Store, account domain.ID, address, title string) domain.Conversation {
	t.Helper()
	now := time.Now().UTC()
	c := domain.Conversation{ID: migrationID(), RevisionID: migrationID(), AccountID: account, PeerID: migrationID(), Address: address, Title: title, Kind: "direct", UpdatedAt: now}
	result, err := s.OpenConversation(t.Context(), application.ConversationMutation{Operation: application.Operation{ID: migrationID(), ResourceID: c.ID, CreatedAt: now, Kind: "open_conversation"}, Fingerprint: string(migrationID()), Conversation: c})
	if err != nil {
		t.Fatal(err)
	}
	return result.Conversation
}

func TestSharedCatalogAccountSetFiltersBeforePagination(t *testing.T) {
	s, e, first := outboxFixture(t)
	at := time.Now().UTC()
	replySeed(t, s, first, "https://example.test/first", at, nil, nil)
	accounts := []domain.ID{first.AccountID}
	for i, label := range []string{"included", "zebras"} {
		a, err := e.Register(t.Context(), migrationID(), application.RegisterInput{Protocol: "xmpp", Address: label + "@localhost", Label: label})
		if err != nil {
			t.Fatal(err)
		}
		accounts = append(accounts, a.Account.ID)
		c := catalogConversation(t, s, a.Account.ID, "peer@localhost", label)
		replySeed(t, s, c, "https://example.test/"+label, at.Add(time.Duration(i+1)*time.Second), nil, nil)
	}
	stop := startCatalog(t, e)
	defer stop()
	waitCatalog(t, s, 3)

	query := domain.SharedContentQuery{Type: "links", AccountIDs: []domain.ID{accounts[1], accounts[0]}, Limit: 1, IncludeFirstPageCounts: true}
	for i, want := range []domain.ID{accounts[1], accounts[0]} {
		page, err := e.SharedContent(t.Context(), query)
		if err != nil || len(page.Items) != 1 || page.Items[0].Message.AccountID != want ||
			page.Indexing.TotalMessages != 2 || (page.NextCursor != "") != (i == 0) {
			t.Fatalf("page %d: %+v, %v", i, page, err)
		}
		if page.FirstPageCounts == nil || *page.FirstPageCounts != (domain.SharedContentFirstPageCounts{Links: 1}) {
			t.Fatalf("page %d counts: %+v", i, page.FirstPageCounts)
		}
		query.Cursor = page.NextCursor
	}
	query.Search, query.Limit = "zebras", 10
	page, err := e.SharedContent(t.Context(), query)
	if err != nil || page.FirstPageCounts == nil || *page.FirstPageCounts != (domain.SharedContentFirstPageCounts{}) {
		t.Fatalf("excluded account appeared in counts: %+v, %v", page.FirstPageCounts, err)
	}
}

func TestSharedCatalogBackfillsAllHistoryAndKeepsMessageScope(t *testing.T) {
	s, e, c := outboxFixture(t)
	at := time.Date(2020, 1, 1, 1, 1, 1, 123456789, time.UTC)
	originals := []domain.Message{}
	for i := range 70 {
		originals = append(originals, replySeed(t, s, c, fmt.Sprintf("ÁRVORE Straße https://example.test/%02d.png https://example.test/report%02d.pdf https://example.test/article%02d", i, i, i), at, nil, nil))
	}
	// A second conversation and account share a URL but retain independent sources.
	other := catalogConversation(t, s, c.AccountID, "other@localhost", "Other chat")
	replySeed(t, s, other, "https://example.test/00.png", at.Add(time.Hour), nil, nil)
	a, err := e.Register(t.Context(), migrationID(), application.RegisterInput{Protocol: "xmpp", Address: "second@localhost", Label: "Second"})
	if err != nil {
		t.Fatal(err)
	}
	second := catalogConversation(t, s, a.Account.ID, "peer@localhost", "Second chat")
	replySeed(t, s, second, "https://example.test/00.png", at.Add(2*time.Hour), nil, nil)
	before, err := e.SharedContent(t.Context(), domain.SharedContentQuery{Type: "media", Limit: 10})
	if err != nil || before.Indexing.State != "pending" || len(before.Items) != 0 {
		t.Fatal(before, err)
	}
	stop := startCatalog(t, e)
	defer stop()
	waitCatalog(t, s, 72)
	seen := map[domain.ID]bool{}
	cursor := ""
	for {
		page, err := e.SharedContent(t.Context(), domain.SharedContentQuery{Type: "media", Limit: 7, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if seen[item.ID] {
				t.Fatal("duplicate cursor item")
			}
			seen[item.ID] = true
			if item.Preview == nil || item.Preview.MessageID != item.Message.ID || item.Preview.AccountID != item.Message.AccountID || item.Preview.DownloadState != "not_downloaded" {
				t.Fatal("preview source/download mismatch", item)
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != 72 {
		t.Fatal("catalog omitted unviewed history", len(seen))
	}
	filtered, err := e.SharedContent(t.Context(), domain.SharedContentQuery{Type: "media", AccountID: c.AccountID, ConversationID: c.ID, Search: "árvore STRASSE", Limit: 100})
	if err != nil || len(filtered.Items) != 70 {
		t.Fatal("Unicode metadata/scope", len(filtered.Items), err)
	}
	scoped := domain.SharedContentQuery{
		Type: "links", AccountID: c.AccountID, ConversationID: c.ID,
		MessageIDs: []domain.ID{originals[0].ID, originals[1].ID},
		Search:     "árvore", Limit: 10, IncludeFirstPageCounts: true,
	}
	page, err := e.SharedContent(t.Context(), scoped)
	if err != nil || len(page.Items) != 2 || page.Indexing.State != "complete" ||
		page.Indexing.TotalMessages != 2 || page.Indexing.IndexedMessages != 2 ||
		page.FirstPageCounts == nil || *page.FirstPageCounts != (domain.SharedContentFirstPageCounts{Media: 2, Links: 2, Documents: 2}) {
		t.Fatalf("message scope and search must apply to counts: %+v, %v", page, err)
	}
	docs, err := e.SharedContent(t.Context(), domain.SharedContentQuery{Type: "documents", ConversationID: c.ID, Limit: 100})
	if err != nil || len(docs.Items) != 70 {
		t.Fatal(len(docs.Items), err)
	}
	for _, item := range docs.Items {
		if item.Preview != nil {
			t.Fatal("document listing registered a preview")
		}
	}
	if _, err = e.SharedContent(t.Context(), domain.SharedContentQuery{Type: "media", AccountID: a.Account.ID, ConversationID: c.ID, Limit: 10}); !errors.Is(err, application.ErrInvalid) {
		t.Fatal("cross-account conversation accepted", err)
	}
	for _, m := range originals {
		current, err := e.Message(t.Context(), m.ID)
		if err != nil || current.OriginalText != m.OriginalText || current.RevisionID != m.RevisionID {
			t.Fatal("indexing edited source", err)
		}
	}
	pending, err := s.PendingMessages(t.Context(), time.Now().Add(time.Hour))
	if err != nil || len(pending) != 0 {
		t.Fatal("indexing queued sends", err)
	}
}
func TestReplyMetadataReindexesCatalogWithoutStaleQuoteLinks(t *testing.T) {
	s, e, c := outboxFixture(t)
	raw := "> https://example.test/quoted.png\nhttps://example.test/own.png"
	m := replySeed(t, s, c, raw, time.Now().UTC(), nil, nil)
	stop := startCatalog(t, e)
	defer stop()
	waitCatalog(t, s, 1)
	page, err := e.SharedContent(t.Context(), domain.SharedContentQuery{Type: "media", Limit: 10})
	if err != nil || len(page.Items) != 2 {
		t.Fatal(page, err)
	}
	m.ReplyReference = &domain.ReplyReference{Target: domain.MessageReference{ID: "missing-original"}, Fallback: []domain.TextRange{{Start: 0, End: len([]rune("> https://example.test/quoted.png\n"))}}}
	if err = s.ReceiveMessage(t.Context(), c, m, migrationID()); err != nil {
		t.Fatal(err)
	}
	waitCatalog(t, s, 1)
	page, err = e.SharedContent(t.Context(), domain.SharedContentQuery{Type: "media", Limit: 10})
	if err != nil || len(page.Items) != 1 || page.Items[0].URL != "https://example.test/own.png" || page.Items[0].Message.OriginalText != raw {
		t.Fatal("quoted fallback leaked into catalog", page, err)
	}
}
func TestSharedCatalogAtomicIndexAndPreviewOutsideFirstThree(t *testing.T) {
	s, e, c := outboxFixture(t)
	m := replySeed(t, s, c, "https://example.test/1 https://example.test/2 https://example.test/3 https://example.test/fourth.png", time.Now().UTC(), nil, nil)
	refs := application.SharedReferences(m.OriginalText)
	for i := range refs {
		refs[i].ID = migrationID()
		refs[i].RevisionID = migrationID()
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_catalog BEFORE INSERT ON shared_content_state BEGIN SELECT RAISE(ABORT,'injected failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if err := s.IndexSharedContent(t.Context(), []domain.SharedContentMutation{{Message: m, References: refs, ParserVersion: application.SharedContentVersion}}); err == nil {
		t.Fatal("injected failure accepted")
	}
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM shared_content").Scan(&n); err != nil || n != 0 {
		t.Fatal("partial catalog rows", n, err)
	}
	if _, err := s.db.Exec("DROP TRIGGER fail_catalog"); err != nil {
		t.Fatal(err)
	}
	if err := s.IndexSharedContent(t.Context(), []domain.SharedContentMutation{{Message: m, References: refs, ParserVersion: application.SharedContentVersion}}); err != nil {
		t.Fatal(err)
	}
	page, err := e.SharedContent(t.Context(), domain.SharedContentQuery{Type: "media", Limit: 10})
	if err != nil || len(page.Items) != 1 || page.Items[0].Preview == nil || page.Items[0].Preview.URL != refs[3].URL {
		t.Fatal("fourth source reference has no real preview", page, err)
	}
	first := page.Items[0].ID
	if err = s.IndexSharedContent(t.Context(), []domain.SharedContentMutation{{Message: m, References: refs, ParserVersion: application.SharedContentVersion}}); err != nil {
		t.Fatal(err)
	}
	again, _ := e.SharedContent(t.Context(), domain.SharedContentQuery{Type: "media", Limit: 10})
	if again.Items[0].ID != first {
		t.Fatal("reindex replaced stable catalog identity")
	}
}

func TestSharedCatalogMigrationRoundTripPreservesSourceAndOutbox(t *testing.T) {
	s, e, c := outboxFixture(t)
	sent, err := e.SendMessage(t.Context(), migrationID(), c.ID, "retained pending https://example.test/file.png")
	if err != nil {
		t.Fatal(err)
	}
	star, err := e.SetMessageStar(t.Context(), migrationID(), sent.Message.ID, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.CommitHistoryCheckpoint(t.Context(), c.AccountID, "alice@localhost", "keep-archive-position"); err != nil {
		t.Fatal(err)
	}
	driver, err := migratesqlite.WithInstance(s.db, &migratesqlite.Config{})
	if err != nil {
		t.Fatal(err)
	}
	source, err := iofs.New(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = source.Close() }()
	runner, err := migrate.NewWithInstance("iofs", source, "sqlite", driver)
	if err != nil {
		t.Fatal(err)
	}
	if err = runner.Migrate(7); err != nil {
		t.Fatal(err)
	}
	if err = runner.Migrate(8); err != nil {
		t.Fatal(err)
	}
	catalogCursor, err := e.HistoryCheckpoint(t.Context(), c.AccountID, "alice@localhost")
	if err != nil || catalogCursor != "keep-archive-position" {
		t.Fatal("catalog upgrade reset server archive", err)
	}
	if err = runner.Up(); err != nil {
		t.Fatal(err)
	}
	got, err := e.Message(t.Context(), sent.Message.ID)
	if err != nil || !reflect.DeepEqual(got, sent.Message) {
		t.Fatal("catalog migration modified source", err)
	}
	pending, err := s.PendingMessages(t.Context(), time.Now().Add(time.Hour))
	if err != nil || len(pending) != 1 || pending[0].ID != got.ID {
		t.Fatal("migration lost pending send", err)
	}
	current, err := e.MessageStar(t.Context(), got.ID)
	if err != nil || current.RevisionID != star.Star.RevisionID || !current.Starred {
		t.Fatal("migration changed source star", err)
	}
	cursor, err := e.HistoryCheckpoint(t.Context(), c.AccountID, "alice@localhost")
	if err != nil || cursor != "" {
		t.Fatal("correction upgrade did not request metadata replay", err)
	}
	var retainedScope int
	if err = s.db.QueryRow("SELECT COUNT(*) FROM archive_checkpoints WHERE account_id=? AND scope=?", c.AccountID, "alice@localhost").Scan(&retainedScope); err != nil || retainedScope != 1 {
		t.Fatal("correction upgrade removed archive scope", err)
	}
	page, err := e.SharedContent(t.Context(), domain.SharedContentQuery{Type: "media", Limit: 10})
	if err != nil || page.Indexing.State != "pending" || len(page.Items) != 0 {
		t.Fatal("derived index not rebuildable", err)
	}
}

func TestCachedImageEvidenceReclassifiesOpaqueReferenceAndSearchesTitle(t *testing.T) {
	s, e, c := outboxFixture(t)
	m := replySeed(t, s, c, "caption https://example.test/opaque", time.Now().UTC(), nil, nil)
	refs := application.SharedReferences(m.OriginalText)
	for i := range refs {
		refs[i].ID = migrationID()
		refs[i].RevisionID = migrationID()
	}
	if err := s.IndexSharedContent(t.Context(), []domain.SharedContentMutation{{Message: m, References: refs, ParserVersion: application.SharedContentVersion}}); err != nil {
		t.Fatal(err)
	}
	links, err := e.SharedContent(t.Context(), domain.SharedContentQuery{Type: "links", Limit: 10})
	if err != nil || len(links.Items) != 1 || links.Items[0].Preview == nil {
		t.Fatal(links, err)
	}
	p := *links.Items[0].Preview
	previous := p.RevisionID
	p.RevisionID = migrationID()
	p.Kind = "image"
	p.State = "ready"
	p.Title = "ÁLBUM São Paulo"
	if err = s.SavePreview(t.Context(), p, previous); err != nil {
		t.Fatal(err)
	}
	media, err := e.SharedContent(t.Context(), domain.SharedContentQuery{Type: "media", Search: "álbum SÃO", Limit: 10})
	if err != nil || len(media.Items) != 1 || media.Items[0].Kind != "image" || media.Items[0].Preview.ID != p.ID || media.Items[0].ID != links.Items[0].ID {
		t.Fatal("cached MIME/title evidence lost", media, err)
	}
	links, err = e.SharedContent(t.Context(), domain.SharedContentQuery{Type: "links", Limit: 10})
	if err != nil || len(links.Items) != 0 {
		t.Fatal("known image remained in links", err)
	}
}

func TestVideoCatalogUsesEffectiveKindBeforePagination(t *testing.T) {
	s, e, c := outboxFixture(t)
	at := time.Now().UTC()
	video := replySeed(t, s, c, "https://example.test/older.mp4", at, nil, nil)
	image := replySeed(t, s, c, "https://example.test/image.mp4", at.Add(time.Second), nil, nil)
	_, err := s.EnsurePreview(t.Context(), domain.MessagePreview{ID: migrationID(), RevisionID: migrationID(), AccountID: c.AccountID, ConversationID: c.ID, MessageID: image.ID, URL: image.OriginalText, Kind: "image", State: "ready", DownloadState: "not_downloaded", UpdatedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	stop := startCatalog(t, e)
	defer stop()
	waitCatalog(t, s, 2)
	page, err := e.SharedContent(t.Context(), domain.SharedContentQuery{Type: "video", Limit: 1})
	if err != nil || len(page.Items) != 1 || page.Items[0].Kind != "video" || page.Items[0].Message.ID != video.ID || page.NextCursor != "" {
		t.Fatal("effective image consumed video page", page, err)
	}
	images, err := s.SharedContent(t.Context(), domain.SharedContentQuery{Type: "media", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range images.Items {
		if item.Message.ID == image.ID {
			if _, err := s.SharedContentItem(t.Context(), item.ID); !errors.Is(err, application.ErrNotFound) {
				t.Fatal("effective image can be opened as video", err)
			}
		}
	}
}
