//go:build searchprototype

package sqlite

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"github.com/araihu/oie/internal/search"
	"github.com/blevesearch/bleve/v2"
)

func prototypeID(n int) domain.ID {
	return domain.ID(fmt.Sprintf("019cd000-0000-7000-8000-%012x", n))
}

func prototypeOptions(t *testing.T, name string, fallback, minimum int) int {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	number, err := strconv.Atoi(value)
	if err != nil || number < minimum {
		t.Fatalf("%s must be an integer >= %d", name, minimum)
	}
	return number
}

func prototypeMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func prototypeFixture(t *testing.T, size int) (*Store, [2]domain.ID, [2]domain.ID) {
	t.Helper()
	ctx := t.Context()
	store, err := Open(ctx, t.TempDir(), prototypeID(1))
	prototypeMust(t, err)
	t.Cleanup(func() { _ = store.Close() })
	accounts := [2]domain.ID{prototypeID(10), prototypeID(11)}
	rooms := [2]domain.ID{prototypeID(20), prototypeID(21)}
	tx, err := store.db.BeginTx(ctx, nil)
	prototypeMust(t, err)
	defer func() { _ = tx.Rollback() }()
	for i, account := range accounts {
		rev := prototypeID(30 + i)
		address := fmt.Sprintf("account%d@example.test", i)
		_, err = tx.ExecContext(ctx, "INSERT INTO accounts(id,revision_id,protocol,address,label,created_at) VALUES(?,?,'xmpp',?,'Test',?)", account, rev, address, prototypeTime)
		prototypeMust(t, err)
		_, err = tx.ExecContext(ctx, "INSERT INTO revisions VALUES(?,?,'[]','{}',?)", rev, account, prototypeTime)
		prototypeMust(t, err)
		room := domain.Conversation{ID: rooms[i], RevisionID: prototypeID(40 + i), AccountID: account, PeerID: prototypeID(50 + i), Kind: "room", Title: fmt.Sprintf("Room %d", i), Address: fmt.Sprintf("room%d@example.test", i), UpdatedAt: prototypeTime}
		_, err = tx.ExecContext(ctx, "INSERT INTO conversations(id,revision_id,account_id,address,peer_id,payload,activity_sequence) VALUES(?,?,?,?,?,?,?)", room.ID, room.RevisionID, account, room.Address, room.PeerID, jsonText(room), i+1)
		prototypeMust(t, err)
	}
	_, err = tx.ExecContext(ctx, `CREATE INDEX prototype_timeline ON messages(COALESCE(timeline_at_ms,created_at_ms) DESC,id DESC);
WITH RECURSIVE seq(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM seq WHERE n<?1),
seed(n,id,rid,account,room,body) AS (
SELECT n,printf('019cd000-0000-7000-8000-%012x',1000+n),printf('019cd000-0000-7000-8001-%012x',1000+n),
CASE WHEN n%1000=0 THEN ?3 ELSE ?2 END,CASE WHEN n%1000=0 THEN ?5 ELSE ?4 END,
CASE WHEN n%1000=0 THEN 'O arquivo do contrato tem prazo em novembro'
WHEN n%4=0 THEN 'Recebi o pagamento e confirmei o faturamento para a semana'
ELSE 'O vídeo está pronto e vou mandar a versão amanhã' END || ' ' || n ||
CASE WHEN n%1777=0 THEN ' documento anexo' ELSE '' END
FROM seq)
INSERT INTO messages(id,revision_id,account_id,conversation_id,provider_id,is_own,state,payload,created_at_ms)
SELECT id,rid,account,room,'',0,'received',json_object('ID',id,'RevisionID',rid,'AccountID',account,
'ConversationID',room,'Sender',json_object('ID',room,'DisplayName','Participante'),
'CreatedAt',?6,'OriginalText',body,'SendState','received'),1700000000000+n
FROM seed;
`, size, accounts[0], accounts[1], rooms[0], rooms[1], prototypeTime.Format(time.RFC3339Nano))
	prototypeMust(t, err)
	_, err = tx.ExecContext(ctx, "INSERT INTO message_revisions SELECT revision_id,id,'[]',payload,? FROM messages", stamp(prototypeTime))
	prototypeMust(t, err)
	prototypeMust(t, tx.Commit())
	// Reproduce a populated SQLite planner. Without table statistics, the
	// empty sender-identity table can trigger one room-wide scan per hit.
	_, err = store.db.ExecContext(ctx, "ANALYZE")
	prototypeMust(t, err)
	return store, accounts, rooms
}

