package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dougkirkley/dross/internal/auth"
	"github.com/dougkirkley/dross/internal/forge"
)

// The git-host endpoints: connect an account, list what it can see, forget it.
//
// The flow is a browser one and cannot be anything else. An authorization code
// is redeemed by whoever holds it, so the code has to reach this server and not
// the client, which means the host redirects a browser back here rather than
// handing anything to the app. The app's part is to send the browser to
// /connect and to read the result afterwards from /api/forges.
//
// Everything below sits on the API mux, so the surrounding authentication
// middleware has already established who is asking. That matters most at the
// callback: the token being exchanged there is filed under an identity, and an
// unauthenticated callback would have nowhere to file it but a guess.

const forgeStateCookie = "dross_forge"

// forgeFlow is the pending authorization, kept in a signed cookie for the one
// round trip through the host.
//
// The redirect URI is carried rather than recomputed. Both hosts compare the
// one sent to the token endpoint against the one that started the flow and
// refuse the exchange if they differ, and the two are computed from a request
// each — a browser that starts the flow on one hostname and comes back on
// another would otherwise fail at the exchange with an error naming neither.
type forgeFlow struct {
	State    string `json:"s"`
	Owner    string `json:"o"`
	Host     string `json:"h"`
	Redirect string `json:"r"`
	Next     string `json:"n"`
	Expires  int64  `json:"e"`
}

// handleForges reports the hosts this server can offer and which of them the
// caller has connected.
//
// One request answers the whole picker, including the install link GitHub needs
// and GitLab does not. Clients ask this after a callback too: the callback
// redirects a browser to the app rather than returning a body, so this is where
// the app finds out the connection took.
func (s *Server) handleForges(w http.ResponseWriter, r *http.Request) {
	owner := auth.Identity(r)
	type host struct {
		Name       string `json:"name"`
		Connected  bool   `json:"connected"`
		InstallURL string `json:"install_url,omitempty"`
	}
	hosts := []host{}
	for _, name := range forge.Names() {
		h := host{Name: name}
		if s.forges != nil && owner != "" {
			_, h.Connected = s.forges.Load(owner, name)
		}
		if f, ok := forge.Get(name); ok {
			h.InstallURL = installURL(f)
		}
		hosts = append(hosts, h)
	}
	// Whether connecting is possible at all, which is a different question from
	// whether any host is configured: a server with no cookie secret has
	// nowhere to keep a credential, and a client that knows that can say so
	// instead of offering a button that fails at the end of a redirect.
	writeJSON(w, http.StatusOK, map[string]any{
		"hosts":       hosts,
		"can_connect": s.forges != nil,
	})
}

// handleForgeConnect sends the browser to the host's authorization page.
func (s *Server) handleForgeConnect(w http.ResponseWriter, r *http.Request) {
	owner, f, ok := s.forgeFor(w, r)
	if !ok {
		return
	}
	redirect := s.forgeRedirectURI(r, f.Name())
	flow := forgeFlow{
		State:    randomState(),
		Owner:    owner,
		Host:     f.Name(),
		Redirect: redirect,
		Next:     localPath(r.URL.Query().Get("next")),
		Expires:  time.Now().Add(10 * time.Minute).Unix(),
	}
	blob, err := json.Marshal(flow)
	if err != nil {
		http.Error(w, "could not start the connection", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: forgeStateCookie, Value: s.forges.signState(string(blob)), Path: "/",
		Expires: time.Now().Add(10 * time.Minute), HttpOnly: true, Secure: s.secureCookies(),
		// Lax, not Strict: the callback is a top-level navigation from the host
		// back to here, and Strict would withhold the cookie on exactly the one
		// request that needs it.
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, f.AuthCodeURL(flow.State, redirect), http.StatusFound)
}

// handleForgeCallback finishes the flow: check the state, trade the code, keep
// the token.
func (s *Server) handleForgeCallback(w http.ResponseWriter, r *http.Request) {
	owner, f, ok := s.forgeFor(w, r)
	if !ok {
		return
	}
	c, err := r.Cookie(forgeStateCookie)
	if err != nil {
		http.Error(w, "missing state", http.StatusBadRequest)
		return
	}
	// Cleared before the outcome is known. Whatever happens next, this flow is
	// over, and a state left behind is one a second callback could replay.
	s.clearForgeState(w)

	payload, ok := s.forges.readState(c.Value)
	if !ok {
		http.Error(w, "bad state", http.StatusBadRequest)
		return
	}
	var flow forgeFlow
	if err := json.Unmarshal([]byte(payload), &flow); err != nil {
		http.Error(w, "bad state", http.StatusBadRequest)
		return
	}
	// The signature proves this server issued the cookie; these three prove it
	// issued it for this person, this host and recently. Without the owner
	// check a signed-in user could be walked through a flow that files somebody
	// else's account under their identity — the state is unguessable, but a
	// cookie planted by an attacker who ran the flow themselves is not.
	if flow.Owner != owner || flow.Host != f.Name() || time.Now().Unix() > flow.Expires {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}
	if subtle.ConstantTimeCompare([]byte(flow.State), []byte(r.URL.Query().Get("state"))) != 1 {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}
	// Declining on the host's page is a decision, not a failure. It comes back
	// as ?error=access_denied and belongs in front of the person who made it,
	// which means the app rather than a plain-text page.
	if e := r.URL.Query().Get("error"); e != "" {
		http.Redirect(w, r, withParam(flow.Next, "forge_error", e), http.StatusFound)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "no authorization code", http.StatusBadRequest)
		return
	}
	tok, err := f.Exchange(r.Context(), code, flow.Redirect)
	if err != nil {
		http.Redirect(w, r, withParam(flow.Next, "forge_error", err.Error()), http.StatusFound)
		return
	}
	if err := s.forges.Save(owner, f.Name(), tok); err != nil {
		http.Redirect(w, r, withParam(flow.Next, "forge_error", "could not store the credential"), http.StatusFound)
		return
	}
	http.Redirect(w, r, withParam(flow.Next, "forge", f.Name()), http.StatusFound)
}

