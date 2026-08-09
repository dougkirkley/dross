package openai

import "testing"

// TestNormalizeBase pins which spellings of an OpenAI-compatible endpoint are
// filled in and which are left exactly as written.
//
// The bare host is the case that cost a benchmark run: OPENAI_BASE_URL set to
// "https://ollama.example" made every request ask for /chat/completions at the
// root, and the 404 that came back said nothing about the missing /v1.
func TestNormalizeBase(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://ollama.example", "https://ollama.example/v1"},
		{"https://ollama.example/", "https://ollama.example/v1"},
		{"https://ollama.example/v1", "https://ollama.example/v1"},
		{"https://ollama.example/v1/", "https://ollama.example/v1"},
		// A path the operator chose is a decision, not an omission: a gateway
		// mounted here means this, and appending /v1 to it would break a working
		// configuration to fix a broken one.
		{"https://gw.example/openai", "https://gw.example/openai"},
		{"https://gw.example/openai/v1", "https://gw.example/openai/v1"},
		{"http://localhost:11434", "http://localhost:11434/v1"},
		{"", "https://api.openai.com/v1"},
		// Not a URL at all — handed back rather than guessed at.
		{"nonsense", "nonsense"},
	} {
		if got := NormalizeBase(tc.in); got != tc.want {
			t.Errorf("NormalizeBase(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
