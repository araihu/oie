package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/adapters/web"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"github.com/blevesearch/bleve/v2"
)

func requireIndexTest(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func waitIndex(t *testing.T, s *Store, condition func(dbgen.MessageSearchIndex) bool) dbgen.MessageSearchIndex {
	t.Helper()
	return waitIndexUntil(t, s, 2*time.Minute, condition)
}

func waitIndexUntil(t *testing.T, s *Store, timeout time.Duration, condition func(dbgen.MessageSearchIndex) bool) dbgen.MessageSearchIndex {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	for ctx.Err() == nil {
		state, err := s.read.GetMessageSearchIndex(ctx)
		if err == nil && condition(state) {
			return state
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("message index did not reach required state")
	return dbgen.MessageSearchIndex{}
}

func waitIndexReady(t *testing.T, s *Store, previous string) dbgen.MessageSearchIndex {
	t.Helper()
	return waitIndexUntil(t, s, 2*time.Minute, func(state dbgen.MessageSearchIndex) bool {
		return indexIsReady(s, state, previous)
	})
}

func indexIsReady(s *Store, state dbgen.MessageSearchIndex, previous string) bool {
	if state.Ready == 0 || previous != "" && state.Generation == previous {
		return false
	}
	index := s.messageIndex
	if !index.gate.TryRLock() {
		return false
	}
	defer index.gate.RUnlock()
	return index.active != nil && index.active.state.Generation == state.Generation
}

func indexSearchOracle(t *testing.T, s *Store, search domain.MessageSearchQuery) domain.MessageSearchPage {
	t.Helper()
	page, err := s.searchMessages(t.Context(), search, beginRead,
		func(ctx context.Context, _ *sql.Tx, q *dbgen.Queries, p dbgen.SearchMessagesParams) ([]dbgen.SearchMessagesRow, error) {
			return q.SearchMessages(ctx, p)
		})
	requireIndexTest(t, err)
	return page
}

func checkIndexParity(t *testing.T, s *Store, request domain.MessageSearchQuery) {
	t.Helper()
	request.ScopeHash = strings.Repeat("a", 64)
	for {
		want := indexSearchOracle(t, s, request)
		got, err := s.SearchMessages(t.Context(), request)
		requireIndexTest(t, err)
		if jsonText(got) != jsonText(want) {
			t.Fatalf("query %q mismatched: want %d hits, got %d\nwant %.1000s\ngot %.1000s", request.Search, len(want.Items), len(got.Items), jsonText(want), jsonText(got))
		}
		if want.NextCursor == "" {
			break
		}
		if request.Cursor == want.NextCursor {
			t.Fatal("cursor did not progress")
		}
		request.Cursor = want.NextCursor
	}
}

func assertIndexedCandidate(t *testing.T, s *Store, term string, id domain.ID) {
	t.Helper()
	assertScopedIndexedCandidate(t, s, dbgen.SearchMessagesParams{Search: term, AccountIds: "[]"}, id)
}

func assertScopedIndexedCandidate(t *testing.T, s *Store, p dbgen.SearchMessagesParams, id domain.ID) {
	t.Helper()
	ctx := t.Context()
	query, err := messageIndexQuery(p)
	requireIndexTest(t, err)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tx, q, err := beginRead(ctx, s)
		requireIndexTest(t, err)
		ids, ok := s.messageIndex.candidates(ctx, q, p, query)
		requireIndexTest(t, tx.Rollback())
		if ok && slices.Contains(ids, string(id)) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("index did not include %s for %q", id, p.Search)
}

func TestExperimentalMessageIndexParity(t *testing.T) {
	old, e, c := outboxFixture(t)
	dir := old.dir
	at := time.Now().UTC().Add(-time.Hour)
	room := domain.Conversation{ID: migrationID(), RevisionID: migrationID(), AccountID: c.AccountID, PeerID: migrationID(), Kind: "room", Address: "search@conference.localhost", Title: "Search room", UpdatedAt: at}
	roomHit := replySeed(t, old, room, "primeiro docunabstrato", at, nil, nil)
	// Clear the small-room threshold while ensuring the query stays selective.
	_, err := old.db.ExecContext(t.Context(), `WITH RECURSIVE seq(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM seq WHERE n<?1)
	 INSERT INTO messages(id,revision_id,account_id,conversation_id,provider_id,is_own,state,payload,created_at_ms)
	 SELECT printf('019cd000-0000-7000-8000-%012x',n),m.revision_id,m.account_id,m.conversation_id,'',0,'received',
	 json_set(m.payload,'$.OriginalText','other content'),m.created_at_ms FROM seq CROSS JOIN messages m WHERE m.id=?2`, searchSmallRoom, roomHit.ID)
	requireIndexTest(t, err)
	replySeed(t, old, c, "O vÍdeo está pronto", at, nil, nil)
	first := replySeed(t, old, c, "documento e orçamento", at, nil, nil)
	replySeed(t, old, c, "anxarquivo 漢字左右", at, nil, nil)
	replySeed(t, old, c, strings.Repeat("x ", searchIndexMaxDocumentBytes/2)+" arquivo", at, nil, nil)
	foreign, err := e.Register(t.Context(), migrationID(), application.RegisterInput{Protocol: "xmpp", Address: "other@localhost", Label: "Other"})
	requireIndexTest(t, err)
	other := domain.Conversation{ID: migrationID(), RevisionID: migrationID(), AccountID: foreign.Account.ID, PeerID: migrationID(), Kind: "direct", Address: "other-peer@localhost", Title: "Other", UpdatedAt: at}
	replySeed(t, old, other, "arquivo documento", at, nil, nil)
	e.SetProtocols(&outboundProtocol{})
	receiveCorrection(t, e, c, "one", "arquivo anterior", "", "proof:bob", "bob", at.Add(time.Second))
	receiveCorrection(t, e, c, "two", "texto atualizado", "one", "proof:bob", "bob", at.Add(2*time.Second))
	requireIndexTest(t, old.Close())

	s, err := Open(t.Context(), dir, migrationID(), WithMessageSearchIndex())
	requireIndexTest(t, err)
	defer func() { _ = s.Close() }()
	waitIndexReady(t, s, "")
	assertIndexedCandidate(t, s, "docun", first.ID)
	assertScopedIndexedCandidate(t, s, dbgen.SearchMessagesParams{Search: "docunabstrato", AccountIds: "[]", AccountID: string(c.AccountID), ConversationID: string(room.ID)}, roomHit.ID)
	checkIndexParity(t, s, domain.MessageSearchQuery{Search: "docunabstrato", AccountID: c.AccountID, ConversationID: room.ID, Limit: 1})
	for _, term := range []string{"docun", "arqiuvo", "viedo", "orcamento", "arquivo 漢字右左", ".", strings.Repeat("Ａ", 60), "inexistente"} {
		for _, history := range []bool{false, true} {
			checkIndexParity(t, s, domain.MessageSearchQuery{Search: term, IncludeEditHistory: history, Limit: 1})
		}
	}
	checkIndexParity(t, s, domain.MessageSearchQuery{Search: "arquivo", AccountIDs: []domain.ID{c.AccountID, other.AccountID}, IncludeEditHistory: true, Limit: 1})
	checkIndexParity(t, s, domain.MessageSearchQuery{Search: "arquivo", AccountID: c.AccountID, ConversationID: c.ID, Limit: 2})

	url := "https://example.test/search-index"
	refMessage := replySeed(t, s, c, "nova fonte "+url+" https://example.test/second-index-ref", at, nil, nil)
	refs := application.SharedReferences(refMessage.OriginalText)
	for n := range refs {
		refs[n].ID, refs[n].RevisionID = migrationID(), migrationID()
	}
	requireIndexTest(t, s.IndexSharedContent(t.Context(), []domain.SharedContentMutation{{Message: refMessage, References: refs, ParserVersion: application.SharedContentVersion}}))
	_, err = s.EnsurePreview(t.Context(), domain.MessagePreview{ID: migrationID(), RevisionID: migrationID(), MessageID: refMessage.ID, AccountID: c.AccountID, ConversationID: c.ID, URL: url, Title: "Termo antediluviano", Description: "cotações", Kind: "link", State: "ready", DownloadState: "not_downloaded", UpdatedAt: at})
	requireIndexTest(t, err)
	checkIndexParity(t, s, domain.MessageSearchQuery{Search: "antediluviano cotacoes", Limit: 2})
	waitIndex(t, s, func(dbgen.MessageSearchIndex) bool {
		pending, err := s.read.ListMessageSearchPending(t.Context(), 1)
		return err == nil && len(pending) == 0
	})
	assertIndexedCandidate(t, s, "antediluviano", refMessage.ID)
	checkIndexParity(t, s, domain.MessageSearchQuery{Search: "antediluviano cotacoes", Limit: 2})
}

func TestExperimentalMessageIndexScopesTheOverlayBeforeCounting(t *testing.T) {
	old, e, chat := outboxFixture(t)
	seedIndexBenchmark(t, old, chat, searchSmallRoom)
	at := time.Now().UTC()
	hit := replySeed(t, old, chat, "arquivo de revisão", at, nil, nil)
	account, err := e.Register(t.Context(), migrationID(), application.RegisterInput{Protocol: "xmpp", Address: "foreign@localhost", Label: "Foreign"})
	requireIndexTest(t, err)
	other := domain.Conversation{ID: migrationID(), RevisionID: migrationID(), AccountID: account.Account.ID, PeerID: migrationID(), Kind: "direct", Address: "review@localhost", Title: "Review", UpdatedAt: at}
	replySeed(t, old, other, "other chat", at, nil, nil)
	const insert = `WITH RECURSIVE seq(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM seq WHERE n<?1)
	 INSERT INTO messages(id,revision_id,account_id,conversation_id,provider_id,is_own,state,payload,created_at_ms)
	 SELECT printf('019cd001-0000-7000-8000-%012x',n),'',?3,?4,'',0,'received',
	 json_object('OriginalText',?2),1700000000000+n FROM seq`
	_, err = old.db.ExecContext(t.Context(), insert, searchCandidateLimit+1, strings.Repeat("a", searchIndexMaxDocumentBytes+1), other.AccountID, other.ID)
	requireIndexTest(t, err)
	localException := replySeed(t, old, chat, strings.Repeat("x ", searchIndexMaxDocumentBytes/2)+" arquivo", at, nil, nil)
	dir := old.dir
	requireIndexTest(t, old.Close())
	s, err := Open(t.Context(), dir, migrationID(), WithMessageSearchIndex())
	requireIndexTest(t, err)
	t.Cleanup(func() { _ = s.Close() })
	waitIndexReady(t, s, "")
	overlay, err := s.read.ListMessageSearchUnindexed(t.Context(), searchCandidateLimit+1)
	requireIndexTest(t, err)
	if len(overlay) != searchCandidateLimit+1 {
		t.Fatalf("expected unscoped overflow, got %d", len(overlay))
	}

	for _, p := range []dbgen.SearchMessagesParams{
		{Search: "arqiuvo", AccountIds: "[]", ConversationID: string(chat.ID)},
		{Search: "arqiuvo", AccountIds: "[]", AccountID: string(chat.AccountID)},
		{Search: "arqiuvo", AccountIds: jsonText([]domain.ID{chat.AccountID})},
	} {
		assertScopedIndexedCandidate(t, s, p, hit.ID)
		assertScopedIndexedCandidate(t, s, p, localException.ID)
	}
	checkIndexParity(t, s, domain.MessageSearchQuery{Search: "arqiuvo", ConversationID: chat.ID, Limit: 5})
	checkIndexParity(t, s, domain.MessageSearchQuery{Search: "arqiuvo", AccountID: chat.AccountID, Limit: 5})
	checkIndexParity(t, s, domain.MessageSearchQuery{Search: "arqiuvo", Limit: 5})

	// The presentation receives the same excerpts and source ranges even
	// when the complete chronological SQL query provides the results.
	indexed := openIndexTestWeb(t, openIndexTestHTTP(t, s).URL)
	fallback := openIndexTestWeb(t, openIndexTestHTTP(t, chronologicalIndexControl{s}).URL)
	engineID, _, err := s.Snapshot(t.Context())
	requireIndexTest(t, err)
	params := url.Values{"engineId": {string(engineID)}, "accountId": {string(chat.AccountID)}, "q": {"arqiuvo"}, "searchType": {"messages"}, "limit": {"5"}}
	render := func(server *httptest.Server) string {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/ui/message-search?"+params.Encode(), nil)
		requireIndexTest(t, err)
		response, err := server.Client().Do(req)
		requireIndexTest(t, err)
		defer func() { _ = response.Body.Close() }()
		body, err := io.ReadAll(response.Body)
		requireIndexTest(t, err)
		if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `class="oie-search-highlight"`) {
			t.Fatalf("search returned %d without highlighted results", response.StatusCode)
		}
		return string(body)
	}
	before := s.messageIndex.used.Load()
	if render(indexed) != render(fallback) || s.messageIndex.used.Load() != before+1 {
		t.Fatal("global search HTML differs or did not use Bleve")
	}
	params.Set("conversationId", string(chat.ID))
	params.Set("presentation", "conversation")
	got := render(indexed)
	if !strings.Contains(got, "data-search-ranges=") || got != render(fallback) || s.messageIndex.used.Load() != before+2 {
		t.Fatal("conversation search HTML or ranges differ, or did not use Bleve")
	}
}

func TestExperimentalMessageIndexRecovery(t *testing.T) {
	if dir := os.Getenv("OIE_TEST_CRASH_INDEX_DIR"); dir != "" {
		crashMessageIndexAfterBatch(t, dir)
		return
	}
	old, _, c := outboxFixture(t)
	dir := old.dir
	msg := replySeed(t, old, c, "documento", time.Now().UTC(), nil, nil)
	requireIndexTest(t, old.Close())

	open := func() *Store {
		s, err := Open(t.Context(), dir, migrationID(), WithMessageSearchIndex())
		requireIndexTest(t, err)
		t.Cleanup(func() { _ = s.Close() })
		return s
	}
	s := open()
	first := waitIndexReady(t, s, "")
	requireIndexTest(t, s.Close())

	// A matching checkpoint reopens the same generation.
	s = open()
	if resumed := waitIndexReady(t, s, ""); resumed.Generation != first.Generation {
		t.Fatal("clean restart rebuilt the sidecar")
	}
	requireIndexTest(t, s.Close())

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestExperimentalMessageIndexRecovery$")
	cmd.Env = append(os.Environ(), "OIE_TEST_CRASH_INDEX_DIR="+dir, "OIE_TEST_CRASH_INDEX_ID="+string(msg.ID))
	output, err := cmd.CombinedOutput()
	if code, ok := err.(*exec.ExitError); !ok || code.ExitCode() != 87 {
		t.Fatalf("expected forced process exit after Bleve batch: %v %s", err, output)
	}
	s = open()
	checkIndexParity(t, s, domain.MessageSearchQuery{Search: "descoberta", Limit: 5})
	rebuilt := waitIndexReady(t, s, first.Generation)
	assertIndexedCandidate(t, s, "descoberta", msg.ID)
	checkIndexParity(t, s, domain.MessageSearchQuery{Search: "docun", Limit: 5})
	requireIndexTest(t, s.Close())

	// A missing index is independently recoverable.
	requireIndexTest(t, os.RemoveAll(filepath.Join(dir, "private", "message-search", rebuilt.Generation)))
	s = open()
	rebuilt = waitIndexReady(t, s, rebuilt.Generation)
	assertIndexedCandidate(t, s, "descoberta", msg.ID)
	requireIndexTest(t, s.Close())

	// A sidecar that exists but cannot be opened also rebuilds.
	meta := filepath.Join(dir, "private", "message-search", rebuilt.Generation, "index_meta.json")
	requireIndexTest(t, os.WriteFile(meta, []byte("corrupt metadata"), 0600))
	s = open()
	rebuilt = waitIndexReady(t, s, rebuilt.Generation)
	assertIndexedCandidate(t, s, "descoberta", msg.ID)
	requireIndexTest(t, s.Reset(t.Context(), migrationID()))
	newState := waitIndexReady(t, s, rebuilt.Generation)
	if _, err := os.Stat(filepath.Join(dir, "private", "message-search", rebuilt.Generation)); !os.IsNotExist(err) {
		t.Fatalf("reset retained the old generation: %v", err)
	}
	if newState.Generation == rebuilt.Generation {
		t.Fatal("reset reused generation")
	}
	checkIndexParity(t, s, domain.MessageSearchQuery{Search: "descoberta", Limit: 5})
}

func crashMessageIndexAfterBatch(t *testing.T, dir string) {
	t.Helper()
	s, err := Open(t.Context(), dir, migrationID())
	requireIndexTest(t, err)
	_, err = s.db.ExecContext(t.Context(), "UPDATE messages SET payload=json_set(payload,'$.OriginalText','nova descoberta') WHERE id=?", os.Getenv("OIE_TEST_CRASH_INDEX_ID"))
	requireIndexTest(t, err)
	state, err := s.read.GetMessageSearchIndex(t.Context())
	requireIndexTest(t, err)
	idx, err := bleve.Open(filepath.Join(dir, "private", "message-search", state.Generation))
	requireIndexTest(t, err)
	batch := idx.NewBatch()
	batch.Delete(os.Getenv("OIE_TEST_CRASH_INDEX_ID"))
	batch.SetInternal(searchIndexCheckpointKey, []byte(messageIndexToken()))
	requireIndexTest(t, idx.Batch(batch))
	os.Exit(87)
}

func openIndexTestHTTP(t *testing.T, repository application.Repository) *httptest.Server {
	t.Helper()
	e, err := application.New(repository, "local", func() (domain.ID, error) { return migrationID(), nil }, time.Now)
	requireIndexTest(t, err)
	h, err := httpapi.New(e)
	requireIndexTest(t, err)
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	return server
}

func openIndexTestWeb(t *testing.T, engineURL string) *httptest.Server {
	t.Helper()
	handler, err := web.New(engineURL)
	requireIndexTest(t, err)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func measureIndexRequest(ctx context.Context, client *http.Client, address string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	r, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = r.Body.Close() }()
	if r.StatusCode != http.StatusOK {
		return fmt.Errorf("search returned %d", r.StatusCode)
	}
	var page engineapi.MessageSearchPage
	return json.NewDecoder(r.Body).Decode(&page)
}

func TestExperimentalMessageIndexKeepsNewerPendingWrite(t *testing.T) {
	s, _, c := outboxFixture(t)
	msg := replySeed(t, s, c, "original arquivo", time.Now().UTC(), nil, nil)
	worker := &messageIndex{store: s}
	g, err := worker.open(t.Context())
	requireIndexTest(t, err)
	defer func() { _ = g.index.Close() }()
	for g.state.Ready == 0 {
		work, err := worker.readBatch(t.Context(), g.state)
		requireIndexTest(t, err)
		requireIndexTest(t, worker.publish(t.Context(), g, work))
	}
	update := func(body string) {
		_, err := s.db.ExecContext(t.Context(), "UPDATE messages SET payload=json_set(payload,'$.OriginalText',?) WHERE id=?", body, msg.ID)
		requireIndexTest(t, err)
	}
	update("descoberta intermediaria")
	stale, err := worker.readBatch(t.Context(), g.state)
	requireIndexTest(t, err)
	if len(stale.pending) != 1 {
		t.Fatalf("expected one mutation, got %v", stale.pending)
	}
	update("documento atualizado")
	requireIndexTest(t, worker.publish(t.Context(), g, stale))
	pending, err := s.read.ListMessageSearchPending(t.Context(), 1)
	requireIndexTest(t, err)
	if len(pending) != 1 || pending[0].Token == stale.pending[0].Token {
		t.Fatalf("stale batch acknowledged new mutation: %v", pending)
	}
	check := func(term string, expected int) {
		request := domain.MessageSearchQuery{Search: term, Limit: 5}
		page, err := s.searchMessages(t.Context(), request, beginRead, worker.selectRows)
		requireIndexTest(t, err)
		if len(page.Items) != expected {
			t.Fatalf("%q: got %d hits, want %d", term, len(page.Items), expected)
		}
	}
	check("docun", 1)
	check("descoberta", 0)
	fresh, err := worker.readBatch(t.Context(), g.state)
	requireIndexTest(t, err)
	requireIndexTest(t, worker.publish(t.Context(), g, fresh))
	pending, err = s.read.ListMessageSearchPending(t.Context(), 1)
	requireIndexTest(t, err)
	if len(pending) != 0 {
		t.Fatalf("pending mutation was not acknowledged: %v", pending)
	}
	check("docun", 1)
	check("descoberta", 0)
}

type embeddedBleveIndex interface{ bleve.Index }

type pausedIndexLookup struct {
	embeddedBleveIndex
	started chan struct{}
	resume  chan struct{}
}

func (i pausedIndexLookup) SearchInContext(ctx context.Context, req *bleve.SearchRequest) (*bleve.SearchResult, error) {
	close(i.started)
	<-i.resume
	// The test pauses across commits, outside the real search deadline.
	return i.embeddedBleveIndex.SearchInContext(context.WithoutCancel(ctx), req)
}

func TestExperimentalMessageIndexReadCrossingBatchUsesPinnedSQL(t *testing.T) {
	s, _, c := outboxFixture(t)
	msg := replySeed(t, s, c, "arquivo antigo", time.Now().UTC(), nil, nil)
	worker := &messageIndex{store: s}
	g, err := worker.open(t.Context())
	requireIndexTest(t, err)
	defer func() { _ = g.index.Close() }()
	for g.state.Ready == 0 {
		work, err := worker.readBatch(t.Context(), g.state)
		requireIndexTest(t, err)
		requireIndexTest(t, worker.publish(t.Context(), g, work))
	}
	paused := pausedIndexLookup{embeddedBleveIndex: g.index, started: make(chan struct{}), resume: make(chan struct{})}
	g.index = paused
	resume := sync.OnceFunc(func() { close(paused.resume) })
	defer resume()
	type searchResult struct {
		page domain.MessageSearchPage
		err  error
	}
	result := make(chan searchResult, 1)
	go func() {
		page, err := s.searchMessages(t.Context(), domain.MessageSearchQuery{Search: "arqiuvo", Limit: 5}, beginRead, worker.selectRows)
		result <- searchResult{page: page, err: err}
	}()
	select {
	case <-paused.started:
	case early := <-result:
		t.Fatalf("query did not reach Bleve candidate lookup: %+v", early)
	case <-time.After(5 * time.Second):
		t.Fatal("query did not reach the paused Bleve lookup")
	}
	err = func() error {
		defer resume()
		_, err := s.db.ExecContext(t.Context(), "UPDATE messages SET payload=json_set(payload,'$.OriginalText','texto novo') WHERE id=?", msg.ID)
		if err != nil {
			return err
		}
		work, err := worker.readBatch(t.Context(), g.state)
		if err != nil {
			return err
		}
		return worker.publish(t.Context(), g, work)
	}()
	got := <-result
	requireIndexTest(t, err)
	requireIndexTest(t, got.err)
	if worker.crossedBatch.Load() != 1 || len(got.page.Items) != 1 || got.page.Items[0].Message.ID != msg.ID {
		t.Fatalf("crossed read lost its pinned SQL result: crossed=%d hits=%v", worker.crossedBatch.Load(), got.page.Items)
	}
}

func TestExperimentalMessageIndexResumesBuildCheckpoint(t *testing.T) {
	s, _, c := outboxFixture(t)
	dir := s.dir
	msg := replySeed(t, s, c, "documento antes da reabertura", time.Now().UTC(), nil, nil)
	worker := &messageIndex{store: s}
	g, err := worker.open(t.Context())
	requireIndexTest(t, err)
	work, err := worker.readBatch(t.Context(), g.state)
	requireIndexTest(t, err)
	requireIndexTest(t, worker.publish(t.Context(), g, work))
	if g.state.Cursor != string(msg.ID) || g.state.Ready != 0 {
		t.Fatalf("unexpected partial checkpoint: %+v", g.state)
	}
	// Update an already scanned UUID before the remainder of the build.
	_, err = s.db.ExecContext(t.Context(), "UPDATE messages SET payload=json_set(payload,'$.OriginalText','descoberta durante a construcao') WHERE id=?", msg.ID)
	requireIndexTest(t, err)
	requireIndexTest(t, g.index.Close())
	requireIndexTest(t, s.Close())

	reopened, err := Open(t.Context(), dir, migrationID(), WithMessageSearchIndex())
	requireIndexTest(t, err)
	defer func() { _ = reopened.Close() }()
	ready := waitIndexReady(t, reopened, "")
	if ready.Generation != g.state.Generation {
		t.Fatalf("resumed generation %s, want %s", ready.Generation, g.state.Generation)
	}
	assertIndexedCandidate(t, reopened, "descoberta", msg.ID)
	checkIndexParity(t, reopened, domain.MessageSearchQuery{Search: "docun", Limit: 5})
	checkIndexParity(t, reopened, domain.MessageSearchQuery{Search: "descoberta", Limit: 5})
}
