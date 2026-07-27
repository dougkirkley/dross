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

	"github.com/dougkirkley/holocron/internal/auth"
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

func (s *Server) handleListSessions(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"sessions": s.mgr.list()})
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req CreateReq
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
	}
	sess := s.mgr.create(req)
	writeJSON(w, http.StatusCreated, sess.snapshot())
}

// handleDeleteSession drops a session and cancels any in-flight run.
func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	if !s.mgr.remove(r.PathValue("id")) {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.mgr.get(r.PathValue("id"))
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, sess.snapshot())
}

// handlePrompt starts a run and returns immediately (202). The run continues
// server-side; clients watch via /events or /stream.
func (s *Server) handlePrompt(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.mgr.get(r.PathValue("id"))
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
	sess, ok := s.mgr.get(r.PathValue("id"))
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	sess.interrupt()
	writeJSON(w, http.StatusOK, map[string]string{"status": "interrupted"})
}

// handleEvents returns event history (optionally after ?after=SEQ) for lossless
// reconnect.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.mgr.get(r.PathValue("id"))
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
	sess, ok := s.mgr.get(r.PathValue("id"))
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
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

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
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	file, header, err := r.FormFile("asset")
	if err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
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
	io.Copy(out, file)
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

// handleArtifactGet serves a published blob. Unauthenticated by design — the id
// is the capability. Inline so images/gifs render in a browser.
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
	w.Header().Set("Content-Type", m.Mime)
	w.Header().Set("Content-Disposition", "inline; filename=\""+m.Filename+"\"")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
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
	return abs, nil
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
