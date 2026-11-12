// Package previewfetch enriches message URLs without exposing the engine network.
package previewfetch

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/araihu/oie/internal/adapters/mediafiles"
	"github.com/araihu/oie/internal/application"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
	"golang.org/x/net/idna"
)

type resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}
type Fetcher struct {
	client          *http.Client
	mediaClient     *http.Client
	mediaActive     chan struct{}
	resolver        resolver
	dial            func(context.Context, string, string) (net.Conn, error)
	mu              sync.Mutex
	cache           map[string]cachedLink
	cacheGeneration uint64
}

func New() *Fetcher {
	return newFetcher(net.DefaultResolver, (&net.Dialer{Timeout: 5 * time.Second}).DialContext)
}
func newFetcher(r resolver, dial func(context.Context, string, string) (net.Conn, error)) *Fetcher {
	f := &Fetcher{resolver: r, dial: dial}
	transport := &http.Transport{Proxy: nil, DialContext: f.dialPublic, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second, MaxResponseHeaderBytes: 32 << 10, MaxConnsPerHost: 1, MaxIdleConns: 2, IdleConnTimeout: time.Minute}
	f.client = &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return application.PreviewFailure{Code: "blocked"}
		}
		_, err := f.destination(req.Context(), req.URL)
		return err
	}}
	mediaTransport := transport.Clone()
	mediaTransport.DisableCompression = true
	mediaTransport.MaxConnsPerHost = 2
	f.mediaClient = &http.Client{Transport: mediaTransport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		req.Header.Del("Authorization")
		req.Header.Del("Cookie")
		req.Header.Del("Referer")
		return f.client.CheckRedirect(req, via)
	}}
	f.mediaActive = make(chan struct{}, 4)
	return f
}

var denied = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"), netip.MustParsePrefix("64:ff9b:1::/48"), netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
}

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if ip.Is6() && !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return false
	}
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.Zone() != "" {
		return false
	}
	for _, prefix := range denied {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}
