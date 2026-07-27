// Package auth adds SSO to holocron serve. It verifies identities from any OIDC
// provider (Keycloak, Google, Microsoft, Okta, Auth0) and from Cloudflare
// Access, and can run the browser login flow itself. Three ways in:
//
//   - OIDC login → signed session cookie (browser)
//   - Bearer OIDC id_token, or a static API token (TUI / phone / CLI)
//   - Cloudflare Access header (Cf-Access-Jwt-Assertion), when fronted by Access
//
// All gated by an optional email allowlist. JWT verification uses go-oidc — auth
// crypto is never hand-rolled.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Config wires the SSO. Any of the three modes may be enabled independently.
type Config struct {
	// OIDC login + bearer id_token verification.
	Issuer       string // OIDC issuer URL (discovery); e.g. https://keycloak.example.com/realms/homelab
	ClientID     string // OIDC client id (also the expected audience)
	ClientSecret string
	RedirectURL  string // e.g. https://holocron.example.com/auth/callback

	// Cloudflare Access header verification.
	CFTeamDomain string // https://<team>.cloudflareaccess.com
	CFAudience   string // the Access application AUD tag

	// Static API token for programmatic clients not doing interactive OIDC.
	StaticToken string

	// Allowlist of permitted emails; empty means any verified identity.
	AllowedEmails []string

	// CookieSecret signs the session cookie; random per-process if empty.
	CookieSecret []byte

	// AllowInsecureCookies drops the Secure flag so cookie sessions work over
	// plain http (local dev). Never set in production.
	AllowInsecureCookies bool
}

func (c Config) enabled() bool {
	return c.Issuer != "" || c.CFTeamDomain != "" || c.StaticToken != ""
}

// Authenticator holds the verifiers and issues/validates sessions.
type Authenticator struct {
	cfg        Config
	oauth      *oauth2.Config
	verifier   *oidc.IDTokenVerifier
	cfVerifier *oidc.IDTokenVerifier
	allowed    map[string]bool
	cookieKey  []byte
	insecure   bool
}

const (
	sessionCookie = "holocron_session"
	stateCookie   = "holocron_oauth"
	sessionTTL    = 12 * time.Hour
)

// New builds an Authenticator, running OIDC discovery for whatever is configured.
// Returns (nil, nil) when no auth is configured (caller runs open).
func New(ctx context.Context, cfg Config) (*Authenticator, error) {
	if !cfg.enabled() {
		return nil, nil
	}
	a := &Authenticator{cfg: cfg, allowed: map[string]bool{}}
	for _, e := range cfg.AllowedEmails {
		a.allowed[strings.ToLower(strings.TrimSpace(e))] = true
	}
	a.cookieKey = cfg.CookieSecret
	if len(a.cookieKey) == 0 {
		a.cookieKey = randomBytes(32)
	}
	a.insecure = cfg.AllowInsecureCookies

	if cfg.Issuer != "" {
		p, err := oidc.NewProvider(ctx, cfg.Issuer)
		if err != nil {
			return nil, fmt.Errorf("oidc discovery %q: %w", cfg.Issuer, err)
		}
		a.verifier = p.Verifier(&oidc.Config{ClientID: cfg.ClientID})
		a.oauth = &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Endpoint:     p.Endpoint(),
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		}
	}
	if cfg.CFTeamDomain != "" {
		p, err := oidc.NewProvider(ctx, strings.TrimRight(cfg.CFTeamDomain, "/"))
		if err != nil {
			return nil, fmt.Errorf("cloudflare access discovery: %w", err)
		}
		a.cfVerifier = p.Verifier(&oidc.Config{ClientID: cfg.CFAudience})
	}
	return a, nil
}

type ctxKey struct{}

// Identity returns the authenticated email for a request, if any.
func Identity(r *http.Request) string {
	if v, ok := r.Context().Value(ctxKey{}).(string); ok {
		return v
	}
	return ""
}

