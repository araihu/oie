// Package httpapi translates generated HTTP contracts into application use cases.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"github.com/getkin/kin-openapi/routers"
	middleware "github.com/oapi-codegen/nethttp-middleware"
)

type Server struct{ engine *application.Engine }

func New(engine *application.Engine) (http.Handler, error) {
	schema, err := engineapi.GetSpec()
	if err != nil {
		return nil, err
	}
	schema.Servers = nil
	s := &Server{engine: engine}
	handler := engineapi.HandlerWithOptions(engineapi.NewStrictHandlerWithOptions(s, nil, engineapi.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, _ error) {
			WriteProblem(w, application.ErrInvalid)
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			WriteProblem(w, err)
		},
	}), engineapi.StdHTTPServerOptions{
		ErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, _ error) {
			WriteProblem(w, application.ErrInvalid)
		},
	})
	validated := middleware.OapiRequestValidatorWithOptions(schema, &middleware.Options{
		ErrorHandlerWithOpts: func(_ context.Context, err error, w http.ResponseWriter, _ *http.Request, opts middleware.ErrorHandlerOpts) {
			if errors.Is(err, routers.ErrMethodNotAllowed) {
				writeProblem(w, http.StatusMethodNotAllowed, "invalid_request")
				return
			}
			if opts.StatusCode == http.StatusNotFound {
				WriteProblem(w, application.ErrNotFound)
				return
			}
			WriteProblem(w, application.ErrInvalid)
		},
	})(handler)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		validated.ServeHTTP(w, r)
	}), nil
}

// WriteProblem deliberately exposes stable codes, never raw database/input errors.
func WriteProblem(w http.ResponseWriter, err error) {
	status, code := http.StatusServiceUnavailable, "unavailable"
	switch {
	case errors.Is(err, application.ErrInvalid):
		status, code = http.StatusBadRequest, "invalid_request"
	case errors.Is(err, application.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, application.ErrIdempotencyConflict):
		status, code = http.StatusConflict, "idempotency_conflict"
	case errors.Is(err, application.ErrAccountExists):
		status, code = http.StatusConflict, "account_exists"
	case errors.Is(err, application.ErrStateConflict):
		status, code = http.StatusConflict, "state_conflict"
	case errors.Is(err, application.ErrProtocolUnavailable):
		status, code = http.StatusServiceUnavailable, "protocol_unavailable"
	case errors.Is(err, application.ErrInvalidCursor):
		status, code = http.StatusConflict, "invalid_cursor"
	}
	var conflict application.StateConflict
	var starConflict application.StarConflict
	if errors.As(err, &starConflict) {
		current := star(starConflict.Current)
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(engineapi.Problem{Type: "urn:oie:problem:" + code, Title: http.StatusText(status), Status: status, Code: engineapi.ProblemCode(code), CurrentStar: &current})
		return
	}
	if errors.As(err, &conflict) {
		current := message(conflict.Current)
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(engineapi.Problem{Type: "urn:oie:problem:" + code, Title: http.StatusText(status), Status: status, Code: engineapi.ProblemCode(code), CurrentMessage: &current})
		return
	}
	writeProblem(w, status, code)
}

func writeProblem(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(engineapi.Problem{
		Type: "urn:oie:problem:" + code, Title: http.StatusText(status),
		Status: status, Code: engineapi.ProblemCode(code),
	})
}

func (s *Server) GetHealth(ctx context.Context, _ engineapi.GetHealthRequestObject) (engineapi.GetHealthResponseObject, error) {
	if err := s.engine.Ping(ctx); err != nil {
		return nil, err
	}
	return engineapi.GetHealth200JSONResponse{Status: "ready"}, nil
}

func cursor(id domain.ID, sequence int64) string {
	return fmt.Sprintf("%s:%d", id, sequence)
}

func (s *Server) GetEngine(ctx context.Context, _ engineapi.GetEngineRequestObject) (engineapi.GetEngineResponseObject, error) {
	state, err := s.engine.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return engineapi.GetEngine200JSONResponse{
		Id: string(state.ID), Role: engineapi.EngineRole(state.Role),
		ApiVersion: "0.6.0", EventCursor: cursor(state.ID, state.Sequence),
	}, nil
}

func account(a domain.Account) engineapi.Account {
	return engineapi.Account{
		Id: string(a.ID), RevisionId: string(a.RevisionID), Protocol: engineapi.AccountProtocol(a.Protocol),
		Address: a.Address, Label: a.Label, CreatedAt: a.CreatedAt.UTC(), ConnectionState: "not_connected",
	}
}

func (s *Server) RegisterAccount(ctx context.Context, r engineapi.RegisterAccountRequestObject) (engineapi.RegisterAccountResponseObject, error) {
	if r.Body == nil {
		return nil, application.ErrInvalid
	}
	result, err := s.engine.Register(ctx, domain.ID(r.Params.IdempotencyKey), application.RegisterInput{
		Protocol: string(r.Body.Protocol), Address: r.Body.Address, Label: r.Body.Label,
	})
	if err != nil {
		return nil, err
	}
	if result.Replayed {
		return engineapi.RegisterAccount200JSONResponse{Body: account(result.Account),
			Headers: engineapi.RegisterAccount200ResponseHeaders{IdempotencyReplayed: &result.Replayed}}, nil
	}
	location := "/api/v1/accounts/" + string(result.Account.ID)
	return engineapi.RegisterAccount201JSONResponse{Body: account(result.Account),
		Headers: engineapi.RegisterAccount201ResponseHeaders{Location: &location, IdempotencyReplayed: &result.Replayed}}, nil
}

