package server

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dougkirkley/dross/internal/auth"
)

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleWhoami reports the authenticated identity (empty when auth is off).
func (s *Server) handleWhoami(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"email": auth.Identity(r),
		"auth":  s.auth != nil,
	})
}

func (s *Server) handleProviders(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"provider": s.mgr.lead.Name(),
		"model":    s.mgr.model,
	})
}

// handleModels reports the allowed models to offer in a picker, plus the current
// default.
func (s *Server) handleModels(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"models":  s.mgr.models,
		"current": s.mgr.model,
	})
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"sessions": s.mgr.list(auth.Identity(r))})
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req CreateReq
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
	}
	sess := s.mgr.create(req, auth.Identity(r))
	// In pod mode the agent runs in its own workload, which attaches back over
	// the relay as a device named for the session. Provision it before replying,
	// so a client that immediately prompts finds something to prompt.
	if s.mgr.pods != nil {
		if err := s.mgr.pods.Create(r.Context(), sess.ID, sess.owner, sess.Model); err != nil {
			s.mgr.remove(sess.ID, sess.owner) // don't leave a session with no worker
			http.Error(w, "could not start session: "+err.Error(), http.StatusInternalServerError)
			return
		}
		sess.markDevice(sess.ID)
	}
	writeJSON(w, http.StatusCreated, sess.snapshot())
}

// handlePatchSession updates mutable session fields (currently the model), which
// takes effect on the next prompt.
func (s *Server) handlePatchSession(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.mgr.get(r.PathValue("id"), auth.Identity(r))
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	var body struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if m := strings.TrimSpace(body.Model); m != "" {
		sess.setModel(m)
	}
	writeJSON(w, http.StatusOK, sess.snapshot())
}

// handleDeleteSession drops a session and cancels any in-flight run.
func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// Ownership is enforced by remove(), so it has to come first: tearing the
	// workload down beforehand would let any authenticated caller destroy
	// another owner's session, and its workspace volume with it, by guessing
	// an id — and still be told the session was not found.
	if !s.mgr.remove(id, auth.Identity(r)) {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	if s.mgr.pods != nil {
		// Best effort: a failure here must not block removing the session, or a
		// transient API error would strand it in the list forever. The operator
		// reconciles the leftover away on the next pass.
		_ = s.mgr.pods.Delete(r.Context(), id)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.mgr.get(r.PathValue("id"), auth.Identity(r))
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, sess.snapshot())
}

// handlePrompt starts a run and returns immediately (202). The run continues
// server-side; clients watch via /events or /stream.
func (s *Server) handlePrompt(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.mgr.get(r.PathValue("id"), auth.Identity(r))
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	var body struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Prompt == "" {
		http.Error(w, "need {prompt}", http.StatusBadRequest)
		return
	}
	if err := s.mgr.prompt(sess, body.Prompt); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "running"})
}

func (s *Server) handleInterrupt(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.mgr.get(r.PathValue("id"), auth.Identity(r))
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	sess.interrupt()
	writeJSON(w, http.StatusOK, map[string]string{"status": "interrupted"})
}