func (f *Fetcher) addresses(ctx context.Context, host string) ([]netip.Addr, error) {
	host, err := idna.Lookup.ToASCII(host)
	if err != nil || host == "" {
		return nil, application.PreviewFailure{Code: "blocked"}
	}
	addresses, err := f.resolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(addresses) == 0 {
		return nil, application.PreviewFailure{Code: "fetch_failed"}
	}
	for _, ip := range addresses {
		if !publicIP(ip) {
			return nil, application.PreviewFailure{Code: "blocked"}
		}
	}
	return addresses, nil
}
func (f *Fetcher) destination(ctx context.Context, u *url.URL) ([]netip.Addr, error) {
	if (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.Opaque != "" || len(u.String()) > 4096 {
		return nil, application.PreviewFailure{Code: "blocked"}
	}
	if port := u.Port(); port != "" {
		p, err := strconv.Atoi(port)
		if err != nil || p < 1 || p > 65535 {
			return nil, application.PreviewFailure{Code: "blocked"}
		}
	}
	return f.addresses(ctx, u.Hostname())
}
func (f *Fetcher) dialPublic(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, application.PreviewFailure{Code: "blocked"}
	}
	addresses, err := f.addresses(ctx, host)
	if err != nil {
		return nil, err
	}
	// Connect to a verified literal address, never resolve again inside the dialer.
	var last error
	for _, ip := range addresses {
		conn, err := f.dial(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		last = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, last
}
func (f *Fetcher) get(ctx context.Context, address string) (*http.Response, error) {
	u, err := url.Parse(address)
	if err != nil {
		return nil, application.PreviewFailure{Code: "blocked"}
	}
	if _, err = f.destination(ctx, u); err != nil {
		return nil, err
	}
	u.Fragment = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, application.PreviewFailure{Code: "blocked"}
	}
	req.Header.Set("User-Agent", "Oie/0.5 link-preview")
	req.Header.Set("Accept", "text/html, image/png, image/jpeg, image/gif;q=0.9")
	response, err := f.client.Do(req)
	if err != nil {
		var failure application.PreviewFailure
		if errors.As(err, &failure) {
			return nil, failure
		}
		return nil, application.PreviewFailure{Code: "fetch_failed"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_ = response.Body.Close()
		code := "fetch_failed"
		if response.StatusCode == 404 || response.StatusCode == 410 {
			code = "not_found"
		}
		return nil, application.PreviewFailure{Code: code}
	}
	return response, nil
}
func readBounded(response *http.Response, limit int) ([]byte, error) {
	if response.ContentLength > int64(limit) {
		return nil, application.PreviewFailure{Code: "too_large"}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, int64(limit)+1))
	if err != nil {
		return nil, application.PreviewFailure{Code: "fetch_failed"}
	}
	if len(data) > limit {
		return nil, application.PreviewFailure{Code: "too_large"}
	}
	return data, nil
}
func imageMIME(m string) bool { return m == "image/png" || m == "image/jpeg" || m == "image/gif" }
func (f *Fetcher) Image(ctx context.Context, address string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	response, err := f.get(ctx, address)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	mimeType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if mimeType != "" && !imageMIME(mimeType) && mimeType != "application/octet-stream" {
		return nil, application.PreviewFailure{Code: "unsupported"}
	}
	return readBounded(response, mediafiles.MaxOriginal)
}

// SmallThumbnail uses the same public-address and redirect policy as previews.
// Bound advertised images separately from originals.
func (f *Fetcher) SmallThumbnail(ctx context.Context, address string) ([]byte, error) {
	response, err := f.get(ctx, address)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	mediaType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if !imageMIME(mediaType) {
		return nil, application.PreviewFailure{Code: "unsupported"}
	}
	return readBounded(response, 1<<20)
}
func (f *Fetcher) smallImage(ctx context.Context, address string) []byte {
	response, err := f.get(ctx, address)
	if err != nil {
		return nil
	}
	defer func() { _ = response.Body.Close() }()
	mediaType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if !imageMIME(mediaType) {
		return nil
	}
	data, err := readBounded(response, mediafiles.MaxPreview)
	if err != nil {
		return nil
	}
	return data
}
func plain(s string, limit int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && !unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > limit {
		s = string(r[:limit])
	}
	return s
}

type cachedLink struct {
	metadata            application.PreviewMetadata
	err                 error
	expires, timeStored time.Time
}

func (f *Fetcher) Link(ctx context.Context, address string) (application.PreviewMetadata, error) {
	f.mu.Lock()
	cached, found := f.cache[address]
	generation := f.cacheGeneration
	f.mu.Unlock()
	if found && time.Now().Before(cached.expires) {
		cached.metadata.Image = bytes.Clone(cached.metadata.Image)
		cached.metadata.Favicon = bytes.Clone(cached.metadata.Favicon)
		return cached.metadata, cached.err
	}
	metadata, err := f.link(ctx, address)
	if ctx.Err() != nil {
		return metadata, ctx.Err()
	}
	ttl := 24 * time.Hour
	if err != nil {
		ttl = 5 * time.Minute
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if generation != f.cacheGeneration {
		return metadata, err
	}
	if f.cache == nil {
		f.cache = map[string]cachedLink{}
	}
	if len(f.cache) >= 128 {
		var oldest string
		var at time.Time
		for key, value := range f.cache {
			if oldest == "" || value.timeStored.Before(at) {
				oldest = key
				at = value.timeStored
			}
		}
		delete(f.cache, oldest)
	}
	copy := metadata
	copy.Image = bytes.Clone(copy.Image)
	copy.Favicon = bytes.Clone(copy.Favicon)
	f.cache[address] = cachedLink{metadata: copy, err: err, expires: time.Now().Add(ttl), timeStored: time.Now()}
	return metadata, err
}
func (f *Fetcher) link(ctx context.Context, address string) (application.PreviewMetadata, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	response, err := f.get(ctx, address)
	if err != nil {
		return application.PreviewMetadata{}, err
	}
	closePage := sync.OnceFunc(func() { _ = response.Body.Close() })
	defer closePage()
	out := application.PreviewMetadata{SiteName: plain(response.Request.URL.Hostname(), 200)}
	mediaType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	// A dynamic image endpoint is still an original, not an automatic thumbnail.
	if imageMIME(mediaType) {
		out.IsImage = true
		return out, nil
	}
	if mediaType != "text/html" && mediaType != "application/xhtml+xml" {
		return out, application.PreviewFailure{Code: "unsupported"}
	}
	// Bound reads of the document head independently of the page's total size.
	head := &io.LimitedReader{R: response.Body, N: 1 << 20}
	reader, err := charset.NewReader(head, response.Header.Get("Content-Type"))
	if err != nil {
		return out, application.PreviewFailure{Code: "unsupported"}
	}
	metadata := map[string]string{}
	tokenizer := html.NewTokenizer(reader)
	inTitle := false
	title := ""
	icons := []string{}
parseHead:
	for {
		kind := tokenizer.Next()
		if kind == html.ErrorToken {
			if tokenizer.Err() != io.EOF {
				return out, application.PreviewFailure{Code: "unsupported"}
			}
			if head.N == 0 {
				return out, application.PreviewFailure{Code: "too_large"}
			}
			break
		}
		token := tokenizer.Token()
		switch kind {
		case html.StartTagToken, html.SelfClosingTagToken:
			if token.Data == "body" {
				break parseHead
			}
			if token.Data == "title" {
				inTitle = true
			}
			if token.Data == "link" && len(icons) < 3 {
				rel, href, kind := "", "", ""
				for _, attr := range token.Attr {
					switch strings.ToLower(attr.Key) {
					case "rel":
						rel = strings.ToLower(attr.Val)
					case "href":
						href = attr.Val
					case "type":
						kind = strings.ToLower(attr.Val)
					}
				}
				icon := false
				for _, part := range strings.Fields(rel) {
					if part == "icon" || part == "apple-touch-icon" || part == "apple-touch-icon-precomposed" {
						icon = true
					}
				}
				if icon && href != "" && kind != "image/svg+xml" {
					if address, parseErr := response.Request.URL.Parse(href); parseErr == nil {
						icons = append(icons, address.String())
					}
				}
			}
			if token.Data == "meta" {
				key, value := "", ""
				for _, attr := range token.Attr {
					switch strings.ToLower(attr.Key) {
					case "property", "name":
						key = strings.ToLower(attr.Val)
					case "content":
						value = attr.Val
					}
				}
				if metadata[key] == "" {
					metadata[key] = value
				}
			}
		case html.TextToken:
			if inTitle && len(title) < 4096 {
				title += token.Data
			}
		case html.EndTagToken:
			if token.Data == "head" {
				break parseHead
			}
			if token.Data == "title" {
				inTitle = false
			}
		}
	}
	// Images and icons can use the same host, which allows one connection at a time.
	closePage()
	choose := func(keys ...string) string {
		for _, key := range keys {
			if metadata[key] != "" {
				return metadata[key]
			}
		}
		return ""
	}
	out.Title = plain(choose("og:title", "twitter:title"), 300)
	if out.Title == "" {
		out.Title = plain(title, 300)
	}
	if out.Title == "" {
		out.Title = out.SiteName
	}
	out.Description = plain(choose("og:description", "twitter:description", "description"), 1000)
	if site := plain(metadata["og:site_name"], 200); site != "" {
		out.SiteName = site
	}
	if address := choose("og:image:secure_url", "og:image", "twitter:image"); address != "" {
		imageURL, err := response.Request.URL.Parse(address)
		if err == nil {
			out.Image = f.smallImage(ctx, imageURL.String())
		}
	}
	if len(out.Image) == 0 {
		if len(icons) == 0 {
			if icon, parseErr := response.Request.URL.Parse("/favicon.ico"); parseErr == nil {
				icons = append(icons, icon.String())
			}
		}
		for _, icon := range icons {
			if out.Favicon = f.siteIcon(ctx, icon); len(out.Favicon) > 0 {
				break
			}
		}
	}
	return out, nil
}

// Invalidation also prevents an older in-flight result from re-populating cache.
func (f *Fetcher) InvalidateLink(address string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.cache, address)
	f.cacheGeneration++
}

func (f *Fetcher) siteIcon(ctx context.Context, address string) []byte {
	response, err := f.get(ctx, address)
	if err != nil {
		return nil
	}
	defer func() { _ = response.Body.Close() }()
	kind, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if !imageMIME(kind) && kind != "image/x-icon" && kind != "image/vnd.microsoft.icon" {
		return nil
	}
	data, err := readBounded(response, 64<<10)
	if err != nil {
		return nil
	}
	if imageMIME(kind) {
		return data
	}
	// Modern ICO files may contain PNG entries. DIB/SVG icons remain unsupported
	// and fall through to local initials; no active content reaches the browser.
	if len(data) < 6 || binary.LittleEndian.Uint16(data[:2]) != 0 || binary.LittleEndian.Uint16(data[2:4]) != 1 {
		return nil
	}
	count := int(binary.LittleEndian.Uint16(data[4:6]))
	if count < 1 || count > 32 || len(data) < 6+16*count {
		return nil
	}
	for i := 0; i < count; i++ {
		entry := data[6+16*i : 6+16*(i+1)]
		size := uint64(binary.LittleEndian.Uint32(entry[8:12]))
		offset := uint64(binary.LittleEndian.Uint32(entry[12:16]))
		if offset+size > uint64(len(data)) || size < 8 {
			continue
		}
		image := data[offset : offset+size]
		if bytes.HasPrefix(image, []byte{137, 80, 78, 71, 13, 10, 26, 10}) {
			return image
		}
	}
	return nil
}
