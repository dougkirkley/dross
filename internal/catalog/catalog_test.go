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

	// Every shape ollama's own variable accepts.
	for in, want := range map[string]string{
		"node2:11434":              "http://node2:11434/v1",
		"node2":                    "http://node2:11434/v1",
		"http://node2:11434":       "http://node2:11434/v1",
		"https://ollama.example":   "https://ollama.example:11434/v1",
		"http://node2:11434/":      "http://node2:11434/v1",
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
