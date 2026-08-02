package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingReturnsEmpty(t *testing.T) {
	// Isolate from any real user config so the search finds nothing.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DROSS_CONFIG", "")
	f, path, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "" {
		t.Fatalf("expected no path, got %q", path)
	}
	if f.Provider != "" || f.Worker != nil {
		t.Fatalf("expected empty file, got %+v", f)
	}
}

func TestLoadParsesWorker(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "dross.json")
	os.WriteFile(p, []byte(`{
	  "provider": "anthropic",
	  "model": "claude-opus-5",
	  "style": "terse",
	  "worker": {"provider": "openai", "model": "qwen3-coder:30b", "base_url": "http://x/v1"}
	}`), 0o644)

	f, got, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got != p {
		t.Fatalf("path = %q, want %q", got, p)
	}
	if f.Model != "claude-opus-5" || f.Style != "terse" {
		t.Fatalf("bad file: %+v", f)
	}
	if f.Worker == nil || f.Worker.Model != "qwen3-coder:30b" || f.Worker.BaseURL != "http://x/v1" {
		t.Fatalf("bad worker: %+v", f.Worker)
	}
}

func TestLoadMalformedErrors(t *testing.T) {
	p := filepath.Join(t.TempDir(), "dross.json")
	os.WriteFile(p, []byte(`{not json`), 0o644)
	if _, _, err := Load(p); err == nil {
		t.Fatal("expected error on malformed json")
	}
}

func TestEnvFor(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "ak")
	t.Setenv("ANTHROPIC_BASE_URL", "https://ant")
	t.Setenv("OPENAI_API_KEY", "ok")
	t.Setenv("OPENAI_BASE_URL", "https://oai")

	if b, k := EnvFor("anthropic"); b != "https://ant" || k != "ak" {
		t.Fatalf("anthropic env = %q %q", b, k)
	}
	if b, k := EnvFor("openai"); b != "https://oai" || k != "ok" {
		t.Fatalf("openai env = %q %q", b, k)
	}
}

func TestEnvForOpenAIAPIBaseFallback(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("OPENAI_API_BASE", "https://legacy")
	if b, _ := EnvFor("openai"); b != "https://legacy" {
		t.Fatalf("fallback base = %q", b)
	}
}

// Precedence: explicit flag > env > file > default.
func TestResolvePrecedence(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "env-key")
	t.Setenv("ANTHROPIC_BASE_URL", "")

	file := File{Provider: "anthropic", Model: "file-model", APIKey: "file-key"}

	// Nothing set on the command line: file model wins, env key wins over file key.
	got := Resolve(Layers{
		File:            file,
		ProviderFlag:    "anthropic",
		ProviderDefault: "anthropic",
		ModelFlag:       "claude-opus-5",
		ModelDefault:    "claude-opus-5", // flag not changed
	})
	if got.Model != "file-model" {
		t.Fatalf("model = %q, want file-model", got.Model)
	}
	if got.APIKey != "env-key" {
		t.Fatalf("apiKey = %q, want env-key (env over file)", got.APIKey)
	}

	// Explicit flag overrides everything.
	got = Resolve(Layers{
		File:          file,
		ModelFlag:     "flag-model",
		ModelChanged:  true,
		ModelDefault:  "claude-opus-5",
		APIKeyFlag:    "flag-key",
		APIKeyChanged: true,
	})
	if got.Model != "flag-model" || got.APIKey != "flag-key" {
		t.Fatalf("flag override failed: %+v", got)
	}
}

func TestResolveFallsBackToDefault(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_BASE_URL", "")
	got := Resolve(Layers{
		ProviderFlag:    "openai",
		ProviderDefault: "openai",
		ModelFlag:       "def",
		ModelDefault:    "def",
	})
	if got.Model != "def" {
		t.Fatalf("model = %q, want def", got.Model)
	}
}
