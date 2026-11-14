//go:build searchprototype

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"unicode"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/domain"
	"github.com/araihu/oie/internal/search"
	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/index/scorch"
	"github.com/blevesearch/bleve/v2/search/query"
)

const prototypePrefixLimit = 48
const prototypeBudget = 1024
const prototypeRoomSmall = 512

type prototypeRepo struct {
	*Store
	mode       string
	bleve      bleve.Index
	sql        prototypeSQL
	smallRooms map[string]bool
	// The test fixture is immutable except for explicitly supplied pending IDs.
	pending []string
	path    atomic.Pointer[string]
}

func (r *prototypeRepo) SearchMessages(ctx context.Context, request domain.MessageSearchQuery) (domain.MessageSearchPage, error) {
	return r.searchMessages(ctx, request, beginRead, r.selectRows)
}

func (r *prototypeRepo) recordPath(path string) {
	r.path.Store(&path)
}

func (r *prototypeRepo) lastPath() string {
	path := r.path.Load()
	if path == nil {
		return ""
	}
	return *path
}

func (r *prototypeRepo) selectRows(ctx context.Context, tx *sql.Tx, _ *dbgen.Queries, p dbgen.SearchMessagesParams) ([]dbgen.SearchMessagesRow, error) {
	path := "chronological"
	defer func() { r.recordPath(path) }()
	args := []any{p.CursorID, p.CursorMs, p.CursorCurrent, p.CursorRevisionAt, p.CursorRevisionID, p.PageLimit, p.ParserVersion, p.AccountID, p.AccountIds, p.ConversationID, p.IncludeHistory, p.Search}
	statement := r.sql.chrono
	scanArgs := slices.Clone(args)
	if p.CursorID == "" {
		scanArgs[0], scanArgs[1] = "~", int64(math.MaxInt64)
	}
	if p.ConversationID != "" {
		statement = r.sql.room
	}
	if p.IncludeHistory != 0 {
		path = "history SQL"
		statement = r.sql.lookup
		scanArgs = args
	}
	if r.mode == "chronological" {
		return prototypeRows(ctx, tx, statement, scanArgs...)
	}
	if r.smallRooms[p.ConversationID] {
		path += " (small room)"
		return prototypeRows(ctx, tx, statement, scanArgs...)
	}

	terms := search.Words(search.Fold(strings.TrimSpace(p.Search)))
	var ids []string
	var reason string
	var err error
	switch r.mode {
	case "fts5":
		ids, reason, err = prototypeFTS(ctx, tx, p, terms)
	case "bleve":
		ids, reason, err = prototypeBleve(ctx, r.bleve, p, terms)
	default:
		return nil, fmt.Errorf("unknown prototype arm %q", r.mode)
	}
	if err != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		reason = "index error"
	}
	if reason != "" || len(ids)+len(r.pending) > prototypeBudget {
		if reason == "" {
			reason = "candidate budget"
		}
		path += " (" + reason + ")"
		return prototypeRows(ctx, tx, statement, scanArgs...)
	}
	path = r.mode
	ids = append(ids, r.pending...)
	if len(ids) == 0 {
		return nil, nil
	}
	return prototypeRows(ctx, tx, r.sql.candidates, append(args, jsonText(ids))...)
}

func fuzzyBudget(term []rune) int {
	hasLetter := slices.ContainsFunc(term, unicode.IsLetter)
	switch {
	case !hasLetter || len(term) < 4:
		return 0
	case len(term) < 8:
		return 1
	default:
		return 2
	}
}