// handleForgeRepos lists the repositories the connected account can see.
func (s *Server) handleForgeRepos(w http.ResponseWriter, r *http.Request) {
	owner, f, ok := s.forgeFor(w, r)
	if !ok {
		return
	}
	tok, err := s.forgeToken(r.Context(), owner, f)
	if err != nil {
		http.Error(w, err.Error(), forgeStatus(err))
		return
	}
	repos, err := f.Repos(r.Context(), tok.Access)
	// Authorized but not installed anywhere is a normal state on GitHub and has
	// its own next step, so it is an empty list and the link that fixes it
	// rather than an error the client has to translate.
	if errors.Is(err, forge.ErrNoInstallation) {
		writeJSON(w, http.StatusOK, map[string]any{
			"repos":       []forge.Repo{},
			"install_url": installURL(f),
		})
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"repos": repos})
}

// handleForgeDisconnect forgets the caller's credential for one host.
//
// Local only: this drops what dross holds and does not revoke the grant at the
// host, which is the user's to remove from their own account settings. Saying
// otherwise would be claiming an effect on a system this server does not own.
func (s *Server) handleForgeDisconnect(w http.ResponseWriter, r *http.Request) {
	if o := r.Header.Get("Origin"); o != "" && !s.sameOrigin(o, r) {
		http.Error(w, "bad origin", http.StatusForbidden)
		return
	}
	owner, f, ok := s.forgeFor(w, r)
	if !ok {
		return
	}
	s.forges.Delete(owner, f.Name())
	w.WriteHeader(http.StatusNoContent)
}

// errNotConnected is a user with no usable credential for a host. It is a state
// with one fix — connect again — rather than a failure to report, so it is a
// sentinel the callers turn into that answer.
var errNotConnected = errors.New("not connected")

// forgeToken returns a credential that can be used now, renewing it first if
// the host gave it an expiry that has passed.
//
// The renewal lives here rather than in the forge package because it is the
// half that has somewhere to put the result: a token refreshed and not stored
// is one refresh per request, and on GitLab that is every request.
func (s *Server) forgeToken(ctx context.Context, owner string, f forge.Forge) (forge.Token, error) {
	if s.forges == nil {
		return forge.Token{}, errNotConnected
	}
	tok, ok := s.forges.Load(owner, f.Name())
	if !ok {
		return forge.Token{}, fmt.Errorf("%w to %s", errNotConnected, f.Name())
	}
	if !tok.Expired() {
		return tok, nil
	}
	fresh, err := f.Refresh(ctx, tok.Refresh)
	if err != nil {
		// Including ErrNoRefresh. Getting here at all means the host gave this
		// token an expiry and that expiry has passed, so a host that cannot
		// renew it has left nothing to fall back on. Dropping it is what turns
		// every later call from "authentication failed" into the connect button
		// that actually fixes it.
		s.forges.Delete(owner, f.Name())
		return forge.Token{}, fmt.Errorf("%w to %s: the stored credential has expired", errNotConnected, f.Name())
	}
	// GitLab rotates the refresh token and invalidates the old one, and GitHub
	// returns the same one; a host that returns none has not revoked the one
	// already held.
	if fresh.Refresh == "" {
		fresh.Refresh = tok.Refresh
	}
	if err := s.forges.Save(owner, f.Name(), fresh); err != nil {
		return forge.Token{}, fmt.Errorf("could not store the refreshed credential: %w", err)
	}
	return fresh, nil
}

