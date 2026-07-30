package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// storedSession is the SSO session token captured for a server, so the device
// relay authenticates AS the logged-in user (not a shared secret).
type storedSession struct {
	Server string `json:"server"`
	Token  string `json:"token"`
}

func sessionTokenPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(dir, "dross")
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(d, "session.json"), nil
}

// SaveToken persists the session token issued for server.
func SaveToken(server, token string) error {
	p, err := sessionTokenPath()
	if err != nil {
		return err
	}
	b, _ := json.Marshal(storedSession{Server: server, Token: token})
	return os.WriteFile(p, b, 0o600)
}

// LoadToken returns the stored token if it was issued for server, else "".
func LoadToken(server string) string {
	p, err := sessionTokenPath()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	var s storedSession
	if json.Unmarshal(b, &s) != nil {
		return ""
	}
	if strings.TrimRight(s.Server, "/") == strings.TrimRight(server, "/") {
		return s.Token
	}
	return ""
}

// Login runs the server's OIDC login in the browser and captures the session
// token at a loopback redirect, ties the caller to the logged-in SSO user, and
// stores it. Returns the token.
func Login(ctx context.Context, server string) (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	redirect := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	tok := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		t := r.URL.Query().Get("token")
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<h2>Dross: signed in.</h2><p>You can return to the terminal.</p>")
		tok <- t
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()

	loginURL := strings.TrimRight(server, "/") + "/auth/login?mode=cli&redirect=" + url.QueryEscape(redirect)
	// Always print the URL: openBrowser is best-effort (it fails silently on
	// headless hosts, over SSH, or when no handler is registered), so the user
	// needs a copyable link to fall back to.
	if err := openBrowser(loginURL); err != nil {
		fmt.Fprintf(os.Stderr, "couldn't open a browser automatically.\n")
	}
	fmt.Fprintf(os.Stderr, "sign in at:\n  %s\n", loginURL)

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(3 * time.Minute):
		return "", fmt.Errorf("login timed out")
	case t := <-tok:
		if t == "" {
			return "", fmt.Errorf("no token returned")
		}
		if err := SaveToken(server, t); err != nil {
			return "", err
		}
		return t, nil
	}
}

// openBrowser opens url in the default browser; a no-op error is fine (the URL
// is logged by callers as a fallback).
func openBrowser(u string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler"}
	default:
		cmd = "xdg-open"
	}
	return exec.Command(cmd, append(args, u)...).Start()
}
