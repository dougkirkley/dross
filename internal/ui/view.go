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

// FlowView is a Kratos self-service flow, reduced to what a page needs to draw
// it. The server does the reducing: the template should not be picking through
// a provider's JSON to decide what a button is called.
type FlowView struct {
	Title    string
	Heading  string
	Lead     string
	Action   string // where the form posts; empty means there is nothing to submit
	Method   string
	Hidden   []FlowField // csrf and friends
	Buttons  []FlowButton
	Messages []FlowMessage
	Foot     string
}

type FlowField struct{ Name, Value string }

// FlowButton is one submit. Name and Value matter: Kratos decides what happened
// by which button was pressed, not by the form's URL.
type FlowButton struct {
	Name, Value, Label string
	Style              string // "" for the primary pill, "ghost" for secondary
}

// FlowMessage is something Kratos wants said - an error, or an instruction.
// Kind is a CSS class, so the page shows a refusal differently from a nudge.
type FlowMessage struct {
	Text string
	Kind string // ok | bad | warn
}
