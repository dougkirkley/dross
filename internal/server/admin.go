package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dougkirkley/dross/internal/auth"
	"github.com/dougkirkley/dross/internal/mail"
	"github.com/dougkirkley/dross/internal/ui"
)

// The admin surface: one page that lists accounts and decides them.
//
// Every route below is behind the normal auth middleware and then behind an
// explicit admin check, because "authenticated" means nothing here — the whole
// point of the approval gate is that strangers authenticate successfully.

// UserDirectory is the account store the gate and the admin page talk to. The
// JSON registry in users.go implements it; a Postgres table or an identity
// service would only have to satisfy this, so the store is a swap rather than a
// rewrite.
type UserDirectory interface {
	// SignIn registers an account on first sight and records the visit,
	// reporting whether this was a sign-up.
	SignIn(email, name string) (User, bool)
	List() []User
	Get(email string) (User, bool)
	IsApproved(email string) bool
	IsAdmin(email string) bool
	// HasAdmin reports whether anyone can approve anyone yet.
	HasAdmin() bool
	// ClaimAdmin makes an account the first administrator, and reports whether
	// it got it. Must refuse once one exists.
	ClaimAdmin(email, name string) (User, bool)
	Decide(email string, status Status, by string) (User, bool)
	MarkWelcomed(email string) bool
	// Ensure lifts an account to a status and role, creating it if needed. How
	// the configured admin exists before anyone can approve them.
	Ensure(email string, status Status, role Role)
	// SetRole promotes or demotes an account.
	SetRole(email string, role Role, by string) (User, bool)
}

var (
	_ UserDirectory = (*Users)(nil)
	_ auth.Approver = (*Users)(nil)
	_ UserDirectory = (*KratosDirectory)(nil)
	_ auth.Approver = (*KratosDirectory)(nil)
)

// SetAccounts wires the account directory, the address account mail is copied
// to, and the mailer. The admin surface is served whenever there is a directory
// to administer; who may use it is a role on the record.
func (s *Server) SetAccounts(dir UserDirectory, adminEmail string, n mail.Notifier) {
	s.users = dir
	s.adminEmail = strings.TrimSpace(adminEmail)
	s.notify = n
	if s.auth != nil {
		// Send a pending account to the setup page instead of the holding page
		// while there is nobody who could ever act on their request.
		s.auth.SetFirstRun(dir.HasAdmin, "/setup")
	}
	s.h = s.buildHandler() // routes depend on whether an admin is configured
}

// mailEnabled reports whether a notifier is configured and actually delivers.
// A nil concrete notifier stored in the interface is still non-nil as an
// interface value, so the question has to be asked of the notifier itself.
func (s *Server) mailEnabled() bool { return s.notify != nil && s.notify.Enabled() }

// isAdmin reports whether the request's identity may decide accounts.
//
// The directory is the only answer. --admin-email used to be checked here as
// well, which made an address in a flag grant the role no matter what the store
// said: a demotion could be undone by a restart, and the store could not be
// audited on its own terms. It is now purely a seed, applied once at startup,
// and a server that has lost every admin recovers through the first-run claim
// rather than through a flag.
func (s *Server) isAdmin(r *http.Request) bool {
	email := auth.Identity(r)
	return email != "" && s.users != nil && s.users.IsAdmin(email)
}

