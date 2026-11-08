package httpapi

import (
	"context"
	"errors"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func connection(c domain.Connection) engineapi.Connection {
	out := engineapi.Connection{AccountId: string(c.AccountID), State: engineapi.ConnectionState(c.State), UpdatedAt: c.UpdatedAt}
	if c.LastErrorCode != "" {
		code := engineapi.ConnectionLastErrorCode(c.LastErrorCode)
		out.LastErrorCode = &code
	}
	return out
}
func conversation(c domain.Conversation) engineapi.Conversation {
	return engineapi.Conversation{Id: string(c.ID), RevisionId: string(c.RevisionID), AccountId: string(c.AccountID), Kind: engineapi.ConversationKind(c.Kind), Address: c.Address, Title: c.Title, LastMessage: c.LastMessage, UpdatedAt: c.UpdatedAt, UnreadCount: c.UnreadCount, Archived: c.Archived, Favorite: c.Favorite}
}
func message(m domain.Message) engineapi.Message {
	out := engineapi.Message{Id: string(m.ID), RevisionId: string(m.RevisionID), AccountId: string(m.AccountID), ConversationId: string(m.ConversationID), Sender: engineapi.Sender{Id: string(m.Sender.ID), DisplayName: m.Sender.DisplayName}, OriginalText: m.OriginalText, CreatedAt: m.CreatedAt, IsOwn: m.IsOwn, SendState: engineapi.MessageSendState(m.SendState)}
	if m.OutboundReply != nil {
		target := string(m.OutboundReply.TargetID)
		out.ReplyToMessageId = &target
		if m.Reply == nil {
			out.Reply = &engineapi.MessageReply{Available: false, Excerpt: m.OutboundReply.Excerpt, BodyText: m.OutboundReply.BodyText}
		}
	}
	if m.Historical {
		v := true
		out.Historical = &v
	}
	if m.Reply != nil {
		r := m.Reply
		out.Reply = &engineapi.MessageReply{Available: r.Available, Excerpt: r.Excerpt, BodyText: r.BodyText}
		if r.Available {
			id, name := string(r.MessageID), r.SenderName
			out.Reply.MessageId = &id
			out.Reply.SenderName = &name
		}
	}
	return out
}
func (s *Server) currentAccount(ctx context.Context, a domain.Account) engineapi.Account {
	out := account(a)
	state, err := s.engine.Connection(ctx, a.ID)
	if err == nil {
		out.ConnectionState = engineapi.AccountConnectionState(state.State)
	}
	return out
}
func (s *Server) GetAccountConnection(ctx context.Context, r engineapi.GetAccountConnectionRequestObject) (engineapi.GetAccountConnectionResponseObject, error) {
	c, err := s.engine.Connection(ctx, domain.ID(r.AccountId))
	if err != nil {
		return nil, err
	}
	return engineapi.GetAccountConnection200JSONResponse(connection(c)), nil
}
func (s *Server) ConfigureAccountConnection(ctx context.Context, r engineapi.ConfigureAccountConnectionRequestObject) (engineapi.ConfigureAccountConnectionResponseObject, error) {
	if r.Body == nil || r.Body.Password == nil {
		return nil, application.ErrInvalid
	}
	in := application.ConnectionInput{Password: *r.Body.Password}
	if r.Body.Endpoint != nil {
		in.Endpoint = *r.Body.Endpoint
	}
	c, err := s.engine.ConfigureConnection(ctx, domain.ID(r.AccountId), in)
	if err != nil {
		return nil, err
	}
	return engineapi.ConfigureAccountConnection202JSONResponse(connection(c)), nil
}
func (s *Server) DisconnectAccount(ctx context.Context, r engineapi.DisconnectAccountRequestObject) (engineapi.DisconnectAccountResponseObject, error) {
	c, err := s.engine.Disconnect(ctx, domain.ID(r.AccountId))
	if err != nil {
		return nil, err
	}
	return engineapi.DisconnectAccount200JSONResponse(connection(c)), nil
}
func (s *Server) OpenConversation(ctx context.Context, r engineapi.OpenConversationRequestObject) (engineapi.OpenConversationResponseObject, error) {
	if r.Body == nil {
		return nil, application.ErrInvalid
	}
	result, err := s.engine.OpenConversation(ctx, domain.ID(r.Params.IdempotencyKey), domain.ID(r.Body.AccountId), r.Body.Address)
	if err != nil {
		return nil, err
	}
	if result.Replayed || result.Existing {
		return engineapi.OpenConversation200JSONResponse(conversation(result.Conversation)), nil
	}
	return engineapi.OpenConversation201JSONResponse(conversation(result.Conversation)), nil
}
func (s *Server) GetConversation(ctx context.Context, r engineapi.GetConversationRequestObject) (engineapi.GetConversationResponseObject, error) {
	c, err := s.engine.Conversation(ctx, domain.ID(r.ConversationId))
	if err != nil {
		return nil, err
	}
	return engineapi.GetConversation200JSONResponse(conversation(c)), nil
}
func (s *Server) ListConversations(ctx context.Context, r engineapi.ListConversationsRequestObject) (engineapi.ListConversationsResponseObject, error) {
	in := application.ConversationQuery{Limit: 50}
	p := r.Params
	cursorValue := ""
	if p.Limit != nil {
		in.Limit = *p.Limit
	}
	if p.Cursor != nil {
		cursorValue = *p.Cursor
	}
	if p.AccountId != nil {
		in.AccountID = domain.ID(*p.AccountId)
	}
	if p.Q != nil {
		in.Query = *p.Q
	}
	if p.Kind != nil {
		in.Kind = string(*p.Kind)
	}
	if p.Archived != nil {
		in.Archived = *p.Archived
	}
	if p.FavoritesOnly != nil {
		in.FavoritesOnly = *p.FavoritesOnly
	}
	if p.UnreadOnly != nil {
		in.UnreadOnly = *p.UnreadOnly
	}
	list, err := s.engine.Conversations(ctx, cursorValue, in)
	if err != nil {
		return nil, err
	}
	page := engineapi.ConversationPage{Items: make([]engineapi.Conversation, 0, len(list))}
	if len(list) > in.Limit {
		state, err := s.engine.Snapshot(ctx)
		if err != nil {
			return nil, err
		}
		next := cursor(state.ID, list[in.Limit-1].ActivitySequence)
		page.NextCursor = &next
		list = list[:in.Limit]
	}
	for _, c := range list {
		page.Items = append(page.Items, conversation(c))
	}
	return engineapi.ListConversations200JSONResponse(page), nil
}
func (s *Server) SendMessage(ctx context.Context, r engineapi.SendMessageRequestObject) (engineapi.SendMessageResponseObject, error) {
	if r.Body == nil {
		return nil, application.ErrInvalid
	}
	target := domain.ID("")
	if r.Body.ReplyToMessageId != nil {
		target = domain.ID(*r.Body.ReplyToMessageId)
	}
	result, err := s.engine.SendMessageWithReply(ctx, domain.ID(r.Params.IdempotencyKey), domain.ID(r.ConversationId), r.Body.OriginalText, target)
	if errors.Is(err, application.ErrRoomNotJoined) {
		if current, readErr := s.engine.RoomSession(ctx, domain.ID(r.ConversationId)); readErr == nil {
			return nil, application.RoomStateConflict{Current: current}
		}
	}
	if err != nil {
		return nil, err
	}
	if result.Replayed {
		return engineapi.SendMessage200JSONResponse(message(result.Message)), nil
	}
	return engineapi.SendMessage202JSONResponse(message(result.Message)), nil
}
func (s *Server) GetMessage(ctx context.Context, r engineapi.GetMessageRequestObject) (engineapi.GetMessageResponseObject, error) {
	m, err := s.engine.Message(ctx, domain.ID(r.MessageId))
	if err != nil {
		return nil, err
	}
	return engineapi.GetMessage200JSONResponse(message(m)), nil
}
func (s *Server) ListMessages(ctx context.Context, r engineapi.ListMessagesRequestObject) (engineapi.ListMessagesResponseObject, error) {
	limit := 50
	var cursor domain.ID
	if r.Params.Limit != nil {
		limit = *r.Params.Limit
	}
	if r.Params.Cursor != nil {
		cursor = domain.ID(*r.Params.Cursor)
	}
	list, err := s.engine.Messages(ctx, domain.ID(r.ConversationId), cursor, limit)
	if err != nil {
		return nil, err
	}
	page := engineapi.MessagePage{Items: make([]engineapi.Message, 0, len(list))}
	if len(list) > limit {
		next := string(list[limit-1].ID)
		page.NextCursor = &next
		list = list[:limit]
	}
	for _, m := range list {
		page.Items = append(page.Items, message(m))
	}
	return engineapi.ListMessages200JSONResponse(page), nil
}
func (s *Server) CancelMessage(ctx context.Context, r engineapi.CancelMessageRequestObject) (engineapi.CancelMessageResponseObject, error) {
	if r.Body == nil {
		return nil, application.ErrInvalid
	}
	result, err := s.engine.CancelMessage(ctx, domain.ID(r.Params.IdempotencyKey), domain.ID(r.MessageId), domain.ID(r.Body.ExpectedRevisionId))
	if err != nil {
		return nil, err
	}
	return engineapi.CancelMessage200JSONResponse(message(result.Message)), nil
}
func event(e application.Event, engineID domain.ID) engineapi.Event {
	out := engineapi.Event{Id: cursor(engineID, e.Sequence), Type: engineapi.EventType(e.Type), OccurredAt: e.OccurredAt}
	if e.Account != nil {
		v := account(*e.Account)
		out.Account = &v
	}
	if e.Conversation != nil {
		v := conversation(*e.Conversation)
		out.Conversation = &v
	}
	if e.Message != nil {
		v := message(*e.Message)
		out.Message = &v
	}
	if e.Connection != nil {
		v := connection(*e.Connection)
		out.Connection = &v
	}
	if e.RoomSession != nil {
		value := roomSession(*e.RoomSession)
		out.RoomSession = &value
	}
	if e.Preview != nil {
		v := preview(*e.Preview)
		out.Preview = &v
	}
	if e.Star != nil {
		v := star(*e.Star)
		out.Star = &v
	}
	if e.Avatar != nil {
		v := avatar(*e.Avatar)
		out.Avatar = &v
	}
	if e.History != nil {
		v := history(*e.History)
		out.History = &v
	}
	return out
}

func history(h domain.HistoryStatus) engineapi.HistoryStatus {
	out := engineapi.HistoryStatus{AccountId: string(h.AccountID), State: engineapi.HistoryStatusState(h.State), RetainedMessages: h.RetainedMessages, Conversations: h.Conversations, UpdatedAt: h.UpdatedAt}
	if h.LastErrorCode != "" {
		code := engineapi.HistoryStatusLastErrorCode(h.LastErrorCode)
		out.LastErrorCode = &code
	}
	return out
}
func (s *Server) GetAccountHistory(ctx context.Context, r engineapi.GetAccountHistoryRequestObject) (engineapi.GetAccountHistoryResponseObject, error) {
	h, err := s.engine.History(ctx, domain.ID(r.AccountId))
	if err != nil {
		return nil, err
	}
	return engineapi.GetAccountHistory200JSONResponse(history(h)), nil
}
