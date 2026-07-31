package agent

import (
	"encoding/json"
	"testing"

	"github.com/dougkirkley/dross/pkg/provider"
)

func tcWrite(t *testing.T, path string) provider.ToolCall {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"path": path, "content": "x"})
	return provider.ToolCall{Name: "write", Input: b}
}

func tcBash(t *testing.T, cmd string) provider.ToolCall {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"command": cmd})
	return provider.ToolCall{Name: "bash", Input: b}
}

func TestSensitiveWrite(t *testing.T) {
	sensitive := []provider.ToolCall{
		tcWrite(t, "/Users/x/.ssh/config"),
		tcWrite(t, "~/.ssh/id_ed25519"),
		tcWrite(t, "/Users/x/.aws/credentials"),
		tcWrite(t, "/root/.kube/config"),
		tcWrite(t, "/Users/x/.claude.json"),
		tcWrite(t, "/Users/x/.claude/settings.json"),
		tcWrite(t, "/Users/x/.config/dross/config.json"),
		tcWrite(t, "/srv/holocron/config.yaml"),
		tcWrite(t, "project/.env"),
		tcWrite(t, "project/.env.local"),
		tcWrite(t, "/Users/x/.zshrc"),
		tcWrite(t, "certs/server.pem"),
		tcWrite(t, "certs/server.key"),
		tcWrite(t, "/etc/hosts"),
		tcBash(t, "echo 'Host x' >> ~/.ssh/config"),
		tcBash(t, "sed -i s/a/b/ ~/.zshrc"),
		tcBash(t, "cp secret.pem /Users/x/.ssh/id_rsa"),
	}
	for _, tc := range sensitive {
		if _, bad := sensitiveWrite(tc); !bad {
			t.Errorf("expected sensitive: %s", string(tc.Input))
		}
	}

	safe := []provider.ToolCall{
		tcWrite(t, "src/main.go"),
		tcWrite(t, "README.md"),
		tcWrite(t, "internal/config/config.go"),
		tcBash(t, "cat ~/.ssh/config"),        // read, no write verb
		tcBash(t, "grep Host ~/.ssh/config"),  // read
		tcBash(t, "go build ./..."),           // no sensitive path
		tcBash(t, "echo done > /tmp/out.log"), // write, not sensitive
	}
	for _, tc := range safe {
		if _, bad := sensitiveWrite(tc); bad {
			t.Errorf("expected safe: %s", string(tc.Input))
		}
	}
}
