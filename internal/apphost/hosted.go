package apphost

import (
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"

	"github.com/araihu/goshtoso/assets"
	"github.com/araihu/oie/internal/adapters/hostauth"
	"github.com/araihu/oie/internal/adapters/web"
)

// HostedConfig is the externally visible HTTPS origin and its transport.
type HostedConfig struct {
	PublicURL, TLSCertFile, TLSKeyFile, TrustedTLSProxies string
	OIDCClientID, OIDCClientSecretFile                    string
}

type hostedServer struct {
	auth    *hostauth.Auth
	origin  *url.URL
	tls     *tls.Config
	proxies []netip.Prefix
}

func configureHosted(cfg Config) (*hostedServer, error) {
	c := cfg.Hosted
	origin, err := url.Parse(c.PublicURL)
	if err != nil || origin.Scheme != "https" || origin.Hostname() == "" || origin.User != nil ||
		(origin.Path != "" && origin.Path != "/") || origin.RawPath != "" || origin.ForceQuery ||
		origin.RawQuery != "" || strings.Contains(c.PublicURL, "#") {
		return nil, errors.New("hosted mode requires --public-url with an HTTPS origin")
	}
	origin.Path = ""
	origin.Host = strings.ToLower(origin.Host)
	if origin.Port() == "443" {
		origin.Host = strings.TrimSuffix(origin.Host, ":443")
	}
	hosted := &hostedServer{origin: origin}
	if c.TrustedTLSProxies == "" {
		if c.TLSCertFile == "" || c.TLSKeyFile == "" {
			return nil, errors.New("hosted mode requires a TLS certificate and key or explicit trusted TLS proxies")
		}
		certificate, err := tls.LoadX509KeyPair(c.TLSCertFile, c.TLSKeyFile)
		if err != nil {
			return nil, err
		}
		hosted.tls = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
	} else {
		if c.TLSCertFile != "" || c.TLSKeyFile != "" {
			return nil, errors.New("direct TLS and TLS proxy settings are mutually exclusive")
		}
		for value := range strings.SplitSeq(c.TrustedTLSProxies, ",") {
			prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
			if err != nil || prefix.Bits() == 0 || prefix.Addr().Is4In6() {
				return nil, errors.New("--trusted-tls-proxies requires explicit IPv4 or IPv6 CIDRs")
			}
			// The proxy-to-listener connection carries owner credentials in HTTP.
			// Restrict trusted source ranges to subnets of a private or loopback network.
			if !privateProxyPrefix(prefix) {
				return nil, errors.New("trusted TLS proxies must use private or loopback CIDRs")
			}
			hosted.proxies = append(hosted.proxies, prefix.Masked())
		}
	}
	var secret string
	if c.OIDCClientSecretFile != "" {
		raw, err := os.ReadFile(c.OIDCClientSecretFile)
		if err != nil {
			return nil, err
		}
		secret = strings.TrimSpace(string(raw))
	}
	hosted.auth, err = hostauth.New(hostauth.NewStore(cfg.DataDir), origin.String(), c.OIDCClientID, secret)
	if err != nil {
		return nil, err
	}
	return hosted, nil
}

func privateProxyPrefix(prefix netip.Prefix) bool {
	for _, network := range []netip.Prefix{
		netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("172.16.0.0/12"), netip.MustParsePrefix("192.168.0.0/16"),
		netip.MustParsePrefix("::1/128"), netip.MustParsePrefix("fc00::/7"),
	} {
		if prefix.Bits() >= network.Bits() && network.Contains(prefix.Masked().Addr()) {
			return true
		}
	}
	return false
}

func (s *hostedServer) handler(api, browser, diagnostics http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/", preventCaching(s.auth.API(api)))
	if browser != nil {
		mux.Handle("GET /assets/", assets.Handler())
		mux.HandleFunc("GET /auth/assets/", web.HostedSignInAsset)
		mux.Handle("/", preventCaching(browser))
	}
	if diagnostics != nil {
		authorized := s.auth.API(diagnostics)
		mux.Handle(web.DiagnosticsPath, preventCaching(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if browser != nil && len(r.Header.Values("Authorization")) == 0 {
				browser.ServeHTTP(w, r)
				return
			}
			authorized.ServeHTTP(w, r)
		})))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !s.secureRequest(r) {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		mux.ServeHTTP(w, r)
	})
}

func (s *hostedServer) secureRequest(r *http.Request) bool {
	host := strings.TrimSuffix(strings.ToLower(r.Host), ":443")
	if host != s.origin.Host {
		return false
	}
	if s.tls != nil {
		return r.TLS != nil
	}
	if len(r.Header.Values("X-Forwarded-Proto")) != 1 || r.Header.Get("X-Forwarded-Proto") != "https" {
		return false
	}
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(peer)
	if err != nil {
		return false
	}
	for _, trusted := range s.proxies {
		if trusted.Contains(ip.Unmap()) {
			return true
		}
	}
	return false
}

func preventCaching(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(noStoreWriter{w}, r)
	})
}

// Hosted responses must never be replayed from an HTTP cache after logout.
type noStoreWriter struct{ http.ResponseWriter }

func (w noStoreWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w noStoreWriter) WriteHeader(status int) {
	w.Header().Set("Cache-Control", "no-store")
	w.ResponseWriter.WriteHeader(status)
}

func (w noStoreWriter) Write(data []byte) (int, error) {
	w.Header().Set("Cache-Control", "no-store")
	return w.ResponseWriter.Write(data)
}

func (w noStoreWriter) FlushError() error {
	w.Header().Set("Cache-Control", "no-store")
	return http.NewResponseController(w.ResponseWriter).Flush()
}
