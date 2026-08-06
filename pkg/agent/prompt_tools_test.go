package agent

import (
	"strings"
	"testing"
)

// TestPromptListsTheToolsTheSessionHas is the regression the old prompt had:
// it named four tools while the session was handed ten, so grep and glob were
// invisible to the model that needed them.
func TestPromptListsTheToolsTheSessionHas(t *testing.T) {
	ag, err := New(Config{Name: "t", Model: "m", Workdir: t.TempDir()}, &scriptedProvider{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range ag.enabledTools() {
		names = append(names, d.Name)
	}
	p := systemPrompt(ag.cfg, false, false, names)
	for _, want := range []string{"read(", "write(", "edit(", "grep(", "glob(", "bash("} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt never mentions %s", want)
		}
	}
}

// TestPromptOmitsToolsTheSessionLacks: a restricted session must not be told
// about tools it will be refused, or it spends turns discovering that.
func TestPromptOmitsToolsTheSessionLacks(t *testing.T) {
	ag, err := New(Config{Name: "t", Model: "m", Workdir: t.TempDir()}, &scriptedProvider{},
		WithAllowedTools([]string{"read", "grep"}))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range ag.enabledTools() {
		names = append(names, d.Name)
	}
	p := systemPrompt(ag.cfg, false, false, names)
	if !strings.Contains(p, "read(") || !strings.Contains(p, "grep(") {
		t.Fatalf("prompt should list the allowed tools:\n%s", p)
	}
	for _, gone := range []string{"write(", "edit(", "bash("} {
		if strings.Contains(p, gone) {
			t.Errorf("prompt offers %s to a session that cannot use it", gone)
		}
	}
}

// TestPromptSaysLookupsAreBatched: the loop now runs a turn's read-only calls
// at the same time, and that is only worth anything if the model knows to ask
// for them together.
func TestPromptSaysLookupsAreBatched(t *testing.T) {
	p := basePrompt([]string{"read", "grep", "glob", "bash"})
	if !strings.Contains(p, "at the same time") {
		t.Fatalf("prompt never tells the model its look-ups are batched:\n%s", p)
	}
}
