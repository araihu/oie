package xmpp

import (
	"context"
	"crypto/tls"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/disco"
	"mellium.im/xmpp/stanza"
)

const uploadNamespace = "urn:xmpp:http:upload:0"

const (
	attachmentProgressLifetime = 15 * time.Minute
	maxAttachmentProgress      = 16
)

type attachmentUploadProgress struct {
	service    string
	roomIntent domain.ID
	links      []string
	updatedAt  time.Time
}

var errUploadRejected = errors.New("upload rejected")

func recordUploadLimit(e *entry, session *mx.Session, service string, info disco.Info) {
	for _, form := range info.Form {
		kind, ok := form.Raw("FORM_TYPE")
		if !ok || len(kind) != 1 || kind[0] != uploadNamespace {
			continue
		}
		value, ok := form.Raw("max-file-size")
		if !ok || len(value) != 1 {
			continue
		}
		maxSize, err := strconv.ParseInt(value[0], 10, 64)
		if err != nil || maxSize <= 0 {
			continue
		}
		e.mu.Lock()
		if e.session == session {
			if e.uploadLimits == nil {
				e.uploadLimits = make(map[string]int64)
			}
			e.uploadLimits[service] = maxSize
		}
		e.mu.Unlock()
		return
	}
}

// uploadService is called with e.mu held.
func uploadService(e *entry, largest int64) (string, error) {
	hasService := false
	for _, service := range e.discovery.Services {
		if !serverServiceSupports(service, uploadNamespace) {
			continue
		}
		hasService = true
		if limit := e.uploadLimits[service.Address]; limit == 0 || largest <= limit {
			return service.Address, nil
		}
	}
	if e.discovery.State == "complete" {
		if hasService {
			return "", application.ErrAttachmentTooLarge
		}
		return "", application.ErrProtocolUnavailable
	}
	return "", nil
}

func (m *Manager) AttachmentAvailability(_ context.Context, a domain.Account, attachments []domain.StagedAttachment) error {
	m.mu.RLock()
	e := m.entries[a.ID]
	m.mu.RUnlock()
	if e == nil {
		return nil
	}
	var largest int64
	for _, file := range attachments {
		largest = max(largest, file.Size)
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	_, err := uploadService(e, largest)
	return err
}

type uploadSlot struct {
	Put struct {
		URL     string `xml:"url,attr"`
		Headers []struct {
			Name  string `xml:"name,attr"`
			Value string `xml:",chardata"`
		} `xml:"urn:xmpp:http:upload:0 header"`
	} `xml:"urn:xmpp:http:upload:0 put"`
	Get struct {
		URL string `xml:"url,attr"`
	} `xml:"urn:xmpp:http:upload:0 get"`
}

func secureSlotURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && len(raw) <= 4096 && parsed.Scheme == "https" && parsed.Hostname() != "" && parsed.User == nil && parsed.Fragment == "" &&
		!strings.ContainsAny(raw, " \t\r\n")
}

func decodeUploadSlot(payload string) (uploadSlot, error) {
	var result struct {
		Slots []uploadSlot `xml:"urn:xmpp:http:upload:0 slot"`
	}
	if err := xml.Unmarshal([]byte("<root>"+payload+"</root>"), &result); err != nil {
		return uploadSlot{}, errUploadRejected
	}
	if len(result.Slots) != 1 || !secureSlotURL(result.Slots[0].Put.URL) || !secureSlotURL(result.Slots[0].Get.URL) {
		return uploadSlot{}, errUploadRejected
	}
	return result.Slots[0], nil
}

func sendUpload(ctx context.Context, client *http.Client, slot uploadSlot, file application.OutgoingAttachment) error {
	ctx, cancel := context.WithTimeout(ctx, attachmentUploadTimeout(file.Size))
	defer cancel()
	body, err := file.Open(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return errUploadRejected
	}
	defer func() { _ = body.Close() }()
	var content io.Reader = body
	if file.Size == 0 {
		content = http.NoBody
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, slot.Put.URL, content)
	if err != nil {
		return errUploadRejected
	}
	req.ContentLength = file.Size
	req.Header.Set("Content-Type", file.MediaType)
	for _, field := range slot.Put.Headers {
		name := strings.NewReplacer("\r", "", "\n", "").Replace(field.Name)
		switch strings.ToLower(name) {
		case "authorization", "cookie", "expires":
			req.Header.Add(name, strings.NewReplacer("\r", "", "\n", "").Replace(field.Value))
		}
	}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	_ = response.Body.Close()
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	if response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		return fmt.Errorf("upload service HTTP %d", response.StatusCode)
	}
	return errUploadRejected
}

