package agent

import (
	"strings"
	"testing"
)

// The model can run `env`. Provider keys held by this process must not be
// readable from the sandboxed shell, while ordinary tooling env survives.
func TestSandboxEnvDropsCredentials(t *testing.T) {
	in := []string{
		"PATH=/usr/bin",
		"LANG=en_US.UTF-8",
		"TERM=xterm",
		"HOME=/root",
		"ANTHROPIC_API_KEY=sk-secret",
		"OPENAI_API_KEY=sk-secret",
		"GEMINI_API_KEY=secret",
		"GITHUB_TOKEN=ghp_secret",
		"AWS_SECRET_ACCESS_KEY=secret",
		"DB_PASSWORD=hunter2",
		"CLOUDFLARE_API_TOKEN=secret",
		"SSH_AUTH_SOCK=/tmp/agent.sock",
		"AUTH_COOKIE_SECRET=deadbeef",
		"GIT_AUTHOR_NAME=Doug",
	}
	got := strings.Join(withoutSecrets(in), "\n")

	for _, leaked := range []string{"API_KEY", "GITHUB_TOKEN", "SECRET", "PASSWORD",
		"CLOUDFLARE_API_TOKEN", "SSH_AUTH_SOCK"} {
		if strings.Contains(got, leaked) {
			t.Errorf("credential %s survived into the sandbox env", leaked)
		}
	}
	if strings.Contains(got, "sk-secret") || strings.Contains(got, "hunter2") {
		t.Error("a secret value leaked")
	}
	for _, keep := range []string{"PATH=", "LANG=", "TERM=", "GIT_AUTHOR_NAME=Doug"} {
		if !strings.Contains(got, keep) {
			t.Errorf("ordinary env %q was stripped", keep)
		}
	}
}
