package previewfetch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/araihu/oie/internal/application"
)

type resolveFunc func(context.Context, string, string) ([]netip.Addr, error)

func (f resolveFunc) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	return f(ctx, network, host)
}
func failCode(err error) string {
	var f application.PreviewFailure
	if errors.As(err, &f) {
		return f.Code
	}
	return ""
}
func TestPublicFetchPolicyRejectsPrivateAndMixedDNS(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "::1", "fc00::1", "fec0::1", "::ffff:127.0.0.1", "64:ff9b::7f00:1", "2001:db8::1", "2002:7f00:1::"} {
		if publicIP(netip.MustParseAddr(address)) {
			t.Fatal("local/special address accepted", address)
		}
	}
	for _, addresses := range [][]netip.Addr{{netip.MustParseAddr("127.0.0.1")}, {netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("10.0.0.1")}} {
		dialed := false
		f := newFetcher(resolveFunc(func(context.Context, string, string) ([]netip.Addr, error) { return addresses, nil }), func(context.Context, string, string) (net.Conn, error) {
			dialed = true
			return nil, fmt.Errorf("must not dial")
		})
		if _, err := f.Link(t.Context(), "https://metadata.example/"); failCode(err) != "blocked" || dialed {
			t.Fatal("unsafe destination was contacted", err, dialed)
		}
	}
}
func fixtureFetcher(t *testing.T, handler http.HandlerFunc) (*Fetcher, *atomic.Int32) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	count := new(atomic.Int32)
	resolver := resolveFunc(func(_ context.Context, _ string, host string) ([]netip.Addr, error) {
		if host != "public.example" {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	})
	return newFetcher(resolver, func(ctx context.Context, network, address string) (net.Conn, error) {
		if !strings.HasPrefix(address, "8.8.8.8:") {
			t.Error("unverified literal dial", address)
		}
		count.Add(1)
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}), count
}
func TestRedirectsAndRebindingCannotReachPrivateServices(t *testing.T) {
	f, _ := fixtureFetcher(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://internal.example/metadata", http.StatusFound)
	})
	if _, err := f.Link(t.Context(), "http://public.example/"); failCode(err) != "blocked" {
		t.Fatal("unsafe redirect accepted", err)
	}
	calls := 0
	dialed := false
	f = newFetcher(resolveFunc(func(context.Context, string, string) ([]netip.Addr, error) {
		calls++
		ip := "8.8.8.8"
		if calls > 1 {
			ip = "127.0.0.1"
		}
		return []netip.Addr{netip.MustParseAddr(ip)}, nil
	}), func(context.Context, string, string) (net.Conn, error) {
		dialed = true
		return nil, fmt.Errorf("must not dial")
	})
	if _, err := f.Link(t.Context(), "http://public.example/"); failCode(err) != "blocked" || dialed {
		t.Fatal("rebinding reached dialer", err, dialed)
	}
	for _, address := range []string{"file:///etc/passwd", "http://user:password@public.example/", "http://[::1]/"} {
		if _, err := f.Link(t.Context(), address); failCode(err) != "blocked" {
			t.Fatal("invalid scheme/credentials accepted", err)
		}
	}
}
func TestBoundedMetadataAndOriginalImageSeparation(t *testing.T) {
	thumbnail := []byte{137, 80, 78, 71, 13, 10, 26, 10, 1, 2, 3}
	f, _ := fixtureFetcher(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/page":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprint(w, `<html><head><title>Fallback</title><meta property="og:title" content="Rich &amp; plain"><meta name="description" content="Words"><meta property="og:site_name" content="Publisher"><meta property="og:image" content="http://internal.example/private"></head><body><script>bad()</script></body></html>`)
		case "/image":
			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Content-Length", "99999999")
		case "/large":
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, strings.Repeat("x", (1<<20)+1))
		case "/large-body":
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<html><head><script>`, strings.Repeat("x", 780<<10),
				`</script><meta property="og:title" content="Late title"><meta property="og:image" content="/thumbnail.png"></head><body>`,
				strings.Repeat("x", 1<<20))
		case "/thumbnail.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(thumbnail)
		}
	})
	metadata, err := f.Link(t.Context(), "http://public.example/page")
	if err != nil || metadata.Title != "Rich & plain" || metadata.Description != "Words" || metadata.SiteName != "Publisher" || len(metadata.Image) != 0 {
		t.Fatal("metadata extraction", metadata, err)
	}
	metadata, err = f.Link(t.Context(), "http://public.example/image")
	if err != nil || !metadata.IsImage || len(metadata.Image) != 0 {
		t.Fatal("dynamic original downloaded as thumbnail", err)
	}
	if _, err = f.Image(t.Context(), "http://public.example/image"); failCode(err) != "too_large" {
		t.Fatal("oversized original accepted", err)
	}
	if _, err = f.Link(t.Context(), "http://public.example/large"); failCode(err) != "too_large" {
		t.Fatal("unbounded HTML accepted", err)
	}
	metadata, err = f.Link(t.Context(), "http://public.example/large-body")
	if err != nil || metadata.Title != "Late title" || !bytes.Equal(metadata.Image, thumbnail) {
		t.Fatal("metadata in a large page head", metadata, err)
	}
}

func TestSuppliedThumbnailHasSmallLimitAndPublicDestinationPolicy(t *testing.T) {
	f, count := fixtureFetcher(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		if r.URL.Path == "/huge" {
			w.Header().Set("Content-Length", "1048577")
			return
		}
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "http://internal.example/poster", http.StatusFound)
			return
		}
		_, _ = fmt.Fprint(w, "small-image-placeholder")
	})
	if _, err := f.SmallThumbnail(t.Context(), "http://public.example/huge"); failCode(err) != "too_large" {
		t.Fatal(err)
	}
	if _, err := f.SmallThumbnail(t.Context(), "http://public.example/redirect"); failCode(err) != "blocked" {
		t.Fatal(err)
	}
	before := count.Load()
	if _, err := f.SmallThumbnail(t.Context(), "http://127.0.0.1/private"); failCode(err) != "blocked" || count.Load() != before {
		t.Fatal("local destination fetched", err)
	}
	if data, err := f.SmallThumbnail(t.Context(), "http://public.example/small"); err != nil || len(data) == 0 {
		t.Fatal(err)
	}
}
