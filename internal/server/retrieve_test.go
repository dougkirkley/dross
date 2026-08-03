package server

import (
	"os"
	"strings"
	"testing"
)

// The guard is deliberately generous: searching when it was not needed costs a
// second, and not searching when it was costs the whole feature. So this checks
// that the skips are narrow and everything else goes through.
func TestWorthSearching(t *testing.T) {
	search := []string{
		"best mattresses",
		"who is the prime minister?",
		"is the framework laptop worth it",
		"why is my postgres connection timing out",
		"k8s networkpolicy egress namespaceselector",
		"no idea what this error means: dial tcp i/o timeout", // starts with "no" but is not an acknowledgement
	}
	for _, p := range search {
		if !worthSearching(p) {
			t.Errorf("should have searched for %q", p)
		}
	}

	skip := []string{
		"thanks", "Thanks!", "ok", "okay.", "got it", "cheers", "yep",
		"explain that again", "what did you mean by the second one",
		"summarize that", "shorter", "continue",
		"",
	}
	for _, p := range skip {
		if worthSearching(p) {
			t.Errorf("should not have searched for %q - it is about the conversation, not the world", p)
		}
	}
}

// Reddit is asked on every search. Amazon and Wirecutter are asked when the
// question is about buying something - a product page in the middle of a
// database timeout makes the answer worse, not longer.
func TestLooksLikeShopping(t *testing.T) {
	shopping := []string{
		"best mattresses",
		"is the framework laptop worth it",
		"sony vs bose headphones",
		"recommend a standing desk",
		"cheapest nas for home",
		"best mattress review 2026",
	}
	for _, p := range shopping {
		if !looksLikeShopping(p) {
			t.Errorf("%q should reach Amazon and Wirecutter", p)
		}
	}

	notShopping := []string{
		"why is my postgres connection timing out",
		"explain kubernetes networkpolicy egress",
		"who is the prime minister",
		"what does a 402 status mean",
	}
	for _, p := range notShopping {
		if looksLikeShopping(p) {
			t.Errorf("%q would be polluted by product pages", p)
		}
	}
}

// prompt() documents that it returns immediately, and retrieval takes seconds.
// Doing it before the goroutine made every prompt request block on two to four
// searches, which reads as a hung app - and made Stop useless, because the
// request that would be cancelled had not returned yet.
func TestPromptDoesNotBlockOnRetrieval(t *testing.T) {
	src, err := os.ReadFile("session.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	i := strings.Index(body, "func (m *Manager) prompt(")
	if i < 0 {
		t.Fatal("prompt() is gone; this test needs updating")
	}
	fn := body[i:]
	if j := strings.Index(fn[1:], "\nfunc "); j >= 0 {
		fn = fn[:j]
	}
	call := strings.Index(fn, "m.retrieveFor(")
	if call < 0 {
		t.Skip("retrieval no longer happens in prompt()")
	}
	// The retrieval call has to sit after the goroutine that runs the turn.
	spawn := strings.Index(fn, "go func()")
	if spawn < 0 || call < spawn {
		t.Error("retrieval runs before the run is spawned, so the prompt request waits for it")
	}
	// And it has to be handed the run's context, or Stop cannot reach it.
	if !strings.Contains(fn, "m.retrieveFor(ctx,") {
		t.Error("retrieval does not take the run's context, so Stop cannot cancel it")
	}
}
