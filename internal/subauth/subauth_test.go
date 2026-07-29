package subauth

import (
	"testing"

	"github.com/dougkirkley/ergon/internal/config"
)

func TestParseCallback(t *testing.T) {
	cases := []struct{ in, code, state string }{
		{"abc#xyz", "abc", "xyz"},
		{"just-a-code", "just-a-code", ""},
		{"https://x/callback?code=abc&state=st", "abc", "st"},
	}
	for _, c := range cases {
		code, state := parseCallback(c.in)
		if code != c.code || state != c.state {
			t.Errorf("parseCallback(%q) = (%q,%q), want (%q,%q)", c.in, code, state, c.code, c.state)
		}
	}
}

func TestBeginRequiresConfig(t *testing.T) {
	if _, err := Begin(config.OAuthLogin{}); err == nil {
		t.Fatal("Begin should error without a configured client")
	}
	p, err := Begin(config.OAuthLogin{
		ClientID: "x", AuthorizeURL: "https://a/authorize", TokenURL: "https://a/token",
		RedirectURI: "https://a/cb", Scope: "s", ShowCode: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.verifier == "" || p.state == "" {
		t.Fatal("expected pkce verifier + state")
	}
	for _, want := range []string{"client_id=x", "code_challenge_method=S256", "code=true", "state="} {
		if !contains(p.URL, want) {
			t.Errorf("authorize URL missing %q: %s", want, p.URL)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
