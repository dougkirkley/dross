package cli

import (
	"os"
	"testing"
)

// Live check that OPENAI_BASE_URL points the picker at a real endpoint.
// Skipped unless DROSS_LIVE_OPENAI_BASE names one.
func TestLivePickerOpenAIBase(t *testing.T) {
	base := os.Getenv("DROSS_LIVE_OPENAI_BASE")
	if base == "" {
		t.Skip("set DROSS_LIVE_OPENAI_BASE to an OpenAI-compatible endpoint")
	}
	t.Setenv("OPENAI_BASE_URL", base)
	t.Setenv("OLLAMA_HOST", "")
	for _, g := range pickerGroups() {
		if g.Provider == "openai" {
			t.Logf("openai -> %v", g.Models)
			for _, m := range g.Models {
				if m == "gpt-5" {
					t.Error("still listing catalog previews from a pointed endpoint")
				}
			}
			if len(g.Models) == 0 {
				t.Error("no models from the pointed endpoint")
			}
			return
		}
	}
	t.Error("openai absent from the picker")
}
