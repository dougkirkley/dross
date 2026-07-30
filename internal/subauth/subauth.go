// Package subauth is a generic OAuth 2.0 authorization-code + PKCE client used
// by `dross login` and the claude-sub provider. Everything — client id,
// endpoints, scopes, extra headers — comes from the user's config (see
// config.OAuthLogin). dross ships no OAuth client and impersonates no vendor;
// the user supplies their own client/endpoints.
package subauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dougkirkley/dross/internal/config"
)

// Pending is an in-progress login.
type Pending struct {
	URL      string
	verifier string
	state    string
}

// Begin starts a PKCE login for the configured OAuth client.
func Begin(c config.OAuthLogin) (*Pending, error) {
	if !c.Configured() {
		return nil, fmt.Errorf("no oauth client configured (set [oauth] in your config)")
	}
	verifier, err := randB64(32)
	if err != nil {
		return nil, err
	}
	state, err := randB64(32)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {c.ClientID},
		"redirect_uri":          {c.RedirectURI},
		"scope":                 {c.Scope},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
		"state":                 {state},
	}
	if c.ShowCode {
		q.Set("code", "true")
	}
	return &Pending{URL: c.AuthorizeURL + "?" + q.Encode(), verifier: verifier, state: state}, nil
}

// Complete exchanges the pasted code ("code#state", "code", or a redirect URL)
// for tokens and persists them.
func Complete(c config.OAuthLogin, p *Pending, pasted string) (*Tokens, error) {
	code, gotState := parseCallback(strings.TrimSpace(pasted))
	if gotState != "" && gotState != p.state {
		return nil, fmt.Errorf("state mismatch — restart login")
	}
	body, _ := json.Marshal(map[string]string{
		"grant_type":    "authorization_code",
		"code":          code,
		"code_verifier": p.verifier,
		"client_id":     c.ClientID,
		"redirect_uri":  c.RedirectURI,
		"state":         p.state,
	})
	t, err := post(c.TokenURL, body)
	if err != nil {
		return nil, err
	}
	return t, Save(t)
}

// Tokens holds the OAuth grant.
type Tokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func (t *Tokens) expired() bool { return time.Now().After(t.ExpiresAt.Add(-time.Minute)) }

// AccessToken returns a valid access token, refreshing if needed, or "" if not
// logged in.
func AccessToken(c config.OAuthLogin) (string, error) {
	t, err := Load()
	if err != nil || t == nil {
		return "", err
	}
	if t.expired() && t.RefreshToken != "" {
		body, _ := json.Marshal(map[string]string{
			"grant_type":    "refresh_token",
			"refresh_token": t.RefreshToken,
			"client_id":     c.ClientID,
		})
		nt, err := post(c.TokenURL, body)
		if err != nil {
			return "", err
		}
		if nt.RefreshToken == "" {
			nt.RefreshToken = t.RefreshToken
		}
		if err := Save(nt); err != nil {
			return "", err
		}
		t = nt
	}
	return t.AccessToken, nil
}

func post(tokenURL string, body []byte) (*Tokens, error) {
	resp, err := http.Post(tokenURL, "application/json", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("token endpoint returned %d", resp.StatusCode)
	}
	var raw struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Expires int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	ttl := raw.Expires
	if ttl == 0 {
		ttl = 3600
	}
	return &Tokens{AccessToken: raw.Access, RefreshToken: raw.Refresh,
		ExpiresAt: time.Now().Add(time.Duration(ttl) * time.Second)}, nil
}

func parseCallback(s string) (code, state string) {
	if strings.Contains(s, "code=") {
		q, err := url.Parse(s)
		if err == nil {
			return q.Query().Get("code"), q.Query().Get("state")
		}
	}
	if i := strings.Index(s, "#"); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

func tokenPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(dir, "dross")
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(d, "oauth-token.json"), nil
}

func Save(t *Tokens) error {
	p, err := tokenPath()
	if err != nil {
		return err
	}
	b, _ := json.Marshal(t)
	return os.WriteFile(p, b, 0o600)
}

func Load() (*Tokens, error) {
	p, err := tokenPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var t Tokens
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

func randB64(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
