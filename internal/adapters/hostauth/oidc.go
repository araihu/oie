package hostauth

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	loginCookie         = "__Host-oie-login"
	sessionCookie       = "__Host-oie-session"
	loginLifetime       = 10 * time.Minute
	maxLoginCookieBytes = 3900
)

type providerClient struct {
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
}

type loginAttempt struct {
	State, Nonce, PKCE, Generation string
	Destination                    string
	ExpiresAt                      time.Time
}

func (a *Auth) provider(ctx context.Context, issuer string) (*providerClient, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	a.mu.Lock()
	defer a.mu.Unlock()
	if issuer != a.issuer {
		return nil, errors.New("OIDC issuer changed; restart with the new client credentials")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a.client != nil {
		return a.client, nil
	}
	ctx = oidc.ClientContext(ctx, a.httpClient)
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, err
	}
	endpoint := provider.Endpoint()
	for _, address := range []string{endpoint.AuthURL, endpoint.TokenURL} {
		u, err := url.Parse(address)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || strings.Contains(address, "#") {
			return nil, errors.New("the provider must advertise HTTPS authorization and token endpoints")
		}
	}
	a.client = &providerClient{
		oauth: oauth2.Config{
			ClientID: a.clientID, ClientSecret: a.clientSecret,
			RedirectURL: a.origin + "/auth/callback", Endpoint: endpoint, Scopes: []string{oidc.ScopeOpenID},
		},
		verifier: provider.Verifier(&oidc.Config{ClientID: a.clientID}),
	}
	return a.client, nil
}

