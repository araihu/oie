package xmpp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

const mamNS = "urn:xmpp:mam:2"

var errArchive = errors.New("archive unavailable")
var errArchiveCursor = errors.New("archive cursor expired")
var errUnstable = errors.New("archive not stable")

type archiveMessage struct {
	stanza.Message
	fileSharingMetadata
	replyMetadata
	correctionMetadata
	Body  string `xml:"jabber:client body"`
	Users []struct {
		Items []struct {
			JID string `xml:"jid,attr"`
		} `xml:"item"`
	} `xml:"http://jabber.org/protocol/muc#user x"`
}
type archiveResult struct {
	ID        string `xml:"id,attr"`
	QueryID   string `xml:"queryid,attr"`
	Forwarded struct {
		Delay struct {
			Stamp string `xml:"stamp,attr"`
		} `xml:"urn:xmpp:delay delay"`
		Message *archiveMessage `xml:"jabber:client message"`
	} `xml:"urn:xmpp:forward:0 forwarded"`
}
type archiveQuery struct {
	ID, Scope        string
	SIDVerified      bool
	OccupantVerified bool
	Messages         []application.Incoming
	Bytes, Seen      int
	Err              error
}

func archiveSender(from jid.JID, scope, account string) bool {
	own, _ := jid.Parse(account)
	if scope == account {
		return from.String() == "" || from.Equal(own.Bare()) || from.Equal(own.Domain())
	}
	target, err := jid.Parse(scope)
	return err == nil && from.Equal(target.Bare())
}
func (m *Manager) captureArchive(e *entry, outer stanza.Message, r *archiveResult) error {
	e.historyMu.Lock()
	defer e.historyMu.Unlock()
	q := e.archive
	if q == nil || r.QueryID != q.ID || !archiveSender(outer.From, q.Scope, e.account.Address) {
		return nil
	}
	q.Seen++
	if q.Seen > 100 {
		q.Err = errArchive
		return nil
	}
	if q.Err != nil {
		return nil
	}
	in, ok, err := archivedIncoming(e.account, q.Scope, r, q.SIDVerified, q.OccupantVerified)
	if err != nil {
		q.Err = err
		return nil
	}
	if !ok {
		return nil
	}
	q.Bytes += len(in.OriginalText)
	if q.Bytes > 8<<20 {
		q.Err = errArchive
		return nil
	}
	q.Messages = append(q.Messages, in)
	return nil
}
func archivedIncoming(account domain.Account, scope string, r *archiveResult, trusted ...bool) (application.Incoming, bool, error) {
	msg := r.Forwarded.Message
	if msg == nil || strings.TrimSpace(msg.Body) == "" {
		return application.Incoming{}, false, nil
	}
	if len(r.ID) == 0 || len(r.ID) > 4096 {
		return application.Incoming{}, false, errArchive
	}
	at, err := time.Parse(time.RFC3339Nano, r.Forwarded.Delay.Stamp)
	if err != nil {
		return application.Incoming{}, false, errArchive
	}
	own, _ := jid.Parse(account.Address)
	in := application.Incoming{MediaThumbnails: decodeFileThumbnails(msg.fileSharingMetadata), AccountID: account.ID, ProviderID: msg.ID, OriginalText: msg.Body, CreatedAt: at.UTC(), Historical: true, ArchiveScope: scope, ArchiveID: r.ID, Kind: "direct"}
	if msg.Type == stanza.GroupChatMessage {
		if msg.From.Localpart() == "" || msg.From.Resourcepart() == "" {
			return in, false, errArchive
		}
		if scope != account.Address && msg.From.Bare().String() != scope {
			return in, false, errArchive
		}
		in.Kind = "room"
		in.Address = msg.From.Bare().String()
		in.SenderName = msg.From.Resourcepart()
		// MUC nicknames are not account identities. Only an unambiguous real JID
		// supplied inside an authenticated archive result establishes own authorship.
		if len(msg.Users) == 1 && len(msg.Users[0].Items) == 1 {
			author, err := jid.Parse(msg.Users[0].Items[0].JID)
			if err == nil && author.Localpart() != "" && author.Domainpart() != "" && !author.Bare().Equal(msg.From.Bare()) {
				in.IsOwn = author.Bare().Equal(own.Bare())
				in.SenderAddress = author.Bare().String()
			}
		}

		if msg.ID != "" {
			h := sha256.Sum256([]byte(msg.From.String() + "\x00" + msg.ID))
			in.ProviderID = "room:" + hex.EncodeToString(h[:])
		}
	} else {
		if scope != account.Address || (msg.Type != stanza.ChatMessage && msg.Type != stanza.NormalMessage) {
			return in, false, nil
		}
		in.IsOwn = msg.From.Bare().Equal(own.Bare())
		if in.IsOwn {
			in.Address = msg.To.Bare().String()
		} else {
			if !msg.To.Bare().Equal(own.Bare()) {
				return in, false, errArchive
			}
			in.Address = msg.From.Bare().String()
		}
		peer, err := jid.Parse(in.Address)
		if err != nil || peer.Localpart() == "" {
			return in, false, errArchive
		}
	}
	verified := (len(trusted) == 1 || len(trusted) == 2) && trusted[0] && scope == msg.From.Bare().String()
	occupantVerified := len(trusted) == 2 && trusted[1] && scope == msg.From.Bare().String()
	in.Correction = decodeCorrectionMetadata(msg.correctionMetadata, msg.Message, in.Kind == "room", occupantVerified)
	in.References, in.Reply = decodeReplyMetadata(msg.replyMetadata, msg.Body, msg.From, account.Address, in.Address, in.Kind == "room", verified)
	if in.Kind == "room" {
		if provider := roomProviderID(msg.From, msg.ID, in.References); provider != "" {
			in.ProviderID = provider
		}
		if in.IsOwn && len(msg.Origins) == 1 && usableID(msg.Origins[0].ID) && msg.Origins[0].Text == "" && len(msg.Origins[0].Children) == 0 {
			in.CanonicalID, _ = domain.ParseID(msg.Origins[0].ID)
		}
	}
	if in.Kind == "direct" && len(msg.Origins) == 0 && usableID(msg.ID) {
		in.References = append(in.References, domain.MessageReference{Scope: "sender:" + msg.From.Bare().String(), ID: msg.ID, Author: msg.From.Bare().String()})
	}
	if in.ProviderID == "" {
		h := sha256.Sum256([]byte(scope + "\x00" + r.ID))
		in.ProviderID = "archive:" + hex.EncodeToString(h[:])
	}
	return in, true, nil
}
func randomQueryID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}
func escaped(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
func (m *Manager) acquireHistory(ctx context.Context) error {
	select {
	case m.historyGate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (m *Manager) readHistoryIQ(ctx context.Context, e *entry, s *mx.Session, scope string, kind stanza.IQType, payload string) (string, error) {
	if err := m.acquireHistory(ctx); err != nil {
		return "", err
	}
	defer func() { <-m.historyGate }()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	to, err := jid.Parse(scope)
	if err != nil {
		return "", err
	}
	response, err := s.SendIQ(ctx, stanza.IQ{To: to, Type: kind}.Wrap(xml.NewDecoder(strings.NewReader(payload))))
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Close() }()
	var result struct {
		stanza.IQ
		Inner string `xml:",innerxml"`
		Error struct {
			NotFound     *struct{} `xml:"urn:ietf:params:xml:ns:xmpp-stanzas item-not-found"`
			Forbidden    *struct{} `xml:"urn:ietf:params:xml:ns:xmpp-stanzas forbidden"`
			Unauthorized *struct{} `xml:"urn:ietf:params:xml:ns:xmpp-stanzas not-authorized"`
		} `xml:"error"`
	}
	// NewTokenDecoder cannot preserve innerxml from an external TokenReader.
	// Re-encode the bounded response before extracting its namespace-aware payload.
	var raw strings.Builder
	encoder := xml.NewEncoder(&raw)
	bounded := &boundedTokens{reader: response, remaining: 2 << 20}
	for {
		token, readErr := bounded.Token()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", readErr
		}
		if err = encoder.EncodeToken(token); err != nil {
			return "", err
		}
	}
	if err = encoder.Flush(); err != nil {
		return "", err
	}
	if err = xml.Unmarshal([]byte(raw.String()), &result); err != nil {
		return "", err
	}
	if !archiveSender(result.From, scope, e.account.Address) {
		return "", errArchive
	}
	if result.Type == stanza.ErrorIQ {
		if result.Error.Forbidden != nil || result.Error.Unauthorized != nil {
			return "", errDetailPermission
		}
		if result.Error.NotFound != nil {
			return "", errArchiveCursor
		}
		return "", errArchive
	}
	if result.Type != stanza.ResultIQ {
		return "", errArchive
	}
	return result.Inner, nil
}
func (m *Manager) supportsArchive(ctx context.Context, e *entry, s *mx.Session, scope string) (bool, error) {
	info, err := m.queryInfo(ctx, e, s, scope)
	kind := "room"
	if scope == e.account.Address {
		kind = "account"
	}
	recordDiscovery(e, s, discovered(kind, scope, info, err))
	if err != nil {
		return false, err
	}
	mam, sid := false, false
	for _, feature := range info.Features {
		mam = mam || feature.Var == mamNS
		sid = sid || feature.Var == sidNS
	}
	e.historyMu.Lock()
	if e.sidScopes == nil {
		e.sidScopes = map[string]bool{}
	}
	e.sidScopes[scope] = sid
	e.historyMu.Unlock()
	return mam, nil
}

type savedRoom struct {
	Address, Title string
	AutoJoin       bool
}

func (m *Manager) savedRooms(ctx context.Context, e *entry, s *mx.Session) ([]savedRoom, error) {
	payload, err := m.readHistoryIQ(ctx, e, s, e.account.Address, stanza.GetIQ, `<pubsub xmlns="http://jabber.org/protocol/pubsub"><items node="urn:xmpp:bookmarks:1"/></pubsub>`)
	rooms := []savedRoom{}
	if err == nil {
		if parsed, parseErr := parseModernSavedRooms(payload); parseErr == nil {
			return parsed, nil
		}
	}
	payload, err = m.readHistoryIQ(ctx, e, s, e.account.Address, stanza.GetIQ, `<query xmlns="jabber:iq:private"><storage xmlns="storage:bookmarks"/></query>`)
	if err != nil {
		if errors.Is(err, errArchive) || errors.Is(err, errArchiveCursor) {
			return rooms, nil
		}
		return nil, err
	}
	return parseLegacySavedRooms(payload)
}

func parseModernSavedRooms(payload string) ([]savedRoom, error) {
	var modern struct {
		Pubsub struct {
			Items struct {
				Node string `xml:"node,attr"`
				Item []struct {
					ID         string `xml:"id,attr"`
					Conference struct {
						Name     string `xml:"name,attr"`
						AutoJoin string `xml:"autojoin,attr"`
					} `xml:"urn:xmpp:bookmarks:1 conference"`
				} `xml:"item"`
			} `xml:"items"`
		} `xml:"http://jabber.org/protocol/pubsub pubsub"`
	}
	if err := xml.Unmarshal([]byte("<root>"+payload+"</root>"), &modern); err != nil {
		return nil, err
	}
	if modern.Pubsub.Items.Node != "urn:xmpp:bookmarks:1" {
		return nil, errArchive
	}
	rooms := []savedRoom{}
	for _, item := range modern.Pubsub.Items.Item {
		rooms = append(rooms, savedRoom{Address: item.ID, Title: item.Conference.Name, AutoJoin: bookmarkAutoJoin(item.Conference.AutoJoin)})
	}
	return rooms, nil
}

func parseLegacySavedRooms(payload string) ([]savedRoom, error) {
	var legacy struct {
		Query struct {
			Storage struct {
				Room []struct {
					JID      string `xml:"jid,attr"`
					Name     string `xml:"name,attr"`
					AutoJoin string `xml:"autojoin,attr"`
				} `xml:"storage:bookmarks conference"`
			} `xml:"storage:bookmarks storage"`
		} `xml:"jabber:iq:private query"`
	}
	if err := xml.Unmarshal([]byte("<root>"+payload+"</root>"), &legacy); err != nil {
		return nil, err
	}
	rooms := []savedRoom{}
	for _, room := range legacy.Query.Storage.Room {
		rooms = append(rooms, savedRoom{Address: room.JID, Title: room.Name, AutoJoin: bookmarkAutoJoin(room.AutoJoin)})
	}
	return rooms, nil
}

func bookmarkAutoJoin(value string) bool {
	value = strings.TrimSpace(value)
	return value == "true" || value == "1"
}

func restoreSavedRoom(ctx context.Context, host application.HistoryHost, account domain.Account, room savedRoom) error {
	if bookmarks, ok := host.(application.RoomBookmarkHost); ok {
		return bookmarks.RestoreRoomBookmark(ctx, application.RoomBookmark{
			AccountID: account.ID, Address: room.Address, Title: room.Title,
			AutoJoin: room.AutoJoin, Nickname: defaultRoomNickname(account),
		})
	}
	return host.RestoreConversation(ctx, account.ID, room.Address, "room", room.Title)
}

func (m *Manager) recoverHistory(ctx context.Context, e *entry, s *mx.Session) {
	host, ok := m.host.(application.HistoryHost)
	if !ok {
		return
	}
	status := domain.HistoryStatus{AccountID: e.account.ID, State: "syncing"}
	if host.HistoryChanged(ctx, status) != nil {
		return
	}
	partial := false
	discoveryFailed := false
	rooms, err := m.savedRooms(ctx, e, s)
	if err != nil {
		partial = true
		discoveryFailed = true
	}
	scopes := []string{e.account.Address}
	seen := map[string]bool{e.account.Address: true}
	for _, room := range rooms {
		j, err := jid.Parse(room.Address)
		if err != nil || j.Localpart() == "" {
			partial = true
			continue
		}
		address := j.Bare().String()
		if seen[address] {
			continue
		}
		seen[address] = true
		room.Address = address
		if restoreSavedRoom(ctx, host, e.account, room) != nil {
			partial = true
			continue
		}
		m.wakeAvatars(e)
		scopes = append(scopes, address)
	}
	anySupported := false
	for _, scope := range scopes {
		if ctx.Err() != nil {
			return
		}
		supported, err := m.supportsArchive(ctx, e, s, scope)
		if err != nil {
			partial = true
			discoveryFailed = true
			continue
		}
		if !supported {
			partial = true
			continue
		}
		anySupported = true
		if err = m.recoverArchive(ctx, host, e, s, scope); err != nil {
			if ctx.Err() != nil {
				return
			}
			partial = true
			status.LastErrorCode = "archive_failed"
			if errors.Is(err, errUnstable) {
				status.LastErrorCode = "unstable_archive"
			}
		}
	}
	if ctx.Err() != nil {
		return
	}
	status.State = "complete"
	if partial {
		status.State = "partial"
	}
	if !anySupported {
		status.State = "unsupported"
		if discoveryFailed {
			status.State = "error"
			status.LastErrorCode = "discovery_failed"
		}
	}
	_ = host.HistoryChanged(ctx, status)
}
func (m *Manager) recoverArchive(ctx context.Context, host application.HistoryHost, e *entry, s *mx.Session, scope string) error {
	after, err := host.HistoryCheckpoint(ctx, e.account.ID, scope)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	reset := false
	for ctx.Err() == nil {
		query := &archiveQuery{ID: randomQueryID(), Scope: scope}
		if query.ID == "" {
			return errArchive
		}
		e.historyMu.Lock()
		query.SIDVerified = e.sidScopes[scope]
		query.OccupantVerified = e.occupantScopes[scope]
		e.archive = query
		e.historyMu.Unlock()
		set := `<max>50</max>`
		if after != "" {
			set += `<after>` + escaped(after) + `</after>`
		}
		payload, err := m.readHistoryIQ(ctx, e, s, scope, stanza.SetIQ, `<query xmlns="urn:xmpp:mam:2" queryid="`+query.ID+`"><set xmlns="http://jabber.org/protocol/rsm">`+set+`</set></query>`)
		e.historyMu.Lock()
		e.archive = nil
		e.historyMu.Unlock()
		if errors.Is(err, errArchiveCursor) && after != "" && !reset {
			after = ""
			reset = true
			if err = host.CommitHistoryCheckpoint(ctx, e.account.ID, scope, ""); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if query.Err != nil {
			return query.Err
		}
		var result struct {
			Fin *struct {
				Complete bool   `xml:"complete,attr"`
				Stable   string `xml:"stable,attr"`
				Set      struct {
					Last string `xml:"last"`
				} `xml:"http://jabber.org/protocol/rsm set"`
			} `xml:"urn:xmpp:mam:2 fin"`
		}
		decodeErr := xml.Unmarshal([]byte("<root>"+payload+"</root>"), &result)
		if decodeErr != nil || result.Fin == nil {
			return errArchive
		}
		for _, in := range query.Messages {
			if err = m.host.Receive(ctx, in); err != nil {
				return err
			}
		}
		if result.Fin.Stable == "false" {
			return errUnstable
		}
		last := result.Fin.Set.Last
		if last != "" {
			if len(last) > 4096 || last == after || seen[last] {
				return errArchive
			}
			seen[last] = true
			if err = host.CommitHistoryCheckpoint(ctx, e.account.ID, scope, last); err != nil {
				return err
			}
			after = last
		} else if !result.Fin.Complete {
			return errArchive
		}
		if err = host.HistoryChanged(ctx, domain.HistoryStatus{AccountID: e.account.ID, State: "syncing"}); err != nil {
			return err
		}
		if result.Fin.Complete {
			return nil
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return ctx.Err()
}

// Retry partial/transient recovery with bounded backoff while keeping live I/O
// independent. Completed passes catch up from checkpoints on reconnect.
func (m *Manager) historyLoop(ctx context.Context, e *entry, s *mx.Session) {
	host, ok := m.host.(application.HistoryHost)
	if !ok {
		return
	}
	delay := time.Minute
	for ctx.Err() == nil {
		m.recoverHistory(ctx, e, s)
		if ctx.Err() != nil {
			return
		}
		status, err := host.History(ctx, e.account.ID)
		if err != nil || (status.State != "partial" && status.State != "error") {
			return
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		delay *= 2
		if delay > 5*time.Minute {
			delay = 5 * time.Minute
		}
	}
}