var prototypeTime = time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

func prototypeIndexes(t *testing.T, store *Store, statements prototypeSQL) (bleve.Index, time.Duration, time.Duration, int64, int64) {
	t.Helper()
	ctx := t.Context()
	// Build from the same snapshot, with the same normalized text and source IDs.
	projectionStarted := time.Now()
	rows, err := store.readDB.QueryContext(ctx, statements.projection, application.SharedContentVersion)
	prototypeMust(t, err)
	type document struct{ id, account, room, folded string }
	var docs []document
	for rows.Next() {
		var d document
		prototypeMust(t, rows.Scan(&d.id, &d.account, &d.room, &d.folded))
		d.folded = strings.ReplaceAll(search.Fold(d.folded), "\x00", " ")
		docs = append(docs, d)
	}
	prototypeMust(t, rows.Err())
	prototypeMust(t, rows.Close())
	t.Logf("common projection and fold: %s (%d documents)", time.Since(projectionStarted), len(docs))

	checkpoint := func() int64 {
		_, err := store.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
		prototypeMust(t, err)
		info, err := os.Stat(filepath.Join(store.dir, "oie.sqlite"))
		prototypeMust(t, err)
		return info.Size()
	}
	before := checkpoint()
	start := time.Now()
	tx, err := store.db.BeginTx(ctx, nil)
	prototypeMust(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `CREATE TABLE prototype_documents(docid INTEGER PRIMARY KEY,id TEXT NOT NULL UNIQUE,account TEXT NOT NULL,conversation TEXT NOT NULL);
CREATE VIRTUAL TABLE prototype_fts USING fts5(text,tokenize='trigram case_sensitive 1',detail=none,columnsize=0,content='')`)
	prototypeMust(t, err)
	for i, d := range docs {
		_, err = tx.ExecContext(ctx, "INSERT INTO prototype_documents VALUES(?,?,?,?)", i+1, d.id, d.account, d.room)
		prototypeMust(t, err)
		_, err = tx.ExecContext(ctx, "INSERT INTO prototype_fts(rowid,text) VALUES(?,?)", i+1, d.folded)
		prototypeMust(t, err)
	}
	prototypeMust(t, tx.Commit())
	ftsTime := time.Since(start)
	ftsBytes := checkpoint() - before

	path := filepath.Join(store.dir, "search.bleve")
	start = time.Now()
	idx, err := prototypeNewBleve(path)
	prototypeMust(t, err)
	batch := idx.NewBatch()
	for _, d := range docs {
		prefixes, grams := prototypeTokens(d.folded)
		prototypeMust(t, batch.Index(d.id, map[string]any{"prefix": prefixes, "gram": grams, "account": d.account, "conversation": d.room}))
		if batch.Size() == 1000 {
			prototypeMust(t, idx.Batch(batch))
			batch = idx.NewBatch()
		}
	}
	if batch.Size() > 0 {
		prototypeMust(t, idx.Batch(batch))
	}
	prototypeMust(t, idx.Close())
	bleveTime := time.Since(start)
	var bleveBytes int64
	prototypeMust(t, filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err == nil {
			bleveBytes += info.Size()
		}
		return err
	}))
	idx, err = bleve.Open(path)
	prototypeMust(t, err)
	t.Cleanup(func() { _ = idx.Close() })
	return idx, ftsTime, bleveTime, ftsBytes, bleveBytes
}

func prototypeServer(t *testing.T, repo application.Repository) *httptest.Server {
	t.Helper()
	engine, err := application.New(repo, "local", func() (domain.ID, error) { return migrationID(), nil }, time.Now)
	prototypeMust(t, err)
	handler, err := httpapi.New(engine)
	prototypeMust(t, err)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func prototypePage(t *testing.T, client *http.Client, address string, params url.Values) (engineapi.MessageSearchPage, time.Duration) {
	t.Helper()
	start := time.Now()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, address+"/api/v1/messages/search?"+params.Encode(), nil)
	prototypeMust(t, err)
	resp, err := client.Do(req)
	prototypeMust(t, err)
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	prototypeMust(t, err)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search %s got %d: %s", params.Encode(), resp.StatusCode, data)
	}
	var page engineapi.MessageSearchPage
	prototypeMust(t, json.Unmarshal(data, &page))
	return page, time.Since(start)
}

