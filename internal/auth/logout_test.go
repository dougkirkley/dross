package auth

import "testing"

func TestIDPLogoutURL(t *testing.T) {
	cases := []struct{ team, issuer, want string }{
		{"https://acme.cloudflareaccess.com", "", "https://acme.cloudflareaccess.com/cdn-cgi/access/logout"},
		// Derived from an Access issuer when the team domain isn't set separately.
		{"", "https://acme.cloudflareaccess.com/cdn-cgi/access/sso/oidc/abc123",
			"https://acme.cloudflareaccess.com/cdn-cgi/access/logout"},
		// A non-Access IdP has no known logout endpoint; don't invent one.
		{"", "https://keycloak.example.com/realms/homelab", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		a := &Authenticator{cfg: Config{CFTeamDomain: c.team, Issuer: c.issuer}}
		if got := a.idpLogoutURL(); got != c.want {
			t.Errorf("team=%q issuer=%q: got %q want %q", c.team, c.issuer, got, c.want)
		}
	}
}