func attachmentUploadTimeout(size int64) time.Duration {
	// Give each file its own allowance for connection setup and a slow uplink.
	const bytesPerSecond = 64 << 10
	return 30*time.Second + time.Duration(size)*time.Second/bytesPerSecond
}

// attachmentUploadCurrent is called with e.mu held.
func (e *entry) attachmentUploadCurrent(session *mx.Session, out application.Outgoing) bool {
	if e.session != session || e.state.State != "connected" {
		return false
	}
	if out.Kind != "room" {
		return true
	}
	room := e.rooms[out.Address]
	return room != nil && room.session == session && room.snapshot.State == "joined" && room.snapshot.IntentRevisionID == out.RoomIntentRevisionID
}

func (e *entry) saveAttachmentUpload(session *mx.Session, out application.Outgoing, service string, links []string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.attachmentUploadCurrent(session, out) {
		return false
	}
	if e.attachmentUploads == nil {
		e.attachmentUploads = make(map[domain.ID]attachmentUploadProgress)
	}
	now := time.Now()
	if _, exists := e.attachmentUploads[out.ID]; !exists && len(e.attachmentUploads) >= maxAttachmentProgress {
		var oldestID domain.ID
		var oldest time.Time
		for id, progress := range e.attachmentUploads {
			if oldestID == "" || progress.updatedAt.Before(oldest) {
				oldestID, oldest = id, progress.updatedAt
			}
		}
		delete(e.attachmentUploads, oldestID)
	}
	e.attachmentUploads[out.ID] = attachmentUploadProgress{service: service, roomIntent: out.RoomIntentRevisionID, links: slices.Clone(links), updatedAt: now}
	return true
}

func (e *entry) forgetAttachmentUpload(session *mx.Session, id domain.ID) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.session == session {
		delete(e.attachmentUploads, id)
	}
}

func (m *Manager) uploadMessageAttachments(ctx context.Context, e *entry, session *mx.Session, out application.Outgoing) (string, string) {
	var largest int64
	for _, file := range out.Attachments {
		largest = max(largest, file.Size)
	}
	e.mu.RLock()
	service, err := uploadService(e, largest)
	current := e.attachmentUploadCurrent(session, out)
	sessionCtx := e.archiveSessionCtx
	progress := e.attachmentUploads[out.ID]
	e.mu.RUnlock()
	canceled := func() bool { return ctx.Err() != nil || (sessionCtx != nil && sessionCtx.Err() != nil) }
	if !current || canceled() {
		return "", application.NotSent
	}
	if sessionCtx != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
		stop := context.AfterFunc(sessionCtx, cancel)
		defer stop()
	}
	if err != nil {
		return "", application.Failed
	}
	if service == "" {
		return "", application.NotSent
	}

	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: m.roots, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var links []string
	if progress.service == service && progress.roomIntent == out.RoomIntentRevisionID &&
		time.Since(progress.updatedAt) < attachmentProgressLifetime && len(progress.links) <= len(out.Attachments) {
		// Once dispatch starts the composition cannot change. Reuse completed
		// uploads only for this message, provider service and connection.
		links = slices.Clone(progress.links)
	}
	for _, file := range out.Attachments[len(links):] {
		request := fmt.Sprintf(`<request xmlns="%s" filename="%s" size="%d" content-type="%s"/>`, uploadNamespace, escaped(file.FileName), file.Size, escaped(file.MediaType))
		reply, err := readArchiveIQ(ctx, e, session, service, stanza.GetIQ, request)
		if canceled() {
			return "", application.NotSent
		}
		if err != nil {
			if errors.Is(err, errArchive) || errors.Is(err, errDetailPermission) || errors.Is(err, errArchiveCursor) {
				return "", application.Failed
			}
			return "", application.NotSent
		}
		slot, err := decodeUploadSlot(reply)
		if err != nil {
			return "", application.Failed
		}
		e.mu.RLock()
		current = e.attachmentUploadCurrent(session, out)
		e.mu.RUnlock()
		if !current {
			return "", application.NotSent
		}
		err = sendUpload(ctx, client, slot, file)
		if canceled() {
			return "", application.NotSent
		}
		if err != nil {
			if errors.Is(err, errUploadRejected) {
				return "", application.Failed
			}
			return "", application.NotSent
		}
		links = append(links, slot.Get.URL)
		if !e.saveAttachmentUpload(session, out, service, links) {
			return "", application.NotSent
		}
	}
	if canceled() {
		return "", application.NotSent
	}
	if out.OriginalText != "" {
		links = append([]string{out.OriginalText}, links...)
	}
	return strings.Join(links, "\n"), ""
}
