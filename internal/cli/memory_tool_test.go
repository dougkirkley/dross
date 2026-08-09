package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dougkirkley/dross/internal/config"
	"github.com/dougkirkley/dross/internal/memory"
)

func boolp(b bool) *bool { return &b }

// TestMemoryDefaultsOnAndCanBeTurnedOff is the config contract: absent means on,
// and "memory": false means off. A plain bool could not tell absent from false,
// which is why the field is a pointer.
func TestMemoryDefaultsOnAndCanBeTurnedOff(t *testing.T) {
	if !memoryEnabled(nil) {
		t.Error("no config at all should still have memory")
	}
	if !memoryEnabled(&config.File{}) {
		t.Error("an unset field should mean on")
	}
	if !memoryEnabled(&config.File{Memory: boolp(true)}) {
		t.Error("explicit true should mean on")
	}
	if memoryEnabled(&config.File{Memory: boolp(false)}) {
		t.Error(`"memory": false must turn it off`)
	}
}

// Turning it off has to take the tool away too, not just the prompt: a session
// that cannot read its memories but can still write them is the worst of both.
func TestMemoryOffGivesNoStore(t *testing.T) {
	if st := openMemory(&config.File{Memory: boolp(false)}, t.TempDir()); st != nil {
		t.Fatal("memory is off; there should be no store")
	}
	if st := openMemory(&config.File{}, t.TempDir()); st == nil {
		t.Fatal("memory is on by default; there should be a store")
	}
}

// A handler with no store answers instead of panicking — openMemory returns nil
// when the config dir is unavailable, and that is a working session without
// memory, not a broken one.
func TestMemoryToolWithNoStoreSaysSo(t *testing.T) {
	out, isErr := memoryToolHandler(nil)(context.Background(), json.RawMessage(`{"op":"list"}`))
	if !isErr || !strings.Contains(out, "turned off") {
		t.Fatalf("got %q, isErr=%v", out, isErr)
	}
}

func TestMemoryToolRoundTrip(t *testing.T) {
	set, err := memory.OpenSet(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := memoryToolHandler(set)
	call := func(body string) (string, bool) {
		return h(context.Background(), json.RawMessage(body))
	}

	if out, _ := call(`{"op":"list"}`); !strings.Contains(out, "no memories") {
		t.Fatalf("empty list = %q", out)
	}
	if out, isErr := call(`{"op":"write","name":"splits-commits","content":"Wants commits split by concern. Why: easier to revert one."}`); isErr {
		t.Fatalf("write failed: %q", out)
	}
	out, _ := call(`{"op":"list"}`)
	if !strings.Contains(out, "splits-commits") || !strings.Contains(out, "split by concern") {
		t.Fatalf("list = %q", out)
	}
	if out, isErr := call(`{"op":"delete","name":"splits-commits"}`); isErr {
		t.Fatalf("delete failed: %q", out)
	}
	if out, _ := call(`{"op":"list"}`); !strings.Contains(out, "no memories") {
		t.Fatalf("after delete, list = %q", out)
	}
}

// The refusals a model will actually hit, each answered with what to do
// instead — a tool error that only says "invalid" costs a turn to decode.
func TestMemoryToolRefusalsExplainThemselves(t *testing.T) {
	set, err := memory.OpenSet(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := memoryToolHandler(set)
	for _, tc := range []struct{ body, want string }{
		{`{"op":"write","content":"x"}`, "needs a name"},
		{`{"op":"write","name":"x"}`, "op=delete"},
		{`{"op":"delete"}`, "which memory"},
		{`{"op":"sing"}`, "write, delete or list"},
		{`{"op":"write","name":"../escape","content":"x"}`, "lower-case"},
	} {
		out, isErr := h(context.Background(), json.RawMessage(tc.body))
		if !isErr {
			t.Errorf("%s should have been refused, got %q", tc.body, out)
		}
		if !strings.Contains(out, tc.want) {
			t.Errorf("%s: refusal %q does not mention %q", tc.body, out, tc.want)
		}
	}
}

// TestMemoryScopes: a global note is read in every session the user starts, so
// which pool a write lands in is the consequential part of the tool.
func TestMemoryScopes(t *testing.T) {
	cfg := t.TempDir()
	set, err := memory.OpenSet(cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := memoryToolHandler(set)
	call := func(body string) (string, bool) {
		return h(context.Background(), json.RawMessage(body))
	}

	must := func(body string) {
		t.Helper()
		if out, isErr := call(body); isErr {
			t.Fatalf("%s: %q", body, out)
		}
	}
	must(`{"op":"write","name":"needs-sqlite-tag","content":"Tests need -tags sqlite here."}`)
	must(`{"op":"write","scope":"global","name":"explain-first","content":"Explain the tradeoff before writing code."}`)

	// list shows both pools, labelled — a model checking whether something is
	// already known must not be shown half the answer.
	out, _ := call(`{"op":"list"}`)
	if !strings.Contains(out, "[project] needs-sqlite-tag") {
		t.Errorf("project memory missing or unlabelled:\n%s", out)
	}
	if !strings.Contains(out, "[global] explain-first") {
		t.Errorf("global memory missing or unlabelled:\n%s", out)
	}

	// The global note follows the user into an unrelated workspace; the project
	// note does not.
	other, err := memory.OpenSet(cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := other.Prompt()
	if !strings.Contains(p, "Explain the tradeoff") {
		t.Errorf("a global memory should reach every project:\n%s", p)
	}
	if strings.Contains(p, "sqlite") {
		t.Errorf("a project memory leaked into another workspace:\n%s", p)
	}

	// And the write said where it went, so the user can see a global note being
	// made on their behalf.
	if out, _ := call(`{"op":"write","scope":"global","name":"x","content":"y"}`); !strings.Contains(out, "globally") {
		t.Errorf("a global write should say so, got %q", out)
	}
}
