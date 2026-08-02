// Package ui holds the pages dross renders itself, as templ components: the
// sign-in screen, the holding page for an account waiting on approval, and the
// admin surface. Generated code (*_templ.go) is committed, so building the
// binary needs no extra tool.
//
//go:generate go run github.com/a-h/templ/cmd/templ generate
package ui

import (
	"strconv"
	"time"
)

// Account is what a page needs to know about a person: a view model, not the
// registry's record. The pages must not have to change because a field was
// added to storage, and ui must not import the server to draw a list.
type Account struct {
	Email     string
	Name      string
	Status    string // pending | approved | denied
	FirstSeen time.Time
	LastSeen  time.Time
	Logins    int
	DecidedBy string
}

// AdminView is everything the accounts page renders.
type AdminView struct {
	Admin       string // whoever is signed in and allowed to decide
	Pending     []Account
	Decided     []Account
	Notice      string // flash: what just happened
	Problem     string // flash: what just went wrong
	MailEnabled bool   // false means approving tells nobody, and the page says so
}

// ago renders a timestamp as something readable at a glance.
func ago(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m ago"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h ago"
	default:
		return strconv.Itoa(int(d.Hours()/24)) + "d ago"
	}
}

// signIns reads as "1 sign-in" / "4 sign-ins".
func signIns(n int) string {
	if n == 1 {
		return "1 sign-in"
	}
	return strconv.Itoa(n) + " sign-ins"
}
