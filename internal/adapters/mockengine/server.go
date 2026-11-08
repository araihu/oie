// Package mockengine is an independent, read-only development server. It owns
// sample data only and never opens SQLite or connects a protocol account.
package mockengine

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/adapters/previewapi"
	"github.com/getkin/kin-openapi/routers"
	middleware "github.com/oapi-codegen/nethttp-middleware"
)

//go:embed fixtures/*.json
var files embed.FS

type localeKey struct{}
type Server struct {
	empty    bool
	catalogs map[string]map[string]string
}

func id(n int) string { return fmt.Sprintf("019a5df0-0000-7000-8000-%012d", n) }

func New(empty bool) (http.Handler, error) {
	s := &Server{empty: empty, catalogs: map[string]map[string]string{}}
	for _, lang := range []string{"en", "pt-BR"} {
		data, err := files.ReadFile("fixtures/" + lang + ".json")
		if err != nil {
			return nil, err
		}
		var text map[string]string
		if err = json.Unmarshal(data, &text); err != nil {
			return nil, err
		}
		s.catalogs[lang] = text
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, engineapi.Health{Status: "ready"}) })
	mux.HandleFunc("GET /api/v1/engine", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, engineapi.Engine{Id: id(1), Role: "hosted", ApiVersion: "0.1.0", EventCursor: id(1) + ":0"})
	})
	mux.HandleFunc("GET /api/v1/accounts", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, engineapi.AccountPage{Items: []engineapi.Account{{Id: id(2), RevisionId: id(3), Protocol: "xmpp", Address: "alice@localhost", Label: "Personal", CreatedAt: time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC), ConnectionState: "not_connected"}}})
	})
	schema, err := previewapi.GetSpec()
	if err != nil {
		return nil, err
	}
	schema.Servers = nil
	validated := middleware.OapiRequestValidatorWithOptions(schema, &middleware.Options{ErrorHandlerWithOpts: func(_ context.Context, err error, w http.ResponseWriter, _ *http.Request, opts middleware.ErrorHandlerOpts) {
		status, code := 400, "invalid_request"
		if errors.Is(err, routers.ErrMethodNotAllowed) {
			status = 405
		} else if opts.StatusCode == 404 {
			status = 404
			code = "not_found"
		}
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(problem(status, code))
	}})(previewapi.Handler(previewapi.NewStrictHandler(s, nil)))
	mux.Handle("/api/v1/", validated)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Oie-Mock", "true")
		w.Header().Set("Cache-Control", "no-store")
		lang := "en"
		if strings.HasPrefix(r.Header.Get("Accept-Language"), "pt") {
			lang = "pt-BR"
		}
		mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), localeKey{}, lang)))
	}), nil
}
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func (s *Server) text(ctx context.Context) map[string]string {
	lang, _ := ctx.Value(localeKey{}).(string)
	if lang == "" {
		lang = "en"
	}
	return s.catalogs[lang]
}
func (s *Server) chats(ctx context.Context) []previewapi.Conversation {
	t := s.text(ctx)
	type fixture struct {
		title, initials, last     string
		unread                    int
		group, favorite, archived bool
	}
	values := []fixture{
		{t["Saved"], "ME", t["PreviewSix"], 0, false, true, false},
		{t["Team"], "OI", "Ana: " + t["PreviewWork"], 3, true, false, false},
		{t["Home"], "CA", "Nina: " + t["PreviewFive"], 0, true, true, false},
		{"Nina Martins", "NM", t["PreviewNina"], 2, false, false, false},
		{t["Music"], "SC", "Pedro: " + t["PreviewMusic"], 0, true, false, false},
		{"Ana Costa", "AC", t["PreviewTwo"], 0, false, true, false},
		{t["Family"], "FA", "Clara: " + t["PreviewFamily"], 1, true, false, false},
		{"Pedro Almeida", "PA", t["PreviewCoffee"], 0, false, false, false},
		{t["Books"], "CL", t["PreviewBook"], 0, true, false, false},
		{"Luiza Pereira", "LP", t["PreviewNina"], 0, false, false, false},
		{t["Coffee"], "CC", t["PreviewCoffee"], 0, true, false, true},
	}
	out := make([]previewapi.Conversation, 0)
	if s.empty {
		return out
	}
	for i, f := range values {
		kind := previewapi.ConversationKindDirect
		participants := []string{f.title}
		if f.group {
			kind = previewapi.ConversationKindRoom
			participants = []string{"Ana", "Pedro", "Nina", t["You"]}
		}
		out = append(out, previewapi.Conversation{Id: id(100 + i), RevisionId: id(200 + i), AccountId: id(2), Kind: kind, Address: fmt.Sprintf("sample-%d@localhost", i), Title: f.title, Initials: &f.initials, LastMessage: f.last, UpdatedAt: time.Date(2026, 11, 7, 14, 42-i*4, 0, 0, time.UTC), UnreadCount: f.unread, Archived: f.archived, Favorite: f.favorite, Participants: &participants})
	}
	// Preview ordering puts the illustrated room first, without UI fixture knowledge.
	out[0], out[2] = out[2], out[0]
	for i := range out {
		out[i].UpdatedAt = time.Date(2026, 11, 7, 14, 42-i*4, 0, 0, time.UTC)
	}
	return out
}
func yes(v *bool) bool { return v != nil && *v }
func problem(status int, code string) previewapi.Problem {
	return previewapi.Problem{Type: "urn:oie:problem:" + code, Status: status, Title: http.StatusText(status), Code: previewapi.ProblemCode(code)}
}
func (s *Server) ListConversations(ctx context.Context, r previewapi.ListConversationsRequestObject) (previewapi.ListConversationsResponseObject, error) {
	all := s.chats(ctx)
	start := 0
	if r.Params.Cursor != nil {
		engine, sequence, ok := strings.Cut(*r.Params.Cursor, ":")
		n, err := strconv.Atoi(sequence)
		if !ok || engine != id(1) || err != nil || n < 0 || n > len(all) {
			return previewapi.ListConversations409ApplicationProblemPlusJSONResponse{ConflictApplicationProblemPlusJSONResponse: previewapi.ConflictApplicationProblemPlusJSONResponse(problem(409, "invalid_cursor"))}, nil
		}
		start = n
	}
	out := []previewapi.Conversation{}
	indices := []int{}
	for index, c := range all {
		if index < start {
			continue
		}
		if c.Archived != yes(r.Params.Archived) || yes(r.Params.UnreadOnly) && c.UnreadCount == 0 || yes(r.Params.FavoritesOnly) && !c.Favorite {
			continue
		}
		if r.Params.AccountId != nil && c.AccountId != *r.Params.AccountId {
			continue
		}
		if r.Params.Kind != nil && string(c.Kind) != string(*r.Params.Kind) {
			continue
		}
		if r.Params.Q != nil && !strings.Contains(strings.ToLower(c.Address+" "+c.Title+" "+c.LastMessage), strings.ToLower(*r.Params.Q)) {
			continue
		}
		out = append(out, c)
		indices = append(indices, index)
	}
	page := previewapi.ConversationPage{Items: out}
	limit := pageLimit(r.Params.Limit)
	if len(out) > limit {
		page.Items = out[:limit]
		cursor := id(1) + ":" + strconv.Itoa(indices[limit-1]+1)
		page.NextCursor = &cursor
	}
	return previewapi.ListConversations200JSONResponse(page), nil
}
func pageLimit(v *int) int {
	if v != nil && *v > 0 && *v <= 100 {
		return *v
	}
	return 50
}
func (s *Server) find(ctx context.Context, id string) (*previewapi.Conversation, int) {
	for index, c := range s.chats(ctx) {
		if c.Id == id {
			return &c, index
		}
	}
	return nil, 0
}
func (s *Server) GetConversation(ctx context.Context, r previewapi.GetConversationRequestObject) (previewapi.GetConversationResponseObject, error) {
	c, _ := s.find(ctx, r.ConversationId)
	if c == nil {
		return previewapi.GetConversation404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse: previewapi.NotFoundApplicationProblemPlusJSONResponse(problem(404, "not_found"))}, nil
	}
	return previewapi.GetConversation200JSONResponse(*c), nil
}
func (s *Server) ListMessages(ctx context.Context, r previewapi.ListMessagesRequestObject) (previewapi.ListMessagesResponseObject, error) {
	c, chatIndex := s.find(ctx, r.ConversationId)
	if c == nil {
		return previewapi.ListMessages404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse: previewapi.NotFoundApplicationProblemPlusJSONResponse(problem(404, "not_found"))}, nil
	}
	t := s.text(ctx)
	type line struct {
		name, text string
		own, media bool
	}
	lines := []line{{c.Title, c.LastMessage, false, false}}
	if c.Id == id(102) {
		lines = []line{{"Ana", t["PreviewOne"], false, false}, {"Nina", t["PreviewTwo"], false, false}, {"Nina", t["PreviewThree"], false, false}, {"Nina", "", false, true}, {t["You"], t["PreviewFour"], true, false}, {"Nina", t["PreviewFive"], false, false}, {t["You"], t["PreviewSix"], true, false}}
	}
	out := []previewapi.Message{}
	for i, l := range lines {
		senderID := id(400 + chatIndex)
		if l.name == "Ana" {
			senderID = id(300)
		}
		if l.name == "Nina" {
			senderID = id(301)
		}
		if l.own {
			senderID = id(302)
		}
		state := previewapi.MessageSendState("received")
		if l.own {
			state = "unknown"
		}
		m := previewapi.Message{Id: id(1000 + chatIndex*100 + i), RevisionId: id(10000 + chatIndex*100 + i), AccountId: id(2), ConversationId: c.Id, Sender: previewapi.Sender{Id: senderID, DisplayName: l.name}, OriginalText: l.text, CreatedAt: time.Date(2026, 11, 7, 14, 24+i*2, 0, 0, time.UTC), IsOwn: l.own, SendState: state}
		if l.media {
			m.Attachment = &previewapi.Attachment{Kind: previewapi.Image, Title: t["Trail"], Description: t["TrailBody"], PreviewUrl: "/ui/preview-landscape.svg"}
		}
		out = append(out, m)
	}
	slices.Reverse(out)
	items := []previewapi.Message{}
	for _, m := range out {
		if r.Params.Cursor != nil && m.Id >= *r.Params.Cursor {
			continue
		}
		items = append(items, m)
	}
	page := previewapi.MessagePage{Items: items}
	limit := pageLimit(r.Params.Limit)
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1].Id
		page.NextCursor = &last
	}
	return previewapi.ListMessages200JSONResponse(page), nil
}
