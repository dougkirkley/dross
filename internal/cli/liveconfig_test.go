package cli

import (
	"os"
	"testing"
)

// Uses the real user config, so it only runs when asked.
func TestLiveConfiguredProvider(t *testing.T) {
	if os.Getenv("DROSS_LIVE_CONFIG") == "" {
		t.Skip("set DROSS_LIVE_CONFIG=1 to check the real config")
	}
	t.Setenv("OLLAMA_HOST", "")
	t.Setenv("OPENAI_BASE_URL", "")
	file := loadProviderFile("")
	if len(file.Providers) == 0 {
		t.Fatal("no configured providers")
	}
	for id, d := range file.Providers {
		t.Logf("configured: %s -> %s", id, d.BaseURL)
	}
	for _, g := range pickerGroups(file) {
		if g.Provider == "ollama" {
			t.Logf("picker lists ollama with %d models: %v", len(g.Models), g.Models)
			if len(g.Models) == 0 {
				t.Error("ollama has no models in the picker")
			}
			return
		}
	}
	t.Error("ollama absent from the picker")
}
