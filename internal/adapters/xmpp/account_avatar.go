package xmpp

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"image/png"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"mellium.im/xmpp/stanza"
)

func (e *entry) lockAccountAvatar(ctx context.Context) error {
	select {
	case e.accountAvatarGate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *entry) unlockAccountAvatar() { <-e.accountAvatarGate }

func activeAvatarPNG(payload string) (string, error) {
	var metadata avatarPEP
	if err := xml.Unmarshal([]byte("<root>"+payload+"</root>"), &metadata); err != nil {
		return "", err
	}
	for _, item := range metadata.Pubsub.Items.Item {
		if item.Metadata == nil {
			continue
		}
		for _, info := range item.Metadata.Info {
			if info.ID == item.ID && validSHA1(info.ID) && info.Type == "image/png" && info.URL == "" {
				return info.ID, nil
			}
		}
	}
	return "", nil
}

func (m *Manager) SetAccountAvatar(ctx context.Context, a domain.Account, image []byte) error {
	host, ok := m.host.(application.AvatarHost)
	if !ok {
		return application.ErrProtocolUnavailable
	}
	m.mu.RLock()
	e := m.entries[a.ID]
	m.mu.RUnlock()
	if e == nil {
		return application.ErrStateConflict
	}
	e.mu.RLock()
	session, sessionCtx, state := e.session, e.archiveSessionCtx, e.state.State
	e.mu.RUnlock()
	if session == nil || sessionCtx == nil || state != "connected" {
		return application.ErrStateConflict
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	stop := context.AfterFunc(sessionCtx, cancel)
	defer stop()
	if err := e.lockAccountAvatar(ctx); err != nil {
		return application.ErrProtocolUnavailable
	}
	defer e.unlockAccountAvatar()
	if sessionCtx.Err() != nil {
		return application.ErrStateConflict
	}
	if ctx.Err() != nil {
		return application.ErrProtocolUnavailable
	}

	config, err := png.DecodeConfig(bytes.NewReader(image))
	if err != nil {
		return application.ErrInvalid
	}
	sum := sha1.Sum(image) // XEP-0084 uses SHA-1 as the pubsub item ID.
	hash := hex.EncodeToString(sum[:])
	payload, err := readArchiveIQ(ctx, e, session, a.Address, stanza.GetIQ, avatarMetadataQuery)
	if err != nil && !errors.Is(err, errArchiveCursor) {
		return application.ErrProtocolUnavailable
	}
	previousHash, err := activeAvatarPNG(payload)
	if err != nil {
		return application.ErrProtocolUnavailable
	}
	readData := func(id string) ([]byte, error) {
		result, err := readArchiveIQ(ctx, e, session, a.Address, stanza.GetIQ, avatarDataQuery(id))
		if err != nil {
			return nil, err
		}
		return avatarItemData(result, id)
	}
	var previous []byte
	if previousHash != "" {
		previous, _ = readData(previousHash)
		if previousHash == hash && bytes.Equal(image, previous) {
			return host.StoreAvatar(ctx, a.ID, a.Address, "pep:"+hash, image)
		}
	}
	publish := func(node, id, body string) error {
		const pubsub = `<pubsub xmlns="http://jabber.org/protocol/pubsub">`
		request := pubsub + `<publish node="` + node + `"><item id="` + id + `">` + body + `</item></publish></pubsub>`
		_, err := readArchiveIQ(ctx, e, session, a.Address, stanza.SetIQ, request)
		return err
	}
	publishData := func(id string, data []byte) error {
		body := `<data xmlns="urn:xmpp:avatar:data">` + base64.StdEncoding.EncodeToString(data) + `</data>`
		return publish("urn:xmpp:avatar:data", id, body)
	}

	// Data must exist before the metadata announces it. Reconcile even on a
	// lost acknowledgement, since the server may have applied the update.
	defer func() {
		e.avatarMu.Lock()
		e.avatarDirty[a.Address] = ""
		e.avatarMu.Unlock()
		m.wakeAvatars(e)
	}()
	if err := publishData(hash, image); err != nil {
		return application.ErrProtocolUnavailable
	}
	if previousHash != hash {
		body := fmt.Sprintf(`<metadata xmlns="%s"><info id="%s" type="image/png" bytes="%d" width="%d" height="%d"/></metadata>`,
			avatarMetadataNS, hash, len(image), config.Width, config.Height)
		if err := publish(avatarMetadataNS, hash, body); err != nil {
			// Some servers retain only the most recent data item. If metadata
			// was refused, restore evicted bytes while that photo is still active.
			var refused archiveIQRejected
			if len(previous) > 0 && errors.As(err, &refused) {
				if _, missing := readData(previousHash); errors.Is(missing, errArchiveCursor) || errors.Is(missing, errAvatar) {
					latest, getErr := readArchiveIQ(ctx, e, session, a.Address, stanza.GetIQ, avatarMetadataQuery)
					if active, parseErr := activeAvatarPNG(latest); getErr == nil && parseErr == nil && active == previousHash {
						_ = publishData(previousHash, previous)
					}
				}
			}
			return application.ErrProtocolUnavailable
		}
	}
	persist, done := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer done()
	return host.StoreAvatar(persist, a.ID, a.Address, "pep:"+hash, image)
}

var _ application.AccountAvatarProtocol = (*Manager)(nil)