func prototypeArgs(term string, room, account domain.ID, history bool, limit int) url.Values {
	args := url.Values{"q": {term}, "limit": {strconv.Itoa(limit)}}
	if room != "" {
		args.Set("conversationId", string(room))
	}
	if account != "" {
		args.Set("accountId", string(account))
	}
	if history {
		args.Set("includeEditHistory", "true")
	}
	return args
}

func prototypeSame(t *testing.T, name string, args url.Values, want, got engineapi.MessageSearchPage) {
	t.Helper()
	if jsonText(want) != jsonText(got) {
		t.Fatalf("%s parity %s: want %d hits cursor %v, got %d hits cursor %v\nwant %.600s\ngot %.600s", name, args.Encode(), len(want.Items), want.NextCursor, len(got.Items), got.NextCursor, jsonText(want), jsonText(got))
	}
}

func prototypeP95(samples []time.Duration) time.Duration {
	slices.Sort(samples)
	return samples[int(math.Ceil(.95*float64(len(samples))))-1]
}

func TestMessageSearchPrototypeBenchmark(t *testing.T) {
	if os.Getenv("OIE_SEARCH_BENCH") != "1" {
		t.Skip("set OIE_SEARCH_BENCH=1 to build disk fixtures and measure HTTP")
	}
	size := prototypeOptions(t, "OIE_SEARCH_MESSAGES", 100000, 2000)
	samples := prototypeOptions(t, "OIE_SEARCH_SAMPLES", 40, 1)
	store, accounts, rooms := prototypeFixture(t, size)
	statements, err := prototypeQueries("dbgen/message_search.sql.go")
	prototypeMust(t, err)
	idx, ftsTime, bleveTime, ftsSize, bleveSize := prototypeIndexes(t, store, statements)
	t.Logf("%d messages; FTS5 build %v +%d bytes; Bleve build %v %d bytes", size, ftsTime, ftsSize, bleveTime, bleveSize)
	baseline := prototypeServer(t, store)
	client := baseline.Client()
	client.Timeout = 2 * time.Minute
	t.Cleanup(client.CloseIdleConnections)
	modes := []string{"chronological", "fts5", "bleve"}
	repos := map[string]*prototypeRepo{}
	addresses := map[string]string{}
	small := map[string]bool{}
	for i, n := range []int{size - size/1000, size / 1000} {
		small[string(rooms[i])] = n <= prototypeRoomSmall
	}
	for _, mode := range modes {
		repos[mode] = &prototypeRepo{Store: store, mode: mode, bleve: idx, sql: statements, smallRooms: small}
		addresses[mode] = prototypeServer(t, repos[mode]).URL
	}
	cases := []struct {
		name, term    string
		room, account domain.ID
		next          bool
	}{
		{"arqiuvo-global", "arqiuvo", "", "", true},
		{"arqiuvo-small", "arqiuvo", rooms[1], "", false},
		{"arqiuvo-large-miss", "arqiuvo", rooms[0], "", false},
		{"faturamento-global", "faturamento", "", "", true},
		{"faturamento-small-miss", "faturamento", rooms[1], "", false},
		{"viedo-global", "viedo", "", "", true},
		{"viedo-small-miss", "viedo", rooms[1], "", false},
		{"docun-global", "docun", "", "", true},
		{"docun-account", "docun", "", accounts[0], false},
	}
	expectedPaths := map[string][2]string{
		"arqiuvo-global":     {"fts5", "bleve"},
		"arqiuvo-large-miss": {"fts5", "bleve"},
		"docun-global":       {"no safe term", "bleve"},
		"docun-account":      {"no safe term", "bleve"},
		"faturamento-global": {"candidate budget", "candidate budget"},
		"viedo-global":       {"no safe term", "candidate budget"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args := prototypeArgs(c.term, c.room, c.account, false, 50)
			for pageNo := 1; pageNo <= 2; pageNo++ {
				want, oracleElapsed := prototypePage(t, client, baseline.URL, args)
				t.Logf("page %d oracle in %s", pageNo, oracleElapsed)
				for _, mode := range modes {
					for range 2 {
						got, _ := prototypePage(t, client, addresses[mode], args)
						prototypeSame(t, mode, args, want, got)
					}
				}
				t.Logf("page %d warm; measuring %d HTTP requests per plan", pageNo, samples)
				raw := map[string][]time.Duration{}
				paths := map[string]string{}
				for round := range samples {
					for step := range modes {
						mode := modes[(round+step+pageNo-1)%len(modes)]
						got, elapsed := prototypePage(t, client, addresses[mode], args)
						prototypeSame(t, mode, args, want, got)
						raw[mode] = append(raw[mode], elapsed)
						paths[mode] = repos[mode].lastPath()
					}
				}
				if size == 100000 {
					if expected, ok := expectedPaths[c.name]; ok {
						for i, name := range modes[1:] {
							if !strings.Contains(paths[name], expected[i]) {
								t.Fatalf("%s should take %q, took %q", name, expected[i], paths[name])
							}
						}
					}
				}
				t.Logf("%s page=%d hits=%d n=%d SQL=%v FTS5=%v [%s] Bleve=%v [%s]",
					c.name, pageNo, len(want.Items), samples, prototypeP95(raw[modes[0]]),
					prototypeP95(raw[modes[1]]), paths[modes[1]], prototypeP95(raw[modes[2]]), paths[modes[2]])
				if !c.next || want.NextCursor == nil {
					break
				}
				args.Set("cursor", *want.NextCursor)
			}
		})
	}
}

