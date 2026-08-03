// Package auth adds SSO to dross serve. It verifies identities from any OIDC
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
	"encoding/json"
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
	RedirectURL  string // e.g. https://dross.example.com/auth/callback

	// Cloudflare Access header verification.
	CFTeamDomain string // https://<team>.cloudflareaccess.com
	CFAudience   string // the Access application AUD tag

	// ServiceToken is a machine credential that authenticates AS ServiceIdentity
	// and is still subject to the email allowlist, so it cannot outlive the
	// access rules the way a bypassing token can.
	ServiceToken    string
	ServiceIdentity string // e.g. "service@dross"; required with ServiceToken

	// Allowlist of permitted emails; empty means any verified identity.
	AllowedEmails []string

	// CookieSecret signs the session cookie; random per-process if empty.
	CookieSecret []byte

	// AllowInsecureCookies drops the Secure flag so cookie sessions work over
	// plain http (local dev). Never set in production.
	AllowInsecureCookies bool
}

func (c Config) enabled() bool {
	return c.Issuer != "" || c.CFTeamDomain != "" || c.ServiceToken != ""
}

// Authenticator holds the verifiers and issues/validates sessions.
type Authenticator struct {
	cfg        Config
	oauth      *oauth2.Config
	verifier   *oidc.IDTokenVerifier
	cfVerifier *oidc.IDTokenVerifier
	allowed    map[string]bool
	onSignIn   func(email, name string)
	approved   Approver
	kratos     *KratosSessions
	needsSetup func() bool
	setupPath  string
	cookieKey  []byte
	insecure   bool
}

const (
	sessionCookie = "dross_session"
	stateCookie   = "dross_oauth"
	sessionTTL    = 12 * time.Hour
)

