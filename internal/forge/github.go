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

// GitHub is github.com, or a GitHub Enterprise instance when BaseURL is set.
type GitHub struct {
	Config
	// HTTP is the client used for token and API calls. Nil means
	// http.DefaultClient; tests point it at a stub.
	HTTP *http.Client
}

func (g *GitHub) Name() string { return "github" }

// web and api are the two different hosts GitHub uses: the OAuth dance happens
// on github.com and everything after it on api.github.com. Enterprise puts both
// under one host with the API at /api/v3, which is why this is computed rather
// than written down twice.
func (g *GitHub) web() string {
	if g.BaseURL != "" {
		return strings.TrimRight(g.BaseURL, "/")
	}
	return "https://github.com"
}

func (g *GitHub) api() string {
	if g.BaseURL != "" {
		return strings.TrimRight(g.BaseURL, "/") + "/api/v3"
	}
	return "https://api.github.com"
}

func (g *GitHub) client() *http.Client {
	if g.HTTP != nil {
		return g.HTTP
	}
	return http.DefaultClient
}

func (g *GitHub) AuthCodeURL(state, redirectURI string) string {
	// "repo" rather than "public_repo": a session's whole purpose is to work on
	// code, and the repositories worth attaching a coding agent to are mostly
	// not public ones.
	q := url.Values{
		"client_id":    {g.ClientID},
		"redirect_uri": {redirectURI},
		"scope":        {"repo read:user"},
		"state":        {state},
	}
	return g.web() + "/login/oauth/authorize?" + q.Encode()
}

func (g *GitHub) Exchange(ctx context.Context, code, redirectURI string) (Token, error) {
	form := url.Values{
		"client_id":     {g.ClientID},
		"client_secret": {g.ClientSecret},
		"code":          {code},
		"redirect_uri":  {redirectURI},
	}
	return g.token(ctx, form)
}

func (g *GitHub) Refresh(ctx context.Context, refreshToken string) (Token, error) {
	// Only GitHub Apps with token expiration turned on issue these. A classic
	// OAuth app's token does not expire, so there is nothing to refresh and
	// saying so plainly beats a request that fails with "bad refresh token".
	if refreshToken == "" {
		return Token{}, ErrNoRefresh
	}
	return g.token(ctx, url.Values{
		"client_id":     {g.ClientID},
		"client_secret": {g.ClientSecret},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	})
}

// token posts to the access-token endpoint and reads the reply.
//
// Accept: application/json is required. Without it GitHub answers a
// form-encoded body — a 200 that unmarshals into an empty struct, so the flow
// fails later at the clone with an empty credential instead of here.
func (g *GitHub) token(ctx context.Context, form url.Values) (Token, error) {
	req, err := http.NewRequestWithContext(ctx, "POST",
		g.web()+"/login/oauth/access_token", strings.NewReader(form.Encode()))
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
	if resp.StatusCode != http.StatusOK {
		return Token{}, fmt.Errorf("github token: %s", resp.Status)
	}
	var body struct {
		Access    string `json:"access_token"`
		Refresh   string `json:"refresh_token"`
		ExpiresIn int    `json:"expires_in"`
		Error     string `json:"error"`
		ErrorDesc string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Token{}, fmt.Errorf("github token: %w", err)
	}
	// GitHub reports a refused exchange as 200 with an error field, so the
	// status check above does not catch it.
	if body.Error != "" {
		if body.ErrorDesc != "" {
			return Token{}, fmt.Errorf("github: %s", body.ErrorDesc)
		}
		return Token{}, fmt.Errorf("github: %s", body.Error)
	}
	if body.Access == "" {
		return Token{}, fmt.Errorf("github token: no access token in the reply")
	}
	t := Token{Access: body.Access, Refresh: body.Refresh}
	if body.ExpiresIn > 0 {
		t.Expiry = time.Now().Add(time.Duration(body.ExpiresIn) * time.Second)
	}
	return t, nil
}

func (g *GitHub) Repos(ctx context.Context, accessToken string) ([]Repo, error) {
	// affiliation excludes the repositories a user can merely see through an
	// organisation they belong to, which on a large org is thousands of entries
	// nobody is looking for.
	u := g.api() + "/user/repos?per_page=100&sort=updated&affiliation=owner,collaborator,organization_member"
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := g.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github repos: %s", resp.Status)
	}
	var raw []struct {
		FullName      string    `json:"full_name"`
		CloneURL      string    `json:"clone_url"`
		DefaultBranch string    `json:"default_branch"`
		Private       bool      `json:"private"`
		PushedAt      time.Time `json:"pushed_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("github repos: %w", err)
	}
	out := make([]Repo, 0, len(raw))
	for _, r := range raw {
		out = append(out, Repo{
			FullName: r.FullName, CloneURL: r.CloneURL,
			Branch: r.DefaultBranch, Private: r.Private, Updated: r.PushedAt,
		})
	}
	return out, nil
}

// CloneURL credentials a GitHub clone.
//
// "x-access-token" is what GitHub documents for a token in the username slot.
// The token also works as the username on its own, but only for classic OAuth
// tokens — the installation tokens a GitHub App issues do not, and the
// difference does not show up until somebody switches.
func (g *GitHub) CloneURL(repoURL, accessToken string) string {
	return withCredential(repoURL, "x-access-token", accessToken)
}
