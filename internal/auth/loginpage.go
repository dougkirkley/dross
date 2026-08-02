package auth

import (
	"context"
	"net/http"

	"github.com/dougkirkley/dross/internal/ui"
)

// The two screens dross shows someone who is not through the door yet. Both are
// templ components in internal/ui: the markup is checked when the binary is
// built, and the sign-in screen, the holding page and the admin page share one
// card, one palette and one mascot instead of three copies of each.

// serveLoginPage renders the sign-in screen, carrying the originally requested
// path through so the user lands where they were headed.
func serveLoginPage(w http.ResponseWriter, loginURL string) {
	writePage(w, http.StatusOK)
	_ = ui.Login(loginURL).Render(context.Background(), w)
}

// servePendingPage renders the holding page for an account that is authenticated
// but not approved. It is a 403, not a 200: the answer really is no, and
// something parsing the response must not be told otherwise because a browser
// gets a friendly page.
func servePendingPage(w http.ResponseWriter, email string) {
	writePage(w, http.StatusForbidden)
	_ = ui.Pending(email).Render(context.Background(), w)
}

// writePage sets the headers both pages share. The markup is self-contained, so
// the policy allows nothing beyond the stylesheet it carries inline.
func writePage(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; img-src data:; form-action 'self'")
	w.WriteHeader(status)
}