// New builds an Authenticator, running OIDC discovery for whatever is configured.
// Returns (nil, nil) when no auth is configured (caller runs open).
func New(ctx context.Context, cfg Config) (*Authenticator, error) {
	if !cfg.enabled() {
		return nil, nil
	}
	if cfg.ServiceToken != "" && strings.TrimSpace(cfg.ServiceIdentity) == "" {
		return nil, fmt.Errorf("service token requires a service identity")
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

// IsServiceIdentity reports whether email is the machine credential's identity,
// so callers can refuse it where a human is required.
func (a *Authenticator) IsServiceIdentity(email string) bool {
	id := strings.TrimSpace(a.cfg.ServiceIdentity)
	return id != "" && strings.EqualFold(email, id)
}

// OnSignIn registers a callback fired after a verified, permitted login — the
// hook a user registry hangs off.
func (a *Authenticator) OnSignIn(fn func(email, name string)) { a.onSignIn = fn }

// Approver is the authorization half of the split: authentication says who
// someone is, an Approver says whether they may use the application. An
// interface rather than a callback so the store behind it — a JSON registry
// today, a database or an identity service later — is a substitution.
type Approver interface {
	IsApproved(email string) bool
}

// SetNeedsSetup installs the question "does this server still have nobody who
// can approve anyone?". While the answer is yes, a fresh install is a waiting
// room with no staff — everyone authenticates, everyone is pending, and the only
// person who could fix it is refused along with the rest — so the middleware
// lets the application load and explain itself.
//
// The predicate is phrased as the condition that opens the door, not its
// opposite. It was HasAdmin here once, which is the inverse, and inverted
// silently: with no admin the person who could fix it got the holding page, and
// with an admin every unapproved account got the app shell instead. Neither
// half looks wrong from the outside.
//
// The setup path itself is not exempted here — it is registered outside this
// middleware, so it answers whether or not the caller is approved.
func (a *Authenticator) SetNeedsSetup(needsSetup func() bool, path string) {
	a.needsSetup, a.setupPath = needsSetup, path
}

// SetApproval installs the gate. Unset, every verified identity is allowed,
// which is how a local run behaves.
//
// One check for the whole app rather than one per handler, so a route added
// later is gated by default instead of by remembering.
func (a *Authenticator) SetApproval(p Approver) { a.approved = p }

type ctxKey struct{}

// WithIdentity attaches a verified identity to a context, for authenticators
// that run outside the standard middleware (the device relay).
func WithIdentity(ctx context.Context, email string) context.Context {
	return context.WithValue(ctx, ctxKey{}, email)
}

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
			// Kratos owns the login when it is configured, and it has to be the
			// one that starts the flow: it sets the anti-CSRF cookie and mints
			// the flow id, and a page drawn without either can only fail on
			// submit. It redirects back to /login?flow=..., which dross draws.
			if wantsHTML(r) && a.kratos != nil {
				http.Redirect(w, r, "/kratos/self-service/login/browser?return_to="+
					url.QueryEscape(safeNext(r.URL.RequestURI())), http.StatusFound)
				return
			}
			if wantsHTML(r) && a.oauth != nil {
				// Show dross's own sign-in screen rather than bouncing straight to
				// the identity provider, so the first thing a user sees is dross.
				// The button starts the same OIDC flow.
				serveLoginPage(w, "/auth/login?next="+url.QueryEscape(safeNext(r.URL.RequestURI())))
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
		// Authenticated but not yet approved. The service token is exempt: it is
		// a machine credential with no user record, and making it have one would
		// mean an automated check could be "approved" by mistake.
		if a.approved != nil && !a.IsServiceIdentity(email) && !a.approved.IsApproved(email) {
			// Nobody can approve them because nobody can approve anyone. Send
			// them somewhere that can be acted on rather than a page that says
			// wait for a person who does not exist.
			if a.needsSetup != nil && a.needsSetup() {
				// Let the application load. It asks whether setup is needed and
				// says so itself, which is the difference between a product
				// that explains its state and a URL somebody has to be told.
				//
				// Only the page: every API route stays refused below, apart
				// from the two the setup screen calls, which are registered
				// outside this gate. So what gets through here is the static
				// shell, and nothing it could ask for yet.
				// Anything that is not the API: the document, and every asset it
				// pulls in behind it. Keying this on "Accept: text/html" was
				// enough for the navigation and nothing else - a browser asks
				// for scripts and fonts with Accept: */*, so the shell loaded
				// and every file it needed to actually run was refused. An empty
				// page, and no error a person could act on.
				if !strings.HasPrefix(r.URL.Path, "/api/") {
					w.Header().Set("Cache-Control", "private, no-store")
					next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, email)))
					return
				}
				http.Error(w, "forbidden: this server has no administrator yet", http.StatusForbidden)
				return
			}
			if wantsHTML(r) {
				servePendingPage(w, email)
				return
			}
			http.Error(w, "forbidden: "+email+" is awaiting approval", http.StatusForbidden)
			return
		}
		// Anything behind auth must never be stored by a shared cache. Without
		// this a CDN happily caches a signed-in user's response and serves it to
		// anonymous clients — Cloudflare's default rules did exactly that to the
		// app bundle. Handlers may override with something stricter.
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Add("Vary", "Authorization")
		w.Header().Add("Vary", "Cookie")
		ctx := context.WithValue(r.Context(), ctxKey{}, email)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Authenticate requires a verified identity and nothing more. Used by the
// first-run setup route, which by definition has to answer someone who is not
// approved — refusing them there is refusing the only person who can fix it.
func (a *Authenticator) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		email, _, ok := a.identify(r)
		if !ok {
			if wantsHTML(r) && a.oauth != nil {
				next := url.QueryEscape(safeNext(r.URL.RequestURI()))
				serveLoginPage(w, "/auth/login?next="+next)
				return
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, email)))
	})
}