func newLoginCipher() (cipher.AEAD, error) {
	var secret [32]byte
	rand.Read(secret[:])
	block, err := aes.NewCipher(secret[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}

func (a *Auth) beginLogin(w http.ResponseWriter, r *http.Request, page SignInPage) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	destination := workspaceDestination(r.URL.Query().Get("next"))
	current, err := a.store.read()
	if err != nil {
		signInPage(w, r, page, http.StatusServiceUnavailable, destination)
		return
	}
	client, err := a.provider(r.Context(), current.Owner.Issuer)
	if err != nil {
		signInPage(w, r, page, http.StatusServiceUnavailable, destination)
		return
	}
	attempt := loginAttempt{
		State: newCredential(), Nonce: newCredential(), PKCE: oauth2.GenerateVerifier(),
		Generation: current.Generation, Destination: destination, ExpiresAt: time.Now().Add(loginLifetime),
	}
	var plain bytes.Buffer
	encoder := json.NewEncoder(&plain)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(attempt); err != nil {
		signInPage(w, r, page, http.StatusServiceUnavailable, destination)
		return
	}
	cookie := base64.RawURLEncoding.EncodeToString(a.loginCipher.Seal(nil, nil, plain.Bytes(), nil))
	setCookie(w, loginCookie, cookie, loginLifetime)
	http.Redirect(w, r, client.oauth.AuthCodeURL(attempt.State, oidc.Nonce(attempt.Nonce), oauth2.S256ChallengeOption(attempt.PKCE)), http.StatusSeeOther)
}

func (a *Auth) finishLogin(w http.ResponseWriter, r *http.Request, page SignInPage) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	setCookie(w, loginCookie, "", -time.Second)
	attempt, err := a.loginAttempt(r)
	if err != nil {
		signInPage(w, r, page, http.StatusUnauthorized, "/")
		return
	}
	destination := workspaceDestination(attempt.Destination)
	query := r.URL.Query()
	if len(query["code"]) != 1 || query.Get("code") == "" || query.Has("error") {
		signInPage(w, r, page, http.StatusUnauthorized, destination)
		return
	}
	current, err := a.store.read()
	if err != nil {
		signInPage(w, r, page, http.StatusServiceUnavailable, destination)
		return
	}
	if current.Generation != attempt.Generation {
		signInPage(w, r, page, http.StatusUnauthorized, destination)
		return
	}
	client, err := a.provider(r.Context(), current.Owner.Issuer)
	if err != nil {
		signInPage(w, r, page, http.StatusServiceUnavailable, destination)
		return
	}
	ctx := oidc.ClientContext(r.Context(), a.httpClient)
	token, err := client.oauth.Exchange(ctx, query.Get("code"), oauth2.VerifierOption(attempt.PKCE))
	if err != nil {
		signInPage(w, r, page, http.StatusUnauthorized, destination)
		return
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok {
		signInPage(w, r, page, http.StatusUnauthorized, destination)
		return
	}
	id, err := client.verifier.Verify(ctx, raw)
	if err != nil || id.Issuer != current.Owner.Issuer || id.Subject != current.Owner.Subject ||
		subtle.ConstantTimeCompare([]byte(id.Nonce), []byte(attempt.Nonce)) != 1 || !validAuthorizedParty(id, a.clientID) {
		signInPage(w, r, page, http.StatusUnauthorized, destination)
		return
	}
	secret, err := a.store.createSession(r.Context(), current.Generation)
	if err != nil {
		signInPage(w, r, page, http.StatusServiceUnavailable, destination)
		return
	}
	setCookie(w, sessionCookie, secret, sessionLifetime)
	http.Redirect(w, r, destination, http.StatusSeeOther)
}

func validAuthorizedParty(id *oidc.IDToken, clientID string) bool {
	var claims struct {
		AuthorizedParty string `json:"azp"`
	}
	if id.Claims(&claims) != nil {
		return false
	}
	if len(id.Audience) > 1 {
		return claims.AuthorizedParty == clientID
	}
	return claims.AuthorizedParty == "" || claims.AuthorizedParty == clientID
}

func (a *Auth) loginAttempt(r *http.Request) (loginAttempt, error) {
	cookie, err := r.Cookie(loginCookie)
	if err != nil || len(cookie.Value) > maxLoginCookieBytes {
		return loginAttempt{}, errUnauthorized
	}
	data, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil {
		return loginAttempt{}, errUnauthorized
	}
	plain, err := a.loginCipher.Open(nil, nil, data, nil)
	if err != nil {
		return loginAttempt{}, errUnauthorized
	}
	var attempt loginAttempt
	query := r.URL.Query()
	if json.Unmarshal(plain, &attempt) != nil || !time.Now().Before(attempt.ExpiresAt) ||
		len(query["state"]) != 1 ||
		subtle.ConstantTimeCompare([]byte(query.Get("state")), []byte(attempt.State)) != 1 {
		return loginAttempt{}, errUnauthorized
	}
	return attempt, nil
}

func setCookie(w http.ResponseWriter, name, value string, lifetime time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: "/", MaxAge: int(lifetime / time.Second),
		Expires: time.Now().Add(lifetime).UTC(), HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
}

type httpsOnly struct{ next http.RoundTripper }

func (t httpsOnly) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "https" || r.URL.User != nil {
		return nil, errors.New("OIDC requests require HTTPS")
	}
	return t.next.RoundTrip(r)
}

func newProviderHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second, Transport: httpsOnly{http.DefaultTransport},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Auth provides the HTTP authentication boundary. Only the host commands can
// change the owner and manage API keys.
type Auth struct {
	store        Store
	origin       string
	clientID     string
	issuer       string
	clientSecret string
	loginCipher  cipher.AEAD
	httpClient   *http.Client
	mu           sync.Mutex
	client       *providerClient
}

func New(store Store, origin, clientID, clientSecret string) (*Auth, error) {
	current, err := store.read()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(clientID) == "" || strings.TrimSpace(clientSecret) == "" {
		return nil, errors.New("hosted browser sign-in requires an OIDC client ID and secret")
	}
	loginCipher, err := newLoginCipher()
	if err != nil {
		return nil, err
	}
	return &Auth{
		store: store, origin: origin, clientID: clientID, clientSecret: clientSecret, issuer: current.Owner.Issuer,
		loginCipher: loginCipher, httpClient: newProviderHTTPClient(),
	}, nil
}
