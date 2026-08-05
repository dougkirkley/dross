package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// GitHub is github.com, or a GitHub Enterprise instance when BaseURL is set.
//
// A GitHub App rather than an OAuth App. The two share the authorization-code
// flow exactly — same authorize endpoint, same token endpoint, same client id
// and secret — and differ in what the resulting token can see. An OAuth App
// gets everything the user can reach and cannot be narrowed; a GitHub App sees
// only the repositories the user chose when installing it. Since the thing
// being granted here is read access to somebody's private source, being able to
// grant it one repository at a time is worth the extra endpoint below.
//
// The App's private key is deliberately not here. Signing a JWT to mint
// installation tokens would mean holding a credential that can act on every
// installation of the app; a user access token can only ever do what the person
// who authorized it can do, which is all a session needs.
type GitHub struct {
	Config
	// AppSlug is the app's name in URLs — github.com/apps/<slug>. Needed to
	// send someone to install it, which is a step OAuth Apps do not have and
	// which is easy to leave out until a user with no installation gets an
	// empty repository list and no way to act on it.
	AppSlug string

	// HTTP is the client used for token and API calls. Nil means
	// http.DefaultClient; tests point it at a stub.
	HTTP *http.Client
}

// ErrNoInstallation means the user authorized the app but has not installed it
// anywhere, so there is nothing to clone. It is a normal state, not a failure:
// the two are separate steps on GitHub and doing only the first is easy.
var ErrNoInstallation = fmt.Errorf("the app is not installed on any account")

// InstallURL is where a user goes to choose which repositories dross may see.
func (g *GitHub) InstallURL() string {
	if g.AppSlug == "" {
		return ""
	}
	return g.web() + "/apps/" + g.AppSlug + "/installations/new"
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
	// No scope parameter. A GitHub App's permissions are declared on the app
	// itself and chosen again by whoever installs it; one sent here is ignored.
	// Including it would describe access this flow does not actually decide,
	// which is worse than saying nothing — it reads like the request is asking
	// for "repo" when the app's own settings are the only thing that grants it.
	q := url.Values{
		"client_id":    {g.ClientID},
		"redirect_uri": {redirectURI},
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

// Repos lists what the user let this app see.
//
// Two requests, not one. /user/repos answers with everything the person can
// reach and takes no notice of app installations at all, so using it would
// offer repositories that the clone then fails on — the listing would say the
// app has access it does not have. The installations have to be asked first,
// and each one asked what it contains.
func (g *GitHub) Repos(ctx context.Context, accessToken string) ([]Repo, error) {
	var installs struct {
		Installations []struct {
			ID int64 `json:"id"`
		} `json:"installations"`
	}
	if err := g.getJSON(ctx, accessToken, g.api()+"/user/installations?per_page=100", &installs); err != nil {
		return nil, err
	}
	if len(installs.Installations) == 0 {
		return nil, ErrNoInstallation
	}

	var out []Repo
	seen := map[string]bool{}
	for _, in := range installs.Installations {
		var page struct {
			Repositories []struct {
				FullName      string    `json:"full_name"`
				CloneURL      string    `json:"clone_url"`
				DefaultBranch string    `json:"default_branch"`
				Private       bool      `json:"private"`
				PushedAt      time.Time `json:"pushed_at"`
			} `json:"repositories"`
		}
		u := fmt.Sprintf("%s/user/installations/%d/repositories?per_page=100", g.api(), in.ID)
		if err := g.getJSON(ctx, accessToken, u, &page); err != nil {
			return nil, err
		}
		for _, r := range page.Repositories {
			// One repository can appear under two installations — a fork in a
			// personal account and the upstream in an org both granted — and
			// the same entry twice in a picker reads as a bug.
			if seen[r.FullName] {
				continue
			}
			seen[r.FullName] = true
			out = append(out, Repo{
				FullName: r.FullName, CloneURL: r.CloneURL,
				Branch: r.DefaultBranch, Private: r.Private, Updated: r.PushedAt,
			})
		}
	}
	// Most recently pushed first: nobody scrolls to find what they were working
	// in an hour ago. The API sorts within an installation, not across them.
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out, nil
}

func (g *GitHub) getJSON(ctx context.Context, accessToken, url string, into any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := g.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("github %s: %s", url, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
		return fmt.Errorf("github %s: %w", url, err)
	}
	return nil
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
