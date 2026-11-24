package xmpp

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/araihu/oie/internal/adapters/previewfetch"
	"github.com/araihu/oie/internal/adapters/xmpp/omemo"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func (m *Manager) SetPrivateOMEMOUploadOrigin(value string) error {
	if value == "" {
		return nil
	}
	u, err := url.Parse(value)
	if err != nil || !secureSlotURL(value) || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.ForceQuery || strings.Contains(value, "#") {
		return errors.New("OMEMO upload origin must be a bare HTTPS origin")
	}
	m.privateUploadHost = u.Host
	m.privateUploadClient = &http.Client{
		Timeout: 2 * time.Minute,
		Transport: &http.Transport{
			TLSClientConfig:    &tls.Config{RootCAs: m.roots, MinVersion: tls.VersionTLS12},
			DisableCompression: true, MaxResponseHeaderBytes: 16 << 10,
			ResponseHeaderTimeout: 8 * time.Second, IdleConnTimeout: 30 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return nil
}

func (m *Manager) OpenOMEMOAttachment(ctx context.Context, file domain.EncryptedAttachment) (application.MediaRead, error) {
	var empty application.MediaRead
	blocked := application.MediaFailure{Code: "media_blocked"}
	failed := application.MediaFailure{Code: "media_fetch_failed"}
	if !secureSlotURL(file.URL) || strings.Contains(file.URL, "#") {
		return empty, blocked
	}
	select {
	case m.encryptedDownloads <- struct{}{}:
	default:
		return empty, application.MediaFailure{Code: "media_busy"}
	}
	accepted := false
	defer func() {
		if !accepted {
			<-m.encryptedDownloads
		}
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, file.URL, nil)
	if err != nil {
		return empty, blocked
	}
	client := m.publicUploadClient
	if m.privateUploadClient != nil && strings.EqualFold(req.URL.Host, m.privateUploadHost) {
		client = m.privateUploadClient
	}
	req.Header.Set("Accept-Encoding", "identity")
	res, err := client.Do(req)
	if err != nil {
		var denial application.PreviewFailure
		if errors.As(err, &denial) && denial.Code == "blocked" {
			return empty, blocked
		}
		return empty, failed
	}
	defer func() { _ = res.Body.Close() }()
	const limit = application.MaxStagedAttachmentSize + omemo.MediaTagSize
	if res.StatusCode != http.StatusOK || res.ContentLength > limit ||
		res.Header.Get("Content-Encoding") != "" && res.Header.Get("Content-Encoding") != "identity" {
		return empty, failed
	}
	ciphertext, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil || len(ciphertext) > limit {
		return empty, failed
	}
	plain, err := omemo.OpenMedia(file, ciphertext)
	if err != nil {
		return empty, failed
	}
	accepted = true
	body := &omemoDownloadBody{Reader: bytes.NewReader(plain), release: func() {
		clear(plain)
		<-m.encryptedDownloads
	}}
	return application.MediaRead{FileName: file.FileName, Body: body,
		MediaType: "application/octet-stream", Length: int64(len(plain)), Total: int64(len(plain))}, nil
}

func publicOMEMOUploadClient() *http.Client {
	client := previewfetch.PublicHTTPSClient()
	client.Timeout = 2 * time.Minute
	return client
}

type omemoDownloadBody struct {
	*bytes.Reader
	once    sync.Once
	release func()
}

func (b *omemoDownloadBody) Close() error {
	b.once.Do(b.release)
	return nil
}
