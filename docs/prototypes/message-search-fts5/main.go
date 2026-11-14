// Command message-search-fts5 compares candidate selection plans on a synthetic corpus.
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	_ "github.com/araihu/oie/internal/adapters/sqlite"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/search"
)

func replaceOnce(source, before, after string) string {
	if strings.Count(source, before) != 1 {
		panic("expected exactly one occurrence in SQL: " + before)
	}
	return strings.Replace(source, before, after, 1)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	runs := flag.Int("runs", 5, "timed samples per plan and query")
	flag.Parse()
	if *runs < 1 {
		panic("runs must be positive")
	}
	db, err := sql.Open("sqlite", ":memory:")
	must(err)
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	exec := func(statement string) {
		_, err := db.Exec(statement)
		must(err)
	}
	migrations, err := filepath.Glob("internal/adapters/sqlite/migrations/*.up.sql")
	must(err)
	for _, path := range migrations {
		b, err := os.ReadFile(path)
		must(err)
		exec(string(b))
	}
	var sqliteVersion string
	must(db.QueryRow("SELECT sqlite_version()").Scan(&sqliteVersion))
	fmt.Println("SQLite", sqliteVersion)
	exec(`INSERT INTO accounts VALUES('a','ar','xmpp','a@example.org','Test','2020-01-01');
 INSERT INTO conversations(id,revision_id,account_id,address,peer_id,payload,activity_sequence) VALUES ('a','cr','a','one@example.org','none','{}',1),('b','cr2','a','two@example.org','none','{}',2);
 WITH RECURSIVE seq(n) AS(VALUES(1) UNION ALL SELECT n+1 FROM seq WHERE n<100000)
 INSERT INTO messages(id,revision_id,account_id,conversation_id,provider_id,is_own,state,payload,created_at_ms)
 SELECT printf('m%06d',n), printf('r%06d',n),'a',CASE WHEN n%1000=0 THEN 'b' ELSE 'a' END,'',0,'received',
 json_object('OriginalText', CASE WHEN n%1000=0 THEN 'O arquivo do contrato tem prazo em novembro ' WHEN n%4=0 THEN 'Recebi o pagamento e confirmei o faturamento para a semana ' ELSE 'O vídeo está pronto e vou mandar a versão amanhã ' END || n),n
 FROM seq;
 INSERT INTO message_revisions SELECT revision_id,id,'[]',payload,'2020-01-01' FROM messages;
 ANALYZE;
 CREATE VIRTUAL TABLE idx USING fts5(text, tokenize='trigram case_sensitive 1', detail=none, columnsize=0);`)
	begin := time.Now()
	tx, err := db.Begin()
	must(err)
	stmt, err := tx.Prepare("INSERT INTO idx(rowid,text) VALUES(?,?)")
	must(err)
	for n := 1; n <= 100000; n++ {
		var text string
		switch {
		case n%1000 == 0:
			text = "O arquivo do contrato tem prazo em novembro "
		case n%4 == 0:
			text = "Recebi o pagamento e confirmei o faturamento para a semana "
		default:
			text = "O vídeo está pronto e vou mandar a versão amanhã "
		}
		_, err = stmt.Exec(n, search.Fold(fmt.Sprint(text, n)))
		must(err)
	}
	must(stmt.Close())
	must(tx.Commit())
	fmt.Println("FTS build", time.Since(begin))
	b, err := os.ReadFile("internal/adapters/sqlite/dbgen/message_search.sql.go")
	must(err)
	_, source, found := strings.Cut(string(b), "const searchMessages = `")
	if !found {
		panic("generated SearchMessages query was not found")
	}
	base, _, _ := strings.Cut(source, "`")
	lookup := replaceOnce(base, `WITH initial_revisions AS (
 SELECT entity_id,min(rowid) AS revision_row FROM message_revisions GROUP BY entity_id
), roots AS (`, `WITH roots AS (`)
	lookup = replaceOnce(lookup, ` JOIN initial_revisions initial ON initial.entity_id=m.id
 JOIN message_revisions first_source ON first_source.rowid=initial.revision_row`, ` JOIN message_revisions first_source ON first_source.rowid=(SELECT rowid FROM message_revisions first WHERE first.entity_id=m.id ORDER BY rowid LIMIT 1)`)
	lookup = replaceOnce(lookup, ` LEFT JOIN initial_revisions head_initial ON head_initial.entity_id=h.source_id
 LEFT JOIN message_revisions head_source ON head_source.rowid=head_initial.revision_row`, ` LEFT JOIN message_revisions head_source ON head_source.rowid=(SELECT rowid FROM message_revisions first WHERE first.entity_id=h.source_id ORDER BY rowid LIMIT 1)`)
	lookup = replaceOnce(lookup, ` JOIN initial_revisions first ON first.entity_id=f.message_id
 JOIN message_revisions source ON source.rowid=first.revision_row`, ` JOIN message_revisions source ON source.rowid=(SELECT rowid FROM message_revisions first WHERE first.entity_id=f.message_id ORDER BY rowid LIMIT 1)`)
	seek := `WITH page AS MATERIALIZED (
 SELECT m.id, COALESCE(m.timeline_at_ms,m.created_at_ms) AS position_ms, m.timeline_at_ms, m.payload, m.account_id,m.conversation_id,h.source_id,
 CAST(json_extract(COALESCE(h.payload,m.payload),'$.OriginalText') AS TEXT) AS body,
 CAST(COALESCE((SELECT group_concat(sc.url||' '||sc.file_name||' '||COALESCE(json_extract(p.payload,'$.Title'),'')||' '||COALESCE(json_extract(p.payload,'$.Description'),''),' ') FROM shared_content sc JOIN shared_content_state state ON state.message_id=sc.message_id AND state.parser_version>=?7 LEFT JOIN message_previews p ON p.message_id=sc.message_id AND p.url=sc.url WHERE sc.message_id=m.id),'') AS TEXT) AS metadata
 FROM messages m INDEXED BY messages_experiment_global_timeline
 LEFT JOIN message_content_heads h ON h.canonical_id=m.id
 WHERE EXISTS(SELECT 1 FROM conversations c WHERE c.id=m.conversation_id AND json_extract(c.payload,'$.Request') IS NULL)
 AND EXISTS(SELECT 1 FROM message_revisions r WHERE r.entity_id=m.id)
 AND NOT EXISTS(SELECT 1 FROM message_canonical_aliases alias WHERE alias.alias_id=m.id)
 AND NOT EXISTS(SELECT 1 FROM hidden_scheduled_messages h WHERE h.message_id=m.id)
 AND (CAST(?8 AS TEXT)='' OR m.account_id=CAST(?8 AS TEXT))
 AND (CAST(?9 AS TEXT)='[]' OR m.account_id IN (SELECT value FROM json_each(CAST(?9 AS TEXT))))
 AND (CAST(?10 AS TEXT)='' OR m.conversation_id=CAST(?10 AS TEXT))
 AND (CAST(?1 AS TEXT)='' OR (COALESCE(m.timeline_at_ms,m.created_at_ms),m.id,1,'','')<(CAST(?2 AS INTEGER),CAST(?1 AS TEXT),CAST(?3 AS INTEGER),CAST(?4 AS TEXT),CAST(?5 AS TEXT)))
 AND CAST(?11 AS INTEGER)=0
 AND oie_search_match(CAST(json_extract(COALESCE(h.payload,m.payload),'$.OriginalText') AS TEXT)||' '||CAST(COALESCE((SELECT group_concat(sc.url||' '||sc.file_name||' '||COALESCE(json_extract(p.payload,'$.Title'),'')||' '||COALESCE(json_extract(p.payload,'$.Description'),''),' ') FROM shared_content sc JOIN shared_content_state state ON state.message_id=sc.message_id AND state.parser_version>=?7 LEFT JOIN message_previews p ON p.message_id=sc.message_id AND p.url=sc.url WHERE sc.message_id=m.id),'') AS TEXT),CAST(?12 AS TEXT))=1
 ORDER BY COALESCE(m.timeline_at_ms,m.created_at_ms) DESC,m.id DESC LIMIT ?6
 )
 SELECT page.id,page.position_ms,page.timeline_at_ms,page.payload,a.label,a.address,c.payload,
 CAST(COALESCE(head_source.id,first_source.id) AS TEXT),CAST(COALESCE(head_source.created_at,first_source.created_at) AS TEXT),
 page.body,page.metadata,1,'','',CAST(oie_search_match(page.body,CAST(?12 AS TEXT)) AS INTEGER)
 FROM page JOIN accounts a ON a.id=page.account_id JOIN conversations c ON c.id=page.conversation_id
 JOIN message_revisions first_source ON first_source.rowid=(SELECT rowid FROM message_revisions first WHERE first.entity_id=page.id ORDER BY rowid LIMIT 1)
 LEFT JOIN message_revisions head_source ON head_source.rowid=(SELECT rowid FROM message_revisions first WHERE first.entity_id=page.source_id ORDER BY rowid LIMIT 1)
 ORDER BY page.position_ms DESC,page.id DESC`
	candidate := replaceOnce(lookup, " FROM messages m JOIN accounts", " FROM idx CROSS JOIN messages m ON m.rowid=idx.rowid JOIN accounts")
	candidate = replaceOnce(candidate, " WHERE NOT EXISTS(SELECT 1 FROM message_canonical_aliases", " WHERE idx MATCH ?13 AND NOT EXISTS(SELECT 1 FROM message_canonical_aliases")
	// The SQL arms return the same 15 columns as the generated query.
	query := func(statement string, args []any) [][15]any {
		rows, err := db.Query(statement, args...)
		must(err)
		defer func() { _ = rows.Close() }()
		var out [][15]any
		for rows.Next() {
			var row [15]any
			dest := make([]any, len(row))
			for i := range row {
				dest[i] = &row[i]
			}
			must(rows.Scan(dest...))
			out = append(out, row)
		}
		must(rows.Err())
		return out
	}
	explain := func(label, q string, args []any) {
		fmt.Println("PLAN", label)
		rows, err := db.Query("EXPLAIN QUERY PLAN "+q, args...)
		must(err)
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var a, b, c int
			var details string
			must(rows.Scan(&a, &b, &c, &details))
			if strings.Contains(details, "message_revisions") || strings.Contains(details, "MATERIALIZE") || strings.Contains(details, "SCAN idx") || strings.Contains(details, "SEARCH m ") || strings.Contains(details, "SCAN m") {
				fmt.Println(details)
			}
		}
	}
	// The room IDs are synthetic. Room b has 100 messages and room a has 99,900.
	testCases := []struct{ pattern, match, room string }{
		{"arqiuvo", `"arq" OR "uvo"`, ""},
		{"arqiuvo", `"arq" OR "uvo"`, "b"},
		{"arqiuvo", `"arq" OR "uvo"`, "a"},
		{"faturamento", `"fat" OR "ram" OR "nto"`, "b"},
		{"faturamento", `"fat" OR "ram" OR "nto"`, ""},
		{"viedo", "", ""},
		{"viedo", "", "b"},
	}
	for _, item := range testCases {
		args := []any{"", 0, 0, "", "", 51, application.SharedContentVersion, "", "[]", item.room, 0, item.pattern}
		scoped, seekScope := lookup, seek
		if item.room != "" {
			const optionalRoom = "(CAST(?10 AS TEXT)='' OR m.conversation_id=CAST(?10 AS TEXT))"
			const selectedRoom = "m.conversation_id=CAST(?10 AS TEXT)"
			scoped = replaceOnce(scoped, optionalRoom, selectedRoom)
			seekScope = replaceOnce(seekScope, optionalRoom, selectedRoom)
			seekScope = replaceOnce(seekScope, "INDEXED BY messages_experiment_global_timeline", "INDEXED BY messages_timeline")
		}

		fmt.Printf("query=%q room=%q\n", item.pattern, item.room)
		want := query(base, args)
		var cursorArgs []any
		var wantNext [][15]any
		if len(want) == 51 {
			last := want[49]
			cursorArgs = slices.Clone(args)
			cursorArgs[0], cursorArgs[1] = last[0], last[1]
			// is_current is column 11. The revision key is in columns 12 and 13.
			cursorArgs[2], cursorArgs[3], cursorArgs[4] = last[11], last[12], last[13]
			wantNext = query(base, cursorArgs)
		}
		plans := []struct{ name, sql string }{
			{"base", base},
			{"lookup", scoped},
			{"seek", seekScope},
			{"fts", candidate},
		}
		for _, plan := range plans {
			if plan.name == "fts" && item.match == "" {
				continue
			}
			// Only the chronological seek arm gets the proposed global index.
			globalSeek := plan.name == "seek" && item.room == ""
			if globalSeek {
				exec("CREATE INDEX messages_experiment_global_timeline ON messages(COALESCE(timeline_at_ms,created_at_ms) DESC,id DESC)")
			}
			planArgs := slices.Clone(args)
			planCursorArgs := slices.Clone(cursorArgs)
			if plan.name == "fts" {
				planArgs = append(planArgs, item.match)
				if cursorArgs != nil {
					planCursorArgs = append(planCursorArgs, item.match)
				}
			}
			got := query(plan.sql, planArgs)
			if !reflect.DeepEqual(want, got) || cursorArgs != nil && !reflect.DeepEqual(wantNext, query(plan.sql, planCursorArgs)) {
				panic(fmt.Sprintf("SQL page mismatch for %s on %+v", plan.name, item))
			}
			if item.pattern == "arqiuvo" && item.room == "b" {
				explain(plan.name, plan.sql, planArgs)
			}
			samples := make([]time.Duration, *runs)
			for i := range samples {
				start := time.Now()
				query(plan.sql, planArgs)
				samples[i] = time.Since(start)
			}
			slices.Sort(samples)
			fmt.Printf(" %s hits %d median %v\n", plan.name, len(got), samples[len(samples)/2])
			if globalSeek {
				exec("DROP INDEX messages_experiment_global_timeline")
			}
		}
	}
}
