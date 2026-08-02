package server

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/dougkirkley/dross/internal/artifact"
	"github.com/dougkirkley/dross/internal/auth"
	"github.com/dougkirkley/dross/internal/mail"
	"github.com/dougkirkley/dross/internal/storage"
	"github.com/dougkirkley/dross/internal/web"
)

// Server is the HTTP surface for `dross serve`: server-side sessions that
// clients (TUI, web, phone) attach to over SSE. Runs continue whether or not a
// client is connected — the remote-control property (see DESIGN.md).
type Server struct {
	store *storage.SessionStore
	mgr   *Manager
	auth  *auth.Authenticator // nil = open (no SSO)
	arts  *artifact.Store
	hub   *deviceHub // linked devices (session relay)
	h     http.Handler

	// Account approval. All three are set together by SetAccounts; nil users or
	// an empty adminEmail means no admin surface is served at all.
	users      UserDirectory
	adminEmail string
	notify     mail.Notifier

	// Kratos's browser half: the session verifier, and the public API dross
	// proxies so its cookies are same-origin. Nil until it is configured, which
	// is what keeps this a switch rather than a rewrite.
	kratos    *auth.KratosSessions
	kratosURL *url.URL
}

// SetKratosUI wires the browser-facing half of Kratos: the flows dross draws
// and the API it proxies. Sessions are verified by the authenticator, which is
// given the same object.
func (s *Server) SetKratosUI(k *auth.KratosSessions, publicURL string) error {
	u, err := url.Parse(strings.TrimRight(publicURL, "/"))
	if err != nil || u.Host == "" {
		return fmt.Errorf("kratos public URL %q is not a URL", publicURL)
	}
	s.kratos, s.kratosURL = k, u
	s.h = s.buildHandler()
	return nil
}

// New creates a Server over a session Manager. If a is non-nil, /api routes
// (except health) require SSO; /auth/* serve the login flow. arts backs the
// artifact publish/share endpoints.
func New(store *storage.SessionStore, mgr *Manager, a *auth.Authenticator, arts *artifact.Store) *Server {
	s := &Server{store: store, mgr: mgr, auth: a, arts: arts}
	s.hub = newDeviceHub(mgr)
	mgr.hub = s.hub
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
	mux.HandleFunc("GET /api/models", s.handleModels)
	mux.HandleFunc("GET /api/whoami", s.handleWhoami)

	mux.HandleFunc("GET /api/sessions", s.handleListSessions)
	mux.HandleFunc("POST /api/sessions", s.handleCreateSession)
	mux.HandleFunc("GET /api/sessions/{id}", s.handleGetSession)
	mux.HandleFunc("PATCH /api/sessions/{id}", s.handlePatchSession)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.handleDeleteSession)
	mux.HandleFunc("POST /api/sessions/{id}/prompt", s.handlePrompt)
	mux.HandleFunc("POST /api/sessions/{id}/interrupt", s.handleInterrupt)
	mux.HandleFunc("POST /api/sessions/{id}/answer", s.handleAnswer)
	mux.HandleFunc("GET /api/sessions/{id}/events", s.handleEvents)
	mux.HandleFunc("GET /api/sessions/{id}/stream", s.handleStream)

	// Account administration, wrapped in the same check the HTML page uses: the
	// surrounding middleware only proves the caller is signed in, which under
	// the approval model is true of every stranger.
	if s.users != nil {
		mux.Handle("GET /api/admin/users", s.adminOnly(http.HandlerFunc(s.handleAdminUsers)))
		mux.Handle("POST /api/admin/users/{email}/decide", s.adminOnly(http.HandlerFunc(s.handleAdminDecideAPI)))
	}

	mux.HandleFunc("POST /api/upload", s.handleUpload)
	mux.HandleFunc("POST /api/artifacts", s.handleArtifactPublish)
	mux.HandleFunc("POST /api/artifacts/{id}/share", s.handleArtifactShare)
	mux.HandleFunc("DELETE /api/artifacts/{id}/share", s.handleArtifactUnshare)

	return mux
}

