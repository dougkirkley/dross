package agent

import (
	"strings"
	"testing"
)

func TestSandboxEnvScopedToWorkspace(t *testing.T) {
	dir := t.TempDir()
	w, err := newWorkspace(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if w.env == nil {
		t.Fatal("sandbox workspace should have a scoped env")
	}
	find := func(key string) string {
		for i := len(w.env) - 1; i >= 0; i-- { // last wins, like exec
			if strings.HasPrefix(w.env[i], key+"=") {
				return strings.TrimPrefix(w.env[i], key+"=")
			}
		}
		return ""
	}
	for _, key := range []string{"HOME", "GOPATH", "GOBIN", "CARGO_HOME", "PYTHONUSERBASE", "DROSS_TOOLS", "DROSS_WORKSPACE"} {
		v := find(key)
		if v == "" {
			t.Errorf("%s not set in sandbox env", key)
			continue
		}
		if key != "DROSS_WORKSPACE" && !strings.HasPrefix(v, w.root) {
			t.Errorf("%s=%q should be inside workspace %q", key, v, w.root)
		}
	}
	// PATH must keep the sandbox bins first but still include the inherited PATH.
	path := find("PATH")
	if !strings.Contains(path, w.root) {
		t.Errorf("PATH should include sandbox bins under %q: %q", w.root, path)
	}
}

func TestNonSandboxInheritsEnv(t *testing.T) {
	w, err := newWorkspace(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if w.env != nil {
		t.Error("non-sandbox workspace should inherit the parent env (nil)")
	}
}

func TestBlockedCmdSudo(t *testing.T) {
	sb, _ := newWorkspace(t.TempDir(), true)
	plain, _ := newWorkspace(t.TempDir(), false)
	cases := []struct {
		cmd     string
		blocked bool
	}{
		{"sudo apt install foo", true},
		{"echo hi && sudo rm -rf /", true},
		{"cat x | sudo tee /etc/y", true},
		{"doas pkg_add foo", true},
		{"go build ./...", false},
		{"echo pseudo-random", false}, // "sudo" only as a substring must not trip
		{"ls /usr/sudoers", false},
	}
	for _, c := range cases {
		if got := sb.blockedCmd(c.cmd) != ""; got != c.blocked {
			t.Errorf("sandbox blockedCmd(%q) blocked=%v, want %v", c.cmd, got, c.blocked)
		}
		if plain.blockedCmd(c.cmd) != "" {
			t.Errorf("non-sandbox must never block: %q", c.cmd)
		}
	}
}
