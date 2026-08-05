package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dougkirkley/dross/pkg/provider"
)

func newAgent(t *testing.T, opts ...Option) *Agent {
	t.Helper()
	ag, err := New(Config{Name: "t", Model: "m", Workdir: t.TempDir()},
		&scriptedProvider{}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return ag
}

func TestResolveToolName(t *testing.T) {
	ag := newAgent(t)
	for in, want := range map[string]string{
		"read":        "read", // already real: untouched
		"read_file":   "read",
		"cat":         "read",
		"write_file":  "write",
		"shell":       "bash",
		"run_command": "bash",
		"ls":          "glob",
		"list_files":  "glob",
		"rg":          "grep",
		"websearch":   "web_search",
		"fetch":       "web_fetch",
		// Qualified and decorated spellings some models emit.
		"functions.read": "read",
		"read-tool":      "read",
		"web-search":     "web_search",
		"READ":           "read",
	} {
		if got := ag.resolveToolName(in, nil); got != want {
			t.Errorf("resolveToolName(%q) = %q, want %q", in, got, want)
		}
	}
}

// An alias only resolves to a tool that is actually available: ask_user exists
// only when an asker is configured, so without one "ask" stays unmapped rather
// than pointing at a tool the model cannot call.
func TestResolveOnlyToAvailableTools(t *testing.T) {
	if got := newAgent(t).resolveToolName("ask", nil); got != "ask" {
		t.Errorf("got %q; ask_user is not available without an asker", got)
	}
	withAsker := newAgent(t, WithAsker(func(AskRequest) (string, error) { return "", nil }))
	if got := withAsker.resolveToolName("ask", nil); got != "ask_user" {
		t.Errorf("got %q, want ask_user once an asker exists", got)
	}
}

// "search" is the one seen in the wild, and it is genuinely ambiguous: dross
// renders a grep as "searched 1 pattern", while web_search is also a search.
// Decided by what was passed, not guessed.
func TestResolveSearchByArguments(t *testing.T) {
	ag := newAgent(t)
	for _, c := range []struct{ args, want string }{
		{`{"pattern":"func main"}`, "grep"},
		{`{"query":"golang duration parsing"}`, "web_search"},
		{`{}`, "search"}, // no signal: left alone, so the reply names the real tools
	} {
		if got := ag.resolveToolName("search", json.RawMessage(c.args)); got != c.want {
			t.Errorf("search%s = %q, want %q", c.args, got, c.want)
		}
	}
}

// A real tool must never be shadowed by a guess.
func TestResolveNeverShadowsARealTool(t *testing.T) {
	ag := newAgent(t, WithTool(provider.ToolDef{Name: "shell"},
		func(context.Context, json.RawMessage) (string, bool) { return "", false }))
	if got := ag.resolveToolName("shell", nil); got != "shell" {
		t.Errorf("got %q — a registered tool was remapped to a built-in", got)
	}
}

// Nonsense stays nonsense, so the model still gets the list of real tools.
func TestResolveLeavesUnknownsAlone(t *testing.T) {
	ag := newAgent(t)
	for _, in := range []string{"frobnicate", "", "do_the_thing"} {
		if got := ag.resolveToolName(in, nil); got != in {
			t.Errorf("resolveToolName(%q) = %q, want it unchanged", in, got)
		}
	}
}