// Middleware requires a valid identity for the wrapped handler. Browsers are
// redirected to login; API clients get 401.
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		email, exempt, ok := a.identify(r)
		if !ok {
			if wantsHTML(r) && a.oauth != nil {
				http.Redirect(w, r, "/auth/login?next="+url.QueryEscape(safeNext(r.URL.RequestURI())), http.StatusFound)
				return
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// The static token is a pre-shared secret, not an identity — it bypasses
		// the email allowlist. Verified identities do not.
		if !exempt && len(a.allowed) > 0 && !a.allowed[strings.ToLower(email)] {
			http.Error(w, "forbidden: "+email+" is not on the allowlist", http.StatusForbidden)
			return
		}
		ctx := context.WithValue(r.Context(), ctxKey{}, email)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// identify resolves an identity. exempt is true for the static token (skips the
// allowlist).
func (a *Authenticator) identify(r *http.Request) (email string, exempt bool, ok bool) {
	bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")

	// 1. Static API token (constant-time compare).
	if a.cfg.StaticToken != "" && bearer != "" &&
		hmac.Equal([]byte(bearer), []byte(a.cfg.StaticToken)) {
		return "api-token", true, true
	}
	// 2. Cloudflare Access header.
	if a.cfVerifier != nil {
		if tok := r.Header.Get("Cf-Access-Jwt-Assertion"); tok != "" {
			if e, ok := a.verifyEmail(r.Context(), a.cfVerifier, tok); ok {
				return e, false, true
			}
		}
	}
	// 3. Bearer OIDC id_token.
	if a.verifier != nil && bearer != "" {
		if e, ok := a.verifyEmail(r.Context(), a.verifier, bearer); ok {
			return e, false, true
		}
	}
	// 4. Session cookie.
	if c, err := r.Cookie(sessionCookie); err == nil {
		if e, ok := a.readSession(c.Value); ok {
			return e, false, true
		}
	}
	return "", false, false
}

func (a *Authenticator) verifyEmail(ctx context.Context, v *oidc.IDTokenVerifier, raw string) (string, bool) {
	tok, err := v.Verify(ctx, raw)
	if err != nil {
		return "", false
	}
	var claims struct {
		Email string `json:"email"`
	}
	if err := tok.Claims(&claims); err != nil || claims.Email == "" {
		return "", false
	}
	return claims.Email, true
}

// --- OIDC login flow ------------------------------------------------------

// Login starts the OIDC authorization-code flow (with PKCE).
func (a *Authenticator) Login(w http.ResponseWriter, r *http.Request) {
	if a.oauth == nil {
		http.Error(w, "OIDC login not configured", http.StatusNotFound)
		return
	}
	state := hex.EncodeToString(randomBytes(16))
	nonce := hex.EncodeToString(randomBytes(16))
	verifier := oauth2.GenerateVerifier()
	next := safeNext(r.URL.Query().Get("next"))

	// Stash state|nonce|verifier|next in a short-lived signed cookie.
	a.setCookie(w, stateCookie, a.sign(strings.Join([]string{state, nonce, verifier, next}, "|")), 10*time.Minute)

	url := a.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))
	http.Redirect(w, r, url, http.StatusFound)
}

// Callback completes the flow: verify state, exchange the code, verify the
// id_token + nonce, enforce the allowlist, and set the session cookie.
func (a *Authenticator) Callback(w http.ResponseWriter, r *http.Request) {
	if a.oauth == nil {
		http.Error(w, "OIDC login not configured", http.StatusNotFound)
		return
	}
	c, err := r.Cookie(stateCookie)
	if err != nil {
		http.Error(w, "missing state", http.StatusBadRequest)
		return
	}
	val, ok := a.verifyCookie(c.Value)
	if !ok {
		http.Error(w, "bad state", http.StatusBadRequest)
		return
	}
	parts := strings.SplitN(val, "|", 4)
	if len(parts) != 4 || r.URL.Query().Get("state") != parts[0] {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}
	nonce, verifier, next := parts[1], parts[2], parts[3]

	tok, err := a.oauth.Exchange(r.Context(), r.URL.Query().Get("code"), oauth2.VerifierOption(verifier))
	if err != nil {
		http.Error(w, "token exchange failed", http.StatusBadGateway)
		return
	}
	rawID, _ := tok.Extra("id_token").(string)
	idTok, err := a.verifier.Verify(r.Context(), rawID)
	if err != nil || idTok.Nonce != nonce {
		http.Error(w, "id token verification failed", http.StatusUnauthorized)
		return
	}
	var claims struct {
		Email string `json:"email"`
	}
	_ = idTok.Claims(&claims)
	if claims.Email == "" {
		http.Error(w, "no email claim", http.StatusForbidden)
		return
	}
	if len(a.allowed) > 0 && !a.allowed[strings.ToLower(claims.Email)] {
		http.Error(w, "forbidden: "+claims.Email+" is not on the allowlist", http.StatusForbidden)
		return
	}

	a.clearCookie(w, stateCookie)
	a.setCookie(w, sessionCookie, a.signSession(claims.Email), sessionTTL)
	http.Redirect(w, r, next, http.StatusFound)
}

// Logout clears the session cookie.
func (a *Authenticator) Logout(w http.ResponseWriter, r *http.Request) {
	a.clearCookie(w, sessionCookie)
	http.Redirect(w, r, "/", http.StatusFound)
}

// --- signed cookies -------------------------------------------------------

func (a *Authenticator) signSession(email string) string {
	exp := time.Now().Add(sessionTTL).Unix()
	payload := email + "|" + strconv.FormatInt(exp, 10)
	return a.sign(payload)
}

func (a *Authenticator) readSession(v string) (string, bool) {
	payload, ok := a.verifyCookie(v)
	if !ok {
		return "", false
	}
	parts := strings.SplitN(payload, "|", 2)
	if len(parts) != 2 {
		return "", false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", false
	}
	return parts[0], true
}

// sign returns base64(payload).base64(hmac).
func (a *Authenticator) sign(payload string) string {
	mac := hmac.New(sha256.New, a.cookieKey)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *Authenticator) verifyCookie(v string) (string, bool) {
	pb, sb, found := strings.Cut(v, ".")
	if !found {
		return "", false
	}
	payload, err := base64.RawURLEncoding.DecodeString(pb)
	if err != nil {
		return "", false
	}
	sig, err := base64.RawURLEncoding.DecodeString(sb)
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, a.cookieKey)
	mac.Write(payload)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return "", false
	}
	return string(payload), true
}

func (a *Authenticator) setCookie(w http.ResponseWriter, name, value string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: "/",
		Expires: time.Now().Add(ttl), HttpOnly: true, Secure: !a.insecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (a *Authenticator) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: !a.insecure})
}

func wantsHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// safeNext keeps only local, single-slash-rooted paths to prevent open-redirect
// (rejects "//evil.com", "https://…", empty → "/").
func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return "/"
	}
	return next
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}
