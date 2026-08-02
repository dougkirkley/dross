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
