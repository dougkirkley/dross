package server

import (
	"net/http"

	"github.com/dougkirkley/holocron/internal/artifact"
	"github.com/dougkirkley/holocron/internal/auth"
	"github.com/dougkirkley/holocron/internal/storage"
	"github.com/dougkirkley/holocron/internal/web"
)

// Server is the HTTP surface for `holocron serve`: server-side sessions that
// clients (TUI, web, phone) attach to over SSE. Runs continue whether or not a
// client is connected — the remote-control property (see DESIGN.md).
type Server struct {
	store *storage.SessionStore
	mgr   *Manager
	auth  *auth.Authenticator // nil = open (no SSO)
	arts  *artifact.Store
	h     http.Handler
}

// New creates a Server over a session Manager. If a is non-nil, /api routes
// (except health) require SSO; /auth/* serve the login flow. arts backs the
// artifact publish/share endpoints.
func New(store *storage.SessionStore, mgr *Manager, a *auth.Authenticator, arts *artifact.Store) *Server {
	s := &Server{store: store, mgr: mgr, auth: a, arts: arts}
	s.h = s.buildHandler()
	return s
}

// AuthEnabled reports whether SSO is enforced.
func (s *Server) AuthEnabled() bool { return s.auth != nil }

// ServeHTTP applies permissive CORS then dispatches.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, PUT, DELETE")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Last-Event-ID, Cf-Access-Jwt-Assertion")
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	s.h.ServeHTTP(w, r)
}

// apiMux registers the session + upload API.
func (s *Server) apiMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/providers", s.handleProviders)
	mux.HandleFunc("GET /api/whoami", s.handleWhoami)

	mux.HandleFunc("GET /api/sessions", s.handleListSessions)
	mux.HandleFunc("POST /api/sessions", s.handleCreateSession)
	mux.HandleFunc("GET /api/sessions/{id}", s.handleGetSession)
	mux.HandleFunc("POST /api/sessions/{id}/prompt", s.handlePrompt)
	mux.HandleFunc("POST /api/sessions/{id}/interrupt", s.handleInterrupt)
	mux.HandleFunc("GET /api/sessions/{id}/events", s.handleEvents)
	mux.HandleFunc("GET /api/sessions/{id}/stream", s.handleStream)

	mux.HandleFunc("POST /api/upload", s.handleUpload)
	mux.HandleFunc("POST /api/artifacts", s.handleArtifactPublish)
	return mux
}

// buildHandler wires auth around the API. Health and artifact capability URLs
// stay open (the artifact id is the credential); everything else is gated.
func (s *Server) buildHandler() http.Handler {
	api := s.apiMux()
	root := http.NewServeMux()
	root.HandleFunc("GET /a/{id}", s.handleArtifactGet) // capability URL — unauth by design

	// "/" serves the embedded Flutter client when a real build is bundled;
	// otherwise fall back to the API mux (so a UI-less binary still 404s cleanly
	// on non-API paths rather than hard-failing).
	rootFallback := http.Handler(api)
	if h, ok := web.Handler(); ok {
		rootFallback = h
	}

	if s.auth == nil {
		root.Handle("/api/", api)
		root.Handle("/", rootFallback)
		return root
	}
	root.HandleFunc("GET /auth/login", s.auth.Login)
	root.HandleFunc("GET /auth/callback", s.auth.Callback)
	root.HandleFunc("POST /auth/logout", s.auth.Logout)
	root.HandleFunc("GET /auth/logout", s.auth.Logout)
	root.HandleFunc("GET /api/health", s.handleHealth) // more specific than /api/ → wins, unauth
	root.Handle("/api/", s.auth.Middleware(api))
	root.Handle("/", rootFallback) // static UI is public; API stays gated
	return root
}
