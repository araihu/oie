// Package xmpp implements the protocol port using Mellium. No protocol types
// escape this adapter; credentials never enter events, receipts, or message data.
package xmpp

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"mellium.im/sasl"
	"mellium.im/xmlstream"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/dial"
	"mellium.im/xmpp/disco"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

type configuration struct {
	AccountID          domain.ID
	Password, Endpoint string
	Enabled            bool
}
type entry struct {
	account             domain.Account
	config              configuration
	cancel              context.CancelFunc
	mu                  sync.RWMutex
	sendMu              sync.Mutex
	roomNotifyMu        sync.Mutex
	rooms               map[string]*roomSlot
	ownRoomMessages     map[string]ownRoomMessage
	correctionSession   string
	correctionSources   map[string]ownCorrectionSource
	correctionLatest    map[string]domain.ID
	reactionLimits      map[string]reactionLimit
	reactionCommands    map[string]reactionDestination
	state               domain.Connection
	session             *mx.Session
	raw                 net.Conn
	historyMu           sync.Mutex
	archives            map[string]*archiveQuery
	historyWake         chan struct{}
	archiveSessionCtx   context.Context
	archiveCapabilities map[string]roomArchiveCapability
	sidScopes           map[string]bool
	occupantScopes      map[string]bool
	semianonymousRooms  map[string]bool
	roomConfigurations  map[string]roomConfiguration
	discovery           domain.CapabilityDiscovery
	discoveryPartial    bool
	detailsWake         chan struct{}
	details             map[domain.ID]*detailSlot
	peerPresence        map[string]map[string]peerResource
	avatarWake          chan struct{}
	avatarMu            sync.Mutex
	avatarTargets       map[string]string
	avatarDirty         map[string]string
	contactsWake        chan struct{}
	profiles            map[string]*profileSlot
}
type Manager struct {
	dir             string
	roots           *x509.CertPool
	mu              sync.RWMutex
	entries         map[domain.ID]*entry
	ctx             context.Context
	cancel          context.CancelFunc
	host            application.ProtocolHost
	workers         sync.WaitGroup
	archiveQueuesMu sync.Mutex
	archiveQueues   map[string]*archiveQueue
	avatarGate      chan struct{}
}

func New(dir string, roots *x509.CertPool) *Manager {
	return &Manager{dir: dir, roots: roots, entries: make(map[domain.ID]*entry), avatarGate: make(chan struct{}, 1)}
}
func (m *Manager) NormalizeAddress(protocol, address string) (string, error) {
	if protocol != "xmpp" {
		return "", application.ErrInvalid
	}
	j, err := jid.Parse(address)
	if err != nil || j.Localpart() == "" || j.Domainpart() == "" {
		return "", application.ErrInvalid
	}
	return j.Bare().String(), nil
}
func (m *Manager) Start(ctx context.Context, host application.ProtocolHost) error {
	m.ctx, m.cancel = context.WithCancel(ctx)
	m.host = host
	if err := os.MkdirAll(m.dir, 0700); err != nil {
		return err
	}
	files, err := os.ReadDir(m.dir)
	if err != nil {
		return err
	}
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(m.dir, file.Name()))
		if err != nil {
			return err
		}
		var cfg configuration
		if err = json.Unmarshal(data, &cfg); err != nil {
			return errors.New("invalid private connection configuration")
		}
		if _, err = domain.ParseID(string(cfg.AccountID)); err != nil || file.Name() != string(cfg.AccountID)+".json" {
			return errors.New("invalid private connection identity")
		}
		a, err := host.Account(ctx, cfg.AccountID)
		if err != nil {
			return err
		}
		if err = m.validate(a, cfg); err != nil {
			return err
		}
		m.mu.Lock()
		m.replace(a, cfg)
		m.mu.Unlock()
	}
	return nil
}
func (m *Manager) Close() {
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Lock()
	for _, e := range m.entries {
		e.stop()
	}
	m.mu.Unlock()
	m.workers.Wait()
}
func (e *entry) stop() {
	if e.cancel != nil {
		e.cancel()
	}
	e.mu.RLock()
	raw := e.raw
	e.mu.RUnlock()
	if raw != nil {
		_ = raw.Close()
	}
}
func (m *Manager) validate(a domain.Account, cfg configuration) error {
	if _, err := m.NormalizeAddress(a.Protocol, a.Address); err != nil {
		return err
	}
	if cfg.Password == "" || len(cfg.Password) > 4096 || strings.ContainsRune(cfg.Password, 0) {
		return application.ErrInvalid
	}
	if cfg.Endpoint != "" {
		host, port, err := net.SplitHostPort(cfg.Endpoint)
		n, parseErr := strconv.Atoi(port)
		if err != nil || parseErr != nil || host == "" || n < 1 || n > 65535 {
			return application.ErrInvalid
		}
	}
	return nil
}
func (m *Manager) save(ctx context.Context, cfg configuration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.CreateTemp(m.dir, ".connection-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	defer func() { _ = f.Close() }()
	if err = f.Chmod(0600); err != nil {
		return err
	}
	if err = json.NewEncoder(f).Encode(cfg); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), filepath.Join(m.dir, string(cfg.AccountID)+".json")); err != nil {
		return err
	}
	dir, err := os.Open(m.dir)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}