// buildHandler wires auth around the API. Health and explicitly shared
// artifacts (/s/{id}/{token}) stay open; everything else is gated — including
// /a/{id}, so an artifact is private until it is deliberately shared.
func (s *Server) buildHandler() http.Handler {
	api := s.apiMux()
	root := http.NewServeMux()
	// Only a minted share token opens an artifact to the world.
	root.HandleFunc("GET /s/{id}/{token}", s.handleArtifactShared)

	// "/" serves the embedded Flutter client when a real build is bundled;
	// otherwise fall back to the API mux (so a UI-less binary still 404s cleanly
	// on non-API paths rather than hard-failing).
	rootFallback := http.Handler(api)
	if h, ok := web.Handler(); ok {
		rootFallback = h
	}

	// The admin surface. It goes on both paths below: with auth off there is no
	// identity at all, so every request to it is refused, which is the right
	// answer for a local run.
	admin := func(h http.HandlerFunc) http.Handler {
		if s.auth == nil {
			return s.adminOnly(h)
		}
		return s.auth.Middleware(s.adminOnly(h))
	}
	if s.users != nil {
		root.Handle("GET /admin", admin(s.handleAdminPage))
		root.Handle("POST /admin/decide", admin(s.handleAdminDecide))
	}
	// First run. Behind authentication but not behind approval: on a server
	// with no admin every account is pending, so the normal gate would refuse
	// the only person who could change that. Both handlers check HasAdmin
	// themselves, so the route is harmless once the role is taken.
	if s.users != nil {
		setup := func(h http.HandlerFunc) http.Handler {
			if s.auth == nil {
				return h
			}
			return s.auth.Authenticate(h)
		}
		root.Handle("GET /setup", setup(s.handleSetupPage))
		root.Handle("POST /setup/claim", setup(s.handleSetupClaim))
		// The same two questions for the app, which is where someone actually
		// is when they find out this server has no administrator. Registered
		// here rather than on the API mux so they sit outside the approval
		// gate, like the page: an account that cannot be approved by anyone
		// still has to be able to ask, and to fix it.
		root.Handle("GET /api/setup/status", setup(s.handleSetupStatus))
		root.Handle("POST /api/setup/claim", setup(s.handleSetupClaimAPI))
	}

	if s.auth == nil {
		root.HandleFunc("GET /a/{id}", s.handleArtifactGet)
		// The relay too. This used to be registered only on the authenticated
		// path below, so a server started without SSO — a local run, Docker,
		// a test — answered 404 here and no machine could ever link to it.
		// deviceAuth is a pass-through when there is no authenticator, so the
		// same handler is correct for both.
		root.Handle("GET /api/device/connect", s.deviceAuth(http.HandlerFunc(s.handleDeviceConnect)))
		root.Handle("/api/", api)
		root.Handle("/", rootFallback)
		return root
	}
	// The relay authenticates itself: a session pod presents a scoped device
	// token, which the normal middleware deliberately refuses.
	root.Handle("GET /api/device/connect", s.deviceAuth(http.HandlerFunc(s.handleDeviceConnect)))
	// Private artifact view: same gate as the rest of the app.
	root.Handle("GET /a/{id}", s.auth.Middleware(http.HandlerFunc(s.handleArtifactGet)))
	if s.kratos != nil {
		// Kratos's own API, same-origin. Public by necessity: this is the path
		// someone with no session uses to get one.
		root.Handle("/kratos/", s.kratosProxy(s.kratosURL))
		// The pages it redirects browsers to. Public for the same reason.
		root.Handle("GET /login", s.handleKratosFlow("login", "Sign in", "Dross",
			"Sign in to continue.", "Access is limited to approved accounts."))
		root.Handle("GET /registration", s.handleKratosFlow("registration", "Create account", "Dross",
			"Signing in is how you ask for access.", "An admin approves new accounts."))
		root.Handle("GET /settings", s.auth.Middleware(s.handleKratosFlow("settings", "Settings", "Your account",
			"", "Changes apply the next time you sign in.")))
		root.HandleFunc("GET /error", s.handleKratosError)
	}
	root.HandleFunc("GET /auth/login", s.auth.Login)
	root.HandleFunc("GET /auth/callback", s.auth.Callback)
	root.HandleFunc("POST /auth/logout", s.auth.Logout)
	root.HandleFunc("GET /auth/logout", s.auth.Logout)
	root.HandleFunc("GET /api/health", s.handleHealth) // more specific than /api/ → wins, unauth
	// PWA install assets stay public. Safari (and Chrome) fetch the manifest and
	// icons without the session cookie when adding to the home screen; behind
	// auth they 401 and the installed app falls back to a screenshot instead of
	// the real icon. None of these leak anything — they're static branding.
	for _, p := range []string{"GET /manifest.json", "GET /favicon.png", "GET /icons/"} {
		root.Handle(p, rootFallback)
	}
	root.Handle("/api/", s.auth.Middleware(api))
	// Gate the UI too: an unauthenticated browser hitting "/" is redirected to
	// the OIDC login (Google), so signing in unlocks the whole app. /auth/*,
	// health, and shared-artifact links (/s/{id}/{token}) stay public above.
	root.Handle("/", s.auth.Middleware(rootFallback))
	return root
}
