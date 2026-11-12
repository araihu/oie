package xmpp

import (
	"context"
	"errors"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

type directRoomInvitation struct {
	Room     string `xml:"jid,attr"`
	Password string `xml:"password,attr"`
}

func (m *Manager) receiveRoomInvitation(ctx context.Context, e *entry, msg stanza.Message, muc *mucUser, direct []directRoomInvitation) (bool, error) {
	mediated := muc != nil && len(muc.Invites) != 0
	if !mediated && len(direct) == 0 {
		return false, nil
	}
	host, ok := m.host.(application.ConversationRequestHost)
	own, _ := jid.Parse(e.account.Address)
	if !ok || msg.Type != "" && msg.Type != stanza.NormalMessage && msg.Type != stanza.ChatMessage ||
		msg.To.String() != "" && !msg.To.Bare().Equal(own) || msg.From.Localpart() == "" || msg.From.Bare().Equal(own) {
		return true, nil
	}
	room, password := msg.From, ""
	if mediated {
		if len(muc.Invites) != 1 || len(muc.Passwords) > 1 || len(direct) > 1 {
			return true, nil
		}
		if len(muc.Passwords) == 1 {
			password = muc.Passwords[0]
		}
	} else {
		if len(direct) != 1 {
			return true, nil
		}
		var err error
		room, err = jid.Parse(direct[0].Room)
		if err != nil {
			return true, nil
		}
		password = direct[0].Password
	}
	if room.Localpart() == "" || room.Domainpart() == "" || room.Resourcepart() != "" || room.Equal(own) {
		return true, nil
	}
	if err := host.ReceiveRoomInvitation(ctx, e.account.ID, room.String(), password); err != nil && !errors.Is(err, application.ErrInvalid) {
		return true, err
	}
	return true, nil
}

func (m *Manager) checkDirectRequests(ctx context.Context, host application.ConversationRequestHost, e *entry, session *mx.Session, wake <-chan struct{}) {
	for ctx.Err() == nil {
		checks, err := host.PendingDirectHistoryChecks(ctx, e.account.ID)
		if err == nil && len(checks) > 0 {
			for _, chat := range checks {
				if chat.DirectHistoryCheck == nil {
					continue
				}
				call, cancel := context.WithTimeout(ctx, 6*time.Second)
				prior := m.hasPriorDirectHistory(call, e, session, chat)
				cancel()
				if ctx.Err() != nil || host.ResolveDirectHistoryCheck(ctx, chat.ID, *chat.DirectHistoryCheck, prior) != nil {
					return
				}
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
		}
	}
}

func (m *Manager) hasPriorDirectHistory(ctx context.Context, e *entry, session *mx.Session, chat domain.Conversation) bool {
	check := chat.DirectHistoryCheck
	if supported, err := m.supportsArchive(ctx, e, session, e.account.Address); err != nil || !supported {
		return false
	}
	release, err := m.acquireArchive(ctx, e.account.Address, archiveMaintenance)
	if err != nil {
		return false
	}
	defer release()
	request := archivePageRequest{With: chat.Address, Backward: true, Before: check.ArchiveID, Limit: 12}
	page, err := m.readArchivePage(ctx, e, session, e.account.Address, request)
	if errors.Is(err, errArchiveCursor) && request.Before != "" {
		request.Before = ""
		page, err = m.readArchivePage(ctx, e, session, e.account.Address, request)
	}
	if err != nil {
		return false
	}
	return priorDirectMessage(page.Query.Messages, chat.Address, *check, request.Before != "")
}

func priorDirectMessage(messages []application.Incoming, peer string, check domain.DirectHistoryCheck, boundedBefore bool) bool {
	anchor := len(messages)
	if !boundedBefore {
		anchor = -1
		for i, message := range messages {
			if message.Kind != "direct" || message.Address != peer || message.IsOwn || message.Reaction != nil {
				continue
			}
			match := check.ArchiveID != "" && message.ArchiveID == check.ArchiveID ||
				check.ArchiveID == "" && check.ProviderID != "" && message.ProviderID == check.ProviderID
			if match {
				if anchor >= 0 {
					return false
				}
				anchor = i
			}
		}
		if anchor < 0 {
			return false
		}
	}
	for _, message := range messages[:anchor] {
		if message.Kind == "direct" && message.Address == peer && message.Reaction == nil && message.OriginalText != "" &&
			(message.IsOwn || check.ProviderID == "" || message.ProviderID != check.ProviderID) &&
			(check.ArchiveID == "" || message.ArchiveID != check.ArchiveID) {
			return true
		}
	}
	return false
}
