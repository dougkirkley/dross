package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Kratos sessions, as a third way to prove who someone is — alongside the
// signed cookie this package issues and the bearer tokens it verifies.
//
// It is deliberately additive. Cutting straight over would invalidate every
// session in existence at the moment of the deploy, and "everyone is signed out
// and the new path is untested" is a bad pair of facts to learn together. While
// both are wired, an old cookie keeps working and a Kratos session starts
// working, and the old one can be removed once nothing is using it.
//
// dross does not read Kratos's session cookie itself. It asks Kratos, over the
// admin-free public API, forwarding the browser's cookie: the session is
// Kratos's to interpret, including revocation, expiry and whether a second
// factor was satisfied. Verifying it here would mean reimplementing all of that
// and getting a different answer.

// KratosSessions verifies browser sessions against a Kratos public API.
type KratosSessions struct {
	public string
	client *http.Client
}

// NewKratosSessions points at the public API, e.g.
// http://kratos-public.dross-system.svc.
func NewKratosSessions(publicURL string) (*KratosSessions, error) {
	publicURL = strings.TrimRight(strings.TrimSpace(publicURL), "/")
	if publicURL == "" {
		return nil, fmt.Errorf("kratos public URL is empty")
	}
	return &KratosSessions{
		public: publicURL,
		client: &http.Client{
			Timeout: 8 * time.Second,
			// Kratos answers an unauthenticated whoami with a redirect to the
			// login flow. That is an answer, not a place to follow.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

// SetKratosSessions makes Kratos sessions count as an identity.
func (a *Authenticator) SetKratosSessions(k *KratosSessions) { a.kratos = k }

// KratosEnabled reports whether Kratos is wired in, so callers can send people
// to its flows rather than to this package's own login.
func (a *Authenticator) KratosEnabled() bool { return a.kratos != nil }

// whoamiResponse is the part of a session dross needs.
type whoamiResponse struct {
	Active   *bool `json:"active"`
	Identity struct {
		Traits struct {
			Email string `json:"email"`
		} `json:"traits"`
	} `json:"identity"`
}

// Identity returns the email behind the request's Kratos session, if it has one.
func (k *KratosSessions) Identity(r *http.Request) (string, bool) {
	// No cookie, no session: skip the round trip on every anonymous request.
	if _, err := r.Cookie("ory_kratos_session"); err != nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, k.public+"/sessions/whoami", nil)
	if err != nil {
		return "", false
	}
	// Forward the browser's cookies verbatim. Kratos decides what they mean.
	if c := r.Header.Get("Cookie"); c != "" {
		req.Header.Set("Cookie", c)
	}
	resp, err := k.client.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	var out whoamiResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", false
	}
	// A session can exist and not be active — expired, or awaiting a second
	// factor. Absent means active, per the API; false never does.
	if out.Active != nil && !*out.Active {
		return "", false
	}
	email := strings.TrimSpace(out.Identity.Traits.Email)
	return email, email != ""
}

// LogoutURL asks Kratos for a browser logout link, which carries a one-time
// token so a stray link cannot sign somebody out. Empty if Kratos will not
// issue one, which is the honest answer when the caller has no session.
func (k *KratosSessions) LogoutURL(r *http.Request) string {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, k.public+"/self-service/logout/browser", nil)
	if err != nil {
		return ""
	}
	if c := r.Header.Get("Cookie"); c != "" {
		req.Header.Set("Cookie", c)
	}
	resp, err := k.client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var out struct {
		LogoutURL string `json:"logout_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return ""
	}
	return out.LogoutURL
}

// Flow fetches a self-service flow by id so dross can draw it. kind is "login",
// "registration", "settings" or "error".
func (k *KratosSessions) Flow(r *http.Request, kind, id string) (*UIFlow, error) {
	path := "/self-service/" + kind + "/flows?id="
	if kind == "error" {
		path = "/self-service/errors?id="
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, k.public+path+id, nil)
	if err != nil {
		return nil, err
	}
	if c := r.Header.Get("Cookie"); c != "" {
		req.Header.Set("Cookie", c)
	}
	resp, err := k.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kratos %s flow %s: %s", kind, id, resp.Status)
	}
	var f UIFlow
	if err := json.NewDecoder(resp.Body).Decode(&f); err != nil {
		return nil, err
	}
	return &f, nil
}

// UIFlow is a self-service flow as far as drawing it goes. Kratos describes its
// forms as data — where to post, what fields to include — so a client renders
// whatever the server is currently configured to ask for, rather than hardcoding
// today's answer and breaking when a method is turned on.
type UIFlow struct {
	ID string `json:"id"`
	UI struct {
		Action   string      `json:"action"`
		Method   string      `json:"method"`
		Messages []UIMessage `json:"messages"`
		Nodes    []UINode    `json:"nodes"`
	} `json:"ui"`
}

type UIMessage struct {
	Text string `json:"text"`
	Type string `json:"type"`
}

type UINode struct {
	Type       string `json:"type"`
	Group      string `json:"group"`
	Attributes struct {
		Name     string `json:"name"`
		Type     string `json:"type"`
		Value    any    `json:"value"`
		Required bool   `json:"required"`
		Disabled bool   `json:"disabled"`
	} `json:"attributes"`
	Meta struct {
		Label struct {
			Text string `json:"text"`
		} `json:"label"`
	} `json:"meta"`
}
