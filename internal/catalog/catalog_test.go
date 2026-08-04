package catalog

import "testing"

func TestCatalog(t *testing.T) {
	g, ok := Get("groq")
	if !ok || g.Impl != "openai" || g.BaseURL == "" || g.APIKeyEnv != "GROQ_API_KEY" {
		t.Fatalf("groq entry wrong: %+v ok=%v", g, ok)
	}
	if _, ok := Get("nope"); ok {
		t.Fatal("unknown provider should not resolve")
	}
	if len(IDs()) < 10 {
		t.Fatalf("expected a populated catalog, got %d", len(IDs()))
	}
	// anthropic is native, not openai-compatible.
	if a, _ := Get("anthropic"); a.Impl != "anthropic" {
		t.Fatalf("anthropic impl = %q", a.Impl)
	}
}

func TestClaudeCodeSeparateFromAnthropic(t *testing.T) {
	a, ok := Get("anthropic")
	if !ok || a.Impl != "anthropic" {
		t.Fatalf("anthropic missing or wrong impl: %+v", a)
	}
	c, ok := Get("claude-code")
	if !ok {
		t.Fatal("claude-code not in catalog")
	}
	if c.Impl != "claude-code" {
		t.Fatalf("claude-code impl = %q, want claude-code", c.Impl)
	}
	if len(c.Models) == 0 {
		t.Fatal("claude-code should list models")
	}
	var hasA, hasC bool
	for _, id := range IDs() {
		if id == "anthropic" {
			hasA = true
		}
		if id == "claude-code" {
			hasC = true
		}
	}
	if !hasA || !hasC {
		t.Fatalf("IDs() missing anthropic=%v claude-code=%v", hasA, hasC)
	}
}

func TestOllamaHostOverride(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "")
	if p, _ := Get("ollama"); p.BaseURL != "http://localhost:11434/v1" {
		t.Errorf("unset: %q, want the local default", p.BaseURL)
	}

	// Every shape ollama's own variable accepts. The port is what separates a
	// local address from a published one: a bare host with a port is plain
	// http, a bare host without one is a name somebody published and is https.
	for in, want := range map[string]string{
		"node2:11434":            "http://node2:11434/v1",
		"localhost:11434":        "http://localhost:11434/v1",
		"ollama.example.com":     "https://ollama.example.com/v1",
		"http://node2:11434":     "http://node2:11434/v1",
		"http://node2:11434/":    "http://node2:11434/v1",
		"https://ollama.example": "https://ollama.example/v1",
		"http://ollama.example":  "http://ollama.example/v1",
	} {
		t.Setenv("OLLAMA_HOST", in)
		if p, _ := Get("ollama"); p.BaseURL != want {
			t.Errorf("OLLAMA_HOST=%q -> %q, want %q", in, p.BaseURL, want)
		}
	}

	// Junk falls back rather than producing an unusable URL.
	t.Setenv("OLLAMA_HOST", "://")
	if p, _ := Get("ollama"); p.BaseURL != "http://localhost:11434/v1" {
		t.Errorf("garbage: %q, want the local default", p.BaseURL)
	}

	// Only ollama consults it.
	t.Setenv("OLLAMA_HOST", "node2:11434")
	if p, _ := Get("lmstudio"); p.BaseURL != "http://localhost:1234/v1" {
		t.Errorf("lmstudio moved: %q", p.BaseURL)
	}
}

func TestUserProvidersAddAndMerge(t *testing.T) {
	t.Cleanup(func() { SetUserProviders(nil) })
	t.Setenv("OLLAMA_HOST", "")

	SetUserProviders(map[string]Provider{
		// A provider dross ships no knowledge of.
		"homelab": {BaseURL: "https://ollama.example.com/v1", Local: true},
		// A built-in pointed somewhere else, stating only what differs.
		"ollama": {BaseURL: "https://ollama.example.com/v1"},
	})

	got, ok := Get("homelab")
	if !ok || got.BaseURL != "https://ollama.example.com/v1" || !got.Local {
		t.Fatalf("homelab = %+v", got)
	}
	if got.Name != "homelab" || got.Impl != "openai" {
		t.Errorf("defaults not filled: name=%q impl=%q", got.Name, got.Impl)
	}

	// The merge keeps what was not restated, and the environment variable must
	// not overrule an address somebody configured deliberately.
	t.Setenv("OLLAMA_HOST", "somewhere-else.example")
	o, _ := Get("ollama")
	if o.BaseURL != "https://ollama.example.com/v1" {
		t.Errorf("configured base URL lost to $OLLAMA_HOST: %q", o.BaseURL)
	}
	if o.Name != "Ollama" || !o.Local {
		t.Errorf("built-in fields not inherited: %+v", o)
	}

	seen := map[string]int{}
	for _, id := range IDs() {
		seen[id]++
	}
	if seen["homelab"] != 1 || seen["ollama"] != 1 {
		t.Errorf("IDs listed homelab %d, ollama %d; want 1 each", seen["homelab"], seen["ollama"])
	}

	// Clearing puts the built-ins back rather than leaving a hole.
	SetUserProviders(nil)
	if _, ok := Get("homelab"); ok {
		t.Error("homelab survived being cleared")
	}
	if o, _ := Get("ollama"); o.BaseURL == "https://ollama.example.com/v1" {
		t.Error("the built-in ollama did not come back")
	}
}

func TestProviderAPIKey(t *testing.T) {
	t.Setenv("SOME_KEY", "from-env")
	if got := (Provider{Key: "literal", APIKeyEnv: "SOME_KEY"}).APIKey(); got != "literal" {
		t.Errorf("a configured key must win over the variable, got %q", got)
	}
	if got := (Provider{APIKeyEnv: "SOME_KEY"}).APIKey(); got != "from-env" {
		t.Errorf("got %q, want the variable's value", got)
	}
	if got := (Provider{}).APIKey(); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}