// adminOnly refuses a signed-in non-admin with 403 rather than 404. A 404 would
// be a lie: the page exists, they may not have it.
func (s *Server) adminOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.isAdmin(r) {
			http.Error(w, "forbidden: not an administrator", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// handleAdminPage lists accounts, pending ones first.
func (s *Server) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	var pending, decided []ui.Account
	for _, u := range s.users.List() {
		a := ui.Account{
			Email: u.Email, Name: u.Name, Status: string(u.Status),
			FirstSeen: u.FirstSeen, LastSeen: u.LastSeen,
			Logins: u.Logins, DecidedBy: u.DecidedBy,
		}
		if u.Status == StatusPending {
			pending = append(pending, a)
			continue
		}
		decided = append(decided, a)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; img-src data:; form-action 'self'")
	_ = ui.Admin(ui.AdminView{
		Admin:       auth.Identity(r),
		Pending:     pending,
		Decided:     decided,
		Notice:      r.URL.Query().Get("ok"),
		Problem:     r.URL.Query().Get("err"),
		MailEnabled: s.mailEnabled(),
	}).Render(r.Context(), w)
}

// handleAdminDecide approves or denies one account and redirects back, so a
// refresh does not repeat the decision.
func (s *Server) handleAdminDecide(w http.ResponseWriter, r *http.Request) {
	// Same-origin check. The session cookie is SameSite=Lax, so a cross-site
	// POST arrives without it and fails authentication anyway; this is the
	// second lock, and it costs one comparison.
	if o := r.Header.Get("Origin"); o != "" && !sameOrigin(o, r) {
		http.Error(w, "forbidden: cross-origin request", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(r.PostFormValue("email"))
	status := StatusApproved
	if r.PostFormValue("action") == "deny" {
		status = StatusDenied
	}
	if email == "" {
		http.Redirect(w, r, "/admin?err="+url.QueryEscape("no account given"), http.StatusSeeOther)
		return
	}

	_, changed, mailErr := s.decide(r, email, status)
	if !changed {
		http.Redirect(w, r, "/admin?err="+url.QueryEscape(email+" was already "+string(status)), http.StatusSeeOther)
		return
	}
	msg := email + " is " + string(status)
	if mailErr != nil {
		http.Redirect(w, r, "/admin?err="+url.QueryEscape(msg+", but the welcome mail failed: "+mailErr.Error()), http.StatusSeeOther)
		return
	}
	if status == StatusApproved && s.mailEnabled() {
		msg += " and has been welcomed"
	}
	http.Redirect(w, r, "/admin?ok="+url.QueryEscape(msg), http.StatusSeeOther)
}

// decide records the decision and sends the welcome an approval owes. Shared by
// the HTML page and the JSON API so the two cannot drift on the part that
// matters — who was let in, and whether they were told.
//
// The welcome goes out here rather than on the user's next request, so approving
// is the single act that both opens the door and says so. Sent inline: an
// approval is rare, and silently failing to tell someone is worse than a slow
// response. MarkWelcomed claims the send first, so retrying after a mail failure
// cannot double-send one that actually landed.
func (s *Server) decide(r *http.Request, email string, status Status) (User, bool, error) {
	u, changed := s.users.Decide(email, status, auth.Identity(r))
	if !changed {
		return u, false, nil
	}
	if status == StatusApproved && s.mailEnabled() && s.users.MarkWelcomed(email) {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if err := s.notify.Welcome(ctx, mail.Account{Email: u.Email, Name: u.Name}); err != nil {
			return u, true, err
		}
	}
	return u, true, nil
}

// --- JSON API ---------------------------------------------------------------
//
// The same surface for the app, which is where an admin actually is: signed in,
// on a phone, next to the sessions they came for. The HTML page stays as the
// fallback that works when the bundle does not.

// handleAdminUsers lists every account, pending first.
func (s *Server) handleAdminUsers(w http.ResponseWriter, _ *http.Request) {
	all := s.users.List()
	out := make([]map[string]any, 0, len(all))
	add := func(u User) {
		out = append(out, map[string]any{
			"email":      u.Email,
			"name":       u.Name,
			"status":     string(u.Status),
			"role":       string(u.Role),
			"first_seen": u.FirstSeen,
			"last_seen":  u.LastSeen,
			"logins":     u.Logins,
			"decided_by": u.DecidedBy,
		})
	}
	for _, u := range all {
		if u.Status == StatusPending {
			add(u)
		}
	}
	for _, u := range all {
		if u.Status != StatusPending {
			add(u)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out, "mail": s.mailEnabled()})
}

// handleAdminDecideAPI approves or denies one account.
func (s *Server) handleAdminDecideAPI(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.PathValue("email"))
	var body struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		http.Error(w, "bad request body", http.StatusBadRequest)
		return
	}
	status := StatusApproved
	switch body.Action {
	case "approve":
	case "deny":
		status = StatusDenied
	default:
		http.Error(w, `action must be "approve" or "deny"`, http.StatusBadRequest)
		return
	}
	if email == "" {
		http.Error(w, "no account given", http.StatusBadRequest)
		return
	}
	if _, ok := s.users.Get(email); !ok {
		http.Error(w, "no such account", http.StatusNotFound)
		return
	}

	u, changed, mailErr := s.decide(r, email, status)
	resp := map[string]any{
		"email":   u.Email,
		"status":  string(u.Status),
		"changed": changed,
	}
	// A mail failure is not a failed decision: they are in either way, and the
	// admin needs to know which half went wrong rather than being told the
	// whole thing failed and clicking again.
	if mailErr != nil {
		resp["mail_error"] = mailErr.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

// AnnounceSignUp mails a new account, and everyone who could act on it. Fired
// from the sign-in hook, so it runs in the OIDC callback: do it in the
// background with its own context, or a slow mail API delays somebody's login.
//
// Who to tell is a question for the directory rather than for configuration. An
// address in a flag goes stale the moment administration is handed to someone
// else, and says nothing when it is handed to two people.
func (s *Server) AnnounceSignUp(u User) {
	if !s.mailEnabled() {
		return
	}
	acct := mail.Account{Email: u.Email, Name: u.Name}
	var admins []string
	for _, a := range s.users.List() {
		if a.IsAdmin() && !strings.EqualFold(a.Email, u.Email) {
			admins = append(admins, a.Email)
		}
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = s.notify.RequestReceived(ctx, acct)
		for _, to := range admins {
			_ = s.notify.AdminRequest(ctx, to, acct)
		}
	}()
}

// sameOrigin reports whether the Origin header matches the host the request
// arrived on. Behind a proxy the forwarded host is the one the browser used.
func sameOrigin(origin string, r *http.Request) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	host := r.Host
	if f := r.Header.Get("X-Forwarded-Host"); f != "" {
		host = strings.TrimSpace(strings.Split(f, ",")[0])
	}
	return strings.EqualFold(u.Host, host)
}

// --- first run ---------------------------------------------------------------
//
// A server with no admin cannot approve anyone, so the approval gate would hold
// every account for ever, including the one belonging to whoever could fix it.
// While that is true, any authenticated identity is offered the job.
//
// This is the standard bootstrap and it has the standard risk: the first person
// through the door takes the role. What makes it acceptable here is that the
// door is already two locks deep — the identity provider decides who reaches
// dross at all — and that the offer withdraws itself the instant it is taken.

// handleSetupPage offers the admin role, or gets out of the way once it is gone.
func (s *Server) handleSetupPage(w http.ResponseWriter, r *http.Request) {
	if s.users.HasAdmin() {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; img-src data:; form-action 'self'")
	_ = ui.Setup(auth.Identity(r)).Render(r.Context(), w)
}

// handleSetupStatus tells the app whether anyone can approve anyone yet, so it
// can offer the role instead of leaving the person to find a URL. Reachable
// before approval, because the whole point is that approval is impossible.
func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"needs_setup": !s.users.HasAdmin(),
		"email":       auth.Identity(r),
	})
}

// handleSetupClaimAPI is the same claim the page makes, for the app.
func (s *Server) handleSetupClaimAPI(w http.ResponseWriter, r *http.Request) {
	email := auth.Identity(r)
	if email == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	name := ""
	if u, ok := s.users.Get(email); ok {
		name = u.Name
	}
	u, claimed := s.users.ClaimAdmin(email, name)
	if !claimed {
		// Someone else got there first, or the directory could not be read.
		// Either way they are not an administrator, and saying so is the honest
		// answer rather than an error about why.
		writeJSON(w, http.StatusConflict, map[string]any{
			"claimed": false,
			"reason":  "this server already has an administrator",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"claimed": true, "email": u.Email})
}

// handleSetupClaim takes the role, if it is still there to take.
func (s *Server) handleSetupClaim(w http.ResponseWriter, r *http.Request) {
	if o := r.Header.Get("Origin"); o != "" && !sameOrigin(o, r) {
		http.Error(w, "forbidden: cross-origin request", http.StatusForbidden)
		return
	}
	email := auth.Identity(r)
	if email == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	name := ""
	if u, ok := s.users.Get(email); ok {
		name = u.Name
	}
	if _, claimed := s.users.ClaimAdmin(email, name); !claimed {
		// Someone else got there first. They are a normal pending account now,
		// which is exactly what the holding page is for.
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin?ok="+url.QueryEscape("you are the administrator"), http.StatusSeeOther)
}
