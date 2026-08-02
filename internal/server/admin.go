package server

import (
	"context"
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
	Decide(email string, status Status, by string) (User, bool)
	MarkWelcomed(email string) bool
}

var (
	_ UserDirectory = (*Users)(nil)
	_ auth.Approver = (*Users)(nil)
)

// SetAccounts wires the account directory, the identity allowed to decide, and
// the mailer. Called with a non-empty admin email, the server serves /admin.
func (s *Server) SetAccounts(dir UserDirectory, adminEmail string, n mail.Notifier) {
	s.users = dir
	s.adminEmail = strings.TrimSpace(adminEmail)
	s.notify = n
	s.h = s.buildHandler() // routes depend on whether an admin is configured
}

// mailEnabled reports whether a notifier is configured and actually delivers.
// A nil concrete notifier stored in the interface is still non-nil as an
// interface value, so the question has to be asked of the notifier itself.
func (s *Server) mailEnabled() bool { return s.notify != nil && s.notify.Enabled() }

// isAdmin reports whether the request's identity may decide accounts.
func (s *Server) isAdmin(r *http.Request) bool {
	return s.adminEmail != "" && strings.EqualFold(auth.Identity(r), s.adminEmail)
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
		Admin:       s.adminEmail,
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

	u, changed := s.users.Decide(email, status, auth.Identity(r))
	if !changed {
		http.Redirect(w, r, "/admin?err="+url.QueryEscape(email+" was already "+string(status)), http.StatusSeeOther)
		return
	}

	msg := email + " is " + string(status)
	// The welcome goes out here rather than on their next request, so approving
	// is the single act that both opens the door and says so. Sent inline: an
	// approval is rare and a silent failure to tell someone is worse than a
	// slow page. MarkWelcomed claims the send first, so a retry after a mail
	// failure does not double-send if the first one actually landed.
	if status == StatusApproved && s.mailEnabled() && s.users.MarkWelcomed(email) {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if err := s.notify.Welcome(ctx, mail.Account{Email: u.Email, Name: u.Name}); err != nil {
			http.Redirect(w, r, "/admin?err="+url.QueryEscape(msg+", but the welcome mail failed: "+err.Error()), http.StatusSeeOther)
			return
		}
		msg += " and has been welcomed"
	}
	http.Redirect(w, r, "/admin?ok="+url.QueryEscape(msg), http.StatusSeeOther)
}

// AnnounceSignUp mails a new account and the admin. Fired from the sign-in hook,
// so it runs in the OIDC callback: do it in the background with its own context,
// or a slow mail API delays somebody's login.
func (s *Server) AnnounceSignUp(u User) {
	if !s.mailEnabled() {
		return
	}
	acct := mail.Account{Email: u.Email, Name: u.Name}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = s.notify.RequestReceived(ctx, acct)
		_ = s.notify.AdminRequest(ctx, acct)
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