func (m *Manager) replace(a domain.Account, cfg configuration) *entry {
	if old := m.entries[a.ID]; old != nil {
		old.stop()
	}
	state := "not_connected"
	if cfg.Enabled {
		state = "connecting"
	}
	ctx, cancel := context.WithCancel(m.ctx)
	e := &entry{detailsWake: make(chan struct{}, 1), avatarWake: make(chan struct{}, 1), avatarTargets: map[string]string{}, avatarDirty: map[string]string{}, account: a, config: cfg, cancel: cancel, state: domain.Connection{AccountID: a.ID, State: state, UpdatedAt: time.Now().UTC()}}
	m.entries[a.ID] = e
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		if !cfg.Enabled {
			_ = m.publish(ctx, e, "not_connected", "")
			return
		}
		m.run(ctx, e)
	}()
	return e
}
func (m *Manager) Configure(ctx context.Context, a domain.Account, in application.ConnectionInput) (domain.Connection, error) {
	cfg := configuration{AccountID: a.ID, Password: in.Password, Endpoint: in.Endpoint, Enabled: true}
	if err := m.validate(a, cfg); err != nil {
		return domain.Connection{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx == nil || m.ctx.Err() != nil {
		return domain.Connection{}, application.ErrProtocolUnavailable
	}
	if old := m.entries[a.ID]; old != nil && old.config == cfg {
		old.mu.RLock()
		state := old.state
		old.mu.RUnlock()
		return state, nil
	}
	if err := m.save(ctx, cfg); err != nil {
		return domain.Connection{}, err
	}
	e := m.replace(a, cfg)
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.state, nil
}
func (m *Manager) Disconnect(ctx context.Context, a domain.Account) (domain.Connection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	old := m.entries[a.ID]
	if old == nil {
		return domain.Connection{AccountID: a.ID, State: "not_connected", UpdatedAt: a.CreatedAt}, nil
	}
	cfg := old.config
	cfg.Enabled = false
	if err := m.save(ctx, cfg); err != nil {
		return domain.Connection{}, err
	}
	e := m.replace(a, cfg)
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.state, nil
}
func (m *Manager) Connection(_ context.Context, a domain.Account) (domain.Connection, error) {
	m.mu.RLock()
	e := m.entries[a.ID]
	m.mu.RUnlock()
	if e == nil {
		return domain.Connection{AccountID: a.ID, State: "not_connected", UpdatedAt: a.CreatedAt}, nil
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.state, nil
}
func (m *Manager) publish(ctx context.Context, e *entry, state, code string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	current := m.entries[e.account.ID] == e
	if !current {
		return context.Canceled
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	e.mu.Lock()
	e.state = domain.Connection{AccountID: e.account.ID, State: state, LastErrorCode: code, UpdatedAt: time.Now().UTC()}
	e.historyMu.Lock()
	e.occupantScopes = nil
	e.semianonymousRooms = nil
	e.roomConfigurations = nil
	e.historyMu.Unlock()
	if state != "connected" {
		e.discovery = domain.CapabilityDiscovery{State: "not_connected", Services: []domain.DiscoveredService{}}
		e.discoveryPartial = false
		e.details = nil
		e.peerPresence = nil
		e.reactionLimits = nil
		e.reactionCommands = nil
	} else {
		e.discovery = domain.CapabilityDiscovery{State: "pending", Services: []domain.DiscoveredService{}}
		e.detailsWake = make(chan struct{}, 1)
		e.discoveryPartial = false
		e.details = nil
		e.peerPresence = nil
	}
	snapshot := e.state
	e.mu.Unlock()
	return m.host.ConnectionChanged(ctx, snapshot)
}
func (m *Manager) run(ctx context.Context, e *entry) {
	delay := time.Second
	for ctx.Err() == nil {
		if err := m.publish(ctx, e, "connecting", ""); err != nil {
			return
		}
		session, raw, err := m.connect(ctx, e)
		if err == nil {
			e.mu.Lock()
			historyCtx, historyCancel := context.WithCancel(ctx)
			e.session = session
			e.archiveSessionCtx = historyCtx
			e.raw = raw
			e.correctionSession = randomQueryID()
			e.correctionSources = map[string]ownCorrectionSource{}
			e.correctionLatest = map[string]domain.ID{}
			e.reactionLimits = nil
			e.reactionCommands = nil
			e.contactsWake = make(chan struct{}, 1)
			e.historyWake = make(chan struct{}, 1)
			e.historyMu.Lock()
			e.archiveCapabilities = nil
			e.historyMu.Unlock()
			contactsWake := e.contactsWake
			e.mu.Unlock()
			if ctx.Err() != nil {
				historyCancel()
				_ = raw.Close()
				return
			}
			if err = m.publish(ctx, e, "connected", ""); err == nil {
				m.workers.Add(4)
				go func() { defer m.workers.Done(); m.historyLoop(historyCtx, e, session) }()
				go func() { defer m.workers.Done(); m.avatarLoop(historyCtx, e, session) }()
				go func() { defer m.workers.Done(); m.discoverCapabilities(historyCtx, e, session) }()
				e.mu.RLock()
				detailsWake := e.detailsWake
				e.mu.RUnlock()
				go func() { defer m.workers.Done(); m.detailsLoop(historyCtx, e, session, detailsWake) }()
				if _, ok := m.host.(application.ContactHost); ok {
					m.workers.Add(1)
					go func() { defer m.workers.Done(); m.contactsLoop(historyCtx, e, session, contactsWake) }()
				}
				delay = time.Second
				err = session.Serve(mx.HandlerFunc(func(tokens xmlstream.TokenReadEncoder, start *xml.StartElement) error {
					return m.handle(ctx, e, tokens, start)
				}))
			}
			historyCancel()
			_ = raw.Close()
			m.resetRoomSessions(ctx, e, session)
			e.mu.Lock()
			e.session = nil
			e.raw = nil
			e.correctionSession = ""
			e.correctionSources = nil
			e.correctionLatest = nil
			e.mu.Unlock()
		}
		if ctx.Err() != nil {
			return
		}
		code, permanent := connectionError(err)
		if m.publish(ctx, e, "error", code) != nil {
			return
		}
		if permanent {
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
		if delay > 30*time.Second {
			delay = 30 * time.Second
		}
	}
}

type authenticationError struct{ error }

func connectionError(err error) (string, bool) {
	var cert *tls.CertificateVerificationError
	var unknown x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var record tls.RecordHeaderError
	if errors.As(err, &cert) || errors.As(err, &unknown) || errors.As(err, &hostname) || errors.As(err, &record) {
		return "tls_failed", true
	}
	var auth authenticationError
	if errors.As(err, &auth) {
		var network net.Error
		if !errors.As(auth.error, &network) && !errors.Is(auth.error, io.EOF) && !errors.Is(auth.error, context.DeadlineExceeded) {
			return "authentication_failed", true
		}
	}
	return "connection_failed", false
}
func (m *Manager) connect(ctx context.Context, e *entry) (*mx.Session, net.Conn, error) {
	j, err := jid.Parse(e.account.Address)
	if err != nil {
		return nil, nil, err
	}
	j, err = j.Bare().WithResource("oie-" + string(e.account.ID))
	if err != nil {
		return nil, nil, err
	}
	attempt, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var raw net.Conn
	if e.config.Endpoint != "" {
		raw, err = (&net.Dialer{KeepAlive: 30 * time.Second}).DialContext(attempt, "tcp", e.config.Endpoint)
	} else {
		raw, err = dial.Client(attempt, "tcp", j)
	}
	if err != nil {
		return nil, nil, err
	}
	stop := context.AfterFunc(attempt, func() { _ = raw.Close() })
	defer stop()
	_ = raw.SetDeadline(time.Now().Add(10 * time.Second))
	auth := mx.SASL("", e.config.Password, sasl.ScramSha256Plus, sasl.ScramSha256, sasl.ScramSha1Plus, sasl.ScramSha1, sasl.Plain)
	negotiate := auth.Negotiate
	auth.Negotiate = func(ctx context.Context, s *mx.Session, data interface{}) (mx.SessionState, io.ReadWriter, error) {
		state, rw, err := negotiate(ctx, s, data)
		if err != nil {
			err = authenticationError{err}
		}
		return state, rw, err
	}
	session, err := mx.NewClientSession(attempt, j, raw, mx.StartTLS(&tls.Config{ServerName: j.Domainpart(), RootCAs: m.roots, MinVersion: tls.VersionTLS12}), auth, mx.BindResource())
	if err == nil && session.State()&mx.Secure == 0 {
		err = errors.New("TLS required")
	}
	if err == nil {
		err = session.Send(attempt, stanza.Presence{Type: stanza.AvailablePresence}.Wrap(avatarCaps().TokenReader()))
	}
	if err != nil {
		_ = raw.Close()
		return nil, nil, err
	}
	_ = raw.SetDeadline(time.Time{})
	return session, raw, nil
}
func (m *Manager) Send(ctx context.Context, a domain.Account, out application.Outgoing) string {
	m.mu.RLock()
	e := m.entries[a.ID]
	m.mu.RUnlock()
	if e == nil {
		return application.NotSent
	}
	e.mu.RLock()
	session, raw, state := e.session, e.raw, e.state.State
	e.mu.RUnlock()
	if session == nil || raw == nil || state != "connected" {
		return application.NotSent
	}
	payload, err := buildOutgoing(a, out)
	if err != nil {
		return application.Failed
	}
	e.sendMu.Lock()
	defer e.sendMu.Unlock()
	e.mu.Lock()
	if e.session != session || e.raw != raw || e.state.State != "connected" {
		e.mu.Unlock()
		return application.NotSent
	}
	if out.Correction != nil && !validFrozenCorrection(e, a, out) {
		e.mu.Unlock()
		return application.Failed
	}
	e.mu.Unlock()
	if out.Kind == "room" && out.Reaction == nil {
		e.mu.Lock()
		slot := e.rooms[out.Address]
		if slot == nil || slot.session != session || slot.snapshot.State != "joined" || slot.snapshot.IntentRevisionID != out.RoomIntentRevisionID {
			e.mu.Unlock()
			if out.Correction != nil {
				return application.Failed
			}
			return application.NotSent
		}
		if e.ownRoomMessages == nil {
			e.ownRoomMessages = map[string]ownRoomMessage{}
		}
		if len(e.ownRoomMessages) >= maxOwnRoomMessages {
			for id := range e.ownRoomMessages {
				delete(e.ownRoomMessages, id)
				break
			}
		}
		e.ownRoomMessages[string(out.ID)] = ownRoomMessage{address: out.Address, body: out.OriginalText}
		e.mu.Unlock()
	}

	if ctx.Err() != nil {
		return application.NotSent
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(10 * time.Second)
	}
	_ = raw.SetWriteDeadline(deadline)
	defer func() { _ = raw.SetWriteDeadline(time.Time{}) }()
	if out.Reaction != nil {
		return sendFrozenReaction(ctx, e, session, raw, a, out, payload)
	}
	e.mu.Lock()
	trackCorrectionSource(e, a, out)
	e.mu.Unlock()

	if err = session.Encode(ctx, payload); err != nil {
		_ = raw.Close()
		return application.Unknown
	}
	return application.Submitted
}

type boundedTokens struct {
	reader    xml.TokenReader
	remaining int
}

func (b *boundedTokens) Token() (xml.Token, error) {
	token, err := b.reader.Token()
	if err != nil && (err != io.EOF || token == nil) {
		return token, err
	}
	switch t := token.(type) {
	case xml.CharData:
		b.remaining -= len(t)
	case xml.StartElement:
		b.remaining -= len(t.Name.Local) + len(t.Name.Space)
		for _, a := range t.Attr {
			b.remaining -= len(a.Name.Local) + len(a.Value)
		}
	}
	if b.remaining < 0 {
		return nil, errors.New("XMPP stanza exceeds size limit")
	}
	return token, nil
}
func (m *Manager) handle(ctx context.Context, e *entry, t xmlstream.TokenReadEncoder, start *xml.StartElement) error {
	decoder := xml.NewTokenDecoder(xmlstream.MultiReader(xmlstream.Token(*start), &boundedTokens{reader: t, remaining: 1 << 20}))
	if start.Name.Space != "jabber:client" {
		return nil
	}
	switch start.Name.Local {
	case "message":
		var msg struct {
			stanza.Message
			fileSharingMetadata
			replyMetadata
			correctionMetadata
			reactionMetadata
			Body        string         `xml:"jabber:client body"`
			Archive     *archiveResult `xml:"urn:xmpp:mam:2 result"`
			AvatarEvent *struct {
				Items struct {
					Node string `xml:"node,attr"`
				} `xml:"items"`
			} `xml:"http://jabber.org/protocol/pubsub#event event"`
			MUC   *mucUser `xml:"http://jabber.org/protocol/muc#user x"`
			Delay struct {
				From  string `xml:"from,attr"`
				Stamp string `xml:"stamp,attr"`
			} `xml:"urn:xmpp:delay delay"`
		}
		if err := decoder.Decode(&msg); err != nil {
			return err
		}
		if msg.AvatarEvent != nil && msg.AvatarEvent.Items.Node == avatarMetadataNS && msg.From.Resourcepart() == "" {
			publisher := msg.From
			// The authenticated server may omit From for the account's own PEP events.
			// Never apply that shorthand to an identified remote publisher.
			if publisher.String() == "" {
				publisher, _ = jid.Parse(e.account.Address)
			}
			m.queueAvatar(e, publisher, "", false)
		}
		if msg.Archive != nil {
			return m.captureArchive(e, msg.Message, msg.Archive)
		}
		if msg.MUC != nil && msg.Delay.Stamp == "" && msg.Delay.From == "" {
			if err := m.invalidateRoomConfiguration(ctx, e, msg.Message, msg.MUC); err != nil {
				return err
			}
		}
		if msg.Type == stanza.ErrorMessage {
			if handled, err := m.rejectReaction(ctx, e, msg.Message); handled {
				return err
			}
			own, _ := jid.Parse(e.account.Address)
			address := msg.From.Bare().String()
			fromServer := msg.From.Equal(own.Domain())
			e.mu.RLock()
			outgoing, tracked := e.ownRoomMessages[msg.ID]
			_, knownRoom := e.rooms[address]
			e.mu.RUnlock()
			if (tracked || knownRoom) && msg.From.Resourcepart() != "" {
				return nil
			}
			if tracked {
				room, _ := jid.Parse(outgoing.address)
				switch {
				case msg.From.Equal(room.Bare()):
					address = outgoing.address
				case msg.From.Equal(room.Domain()):
					address = outgoing.address
					fromServer = true
				case fromServer:
					address = outgoing.address
				default:
					return nil
				}
			}
			return m.host.Reject(ctx, application.Rejected{AccountID: e.account.ID, MessageID: msg.ID, Address: address, FromServer: fromServer, FromParticipant: msg.From.Resourcepart() != ""})
		}
		if len(msg.Reactions) != 0 {
			return m.receiveReaction(ctx, e, msg.Message, msg.reactionMetadata, msg.replyMetadata, msg.Occupants, msg.Delay.Stamp, msg.Delay.From)
		}
		if msg.Type == stanza.GroupChatMessage {
			return m.receiveRoomMessage(ctx, e, msg.Message, msg.replyMetadata, msg.Body, msg.Delay.Stamp, msg.Delay.From, msg.fileSharingMetadata, msg.correctionMetadata)
		}
		if msg.Type != stanza.ChatMessage && msg.Type != stanza.NormalMessage {
			return nil
		}
		if strings.TrimSpace(msg.Body) == "" || msg.From.Localpart() == "" {
			return nil
		}
		created := time.Now().UTC()
		if delayed, err := time.Parse(time.RFC3339Nano, msg.Delay.Stamp); err == nil {
			created = delayed.UTC()
		}
		refs, reply := decodeReplyMetadata(msg.replyMetadata, msg.Body, msg.From, e.account.Address, msg.From.Bare().String(), false, false)
		if len(msg.Origins) == 0 && usableID(msg.ID) {
			refs = append(refs, domain.MessageReference{Scope: "sender:" + msg.From.Bare().String(), ID: msg.ID, Author: msg.From.Bare().String()})
		}
		err := m.host.Receive(ctx, application.Incoming{MediaThumbnails: decodeFileThumbnails(msg.fileSharingMetadata), AccountID: e.account.ID, Address: msg.From.Bare().String(), ProviderID: msg.ID, OriginalText: msg.Body, CreatedAt: created, IsOwn: msg.From.Bare().String() == e.account.Address, References: refs, Reply: reply, Correction: decodeCorrectionMetadata(msg.correctionMetadata, msg.Message, false, false)})
		if errors.Is(err, application.ErrInvalid) {
			return nil
		}
		if err == nil {
			m.wakeAvatars(e)
		}
		return err
	case "presence":
		var presence incomingPresence
		if err := decoder.Decode(&presence); err != nil {
			return err
		}
		captureRoomMentionPresence(e, presence)
		if handled, err := m.captureRoomPresence(ctx, e, presence); handled {
			return err
		}
		capturePeerPresence(e, presence)
		if presence.Update != nil && presence.Update.Photo != nil {
			hash := strings.TrimSpace(*presence.Update.Photo)
			if hash == "" || validSHA1(hash) {
				m.queueAvatar(e, presence.From, hash, true)
			}
		}
		return nil
	case "iq":
		iq, err := stanza.NewIQ(*start)
		if err != nil {
			return nil
		}
		if iq.Type != stanza.GetIQ && iq.Type != stanza.SetIQ {
			return nil
		}
		var query struct {
			Ping   *struct{}             `xml:"urn:xmpp:ping ping"`
			Disco  *disco.InfoQuery      `xml:"http://jabber.org/protocol/disco#info query"`
			Roster *contactRosterPayload `xml:"jabber:iq:roster query"`
		}
		if err = decoder.Decode(&query); err != nil {
			return err
		}
		if query.Roster != nil {
			return m.rosterPush(e, iq, *query.Roster, t)
		}
		reply := iq.Error(stanza.Error{Type: stanza.Cancel, Condition: stanza.ServiceUnavailable})
		if query.Ping != nil && iq.Type == stanza.GetIQ {
			reply = iq.Result(nil)
		}
		if query.Disco != nil && iq.Type == stanza.GetIQ {
			info := clientInfo()
			if query.Disco.Node == "" || query.Disco.Node == avatarCaps().Node+"#"+avatarCaps().Ver {
				info.Node = query.Disco.Node
				reply = iq.Result(info.TokenReader())
			} else {
				reply = iq.Error(stanza.Error{Type: stanza.Cancel, Condition: stanza.ItemNotFound})
			}
		}
		_, err = xmlstream.Copy(t, reply)
		return err
	}
	return nil
}
