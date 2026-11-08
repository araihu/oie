package xmpp

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"strings"
	"time"

	"github.com/araihu/oie/internal/application"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/crypto"
	"mellium.im/xmpp/disco"
	"mellium.im/xmpp/disco/info"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

const avatarMetadataNS = "urn:xmpp:avatar:metadata"

var errAvatar = errors.New("profile image unavailable")

type avatarInfo struct {
	ID    string `xml:"id,attr"`
	Type  string `xml:"type,attr"`
	Bytes int    `xml:"bytes,attr"`
	URL   string `xml:"url,attr"`
}
type avatarMetadata struct {
	Info []avatarInfo `xml:"info"`
}
type avatarPEP struct {
	Pubsub struct {
		Items struct {
			Item []struct {
				ID       string          `xml:"id,attr"`
				Metadata *avatarMetadata `xml:"urn:xmpp:avatar:metadata metadata"`
				Data     string          `xml:"urn:xmpp:avatar:data data"`
			} `xml:"item"`
		} `xml:"items"`
	} `xml:"http://jabber.org/protocol/pubsub pubsub"`
}

func validSHA1(s string) bool {
	if len(s) != 40 {
		return false
	}
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 20 && strings.ToLower(s) == s
}
func decodeAvatar(s, expected string) ([]byte, string, error) {
	if len(s) > 2<<20 {
		return nil, "", errAvatar
	}
	normalized := strings.Join(strings.Fields(s), "")
	if base64.StdEncoding.DecodedLen(len(normalized)) > (1<<20)+2 {
		return nil, "", errAvatar
	}
	data, err := base64.StdEncoding.DecodeString(normalized)
	if err != nil || len(data) == 0 || len(data) > 1<<20 {
		return nil, "", errAvatar
	}
	sum := sha1.Sum(data)
	hash := hex.EncodeToString(sum[:])
	if expected != "" && expected != hash {
		return nil, "", errAvatar
	}
	return data, hash, nil
}
func (m *Manager) fetchAvatar(ctx context.Context, host application.AvatarHost, e *entry, s *mx.Session, address, expected string) error {
	cached, _ := host.Avatar(ctx, e.account.ID, address)
	save := func(data []byte, source string) error {
		if cached.SourceToken == source && cached.ImageHash != "" {
			if _, err := host.AvatarImage(ctx, cached.ImageHash); err == nil {
				return nil
			}
		}
		return host.StoreAvatar(ctx, e.account.ID, address, source, data)
	}
	payload, err := m.readHistoryIQ(ctx, e, s, address, stanza.GetIQ, `<pubsub xmlns="http://jabber.org/protocol/pubsub"><items node="urn:xmpp:avatar:metadata" max_items="1"/></pubsub>`)
	if err == nil {
		var pep avatarPEP
		if xml.Unmarshal([]byte("<root>"+payload+"</root>"), &pep) == nil {
			for _, item := range pep.Pubsub.Items.Item {
				if item.Metadata == nil {
					continue
				}
				if len(item.Metadata.Info) == 0 {
					return host.StoreAvatar(ctx, e.account.ID, address, "pep:none", nil)
				}
				for _, info := range item.Metadata.Info {
					if !validSHA1(info.ID) || info.URL != "" || info.Bytes < 0 || info.Bytes > 1<<20 || (info.Type != "image/png" && info.Type != "image/jpeg" && info.Type != "image/gif") {
						continue
					}
					source := "pep:" + info.ID
					if cached.SourceToken == source && cached.ImageHash != "" {
						if _, err := host.AvatarImage(ctx, cached.ImageHash); err == nil {
							return nil
						}
					}
					payload, err = m.readHistoryIQ(ctx, e, s, address, stanza.GetIQ, `<pubsub xmlns="http://jabber.org/protocol/pubsub"><items node="urn:xmpp:avatar:data"><item id="`+info.ID+`"/></items></pubsub>`)
					if err != nil {
						return err
					}
					var data avatarPEP
					if xml.Unmarshal([]byte("<root>"+payload+"</root>"), &data) != nil {
						return errAvatar
					}
					for _, d := range data.Pubsub.Items.Item {
						if d.ID == info.ID {
							decoded, _, err := decodeAvatar(d.Data, info.ID)
							if err != nil {
								return err
							}
							return save(decoded, source)
						}
					}
					return errAvatar
				}
			}
		}
	}
	payload, err = m.readHistoryIQ(ctx, e, s, address, stanza.GetIQ, `<vCard xmlns="vcard-temp"/>`)
	if err != nil {
		return err
	}
	var card struct {
		Card *struct {
			Photo struct {
				Data     string `xml:"BINVAL"`
				External string `xml:"EXTVAL"`
			} `xml:"PHOTO"`
		} `xml:"vcard-temp vCard"`
	}
	if xml.Unmarshal([]byte("<root>"+payload+"</root>"), &card) != nil || card.Card == nil {
		return errAvatar
	}
	if card.Card.Photo.Data == "" {
		if card.Card.Photo.External != "" || expected != "" {
			return errAvatar
		}
		return host.StoreAvatar(ctx, e.account.ID, address, "vcard:none", nil)
	}
	data, hash, err := decodeAvatar(card.Card.Photo.Data, expected)
	if err != nil {
		return err
	}
	return save(data, "vcard:"+hash)
}
func (m *Manager) wakeAvatars(e *entry) {
	select {
	case e.avatarWake <- struct{}{}:
	default:
	}
}
func (m *Manager) queueAvatar(e *entry, from jid.JID, expected string, presence bool) {
	address := from.Bare().String()
	e.avatarMu.Lock()
	kind, known := e.avatarTargets[address]
	if known && (!presence || kind != "room" || from.Resourcepart() == "") {
		e.avatarDirty[address] = expected
	}
	e.avatarMu.Unlock()
	if known {
		m.wakeAvatars(e)
	}
}
func (m *Manager) avatarLoop(ctx context.Context, e *entry, s *mx.Session) {
	host, ok := m.host.(application.AvatarHost)
	if !ok {
		return
	}
	due := map[string]time.Time{}
	attempted := map[string]time.Time{}
	for ctx.Err() == nil {
		targets, err := host.AvatarTargets(ctx, e.account.ID)
		if err != nil {
			return
		}
		e.avatarMu.Lock()
		for _, t := range targets {
			e.avatarTargets[t.Address] = t.Kind
		}
		e.avatarMu.Unlock()
		retrySoon := false
		for _, target := range targets {
			if ctx.Err() != nil {
				return
			}
			e.avatarMu.Lock()
			hint, dirty := e.avatarDirty[target.Address]
			e.avatarMu.Unlock()
			now := time.Now()
			if !dirty && now.Before(due[target.Address]) {
				continue
			}
			if now.Sub(attempted[target.Address]) < 2*time.Second {
				retrySoon = true
				continue
			}
			e.avatarMu.Lock()
			delete(e.avatarDirty, target.Address)
			e.avatarMu.Unlock()
			select {
			case m.avatarGate <- struct{}{}:
			case <-ctx.Done():
				return
			}
			err = m.fetchAvatar(ctx, host, e, s, target.Address, hint)
			<-m.avatarGate
			attempted[target.Address] = time.Now()
			ttl := 24 * time.Hour
			if err != nil {
				ttl = 5 * time.Minute
			}
			due[target.Address] = time.Now().Add(ttl)
			timer := time.NewTimer(250 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		interval := time.Minute
		if retrySoon {
			interval = 2 * time.Second
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-e.avatarWake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// PEP requires advertising interest in avatar notifications through entity caps.
func clientInfo() disco.Info {
	return disco.Info{Identity: []info.Identity{{Category: "client", Type: "pc", Name: "Oie"}}, Features: []info.Feature{{Var: "http://jabber.org/protocol/disco#info"}, {Var: "urn:xmpp:ping"}, {Var: avatarMetadataNS + "+notify"}}}
}
func avatarCaps() disco.Caps {
	return disco.Caps{Hash: crypto.SHA1, Node: "https://github.com/araihu/oie", Ver: clientInfo().Hash(sha1.New())}
}
