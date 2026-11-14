package sqlite

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/search"
	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/index/scorch"
	"github.com/blevesearch/bleve/v2/search/query"
)

const (
	searchPrefixLimit    = 48
	searchCandidateLimit = 1024
	searchSmallRoom      = 512
)

func newMessageIndex(path string) (bleve.Index, error) {
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

func messageIndexTokens(folded string) (prefixes, grams []string) {
	for _, word := range search.Words(folded) {
		chars := []rune(word)
		for n := 3; n <= min(len(chars), searchPrefixLimit); n++ {
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

func messageIndexTerm(field, token string) *query.TermQuery {
	q := bleve.NewTermQuery(token)
	q.SetField(field)
	return q
}

func messageIndexQuery(p dbgen.SearchMessagesParams) (query.Query, error) {
	var groups []query.Query
	for _, term := range search.Words(search.Fold(p.Search)) {
		chars := []rune(term)
		if len(chars) < 3 {
			continue
		}
		distance := search.EditLimit(chars)
		literal := messageIndexTerm("gram", string(chars[:3]))
		if distance == 0 {
			groups = append(groups, literal)
		} else if len(chars)+distance <= searchPrefixLimit {
			fuzzy := bleve.NewFuzzyQuery(term)
			fuzzy.SetField("prefix")
			fuzzy.SetFuzziness(distance)
			fuzzy.SetPrefix(0)
			groups = append(groups, bleve.NewDisjunctionQuery(literal, fuzzy))
		}
	}
	if len(groups) == 0 {
		return nil, nil
	}
	if p.ConversationID != "" {
		groups = append(groups, messageIndexTerm("conversation", p.ConversationID))
	}
	if p.AccountID != "" {
		groups = append(groups, messageIndexTerm("account", p.AccountID))
	}
	if p.AccountIds != "[]" {
		var ids []string
		if err := json.Unmarshal([]byte(p.AccountIds), &ids); err != nil {
			return nil, err
		}
		scopes := make([]query.Query, 0, len(ids))
		for _, id := range ids {
			scopes = append(scopes, messageIndexTerm("account", id))
		}
		groups = append(groups, bleve.NewDisjunctionQuery(scopes...))
	}
	return bleve.NewConjunctionQuery(groups...), nil
}

func lookupMessageIndex(ctx context.Context, idx bleve.Index, q query.Query, budget int) ([]string, bool) {
	req := bleve.NewSearchRequestOptions(q, budget+1, 0, false)
	req.Score = bleve.ScoreNone
	result, err := idx.SearchInContext(ctx, req)
	if err != nil || result.Status == nil || result.Status.Failed != 0 ||
		len(result.Status.Errors) != 0 || result.TotalRelation != bleve.TotalRelationEq ||
		result.Total != uint64(len(result.Hits)) || len(result.Hits) > budget {
		return nil, false
	}
	ids := make([]string, 0, len(result.Hits))
	for _, hit := range result.Hits {
		ids = append(ids, hit.ID)
	}
	return ids, true
}
