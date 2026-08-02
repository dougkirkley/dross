package server

import (
	"strings"
	"testing"
)

// The name says which server asked for the session, and reads as words rather
// than as the hex session id it used to carry.
func TestSessionNameIsPrefixedAndFriendly(t *testing.T) {
	n := sessionName("dross-web")
	if !strings.HasPrefix(n, "dross-web-") {
		t.Errorf("name %q does not name its server", n)
	}
	// prefix + adjective + noun, and the prefix itself contains a dash.
	if parts := strings.Split(n, "-"); len(parts) != 4 {
		t.Errorf("name %q is not <prefix>-<adjective>-<noun>", n)
	}
	// Object names are DNS labels: lower-case alphanumerics and dashes.
	for _, r := range n {
		if !(r == '-' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			t.Fatalf("name %q has %q, which is not valid in an object name", n, r)
		}
	}
}

// An empty prefix must still produce a usable name rather than one starting
// with a dash, which the API server rejects.
func TestSessionNameWithoutAPrefix(t *testing.T) {
	n := sessionName("")
	if strings.HasPrefix(n, "-") || !strings.HasPrefix(n, "dross-") {
		t.Errorf("name %q is not usable without a prefix", n)
	}
}

// Two sessions must not routinely collide, or every second one fails to create.
func TestSessionNamesVary(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 40; i++ {
		seen[sessionName("dross")] = true
	}
	if len(seen) < 20 {
		t.Errorf("only %d distinct names in 40 draws; the suffix is not varying", len(seen))
	}
}