// handleAnswer replies to a permission gate a device session is blocked on.
//
// Owner-scoped through mgr.get like every other session route: answering a gate
// is approving an action, so it has to be at least as protected as reading the
// session it belongs to.
func (s *Server) handleAnswer(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.mgr.get(r.PathValue("id"), auth.Identity(r))
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	var body struct {
		AskID  string `json:"ask_id"`
		Answer string `json:"answer"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !s.mgr.answerDevice(sess.ID, body.AskID, body.Answer) {
		http.Error(w, "no question is waiting on an answer", http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "answered"})
}

// handleEvents returns event history (optionally after ?after=SEQ) for lossless
// reconnect.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.mgr.get(r.PathValue("id"), auth.Identity(r))
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	after, _ := strconv.Atoi(r.URL.Query().Get("after"))
	writeJSON(w, http.StatusOK, map[string]any{"events": sess.history(after)})
}

// handleStream is the live SSE tail. It subscribes first, replays history up to
// the subscribe point (respecting ?after= / Last-Event-ID), then streams live —
// so no event is missed or duplicated across a reconnect.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.mgr.get(r.PathValue("id"), auth.Identity(r))
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	// no-transform stops intermediaries (Cloudflare) from compressing — which
	// would buffer the whole body; X-Accel-Buffering disables nginx-family proxy
	// buffering. Without these the stream only flushes when the run ends.
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// A comment padding sent up front forces byte-count-based proxy buffers to
	// flush immediately, so events start streaming from the first one.
	fmt.Fprint(w, ":"+strings.Repeat(" ", 2048)+"\n\n")
	flusher.Flush()

	after := lastSeq(r)

	ch, historyMax := sess.subscribe()
	defer sess.unsubscribe(ch)

	// Replay missed history, then live-tail skipping anything already replayed.
	for _, e := range sess.history(after) {
		writeSSE(w, e)
	}
	flusher.Flush()

	notify := r.Context().Done()
	for {
		select {
		case <-notify:
			return
		case e := <-ch:
			if e.Seq <= historyMax || e.Seq <= after {
				continue // already sent via history replay
			}
			writeSSE(w, e)
			flusher.Flush()
		}
	}
}

// handleUpload ingests a file into the workspace so the agent can use it. It
// lands under uploads/<name> in the server workspace and the path is returned.
// maxUploadBytes caps a single upload. It has to stay well under the
// container's memory limit: multipart parsing buffers, and a cap larger than
// the pod can hold turns a big upload into an OOM kill — a denial of service
// caused by the very limit meant to prevent one.
const maxUploadBytes = 32 << 20 // 32 MiB

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	// Cap the request: without this one authenticated POST can fill the volume,
	// and r.FormFile spills anything past its in-memory budget to /tmp.
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	file, header, err := r.FormFile("asset")
	if err != nil {
		http.Error(w, "invalid or oversized upload", http.StatusBadRequest)
		return
	}
	defer file.Close()

	name := filepath.Base(header.Filename)
	rel := filepath.Join("uploads", name)
	abs := filepath.Join(s.mgr.workdir, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		http.Error(w, "failed to prepare upload dir", http.StatusInternalServerError)
		return
	}
	out, err := os.Create(abs)
	if err != nil {
		http.Error(w, "failed to save file", http.StatusInternalServerError)
		return
	}
	defer out.Close()
	// A short write must not be reported as success — the agent would then act
	// on a truncated file.
	if _, err := io.Copy(out, file); err != nil {
		out.Close()
		_ = os.Remove(abs)
		http.Error(w, "upload failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": rel, "filename": name, "status": "uploaded"})
}

// handleArtifactPublish reads a workspace file and republishes it at a shareable
// capability URL — the "share a screenshot/gif/report" path.
func (s *Server) handleArtifactPublish(w http.ResponseWriter, r *http.Request) {
	if s.arts == nil {
		http.Error(w, "artifacts not configured", http.StatusNotImplemented)
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" {
		http.Error(w, "need {path}", http.StatusBadRequest)
		return
	}
	abs, err := resolveInside(s.mgr.workdir, req.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		http.Error(w, "cannot read file", http.StatusNotFound)
		return
	}
	mime := detectMime(abs, data)
	m, err := s.arts.Put(data, mime, filepath.Base(req.Path))
	if err != nil {
		http.Error(w, "store failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"url": s.arts.URL(m.ID), "id": m.ID, "mime": m.Mime, "filename": m.Filename,
	})
}

// handleArtifactGet serves a published blob to a signed-in user. This route is
// behind the normal auth middleware; the public path is handleArtifactShared.
func (s *Server) handleArtifactGet(w http.ResponseWriter, r *http.Request) {
	if s.arts == nil {
		http.NotFound(w, r)
		return
	}
	data, m, err := s.arts.Get(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// Private: must not be cached by shared proxies.
	writeBlob(w, data, m.Mime, m.Filename, "private, max-age=300")
}

// handleArtifactShared serves an artifact that has been explicitly shared, to
// anyone holding the token. No account required — this is the share mechanism.
func (s *Server) handleArtifactShared(w http.ResponseWriter, r *http.Request) {
	if s.arts == nil {
		http.NotFound(w, r)
		return
	}
	data, m, err := s.arts.GetShared(r.PathValue("id"), r.PathValue("token"))
	if err != nil {
		http.NotFound(w, r) // unshared and wrong-token look identical
		return
	}
	// Short TTL, not immutable: the token is in the path, so a year-long shared
	// cache entry would keep serving the file after the link was revoked —
	// making "unshare" a lie. Five minutes still absorbs the burst of a link
	// being opened by several people at once.
	writeBlob(w, data, m.Mime, m.Filename, "public, max-age=300")
}

// handleArtifactShare mints a public link for an artifact.
func (s *Server) handleArtifactShare(w http.ResponseWriter, r *http.Request) {
	if s.arts == nil {
		http.Error(w, "artifacts not configured", http.StatusNotImplemented)
		return
	}
	m, err := s.arts.Share(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"url": s.arts.ShareURL(m), "id": m.ID, "filename": m.Filename,
	})
}

// handleArtifactUnshare revokes the public link; the artifact stays viewable to
// signed-in users.
func (s *Server) handleArtifactUnshare(w http.ResponseWriter, r *http.Request) {
	if s.arts == nil {
		http.Error(w, "artifacts not configured", http.StatusNotImplemented)
		return
	}
	if err := s.arts.Unshare(r.PathValue("id")); err != nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "unshared"})
}

// writeBlob sends an artifact inline so images/gifs render in a browser.
func writeBlob(w http.ResponseWriter, data []byte, mime, filename, cache string) {
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Disposition", "inline; filename=\""+filename+"\"")
	w.Header().Set("Cache-Control", cache)
	// Never let a browser sniff a stored blob into something executable.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; media-src 'self'; sandbox")
	w.Write(data)
}

// --- helpers --------------------------------------------------------------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeSSE(w io.Writer, e Event) {
	data, _ := json.Marshal(e)
	fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.Seq, e.Kind, data)
}

// resolveInside turns a workspace-relative (or absolute) path into an absolute
// path proven to sit inside root, rejecting traversal.
func resolveInside(root, p string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	var abs string
	if filepath.IsAbs(p) {
		abs = filepath.Clean(p)
	} else {
		abs = filepath.Join(rootAbs, p)
	}
	rel, err := filepath.Rel(rootAbs, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the workspace", p)
	}
	// A lexical check alone lets a symlink inside the workspace point anywhere:
	// publishing it would read the target and hand it back as a URL. The agent
	// can plant one, so resolve links before trusting the path.
	if err := noSymlinkEscape(rootAbs, abs); err != nil {
		return "", err
	}
	return abs, nil
}

// noSymlinkEscape reports an error if abs, with symlinks resolved, leaves root.
// The leaf may not exist yet, so the deepest existing ancestor is resolved.
func noSymlinkEscape(root, abs string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		realRoot = root
	}
	probe := abs
	for {
		real, err := filepath.EvalSymlinks(probe)
		if err == nil {
			rel, rerr := filepath.Rel(realRoot, real)
			if rerr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return fmt.Errorf("path escapes the workspace via a symlink")
			}
			return nil
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return nil
		}
		probe = parent
	}
}

// detectMime prefers the extension, falling back to content sniffing.
func detectMime(path string, data []byte) string {
	if t := mime.TypeByExtension(filepath.Ext(path)); t != "" {
		return t
	}
	return http.DetectContentType(data)
}

// lastSeq resolves the reconnect cursor from ?after= or the Last-Event-ID header.
func lastSeq(r *http.Request) int {
	if v := r.URL.Query().Get("after"); v != "" {
		n, _ := strconv.Atoi(v)
		return n
	}
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		n, _ := strconv.Atoi(v)
		return n
	}
	return 0
}
