package controller

import (
	"testing"

	dross "github.com/dougkirkley/dross/api/v1alpha1"
)

// The SSO credentials are required: a server that cannot authenticate anyone is
// not worth starting, and a pod stuck in CreateContainerConfigError says so
// more clearly than one that runs and refuses every request.
//
// Everything else is optional. A missing key on a required env var wedges the
// pod, so anything a deployment can reasonably not have — mail, search, a git
// host — has to be marked optional or adopting dross would mean supplying
// credentials for features nobody asked for.
func TestOptionalSecretKeysDoNotWedgeThePod(t *testing.T) {
	env := authEnv(&dross.Dross{Spec: dross.DrossSpec{AuthSecret: "dross-web-auth"}})

	required := map[string]bool{
		"AUTH_CLIENT_ID": true, "AUTH_CLIENT_SECRET": true,
		"AUTH_COOKIE_SECRET": true, "AUTH_SERVICE_TOKEN": true,
	}
	seen := map[string]bool{}
	for _, e := range env {
		seen[e.Name] = true
		ref := e.ValueFrom.SecretKeyRef
		if ref.Name != "dross-web-auth" {
			t.Errorf("%s reads from %q", e.Name, ref.Name)
		}
		if ref.Optional == nil {
			t.Fatalf("%s does not say whether it is optional", e.Name)
		}
		if want := !required[e.Name]; *ref.Optional != want {
			t.Errorf("%s optional = %v; want %v", e.Name, *ref.Optional, want)
		}
	}
	// The forge secrets are the reason this test exists: they arrived after the
	// deployment did, so a Secret without them must still start.
	for _, name := range []string{"GITHUB_CLIENT_SECRET", "GITLAB_CLIENT_SECRET"} {
		if !seen[name] {
			t.Errorf("%s is not passed to the server", name)
		}
	}
}

// No Secret named, nothing to pull.
func TestAuthEnvIsEmptyWithoutASecret(t *testing.T) {
	if got := authEnv(&dross.Dross{}); got != nil {
		t.Errorf("authEnv = %v with no AuthSecret", got)
	}
}
