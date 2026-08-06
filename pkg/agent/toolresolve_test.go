package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func resolver(t *testing.T) *Agent {
	t.Helper()
	ag, err := New(Config{Name: "t", Model: "m", Workdir: t.TempDir()}, &scriptedProvider{})
	if err != nil {
		t.Fatal(err)
	}
	return ag
}

// TestPunctuationDoesNotCostATurn is the one seen in the wild: "search?" never
// reached the search/find disambiguation because of the question mark, so a
// well-formed call failed on typography.
func TestPunctuationDoesNotCostATurn(t *testing.T) {
	ag := resolver(t)
	pattern := json.RawMessage(`{"pattern":"Clamp"}`)
	for _, name := range []string{"search?", "search!", "search.", " search ", `"search"`, "search()"} {
		if got := ag.resolveToolName(name, pattern); got != "grep" {
			t.Errorf("resolveToolName(%q) = %q, want grep", name, got)
		}
	}
}

// TestRegexResolvesToGrep: another name the model reached for, with exactly one
// possible destination.
func TestRegexResolvesToGrep(t *testing.T) {
	ag := resolver(t)
	for _, name := range []string{"regex", "regex_search", "code_search", "search_code", "rg"} {
		if got := ag.resolveToolName(name, nil); got != "grep" {
			t.Errorf("resolveToolName(%q) = %q, want grep", name, got)
		}
	}
}

// TestNormalizeToolName pins the stripping directly.
func TestNormalizeToolName(t *testing.T) {
	for in, want := range map[string]string{
		"functions.read": "read",
		"read_tool":      "read",
		"web-search":     "web_search",
		"grep(pattern)":  "grep",
		"  Edit  ":       "edit",
		"bash;":          "bash",
		"web fetch":      "web_fetch",
	} {
		if got := normalizeToolName(in); got != want {
			t.Errorf("normalizeToolName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestAmbiguousSearchStillAsksTheArguments: "search" means the web or the
// workspace depending on what was passed, and guessing would be worse than the
// existing behaviour.
func TestAmbiguousSearchStillAsksTheArguments(t *testing.T) {
	ag := resolver(t)
	if got := ag.resolveToolName("search", json.RawMessage(`{"query":"go generics"}`)); got != "web_search" {
		t.Errorf("a query means the web, got %q", got)
	}
	if got := ag.resolveToolName("search", json.RawMessage(`{"pattern":"func main"}`)); got != "grep" {
		t.Errorf("a pattern means the workspace, got %q", got)
	}
	if got := ag.resolveToolName("search", json.RawMessage(`{}`)); got != "search" {
		t.Errorf("no signal should stay unresolved, got %q", got)
	}
}

// TestUnknownToolIsNotGuessedAt: "debug" has no obvious home, and sending the
// model somewhere it never meant to go is worse than telling it the truth.
func TestUnknownToolIsNotGuessedAt(t *testing.T) {
	ag := resolver(t)
	if got := ag.resolveToolName("debug", nil); got != "debug" {
		t.Errorf("resolveToolName(debug) = %q, want it left alone", got)
	}
	if near := nearestTool("debug", []string{"read", "write", "edit", "bash", "grep", "glob"}); near != "" {
		t.Errorf("nearestTool(debug) = %q, want no suggestion", near)
	}
}

// TestNearestToolSuggestsATypo is the other half: close enough is worth saying.
func TestNearestToolSuggestsATypo(t *testing.T) {
	names := []string{"read", "write", "edit", "bash", "grep", "glob", "web_search"}
	for in, want := range map[string]string{
		"reed":      "read",
		"greps":     "grep",
		"webserach": "web_search",
		"wriet":     "write",
	} {
		if got := nearestTool(in, names); got != want {
			t.Errorf("nearestTool(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestUnknownToolErrorLeadsWithTheSuggestion: a list of eleven names is a menu,
// the closest one is an answer.
func TestUnknownToolErrorLeadsWithTheSuggestion(t *testing.T) {
	ag := resolver(t)
	out, isErr := ag.execTool(t.Context(), toolCall("1", "reed", map[string]any{"path": "a.go"}), &Result{})
	if !isErr {
		t.Fatal("an unknown tool is an error")
	}
	if !strings.Contains(out, "Did you mean read?") {
		t.Errorf("error should lead with the nearest name: %s", out)
	}
	if !strings.Contains(out, "Available tools:") {
		t.Errorf("error should still list the real tools: %s", out)
	}
}

// TestRealNamesAreNeverRewritten: a plugin or MCP tool must not be shadowed by
// a guess.
func TestRealNamesAreNeverRewritten(t *testing.T) {
	ag := resolver(t)
	for _, name := range []string{"read", "write", "edit", "bash", "grep", "glob"} {
		if got := ag.resolveToolName(name, nil); got != name {
			t.Errorf("resolveToolName(%q) rewrote it to %q", name, got)
		}
	}
}
