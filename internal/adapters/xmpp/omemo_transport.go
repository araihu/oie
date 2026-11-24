package xmpp

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"time"

	"github.com/araihu/oie/internal/adapters/xmpp/omemo"
	"github.com/araihu/oie/internal/application"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/stanza"
)

const deviceNode = omemo.Namespace + ".devicelist"

func bundleNode(id uint32) string {
	return omemo.Namespace + ".bundles:" + strconv.FormatUint(uint64(id), 10)
}

type listedDevice struct {
	ID uint32 `xml:"id,attr"`
}
type deviceList struct {
	XMLName xml.Name       `xml:"eu.siacs.conversations.axolotl list"`
	Devices []listedDevice `xml:"eu.siacs.conversations.axolotl device"`
}
type omemoItems struct {
	XMLName xml.Name `xml:"http://jabber.org/protocol/pubsub pubsub"`
	Items   struct {
		Node    string `xml:"node,attr"`
		Entries []struct {
			List   *deviceList   `xml:"eu.siacs.conversations.axolotl list"`
			Bundle *omemo.Bundle `xml:"eu.siacs.conversations.axolotl bundle"`
		} `xml:"http://jabber.org/protocol/pubsub item"`
	} `xml:"http://jabber.org/protocol/pubsub items"`
}

func (m *Manager) EnableOMEMO(dir string) { m.omemoDir = dir }
func getOMEMOItem(ctx context.Context, e *entry, s *mx.Session, peer, node string) (omemoItems, error) {
	// Node names are local constants or decimal device identifiers.
	var reply omemoItems
	body, err := readArchiveIQ(ctx, e, s, peer, stanza.GetIQ, fmt.Sprintf("<pubsub xmlns='http://jabber.org/protocol/pubsub'><items node='%s' max_items='1'/></pubsub>", node))
	if err != nil {
		return reply, err
	}
	if err = xml.Unmarshal([]byte(body), &reply); err != nil {
		return reply, err
	}
	if reply.Items.Node != node || len(reply.Items.Entries) != 1 {
		return reply, omemo.ErrInvalid
	}
	return reply, nil
}
func getDevices(ctx context.Context, e *entry, s *mx.Session, peer string) (deviceList, error) {
	r, err := getOMEMOItem(ctx, e, s, peer, deviceNode)
	if err != nil {
		return deviceList{}, err
	}
	l := r.Items.Entries[0].List
	if l == nil || len(l.Devices) > 100 {
		return deviceList{}, omemo.ErrInvalid
	}
	seen := map[uint32]bool{}
	for _, d := range l.Devices {
		if d.ID == 0 || d.ID > 0x7fffffff || seen[d.ID] {
			return deviceList{}, omemo.ErrInvalid
		}
		seen[d.ID] = true
	}
	return *l, nil
}
func publishOMEMOItem(ctx context.Context, e *entry, s *mx.Session, node string, value any) error {
	data, err := xml.Marshal(value)
	if err != nil {
		return err
	}
	body := fmt.Sprintf("<pubsub xmlns='http://jabber.org/protocol/pubsub'><publish node='%s'><item id='current'>%s</item></publish><publish-options><x xmlns='jabber:x:data' type='submit'><field var='FORM_TYPE' type='hidden'><value>http://jabber.org/protocol/pubsub#publish-options</value></field><field var='pubsub#access_model'><value>open</value></field></x></publish-options></pubsub>", node, data)
	_, err = readArchiveIQ(ctx, e, s, e.account.Address, stanza.SetIQ, body)
	return err
}
func publishDevice(ctx context.Context, e *entry, s *mx.Session) error {
	id, b, err := e.omemoDevice.Bundle()
	if err != nil {
		return err
	}
	if err = publishOMEMOItem(ctx, e, s, bundleNode(id), b); err != nil {
		return err
	}
	list, err := getDevices(ctx, e, s, e.account.Address)
	if err != nil && !errors.Is(err, errArchiveCursor) {
		return err
	}
	if slices.Contains(list.Devices, listedDevice{id}) {
		return nil
	}
	list.Devices = append(list.Devices, listedDevice{id})
	return publishOMEMOItem(ctx, e, s, deviceNode, list)
}
func (m *Manager) omemoPublishLoop(ctx context.Context, e *entry, s *mx.Session) {
	pending := time.NewTicker(5 * time.Second)
	defer pending.Stop()
	for ctx.Err() == nil {
		interval := 24 * time.Hour
		if err := publishDevice(ctx, e, s); err != nil {
			interval = time.Minute
		}
		refresh := time.NewTimer(interval)
		waiting := true
		for waiting {
			select {
			case <-ctx.Done():
				refresh.Stop()
				return
			case peer := <-e.omemoPeers:
				_, _ = observeDevices(ctx, e, s, peer, 0)
			case <-pending.C:
				ready, err := e.omemoDevice.Ready()
				if err == nil {
					for _, p := range ready {
						if ctx.Err() != nil {
							break
						}
						_ = m.acceptEncrypted(ctx, e, p)
					}
				}
			case <-refresh.C:
				waiting = false
			case <-e.omemoRefresh:
				waiting = false
			}
		}
		refresh.Stop()
	}
}
func (m *Manager) encryptDirect(ctx context.Context, e *entry, s *mx.Session, peer string, bodies []string) ([]omemo.Envelope, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	own, _, err := e.omemoDevice.LocalIdentity()
	if err != nil || peer == e.account.Address {
		return nil, omemo.ErrInvalid
	}
	targets, err := observeDevices(ctx, e, s, peer, 0)
	if err != nil {
		return nil, err
	}
	siblings, err := observeDevices(ctx, e, s, e.account.Address, own)
	if err != nil {
		return nil, err
	}
	targets = append(targets, siblings...)
	payloads := make([]omemo.Envelope, len(bodies))
	for i, body := range bodies {
		envelope, err := e.omemoDevice.Encrypt(ctx, targets, body)
		if err != nil {
			return nil, err
		}
		payloads[i] = envelope
	}
	return payloads, nil
}

