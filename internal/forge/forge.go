// Package forge talks to the git hosts a session's code comes from.
//
// A pod session used to start on an empty volume, which meant the first thing
// anyone did with one was tell the model to go and fetch something. The work
// people actually want done is on a repository they already have, so a session
// starts by cloning one.
//
// GitHub and GitLab are both here from the start, behind one interface. The two
// differ in small, awkward ways — what the token endpoint returns, how a clone
// URL carries a credential, what "list my repositories" is called — and every
// one of those is the kind of difference that gets baked into the calling code
// if only one of them exists while it is being written.
package forge

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Repo is a repository a session can be started on.
type Repo struct {
	// FullName is "owner/name" on both hosts, and is what a person recognises.
	FullName string `json:"full_name"`
	// CloneURL carries no credential. One is added at clone time and never
	// stored, so a repository list can be cached, logged and handed to a client
	// without leaking the token that produced it.
	CloneURL string `json:"clone_url"`
	// Branch is the repository's default branch, cloned when nothing else is
	// asked for.
	Branch string `json:"branch"`
	// Private is shown to the user, who is often choosing between a fork and
	// the upstream of the same name.
	Private bool `json:"private"`
	// Updated orders the list. Nobody scrolls to find the repository they were
	// working in an hour ago.
	Updated time.Time `json:"updated"`
}

// Token is the result of an authorization-code exchange.
//
// Refresh and Expiry are both optional: GitHub's OAuth apps hand out access
// tokens that do not expire unless the installation opts in, where GitLab's
// expire in two hours and must be refreshed. Callers treat a zero Expiry as
// "no reason to think this has lapsed".
type Token struct {
	Access  string    `json:"access"`
	Refresh string    `json:"refresh,omitempty"`
	Expiry  time.Time `json:"expiry,omitempty"`
}

// Expired reports whether the token needs refreshing before use.
//
// With a minute of slack, because the check and the request that follows it are
// not the same instant, and a token that expires between them fails in a way
// that looks like a bad credential rather than a stale one.
func (t Token) Expired() bool {
	return !t.Expiry.IsZero() && time.Now().Add(time.Minute).After(t.Expiry)
}

// Forge is one git host.
type Forge interface {
	// Name is the id used in URLs and in stored credentials: "github",
	// "gitlab".
	Name() string

	// AuthCodeURL is where the browser is sent to start the flow. state is
	// echoed back to the callback and is what proves the response belongs to a
	// request this server made.
	AuthCodeURL(state, redirectURI string) string

	// Exchange trades the callback's code for a token.
	Exchange(ctx context.Context, code, redirectURI string) (Token, error)

	// Refresh renews an expired token. Hosts that do not expire tokens return
	// ErrNoRefresh, which callers treat as "keep using what you have".
	Refresh(ctx context.Context, refreshToken string) (Token, error)

	// Repos lists what this token can see, most recently updated first.
	Repos(ctx context.Context, accessToken string) ([]Repo, error)

	// CloneURL returns a URL that clones without a prompt.
	//
	// Each host wants the credential in its own shape, and getting it wrong
	// fails as an authentication error rather than as anything that names the
	// real problem.
	CloneURL(repoURL, accessToken string) string
}

// ErrNoRefresh is returned by Refresh on a host whose tokens do not expire.
var ErrNoRefresh = fmt.Errorf("this host does not issue refresh tokens")

// Config is one host's OAuth application.
//
// Registered by the operator of the dross server, not by dross: an OAuth client
// identifies the installation to GitHub or GitLab, and shipping one would mean
// every dross in the world asking for repository access as the same
// application.
type Config struct {
	ClientID     string
	ClientSecret string
	// BaseURL points at a self-hosted instance. Empty means the public host.
	// Only GitLab is commonly self-hosted, but GitHub Enterprise exists and the
	// field costs nothing.
	BaseURL string
}

// Configured reports whether this host can be used at all.
func (c Config) Configured() bool { return c.ClientID != "" && c.ClientSecret != "" }

var (
	mu      sync.RWMutex
	forges  = map[string]Forge{}
	ordered []string
)

// Register adds a configured host. Called once at startup for each host the
// server has credentials for.
func Register(f Forge) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := forges[f.Name()]; !dup {
		ordered = append(ordered, f.Name())
		sort.Strings(ordered)
	}
	forges[f.Name()] = f
}

// Get returns a registered host by name.
func Get(name string) (Forge, bool) {
	mu.RLock()
	defer mu.RUnlock()
	f, ok := forges[name]
	return f, ok
}

// Names lists the configured hosts, so a client can offer exactly the ones that
// will work rather than both and an error.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	return append([]string(nil), ordered...)
}

// Reset clears the registry. For tests.
func Reset() {
	mu.Lock()
	defer mu.Unlock()
	forges, ordered = map[string]Forge{}, nil
}

// hostOf returns the host part of a clone URL, for building a credentialed one.
func hostOf(repoURL string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(repoURL, "https://"), "http://")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		return s[:i]
	}
	return s
}

// withCredential rewrites a clone URL to carry a username and token.
//
// The username is not decoration. Both hosts ignore the password half for OAuth
// tokens but require *a* username, and each documents a different one; sending
// the token as the username instead works on neither.
func withCredential(repoURL, user, token string) string {
	rest := strings.TrimPrefix(strings.TrimPrefix(repoURL, "https://"), "http://")
	return "https://" + user + ":" + token + "@" + rest
}
