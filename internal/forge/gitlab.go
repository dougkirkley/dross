package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GitLab is gitlab.com, or a self-hosted instance when BaseURL is set.
//
// Self-hosting is the common case here in a way it is not for GitHub, which is
// why every URL below is built from BaseURL rather than written as a constant.
type GitLab struct {
	Config
	HTTP *http.Client
}

func (g *GitLab) Name() string { return "gitlab" }

func (g *GitLab) host() string {
	if g.BaseURL != "" {
		return strings.TrimRight(g.BaseURL, "/")
	}
	return "https://gitlab.com"
}

func (g *GitLab) client() *http.Client {
	if g.HTTP != nil {
		return g.HTTP
	}
	return http.DefaultClient
}

func (g *GitLab) AuthCodeURL(state, redirectURI string) string {
	// api is broader than the read_repository this needs to clone, but listing
	// a user's projects is part of api and there is no narrower scope that
	// includes it. write_repository is deliberately absent: dross pushes
	// nothing, and asking for permission to would be asking for more than it
	// uses.
	q := url.Values{
		"client_id":     {g.ClientID},
		"redirect_uri":  {redirectURI},
		"response_type": {"code"},
		"scope":         {"read_api read_repository"},
		"state":         {state},
	}
	return g.host() + "/oauth/authorize?" + q.Encode()
}

func (g *GitLab) Exchange(ctx context.Context, code, redirectURI string) (Token, error) {
	return g.token(ctx, url.Values{
		"client_id":     {g.ClientID},
		"client_secret": {g.ClientSecret},
		"code":          {code},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {redirectURI},
	})
}

func (g *GitLab) Refresh(ctx context.Context, refreshToken string) (Token, error) {
	if refreshToken == "" {
		return Token{}, ErrNoRefresh
	}
	// GitLab access tokens last two hours, so unlike GitHub this path is the
	// normal one rather than the exception: a session started the morning after
	// the user connected their account gets here before it gets anywhere near a
	// clone.
	return g.token(ctx, url.Values{
		"client_id":     {g.ClientID},
		"client_secret": {g.ClientSecret},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	})
}

func (g *GitLab) token(ctx context.Context, form url.Values) (Token, error) {
	req, err := http.NewRequestWithContext(ctx, "POST",
		g.host()+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := g.client().Do(req)
	if err != nil {
		return Token{}, err
	}
	defer resp.Body.Close()
	var body struct {
		Access    string `json:"access_token"`
		Refresh   string `json:"refresh_token"`
		ExpiresIn int    `json:"expires_in"`
		Error     string `json:"error"`
		ErrorDesc string `json:"error_description"`
	}
	// Decoded before the status is judged: GitLab answers a refused exchange
	// with 400 and a body that says which of the several possible things went
	// wrong, and "400 Bad Request" on its own sends people to look at their
	// redirect URI when the problem was a reused code.
	dec := json.NewDecoder(resp.Body).Decode(&body)
	if body.Error != "" {
		if body.ErrorDesc != "" {
			return Token{}, fmt.Errorf("gitlab: %s", body.ErrorDesc)
		}
		return Token{}, fmt.Errorf("gitlab: %s", body.Error)
	}
	if resp.StatusCode != http.StatusOK {
		return Token{}, fmt.Errorf("gitlab token: %s", resp.Status)
	}
	if dec != nil {
		return Token{}, fmt.Errorf("gitlab token: %w", dec)
	}
	if body.Access == "" {
		return Token{}, fmt.Errorf("gitlab token: no access token in the reply")
	}
	t := Token{Access: body.Access, Refresh: body.Refresh}
	if body.ExpiresIn > 0 {
		t.Expiry = time.Now().Add(time.Duration(body.ExpiresIn) * time.Second)
	}
	return t, nil
}

func (g *GitLab) Repos(ctx context.Context, accessToken string) ([]Repo, error) {
	// membership=true is what limits this to the user's own projects; without
	// it the endpoint answers with every public project on the instance.
	u := g.host() + "/api/v4/projects?membership=true&per_page=100&order_by=last_activity_at&simple=true"
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := g.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gitlab projects: %s", resp.Status)
	}
	var raw []struct {
		PathWithNamespace string    `json:"path_with_namespace"`
		HTTPURLToRepo     string    `json:"http_url_to_repo"`
		DefaultBranch     string    `json:"default_branch"`
		Visibility        string    `json:"visibility"`
		LastActivityAt    time.Time `json:"last_activity_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("gitlab projects: %w", err)
	}
	out := make([]Repo, 0, len(raw))
	for _, r := range raw {
		out = append(out, Repo{
			FullName: r.PathWithNamespace,
			CloneURL: r.HTTPURLToRepo,
			Branch:   r.DefaultBranch,
			// GitLab has three visibilities and only one of them is open to
			// everyone; "internal" is private as far as anyone outside the
			// instance is concerned.
			Private: r.Visibility != "public",
			Updated: r.LastActivityAt,
		})
	}
	return out, nil
}

// CloneURL credentials a GitLab clone.
//
// "oauth2" in the username slot, which is GitLab's documented form and is not
// interchangeable with GitHub's: the token as the username authenticates
// nothing here.
func (g *GitLab) CloneURL(repoURL, accessToken string) string {
	return withCredential(repoURL, "oauth2", accessToken)
}
