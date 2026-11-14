package httpapi

import (
	"context"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/domain"
)

func presenceObservation(p domain.PresenceObservation) engineapi.PresenceObservation {
	out := engineapi.PresenceObservation{State: engineapi.PresenceObservationState(p.State)}
	if p.StatusText != "" {
		out.StatusText = &p.StatusText
	}
	if !p.ObservedAt.IsZero() {
		out.ObservedAt = &p.ObservedAt
	}
	return out
}
func (s *Server) GetConversationDetails(ctx context.Context, r engineapi.GetConversationDetailsRequestObject) (engineapi.GetConversationDetailsResponseObject, error) {
	d, err := s.engine.ConversationDetails(ctx, domain.ID(r.ConversationId))
	if err != nil {
		return nil, err
	}
	return engineapi.GetConversationDetails200JSONResponse(conversationDetails(d)), nil
}

func conversationDetails(d domain.ConversationDetails) engineapi.ConversationDetails {
	out := engineapi.ConversationDetails{ConversationId: string(d.ConversationID), AccountId: string(d.AccountID), Address: d.Address, Kind: engineapi.ConversationDetailsKind(d.Kind), AccountConnection: connection(d.AccountConnection), State: engineapi.DetailState(d.State), OccupantCount: d.OccupantCount, Features: []engineapi.ConversationDetailsFeatures{}, Participants: engineapi.ConversationParticipants{Scope: engineapi.ConversationParticipantsScope(d.Participants.Scope), State: engineapi.DetailState(d.Participants.State), Truncated: d.Participants.Truncated, Items: []engineapi.ConversationParticipant{}}}
	if d.Name != "" {
		out.Name = &d.Name
	}
	if d.Description != "" {
		out.Description = &d.Description
	}
	if d.Topic != "" {
		out.Topic = &d.Topic
	}
	if d.RoomSession != "" {
		session := engineapi.ConversationDetailsRoomSession(d.RoomSession)
		out.RoomSession = &session
	}
	if !d.CheckedAt.IsZero() {
		out.CheckedAt = &d.CheckedAt
	}
	if !d.Participants.CheckedAt.IsZero() {
		out.Participants.CheckedAt = &d.Participants.CheckedAt
	}
	if d.PeerPresence != nil {
		presence := presenceObservation(*d.PeerPresence)
		out.PeerPresence = &presence
	}
	for _, f := range d.Features {
		out.Features = append(out.Features, engineapi.ConversationDetailsFeatures(f))
	}
	for _, p := range d.Participants.Items {
		out.Participants.Items = append(out.Participants.Items, engineapi.ConversationParticipant{Id: p.ID, DisplayName: p.DisplayName, Source: engineapi.ConversationParticipantSource(p.Source), Presence: presenceObservation(p.Presence)})
	}
	return out
}
