// Package googleauth runs a browser-based Google OAuth login (loopback + PKCE)
// and caches the resulting refresh token so dross can call Google Cloud APIs
// (Vertex AI Gemini) without an API key or a separate gcloud install. The Vertex
// provider uses TokenSource when a login exists, falling back to ADC otherwise.
//
// The OAuth client is the public "installed app" client the Google Gemini CLI
// ships (not a secret; embedded in that CLI) with the cloud-platform scope, so
// the loopback flow is accepted by Google.
package googleauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	clientID     = "681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com"
	clientSecret = "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"
)

func oauthConfig(redirect string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     google.Endpoint,
		RedirectURL:  redirect,
		Scopes:       []string{"https://www.googleapis.com/auth/cloud-platform"},
	}
}

// tokenPath is where the cached OAuth token lives (dross config dir).
func tokenPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(dir, "dross")
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(d, "google-oauth.json"), nil
}

func saveToken(t *oauth2.Token) error {
	p, err := tokenPath()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}

func loadToken() (*oauth2.Token, error) {
	p, err := tokenPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var t oauth2.Token
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// TokenSource returns a refreshing token source from a prior Login, and whether
// one exists. The source persists refreshed tokens back to disk.
func TokenSource(ctx context.Context) (oauth2.TokenSource, bool) {
	t, err := loadToken()
	if err != nil || t == nil || t.RefreshToken == "" {
		return nil, false
	}
	base := oauthConfig("").TokenSource(ctx, t)
	return &savingSource{src: oauth2.ReuseTokenSource(t, base)}, true
}

// savingSource writes each freshly minted token back to disk so the refresh
// token (and rotation, if any) stays current across runs.
type savingSource struct{ src oauth2.TokenSource }

func (s *savingSource) Token() (*oauth2.Token, error) {
	t, err := s.src.Token()
	if err != nil {
		return nil, err
	}
	_ = saveToken(t)
	return t, nil
}

// Login runs the interactive browser OAuth flow and caches the token. It prints
// the URL (in case the browser can't open) and blocks until the callback fires
// or ctx is done.
func Login(ctx context.Context, out interface{ Write([]byte) (int, error) }) error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("googleauth: cannot open loopback listener: %w", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	redirect := fmt.Sprintf("http://localhost:%d/oauth2callback", port)

	conf := oauthConfig(redirect)
	verifier := oauth2.GenerateVerifier()
	state := oauth2.GenerateVerifier() // reuse as a random opaque state
	authURL := conf.AuthCodeURL(state,
		oauth2.AccessTypeOffline,          // request a refresh token
		oauth2.ApprovalForce,              // force consent so a refresh token is returned
		oauth2.S256ChallengeOption(verifier),
	)

	type result struct {
		code string
		err  error
	}
	done := make(chan result, 1)
	srv := &http.Server{}
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			fmt.Fprintf(w, "Login failed: %s. You can close this tab.", e)
			done <- result{err: fmt.Errorf("authorization denied: %s", e)}
			return
		}
		if q.Get("state") != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			done <- result{err: fmt.Errorf("state mismatch — restart login")}
			return
		}
		fmt.Fprint(w, "<html><body style='font-family:sans-serif'>Signed in to dross. You can close this tab.</body></html>")
		done <- result{code: q.Get("code")}
	})
	srv.Handler = mux
	go srv.Serve(ln)
	defer srv.Close()

	fmt.Fprintln(out, "Opening your browser to sign in to Google…")
	fmt.Fprintln(out, "\nIf it doesn't open, visit:\n\n  "+authURL+"\n")
	_ = openBrowser(authURL)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case res := <-done:
		if res.err != nil {
			return res.err
		}
		tok, err := conf.Exchange(ctx, res.code, oauth2.VerifierOption(verifier))
		if err != nil {
			return fmt.Errorf("googleauth: token exchange failed: %w", err)
		}
		if tok.RefreshToken == "" {
			return fmt.Errorf("googleauth: no refresh token returned — try again")
		}
		if err := saveToken(tok); err != nil {
			return err
		}
		return nil
	case <-time.After(5 * time.Minute):
		return fmt.Errorf("googleauth: timed out waiting for the browser callback")
	}
}

// Logout removes the cached token.
func Logout() error {
	p, err := tokenPath()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
