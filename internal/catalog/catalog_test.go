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