func prototypeFTS(ctx context.Context, tx *sql.Tx, p dbgen.SearchMessagesParams, terms []string) ([]string, string, error) {
	var groups []string
	for _, term := range terms {
		chars := []rune(term)
		count := fuzzyBudget(chars) + 1
		if len(chars) < count*4-1 {
			continue
		}
		blocks := make([]string, count)
		for i := range blocks {
			blocks[i] = "\"" + strings.ReplaceAll(string(chars[4*i:4*i+3]), "\"", "\"\"") + "\""
		}
		groups = append(groups, "("+strings.Join(blocks, " OR ")+")")
	}
	if len(groups) == 0 {
		return nil, "no safe term", nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT d.id FROM prototype_fts CROSS JOIN prototype_documents d ON d.docid=prototype_fts.rowid
WHERE prototype_fts MATCH ?
AND (?='' OR d.conversation=?) AND (?='' OR d.account=?)
AND (?='[]' OR d.account IN (SELECT value FROM json_each(?))) LIMIT ?`,
		strings.Join(groups, " AND "), p.ConversationID, p.ConversationID, p.AccountID, p.AccountID, p.AccountIds, p.AccountIds, prototypeBudget+1)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = rows.Close() }()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, "", err
		}
		ids = append(ids, id)
	}
	return ids, "", rows.Err()
}

func prototypeBleve(ctx context.Context, idx bleve.Index, p dbgen.SearchMessagesParams, terms []string) ([]string, string, error) {
	if idx == nil {
		return nil, "", fmt.Errorf("Bleve index unavailable")
	}
	var groups []query.Query
	for _, term := range terms {
		chars := []rune(term)
		if len(chars) < 3 {
			continue
		}
		distance := fuzzyBudget(chars)
		literal := prototypeTerm("gram", string(chars[:3]))
		if distance == 0 {
			groups = append(groups, literal)
		} else if len(chars)+distance <= prototypePrefixLimit {
			fuzzy := bleve.NewFuzzyQuery(term)
			fuzzy.SetField("prefix")
			fuzzy.SetPrefix(0)
			fuzzy.SetFuzziness(distance)
			groups = append(groups, bleve.NewDisjunctionQuery(literal, fuzzy))
		}
	}
	if len(groups) == 0 {
		return nil, "no safe term", nil
	}
	if p.ConversationID != "" {
		groups = append(groups, prototypeTerm("conversation", p.ConversationID))
	}
	if p.AccountID != "" {
		groups = append(groups, prototypeTerm("account", p.AccountID))
	}
	if p.AccountIds != "[]" {
		var accountIDs []string
		if err := json.Unmarshal([]byte(p.AccountIds), &accountIDs); err != nil {
			return nil, "", err
		}
		scopes := make([]query.Query, 0, len(accountIDs))
		for _, id := range accountIDs {
			scopes = append(scopes, prototypeTerm("account", id))
		}
		groups = append(groups, bleve.NewDisjunctionQuery(scopes...))
	}
	req := bleve.NewSearchRequestOptions(bleve.NewConjunctionQuery(groups...), prototypeBudget+1, 0, false)
	req.Score = bleve.ScoreNone
	result, err := idx.SearchInContext(ctx, req)
	if err != nil {
		return nil, "", err
	}
	if result.Status == nil || result.Status.Failed != 0 || len(result.Status.Errors) != 0 || result.TotalRelation == bleve.TotalRelationGte ||
		result.Total != uint64(len(result.Hits)) || len(result.Hits) > prototypeBudget {
		return nil, "candidate budget or partial index result", nil
	}
	ids := make([]string, 0, len(result.Hits))
	for _, hit := range result.Hits {
		ids = append(ids, hit.ID)
	}
	return ids, "", nil
}

func prototypeTerm(field, token string) *query.TermQuery {
	q := bleve.NewTermQuery(token)
	q.SetField(field)
	return q
}

func prototypeNewBleve(path string) (bleve.Index, error) {
	mapping := bleve.NewIndexMapping()
	doc := bleve.NewDocumentStaticMapping()
	for _, name := range []string{"gram", "prefix", "conversation", "account"} {
		field := bleve.NewKeywordFieldMapping()
		field.Store, field.DocValues, field.IncludeInAll, field.IncludeTermVectors = false, false, false, false
		field.SkipFreqNorm = true
		doc.AddFieldMappingsAt(name, field)
	}
	mapping.DefaultMapping = doc
	return bleve.NewUsing(path, mapping, scorch.Name, scorch.Name, nil)
}

func prototypeTokens(folded string) (prefixes, grams []string) {
	for _, word := range search.Words(folded) {
		chars := []rune(word)
		for n := 3; n <= min(len(chars), prototypePrefixLimit); n++ {
			prefixes = append(prefixes, string(chars[:n]))
		}
		for i := 0; i+3 <= len(chars); i++ {
			grams = append(grams, string(chars[i:i+3]))
		}
	}
	slices.Sort(prefixes)
	slices.Sort(grams)
	return slices.Compact(prefixes), slices.Compact(grams)
}

type prototypeSQL struct {
	lookup, candidates, chrono, room, projection string
}

func prototypeQueries(path string) (prototypeSQL, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return prototypeSQL{}, err
	}
	_, source, found := strings.Cut(string(data), "const searchMessages = `")
	if !found {
		return prototypeSQL{}, fmt.Errorf("generated message search SQL missing")
	}
	base, _, _ := strings.Cut(source, "`")
	replace := func(s, old, new string) string {
		if strings.Count(s, old) != 1 {
			panic("prototype SQL must be updated for: " + old)
		}
		return strings.Replace(s, old, new, 1)
	}
	lookup := replace(base, `WITH initial_revisions AS (
 SELECT entity_id,min(rowid) AS revision_row FROM message_revisions GROUP BY entity_id
), roots AS (`, "WITH roots AS (")
	lookup = replace(lookup, ` JOIN initial_revisions initial ON initial.entity_id=m.id
 JOIN message_revisions first_source ON first_source.rowid=initial.revision_row`, ` JOIN message_revisions first_source ON first_source.rowid=(SELECT min(rowid) FROM message_revisions WHERE entity_id=m.id)`)
	lookup = replace(lookup, ` LEFT JOIN initial_revisions head_initial ON head_initial.entity_id=h.source_id
 LEFT JOIN message_revisions head_source ON head_source.rowid=head_initial.revision_row`, ` LEFT JOIN message_revisions head_source ON head_source.rowid=(SELECT min(rowid) FROM message_revisions WHERE entity_id=h.source_id)`)
	lookup = replace(lookup, ` JOIN initial_revisions first ON first.entity_id=f.message_id
 JOIN message_revisions source ON source.rowid=first.revision_row`, ` JOIN message_revisions source ON source.rowid=(SELECT min(rowid) FROM message_revisions WHERE entity_id=f.message_id)`)
	candidates := replace(lookup, `WITH roots AS (`, `WITH candidate_ids AS MATERIALIZED (SELECT DISTINCT value AS id FROM json_each(?13)), roots AS (`)
	candidates = replace(candidates, " FROM messages m JOIN accounts", " FROM candidate_ids x CROSS JOIN messages m ON m.id=x.id JOIN accounts")
	// Build a superset of current/historical searchable text using exactly the
	// authoritative projection. The final SQL still verifies individual variants.
	head, _, found := strings.Cut(lookup, "), matched AS (")
	if !found {
		return prototypeSQL{}, fmt.Errorf("generated variants CTE missing")
	}
	projection := strings.NewReplacer("?7", "?1", "?8", "''", "?9", "'[]'", "?10", "''", "?11", "1").Replace(head) + `)
SELECT v.id,m.account_id,m.conversation_id,group_concat(COALESCE(v.body,'')||' '||v.metadata,' ')
FROM variants v JOIN messages m ON m.id=v.id GROUP BY v.id`

	metadata := strings.SplitN(strings.SplitN(lookup, " CAST(COALESCE((SELECT group_concat", 2)[1], " AS metadata", 2)[0]
	metadata = "CAST(COALESCE((SELECT group_concat" + metadata
	chrono := strings.ReplaceAll(prototypeChronological, "PROTOTYPE_METADATA", metadata)
	room := replace(chrono, "INDEXED BY prototype_timeline", "INDEXED BY messages_timeline")
	room = replace(room, "(CAST(?10 AS TEXT)='' OR m.conversation_id=CAST(?10 AS TEXT))", "m.conversation_id=CAST(?10 AS TEXT)")
	return prototypeSQL{lookup: lookup, candidates: candidates, chrono: chrono, room: room, projection: projection}, nil
}

const prototypeChronological = `WITH page AS MATERIALIZED (
 SELECT m.id,COALESCE(m.timeline_at_ms,m.created_at_ms) AS position_ms,m.timeline_at_ms,m.payload,m.account_id,m.conversation_id,h.source_id,
 CAST(json_extract(COALESCE(h.payload,m.payload),'$.OriginalText') AS TEXT) AS body, PROTOTYPE_METADATA AS metadata
 FROM messages m INDEXED BY prototype_timeline LEFT JOIN message_content_heads h ON h.canonical_id=m.id
 WHERE EXISTS(SELECT 1 FROM conversations c WHERE c.id=m.conversation_id AND json_extract(c.payload,'$.Request') IS NULL)
 AND EXISTS(SELECT 1 FROM message_revisions r WHERE r.entity_id=m.id)
 AND NOT EXISTS(SELECT 1 FROM message_canonical_aliases a WHERE a.alias_id=m.id)
 AND NOT EXISTS(SELECT 1 FROM hidden_scheduled_messages x WHERE x.message_id=m.id)
 AND (CAST(?8 AS TEXT)='' OR m.account_id=CAST(?8 AS TEXT))
 AND (CAST(?9 AS TEXT)='[]' OR m.account_id IN (SELECT value FROM json_each(CAST(?9 AS TEXT))))
 AND (CAST(?10 AS TEXT)='' OR m.conversation_id=CAST(?10 AS TEXT))
 AND COALESCE(m.timeline_at_ms,m.created_at_ms) <= ?2
 AND ((COALESCE(m.timeline_at_ms,m.created_at_ms),m.id,1,'','')<(CAST(?2 AS INTEGER),CAST(?1 AS TEXT),CAST(?3 AS INTEGER),CAST(?4 AS TEXT),CAST(?5 AS TEXT)))
 AND CAST(?11 AS INTEGER)=0
 AND oie_search_match(CAST(json_extract(COALESCE(h.payload,m.payload),'$.OriginalText') AS TEXT)||' '||PROTOTYPE_METADATA,CAST(?12 AS TEXT))=1
 ORDER BY COALESCE(m.timeline_at_ms,m.created_at_ms) DESC,m.id DESC LIMIT ?6
)
SELECT page.id,page.position_ms,page.timeline_at_ms,page.payload,a.label,a.address,c.payload,
 CAST(COALESCE(head_source.id,first_source.id) AS TEXT),CAST(COALESCE(head_source.created_at,first_source.created_at) AS TEXT),
 page.body,page.metadata,1,'','',CAST(oie_search_match(page.body,CAST(?12 AS TEXT)) AS INTEGER)
 FROM page JOIN accounts a ON a.id=page.account_id JOIN conversations c ON c.id=page.conversation_id
 JOIN message_revisions first_source ON first_source.rowid=(SELECT min(rowid) FROM message_revisions WHERE entity_id=page.id)
 LEFT JOIN message_revisions head_source ON head_source.rowid=(SELECT min(rowid) FROM message_revisions WHERE entity_id=page.source_id)
 ORDER BY page.position_ms DESC,page.id DESC`

func prototypeRows(ctx context.Context, tx *sql.Tx, statement string, args ...any) ([]dbgen.SearchMessagesRow, error) {
	rows, err := tx.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []dbgen.SearchMessagesRow
	for rows.Next() {
		var r dbgen.SearchMessagesRow
		if err := rows.Scan(&r.ID, &r.PositionMs, &r.TimelineAtMs, &r.Payload, &r.AccountLabel, &r.AccountAddress,
			&r.ConversationPayload, &r.MatchedRevisionID, &r.MatchedAt, &r.Body, &r.Metadata, &r.IsCurrent,
			&r.RevisionKeyAt, &r.RevisionKeyID, &r.TextMatch); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}
