package cli

import (
	"os"
	"testing"

	"github.com/dougkirkley/dross/internal/config"
)

// Live check against a real ollama. Skipped unless DROSS_LIVE_OLLAMA names one.
func TestLivePicker(t *testing.T) {
	host := os.Getenv("DROSS_LIVE_OLLAMA")
	if host == "" {
		t.Skip("set DROSS_LIVE_OLLAMA to a running server")
	}
	t.Setenv("OLLAMA_HOST", host)
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("OPENAI_API_BASE", "")
	for _, g := range pickerGroups(&config.File{}) {
		if g.Provider == "ollama" {
			t.Logf("ollama -> %v", g.Models)
			if len(g.Models) == 0 {
				t.Error("no models: the picker would not list it")
			}
			return
		}
	}
	t.Error("ollama absent from the picker")
}
