package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dougkirkley/dross/internal/artifact"
)

// Serving artifacts from the terminal.
//
// A session that designs something has nowhere to put it. It can write an HTML
// mockup into the workspace, which puts a throwaway file in somebody's
// repository, or into the scratchpad, where nothing can open it. Neither is a
// design review. What is wanted is a link.
//
// dross already had the whole pipeline — the agent's publish tool, an artifact
// Store, and the routes in `dross serve` that hand back a capability URL — but
// only server-driven sessions were wired to it. WithPublish was called in
// exactly one place, so a local TUI was never offered the tool, and a session
// asked for mockups answered in prose.
//
// So a local session serves its own, reusing the same Store rather than
// inventing a second one: same ids, same on-disk layout, same URL shape. The
// listener binds 127.0.0.1, starts on the first publish rather than on every
// session, and dies with the process. Nothing leaves the machine, which is what
// makes this safe to have on without asking.
//
// A remote-controlled session is the exception and publishes to the server
// instead. The operator is somewhere else, and a link to loopback on this
// machine is no use to them.

// localArtifacts serves a session's artifacts over loopback.
type localArtifacts struct {
	mu    sync.Mutex
	store *artifact.Store
	dir   string
	base  string // "http://127.0.0.1:PORT", empty until the first publish
	count int
}

func newLocalArtifacts() *localArtifacts { return &localArtifacts{} }

// start brings up the store and the listener on first use. Binding a port for
// every session that never publishes would be a listener nobody asked for.
func (l *localArtifacts) start() error {
	if l.base != "" {
		return nil
	}
	dir, err := os.MkdirTemp("", "dross-artifacts-")
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	base := "http://" + ln.Addr().String()
	store, err := artifact.NewStore(dir, base)
	if err != nil {
		ln.Close()
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/a/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/a/")
		data, meta, err := store.Get(id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", meta.Mime)
		// Named, so a download or a "save as" lands on something recognisable
		// rather than the opaque id.
		w.Header().Set("Content-Disposition",
			"inline; filename=\""+filepath.Base(meta.Filename)+"\"")
		_, _ = w.Write(data)
	})
	go http.Serve(ln, mux) //nolint:errcheck // serves until the process exits
	l.store, l.dir, l.base = store, dir, base
	return nil
}

// Publish stores a blob and returns the URL to open it.
func (l *localArtifacts) Publish(name string, data []byte, mime string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.start(); err != nil {
		return "", fmt.Errorf("could not start the local artifact server: %w", err)
	}
	meta, err := l.store.Put(data, mime, name)
	if err != nil {
		return "", err
	}
	l.count++
	return l.store.URL(meta.ID), nil
}

// Published reports how many artifacts this session served, so a one-shot run
// can decide whether it is worth staying alive for.
func (l *localArtifacts) Published() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.count
}

// Base is the loopback root, empty until something has been published.
func (l *localArtifacts) Base() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.base
}

// Close removes the session's artifacts. They are things to look at now, not
// uploads, and leaving them in the temp directory of every machine dross has
// ever run on is litter.
func (l *localArtifacts) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.dir != "" {
		_ = os.RemoveAll(l.dir)
	}
}

// publisherFor returns the publish function a session should use.
//
// remote is asked at publish time rather than at startup, because /rc can be
// turned on part-way through a session and the answer has to follow it.
func publisherFor(local *localArtifacts, remote func() bool,
	viaServer func(name string, data []byte, mime string) (string, error),
) func(string, []byte, string) (string, error) {
	return func(name string, data []byte, mime string) (string, error) {
		if remote != nil && remote() && viaServer != nil {
			return viaServer(name, data, mime)
		}
		return local.Publish(name, data, mime)
	}
}

// serverPublisher uploads an artifact to a dross server and returns its URL.
//
// This is the remote-control path. A session driven from a phone runs here, on
// this machine, and publishes here too — so without this the operator gets a
// link to 127.0.0.1 on a laptop they are not sitting at. The bytes go up and
// the link comes back pointing at the server they are already signed in to.
//
// The token is the one /rc already obtained. Nothing new to sign in to, and no
// artifact leaves the machine unless a remote session asked for it.
func serverPublisher(server string, token func() string) func(string, []byte, string) (string, error) {
	return func(name string, data []byte, mime string) (string, error) {
		tok := ""
		if token != nil {
			tok = token()
		}
		if server == "" || tok == "" {
			return "", fmt.Errorf("no server session to publish to")
		}
		req, err := http.NewRequest(http.MethodPost,
			strings.TrimRight(server, "/")+"/api/artifacts/blob", bytes.NewReader(data))
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("X-Artifact-Filename", filepath.Base(name))
		if mime != "" {
			req.Header.Set("Content-Type", mime)
		}
		resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
			return "", fmt.Errorf("server refused the artifact: %s: %s",
				resp.Status, strings.TrimSpace(string(body)))
		}
		var out struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.URL == "" {
			return "", fmt.Errorf("server accepted the artifact but returned no url")
		}
		return out.URL, nil
	}
}