// identify resolves an identity. exempt is reserved for credentials that skip
// the allowlist; nothing does today.
func (a *Authenticator) identify(r *http.Request) (email string, exempt bool, ok bool) {
	// Require the scheme: TrimPrefix would return the whole header when absent,
	// making a bare (or Basic) Authorization value count as a bearer token.
	bearer, hasBearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !hasBearer {
		bearer = ""
	}

	// 1. Service token: a machine credential with a real identity, so the
	// allowlist still applies and revoking the identity revokes the access.
	if a.cfg.ServiceToken != "" && bearer != "" &&
		hmac.Equal([]byte(bearer), []byte(a.cfg.ServiceToken)) {
		return a.cfg.ServiceIdentity, false, true
	}
	// 2. A Kratos session. First among the cookie-shaped credentials because it
	// is the one being migrated to: a browser holding both an old dross cookie
	// and a Kratos session is mid-transition, and the Kratos one is the answer
	// that will still be true tomorrow.
	if a.kratos != nil {
		if e, ok := a.kratos.Identity(r); ok {
			return e, false, true
		}
	}
	// 3. Cloudflare Access header.
	if a.cfVerifier != nil {
		if tok := r.Header.Get("Cf-Access-Jwt-Assertion"); tok != "" {
			if e, ok := a.verifyEmail(r.Context(), a.cfVerifier, tok); ok {
				return e, false, true
			}
		}
	}
	// 4. Bearer OIDC id_token.
	if a.verifier != nil && bearer != "" {
		if e, ok := a.verifyEmail(r.Context(), a.verifier, bearer); ok {
			return e, false, true
		}
	}
	// 4b. Bearer dross session token (issued to native app clients via the
	// mode=app login flow); the same signed session the cookie carries.
	// Device tokens are excluded: they authorise attaching one device over the
	// relay, not acting as the user against the API.
	if bearer != "" && !IsDeviceToken(bearer) {
		if e, ok := a.readSession(bearer); ok {
			return e, false, true
		}
	}
	// 5. This package's own session cookie — only where Kratos is not running
	// the login. Two cookie-shaped credentials for the same browser is one more
	// than anything needs, and the one being kept is the one a person can sign
	// out of. A deployment without Kratos (a local run) still has this.
	if a.kratos == nil {
		if c, err := r.Cookie(sessionCookie); err == nil {
			if e, ok := a.readSession(c.Value); ok {
				return e, false, true
			}
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

// flowState is the signed, short-lived record of an in-flight login. It is JSON
// rather than pipe-joined: a value containing the separator (next is
// user-influenced) could otherwise inject later fields.
type flowState struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Next     string `json:"x"`
	Mode     string `json:"m"`
	AppState string `json:"a,omitempty"` // client's own state, echoed back to it
}

// Login starts the OIDC authorization-code flow (with PKCE).
//
// With Kratos running the login this exists for the clients Kratos cannot serve:
// the phone, which captures a custom-scheme callback, and the CLI, which
// captures a loopback one. Both need a bearer token rather than a cookie, and
// Kratos issues browser sessions. A browser arriving here is sent to Kratos
// instead, so there is exactly one way for a browser to sign in.
func (a *Authenticator) Login(w http.ResponseWriter, r *http.Request) {
	if a.oauth == nil {
		http.Error(w, "OIDC login not configured", http.StatusNotFound)
		return
	}
	if a.kratos != nil {
		switch r.URL.Query().Get("mode") {
		case "app", "cli":
			// A native client, which is what this flow is still for.
		default:
			http.Redirect(w, r, "/kratos/self-service/login/browser?return_to="+
				url.QueryEscape(safeNext(r.URL.Query().Get("next"))), http.StatusFound)
			return
		}
	}
	state := hex.EncodeToString(randomBytes(16))
	nonce := hex.EncodeToString(randomBytes(16))
	verifier := oauth2.GenerateVerifier()
	next := safeNext(r.URL.Query().Get("next"))
	// mode=app: native clients capture the callback via a custom scheme.
	// mode=cli: local tools capture it at a loopback redirect (?redirect=...);
	// both carry the session as a bearer token instead of a cookie.
	mode := ""
	switch r.URL.Query().Get("mode") {
	case "app":
		mode = "app"
	case "cli":
		if red := r.URL.Query().Get("redirect"); loopbackURL(red) {
			mode = "cli|" + red
		}
	}

	fs := flowState{
		State: state, Nonce: nonce, Verifier: verifier, Next: next, Mode: mode,
		// A native client passes its own state so it can reject a callback it
		// did not initiate.
		AppState: r.URL.Query().Get("state"),
	}
	blob, err := json.Marshal(fs)
	if err != nil {
		http.Error(w, "login failed", http.StatusInternalServerError)
		return
	}
	a.setCookie(w, stateCookie, a.sign(string(blob)), 10*time.Minute)

	opts := []oauth2.AuthCodeOption{oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)}
	// After signing out, ask the provider to actually re-authenticate rather
	// than silently reissuing from its own still-valid session. Standard OIDC
	// parameters; a provider that ignores them just behaves as before.
	if r.URL.Query().Get("reauth") == "1" {
		opts = append(opts,
			oauth2.SetAuthURLParam("prompt", "login"),
			oauth2.SetAuthURLParam("max_age", "0"))
	}
	url := a.oauth.AuthCodeURL(state, opts...)
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
	var fs flowState
	if err := json.Unmarshal([]byte(val), &fs); err != nil || fs.State == "" ||
		r.URL.Query().Get("state") != fs.State {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}
	nonce, verifier, next, mode, appState := fs.Nonce, fs.Verifier, fs.Next, fs.Mode, fs.AppState

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
		Name  string `json:"name"`
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

	// The identity is verified and permitted: register the account. First
	// sign-in is the sign-up; there is no separate registration step.
	if a.onSignIn != nil {
		a.onSignIn(claims.Email, claims.Name)
	}

	a.clearCookie(w, stateCookie)
	session := a.signSession(claims.Email)

	// Native app clients get the session via a custom-scheme redirect; CLI/TUI
	// tools via a loopback redirect; browsers get a session cookie.
	if mode == "app" {
		// Echo the client's state so the app can reject a callback it did not
		// start. Without it any app registering dross:// (or a web page, since
		// the scheme is BROWSABLE) can inject a session token of its choosing.
		cb := appCallbackScheme + "?session=" + url.QueryEscape(session)
		if appState != "" {
			cb += "&state=" + url.QueryEscape(appState)
		}
		http.Redirect(w, r, cb, http.StatusFound)
		return
	}
	if red, ok := strings.CutPrefix(mode, "cli|"); ok && loopbackURL(red) {
		sep := "?"
		if strings.Contains(red, "?") {
			sep = "&"
		}
		http.Redirect(w, r, red+sep+"token="+url.QueryEscape(session), http.StatusFound)
		return
	}
	if a.kratos == nil {
		a.setCookie(w, sessionCookie, session, sessionTTL)
	}
	http.Redirect(w, r, next, http.StatusFound)
}

// appCallbackScheme is the custom-scheme URL native clients register to capture
// the session token from the OIDC callback.
const appCallbackScheme = "dross://auth"

// loopbackURL reports whether u is an http URL on 127.0.0.1/localhost — the only
// redirect target allowed for the CLI login flow (prevents token exfiltration).
func loopbackURL(u string) bool {
	p, err := url.Parse(u)
	if err != nil || p.Scheme != "http" {
		return false
	}
	h := p.Hostname()
	return h == "127.0.0.1" || h == "localhost" || h == "::1"
}

// Logout ends the session, whichever kind it is.
func (a *Authenticator) Logout(w http.ResponseWriter, r *http.Request) {
	a.clearCookie(w, sessionCookie)
	// A Kratos session is Kratos's to end: its logout URL carries a one-time
	// token, so a link cannot be used to sign somebody else out. Clearing our
	// own cookie without this would leave the browser holding a live session
	// and looking signed in again on the next page.
	if a.kratos != nil {
		if u := a.kratos.LogoutURL(r); u != "" {
			http.Redirect(w, r, u, http.StatusFound)
			return
		}
	}
	// Clearing our own cookie is not a logout the user can feel: the identity
	// provider still holds a session, so the next sign-in bounces straight back
	// through without a prompt.
	//
	// This has to be a top-level navigation. Loading the provider's logout in a
	// hidden frame keeps the user on our page but is a third-party request, and
	// browsers that block third-party cookies (Safari by default) drop the
	// clearing Set-Cookie — the sign-out then silently does nothing. Correctness
	// wins over staying on our own page.
	if u := a.idpLogoutURL(); u != "" {
		http.Redirect(w, r, u, http.StatusFound)
		return
	}
	http.Redirect(w, r, "/", http.StatusFound)
}

// idpLogoutURL is the identity provider's logout endpoint, or "" if there isn't
// one to call. Cloudflare Access exposes /cdn-cgi/access/logout on the team
// domain, which we can derive from either the configured team domain or the
// issuer (Access issuers live under the same host).
func (a *Authenticator) idpLogoutURL() string {
	host := strings.TrimRight(a.cfg.CFTeamDomain, "/")
	if host == "" && a.cfg.Issuer != "" {
		if u, err := url.Parse(a.cfg.Issuer); err == nil &&
			strings.HasSuffix(u.Hostname(), ".cloudflareaccess.com") {
			host = u.Scheme + "://" + u.Host
		}
	}
	if host == "" {
		return ""
	}
	return host + "/cdn-cgi/access/logout"
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
	// Must be a purely local reference. Rejecting only "//" is not enough:
	// browsers treat "\" as "/" in the authority position, so "/\evil.com"
	// navigates off-site. Control characters can smuggle a second header line.
	if next == "" || strings.ContainsAny(next, "\\\r\n") {
		return "/"
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" ||
		!strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") {
		return "/"
	}
	return u.RequestURI()
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}