func (s *Server) GetAccount(ctx context.Context, r engineapi.GetAccountRequestObject) (engineapi.GetAccountResponseObject, error) {
	a, err := s.engine.Account(ctx, domain.ID(r.AccountId))
	if err != nil {
		return nil, err
	}
	return engineapi.GetAccount200JSONResponse(s.currentAccount(ctx, a)), nil
}

func (s *Server) ListAccounts(ctx context.Context, r engineapi.ListAccountsRequestObject) (engineapi.ListAccountsResponseObject, error) {
	limit := 50
	if r.Params.Limit != nil {
		limit = *r.Params.Limit
	}
	var after domain.ID
	if r.Params.After != nil {
		after = domain.ID(*r.Params.After)
	}
	list, err := s.engine.Accounts(ctx, after, limit)
	if err != nil {
		return nil, err
	}
	page := engineapi.AccountPage{Items: make([]engineapi.Account, 0)}
	if len(list) > limit {
		next := string(list[limit-1].ID)
		page.NextCursor = &next
		list = list[:limit]
	}
	for _, a := range list {
		page.Items = append(page.Items, s.currentAccount(ctx, a))
	}
	return engineapi.ListAccounts200JSONResponse(page), nil
}

func (s *Server) GetAccountCapabilities(ctx context.Context, r engineapi.GetAccountCapabilitiesRequestObject) (engineapi.GetAccountCapabilitiesResponseObject, error) {
	a, err := s.engine.Account(ctx, domain.ID(r.AccountId))
	if err != nil {
		return nil, err
	}
	state, err := s.engine.Connection(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	result := engineapi.Capabilities{AccountId: string(a.ID), State: "unknown", Reason: "not_connected"}
	if state.State == "connected" {
		supported := true
		result.State = "known"
		result.Reason = "adapter_connected"
		result.DirectText = &supported
	}
	return engineapi.GetAccountCapabilities200JSONResponse(result), nil
}

func (s *Server) GetOperation(ctx context.Context, r engineapi.GetOperationRequestObject) (engineapi.GetOperationResponseObject, error) {
	op, err := s.engine.Operation(ctx, domain.ID(r.OperationId))
	if err != nil {
		return nil, err
	}
	return engineapi.GetOperation200JSONResponse{
		Id: string(op.ID), ResourceId: string(op.ResourceID), CreatedAt: op.CreatedAt,
		Kind: engineapi.OperationKind(op.Kind), Status: "succeeded",
	}, nil
}

func (s *Server) StreamEvents(ctx context.Context, r engineapi.StreamEventsRequestObject) (engineapi.StreamEventsResponseObject, error) {
	state, err := s.engine.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	after := int64(0)
	if r.Params.LastEventID != nil {
		id, sequence, ok := strings.Cut(*r.Params.LastEventID, ":")
		if !ok || id != string(state.ID) {
			return nil, application.ErrInvalidCursor
		}
		after, err = strconv.ParseInt(sequence, 10, 64)
		if err != nil || after < 0 || after > state.Sequence || strconv.FormatInt(after, 10) != sequence {
			return nil, application.ErrInvalidCursor
		}
	}
	return stream{ctx: ctx, engine: s.engine, engineID: state.ID, after: after}, nil
}

type stream struct {
	ctx      context.Context
	engine   *application.Engine
	engineID domain.ID
	after    int64
}

func (s stream) VisitStreamEventsResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	control := http.NewResponseController(w)
	write := func(text string) bool {
		_ = control.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := fmt.Fprint(w, text); err != nil {
			return false
		}
		return control.Flush() == nil
	}
	defer func() { _ = control.SetWriteDeadline(time.Time{}) }()
	if !write(": connected\n\n") {
		return nil
	}
	poll := time.NewTicker(250 * time.Millisecond)
	heartbeat := time.NewTicker(15 * time.Second)
	defer poll.Stop()
	defer heartbeat.Stop()
	for {
		events, err := s.engine.Events(s.ctx, s.after)
		if err != nil {
			// The status is already 200. Close for reconnect; do not append a
			// misleading problem+json response inside an established SSE stream.
			return nil
		}
		for _, e := range events {
			id := cursor(s.engineID, e.Sequence)
			data, err := json.Marshal(event(e, s.engineID))
			if err != nil || !write(fmt.Sprintf("id: %s\nevent: %s\ndata: %s\n\n", id, e.Type, data)) {
				return nil
			}
			s.after = e.Sequence
		}
		if len(events) == 100 {
			continue
		}
		select {
		case <-s.ctx.Done():
			return nil
		case <-heartbeat.C:
			if !write(": heartbeat\n\n") {
				return nil
			}
		case <-poll.C:
		}
	}
}