func TestMessageSearchPrototypeParity(t *testing.T) {
	store, accounts, rooms := prototypeFixture(t, 2000)
	ctx := t.Context()
	exec := func(statement string, args ...any) {
		t.Helper()
		_, err := store.db.ExecContext(ctx, statement, args...)
		prototypeMust(t, err)
	}
	target, alias := prototypeID(1001), prototypeID(1002)
	longTerm := strings.Repeat("a", 55) + "b"
	exec("UPDATE messages SET payload=json_set(payload,'$.OriginalText',?) WHERE id=?",
		"z"+longTerm[1:]+" rarissimotermo", prototypeID(1005))
	exec("UPDATE messages SET payload=json_set(payload,'$.OriginalText','arquivo revisao isolada') WHERE id=?", prototypeID(1006))
	exec("UPDATE messages SET payload=json_set(payload,'$.OriginalText','anxarquivo 漢字左右') WHERE id=?", prototypeID(1007))

	exec("UPDATE messages SET payload=json_set(payload,'$.OriginalText','vÍdeo anxarquivo relatorio cafe 😀 ... 漢字左右') WHERE id=?", target)
	exec("UPDATE message_revisions SET payload=(SELECT payload FROM messages WHERE id=?) WHERE entity_id=?", target, target)
	exec("UPDATE messages SET payload=json_set(payload,'$.OriginalText','documento atual cafe') WHERE id=?", alias)
	exec("UPDATE message_revisions SET payload=(SELECT payload FROM messages WHERE id=?) WHERE entity_id=?", alias, alias)
	exec("INSERT INTO message_canonical_aliases VALUES(?,?)", alias, target)
	exec("INSERT INTO message_content_heads SELECT ?,id,revision_id,payload,? FROM messages WHERE id=?", target, stamp(prototypeTime), alias)
	exec("INSERT INTO message_correction_facts SELECT m.id,'roomscope','bob',m.id,?,oie_source_hash(json_extract(m.payload,'$.OriginalText')),m.revision_id FROM messages m WHERE m.id IN (?,?)", target, target, alias)
	exec("INSERT INTO shared_content_state VALUES(?,?,?)", target, application.SharedContentVersion, "")
	urlText := "https://example.test/one"
	exec("INSERT INTO shared_content VALUES(?,?,?,?,?,?)", prototypeID(900000), prototypeID(900001), target, urlText, "link", "contrato.pdf")
	exec(`INSERT INTO message_previews(id,revision_id,account_id,conversation_id,message_id,url,state,download_state,updated_at,payload)
SELECT ?1,?2,account_id,conversation_id,id,?3,'ready','not_downloaded',?4,json_object('Title','Orçamento raro','Description','cotações internacionais','Kind','link') FROM messages WHERE id=?5`,
		prototypeID(900002), prototypeID(900003), urlText, stamp(prototypeTime), target)
	exec("INSERT INTO shared_content VALUES(?,?,?,?,?,?)", prototypeID(900004), prototypeID(900005), target, "https://example.test/aaa", "link", "relatorio.pdf")
	exec("UPDATE messages SET payload=json_set(payload,'$.OriginalText','video indisponivel') WHERE id=?", prototypeID(1003))
	exec("UPDATE messages SET timeline_at_ms=? WHERE id IN (?,?)", int64(1700000000007), target, prototypeID(1004))
	statements, err := prototypeQueries("dbgen/message_search.sql.go")
	prototypeMust(t, err)
	idx, _, _, _, _ := prototypeIndexes(t, store, statements)

	// Explicit pending IDs model a stale static sidecar after committed source changes.
	pending := []string{string(prototypeID(1003)), string(target)}
	exec("UPDATE messages SET payload=json_set(payload,'$.OriginalText','faturamento zyx nova fonte') WHERE id=?", prototypeID(1003))
	exec("UPDATE message_previews SET payload=json_set(payload,'$.Title','faturamento cosmopolita') WHERE message_id=?", target)

	oracle := prototypeServer(t, store)
	client := oracle.Client()
	t.Cleanup(client.CloseIdleConnections)
	modes := []string{"chronological", "fts5", "bleve"}
	repos := map[string]*prototypeRepo{}
	addresses := map[string]string{}
	for _, mode := range modes {
		repos[mode] = &prototypeRepo{Store: store, mode: mode, bleve: idx, sql: statements, pending: pending}
		addresses[mode] = prototypeServer(t, repos[mode]).URL
	}
	cases := []struct {
		term    string
		history bool
		room    domain.ID
		limit   int
	}{
		{"docun", false, "", 1}, {"documento", false, "", 2}, {"arquivo", true, "", 2}, {"arquivo", false, "", 5},
		{"anxarquivo", true, rooms[0], 2}, {"arquivo 漢字右左", false, rooms[0], 2}, {"cafe 😀", true, rooms[0], 1},
		{"漢字右左", true, "", 2}, {"cafe ...", true, "", 1}, {"...", true, "", 2},
		{"cotacoes cafe", false, "", 1}, {"cosmopolita", false, "", 2}, {"nova fonte", false, "", 2},
		{"video", false, rooms[0], 100}, {"faturamento", true, "", 100},
		{strings.Repeat("Ａ", 60), true, "", 1}, {longTerm + " rarissimotermo", false, "", 1}, {"noexactword777", false, "", 5},
	}
	for _, c := range cases {
		args := prototypeArgs(c.term, c.room, "", c.history, c.limit)
		for page := 0; ; page++ {
			if page > 100 {
				t.Fatal("cursor did not advance")
			}
			want, _ := prototypePage(t, client, oracle.URL, args)
			for _, mode := range modes {
				got, _ := prototypePage(t, client, addresses[mode], args)
				prototypeSame(t, mode, args, want, got)
			}
			if want.NextCursor == nil {
				break
			}
			args.Set("cursor", *want.NextCursor)
		}
	}

	// "arquivo" exists in both accounts. Assert scopes for both
	// single-account and explicitly selected account sets.
	for _, selected := range [][]string{{string(accounts[0])}, {string(accounts[1])}, {string(accounts[1]), string(accounts[0])}} {
		args := prototypeArgs("arquivo", "", "", false, 10)
		args["accountIds"] = selected
		want, _ := prototypePage(t, client, oracle.URL, args)
		if len(want.Items) == 0 {
			t.Fatal("account fixture must return matches", selected)
		}
		for _, mode := range modes {
			got, _ := prototypePage(t, client, addresses[mode], args)
			prototypeSame(t, mode, args, want, got)
			if mode != "chronological" && repos[mode].lastPath() != mode {
				t.Fatalf("account fixture did not exercise %s", mode)
			}
		}
	}

	// FTS and Bleve simulate a permanently failed index and must still use full SQL.
	_, err = store.db.ExecContext(ctx, "DROP TABLE prototype_fts")
	prototypeMust(t, err)
	repos["bleve"].bleve = nil
	args := prototypeArgs("cosmopolita", "", "", false, 10)
	want, _ := prototypePage(t, client, oracle.URL, args)
	for _, mode := range []string{"fts5", "bleve"} {
		got, _ := prototypePage(t, client, addresses[mode], args)
		prototypeSame(t, mode, args, want, got)
		if !strings.Contains(repos[mode].lastPath(), "index error") {
			t.Fatalf("%s did not report complete fallback: %s", mode, repos[mode].lastPath())
		}
	}
}