// errNoSuchRepo is a repository the connected account cannot see.
var errNoSuchRepo = errors.New("no such repository")

// forgeStatus is the answer a forge failure deserves: what the user can act on
// is separated from what they cannot.
func forgeStatus(err error) int {
	switch {
	case errors.Is(err, errNotConnected), errors.Is(err, forge.ErrNoInstallation):
		return http.StatusForbidden
	case errors.Is(err, errNoSuchRepo):
		return http.StatusNotFound
	default:
		// The host answered badly or not at all, which is not this server's
		// fault and not the caller's to fix.
		return http.StatusBadGateway
	}
}

// forgeFor resolves the host named in the path and the identity asking about
// it, answering the request itself when either is missing.
func (s *Server) forgeFor(w http.ResponseWriter, r *http.Request) (string, forge.Forge, bool) {
	if s.forges == nil {
		// No cookie secret, so no key to encrypt a credential with. A server in
		// that state can still serve sessions; it just cannot keep a token.
		http.Error(w, "this server cannot store git credentials", http.StatusNotFound)
		return "", nil, false
	}
	owner := auth.Identity(r)
	if owner == "" {
		http.Error(w, "sign in first", http.StatusUnauthorized)
		return "", nil, false
	}
	f, ok := forge.Get(r.PathValue("host"))
	if !ok {
		http.Error(w, "no such git host", http.StatusNotFound)
		return "", nil, false
	}
	return owner, f, true
}

// forgeRedirectURI is where the host sends the browser back to.
//
// The configured public URL where there is one: it is the only origin in play
// that a client did not supply, and it is the one registered with the OAuth
// application. Without it, the request's own host — right for a local run,
// where the app is reached at the address it is served from.
func (s *Server) forgeRedirectURI(r *http.Request, host string) string {
	base := strings.TrimRight(s.publicURL, "/")
	if base == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	return base + "/api/forges/" + host + "/callback"
}

func (s *Server) clearForgeState(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: forgeStateCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.secureCookies(),
	})
}

// secureCookies follows whatever the authenticator was told about the
// deployment, so the two agree rather than one of them silently failing over
// plain http in local development.
func (s *Server) secureCookies() bool {
	return s.auth == nil || !s.auth.InsecureCookies()
}

// installURL is the "install this app" link for hosts that have such a step.
// GitHub does and GitLab does not, which is a difference worth expressing as a
// missing field rather than as two shapes of response.
func installURL(f forge.Forge) string {
	in, ok := f.(interface{ InstallURL() string })
	if !ok {
		return ""
	}
	return in.InstallURL()
}

// withParam adds a query parameter to a local path, keeping any it already has.
func withParam(path, key, value string) string {
	u, err := url.Parse(localPath(path))
	if err != nil {
		return "/"
	}
	q := u.Query()
	q.Set(key, value)
	u.RawQuery = q.Encode()
	return u.RequestURI()
}

// localPath keeps only same-site references, so a ?next= cannot turn a
// signed-in redirect into an open one. Anything else becomes "/".
//
// Rejecting a leading "//" is not enough on its own: browsers read "\" as "/"
// in the authority position, so "/\evil.com" navigates off site, and a control
// character in a Location header can start a second header line.
func localPath(next string) string {
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

func randomState() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// signState returns base64(payload).base64(hmac), the same shape the session
// cookies use.
func (f *ForgeTokens) signState(payload string) string {
	mac := hmac.New(sha256.New, f.state)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// readState returns the payload of a cookie this server signed.
func (f *ForgeTokens) readState(v string) (string, bool) {
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
	mac := hmac.New(sha256.New, f.state)
	mac.Write(payload)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return "", false
	}
	return string(payload), true
}