// Self must be present in the account's published list before sending. Only
// other devices need a recipient key in the encrypted envelope.
func observeDevices(ctx context.Context, e *entry, s *mx.Session, peer string, self uint32) ([]omemo.Target, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	list, err := getDevices(ctx, e, s, peer)
	if err != nil {
		return nil, err
	}
	if self != 0 && !slices.Contains(list.Devices, listedDevice{self}) {
		return nil, omemo.ErrInvalid
	}
	observed := make(map[uint32]string, len(list.Devices))
	var targets []omemo.Target
	for _, d := range list.Devices {
		if d.ID == self {
			continue
		}
		r, err := getOMEMOItem(ctx, e, s, peer, bundleNode(d.ID))
		if err != nil {
			return nil, err
		}
		b := r.Items.Entries[0].Bundle
		if b == nil {
			return nil, omemo.ErrInvalid
		}
		fp, err := b.Fingerprint()
		if err != nil {
			return nil, err
		}
		observed[d.ID] = fp
		targets = append(targets, omemo.Target{JID: peer, DeviceID: d.ID, Bundle: *b})
	}
	if err := e.omemoDevice.Observe(peer, observed); err != nil {
		return nil, err
	}
	if self == 0 && len(targets) == 0 {
		return nil, omemo.ErrUnverified
	}
	return targets, nil
}
func (m *Manager) receiveEncrypted(ctx context.Context, e *entry, msg stanza.Message, env omemo.Envelope) error {
	peer := msg.From.Bare().String()
	if peer == e.account.Address {
		return nil
	}
	return m.acceptEncrypted(ctx, e, omemo.Pending{Peer: peer, StanzaID: msg.ID, At: time.Now().UTC(), Envelope: env})
}
func (m *Manager) acceptEncrypted(ctx context.Context, e *entry, p omemo.Pending) error {
	var receiveErr error
	err := e.omemoDevice.Receive(ctx, p, func(text string) error {
		_, requests := m.host.(application.ConversationRequestHost)
		in := application.Incoming{Encrypted: true, AccountID: e.account.ID, Kind: "direct", Address: p.Peer, ProviderID: p.Envelope.ProviderID(), CreatedAt: p.At, CheckDirectHistory: requests}
		if !readOMEMOPayload(&in, text) {
			return nil
		}
		receiveErr = m.host.Receive(ctx, in)
		return receiveErr
	})
	if err == nil {
		m.refreshOMEMOPreKeys(e, p.Envelope)
	}
	if receiveErr != nil && !errors.Is(receiveErr, application.ErrInvalid) {
		return receiveErr
	}
	var storageErr *fs.PathError
	if errors.As(err, &storageErr) {
		return err
	}
	if errors.Is(err, omemo.ErrUnverified) && p.Digest == "" {
		err = e.omemoDevice.Hold(p.Peer, p.StanzaID, p.Envelope, p.At)
		if err == nil {
			select {
			case e.omemoPeers <- p.Peer:
			default:
			}
		}
		if errors.As(err, &storageErr) {
			return err
		}
	} else if err != nil && p.Digest != "" && !errors.Is(err, omemo.ErrUnverified) {
		return e.omemoDevice.Discard(p)
	}
	return nil
}

func (m *Manager) refreshOMEMOPreKeys(e *entry, env omemo.Envelope) {
	for _, key := range env.Header.Keys {
		if key.PreKey {
			select {
			case e.omemoRefresh <- struct{}{}:
			default:
			}
			return
		}
	}
}
