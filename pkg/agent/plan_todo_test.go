package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dougkirkley/dross/pkg/provider"
)

func planner(t *testing.T, plan bool) *Agent {
	t.Helper()
	ag, err := New(Config{Name: "t", Model: "m", Workdir: t.TempDir(), Plan: plan}, &scriptedProvider{})
	if err != nil {
		t.Fatal(err)
	}
	return ag
}

// Plan mode is a promise, so it is kept at the gate rather than in the prompt.
// A model told not to write that writes anyway has broken the only thing plan
// mode offers.
func TestPlanModeRefusesAnythingThatChangesTheWorld(t *testing.T) {
	a := planner(t, true)
	for _, name := range []string{"write", "edit", "bash", "task", "publish"} {
		msg, ok := a.gate(provider.ToolCall{Name: name, Input: json.RawMessage(`{}`)})
		if ok {
			t.Errorf("%s should be refused while planning", name)
		}
		if !strings.Contains(msg, "exit_plan_mode") {
			t.Errorf("the refusal should name the way out: %s", msg)
		}
	}
}

// Looking is the whole of what planning is for, so it must not be gated — a
// session that cannot read cannot write a plan worth agreeing to.
func TestPlanModeAllowsLooking(t *testing.T) {
	a := planner(t, true)
	for _, name := range []string{"read", "grep", "glob", "web_search", "todo", "exit_plan_mode"} {
		if _, ok := a.gate(provider.ToolCall{Name: name, Input: json.RawMessage(`{}`)}); !ok {
			t.Errorf("%s should be available while planning", name)
		}
	}
}

// Off by default: an ordinary session is not silently prevented from working.
func TestPlanModeIsOffUnlessAskedFor(t *testing.T) {
	a := planner(t, false)
	if a.Planning() {
		t.Error("a session should not plan unless it was told to")
	}
	if _, ok := a.gate(provider.ToolCall{Name: "write", Input: json.RawMessage(`{}`)}); !ok {
		t.Error("an ordinary session may write")
	}
	// And the tool is not offered, so nothing is invited to call it.
	for _, d := range a.enabledTools() {
		if d.Name == "exit_plan_mode" {
			t.Error("exit_plan_mode should only exist while planning")
		}
	}
}

// With nobody to ask, the plan is recorded and the session proceeds: a headless
// run has no user, and stopping dead would make plan mode unusable exactly
// where it is most useful.
func TestExitPlanModeProceedsWhenThereIsNobodyToAsk(t *testing.T) {
	a := planner(t, true)
	out, isErr := a.exitPlanMode("1. read the thing\n2. change the thing")
	if isErr {
		t.Fatalf("a plan with nobody to approve it is not an error: %s", out)
	}
	if a.Planning() {
		t.Error("the session should be able to act now")
	}
	if _, ok := a.gate(provider.ToolCall{Name: "write", Input: json.RawMessage(`{}`)}); !ok {
		t.Error("write should be back")
	}
}

// A refused plan is not a failed call: the session keeps planning, and it is
// told what was said about it.
func TestARefusedPlanKeepsPlanning(t *testing.T) {
	a := planner(t, true)
	WithAsker(func(AskRequest) (string, error) { return "Keep planning", nil })(a)

	out, isErr := a.exitPlanMode("do the risky thing")
	if isErr {
		t.Errorf("being turned down is not a tool failure: %s", out)
	}
	if !a.Planning() {
		t.Fatal("a refused plan must not unlock the tools")
	}
	if _, ok := a.gate(provider.ToolCall{Name: "write", Input: json.RawMessage(`{}`)}); ok {
		t.Error("write should still be refused")
	}
}

func TestAnAcceptedPlanUnlocksTheTools(t *testing.T) {
	a := planner(t, true)
	WithAsker(func(AskRequest) (string, error) { return planAccept, nil })(a)
	if _, isErr := a.exitPlanMode("1. do it"); isErr {
		t.Fatal("an accepted plan should succeed")
	}
	if a.Planning() {
		t.Error("the session should be able to act now")
	}
}

// An empty plan is nothing to agree to, and saying so beats unlocking the
// tools on the strength of a blank string.
func TestAnEmptyPlanIsRejected(t *testing.T) {
	a := planner(t, true)
	out, isErr := a.exitPlanMode("   ")
	if !isErr {
		t.Error("there is nothing to agree to")
	}
	if !a.Planning() {
		t.Error("an empty plan must not unlock the tools")
	}
	_ = out
}

// --- todo ------------------------------------------------------------------

// The list is handed back rather than acknowledged, because that is most of its
// value: what is current and what is left, in front of the model after every
// update, instead of re-derived from a conversation that may have been
// compacted away.
func TestTodoReturnsTheWholeList(t *testing.T) {
	a := planner(t, false)
	out, isErr := a.writeTodos([]todoItem{
		{Task: "find the bug", Status: "completed"},
		{Task: "fix it", Status: "in_progress"},
		{Task: "run the tests", Status: "pending"},
	})
	if isErr {
		t.Fatalf("a well-formed list is not an error: %s", out)
	}
	for _, want := range []string{"[x] find the bug", "[>] fix it", "[ ] run the tests"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

// Models write "done" and "doing" as often as the canonical words. A status
// nobody can parse is treated as pending rather than rejected: refusing to keep
// the list because of one word costs a turn and helps nobody.
func TestTodoAcceptsTheWordsModelsActuallyWrite(t *testing.T) {
	a := planner(t, false)
	out, _ := a.writeTodos([]todoItem{
		{Content: "one", Status: "done"},
		{Content: "two", Status: "doing"},
		{Content: "three", Status: "banana"},
	})
	for _, want := range []string{"[x] one", "[>] two", "[ ] three"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

// The list is a note to self, so its shape is worth a word rather than a
// rejection — but the word has to be there, or nothing distinguishes a plan
// being worked through from one being ignored.
func TestTodoSaysWhenNothingIsBeingWorkedOn(t *testing.T) {
	a := planner(t, false)
	out, _ := a.writeTodos([]todoItem{{Task: "a", Status: "pending"}, {Task: "b", Status: "pending"}})
	if !strings.Contains(out, "Nothing is in progress") {
		t.Errorf("a list with no current step should say so:\n%s", out)
	}

	out, _ = a.writeTodos([]todoItem{{Task: "a", Status: "done"}, {Task: "b", Status: "done"}})
	if !strings.Contains(out, "Every step is done") {
		t.Errorf("a finished list should say so:\n%s", out)
	}

	out, _ = a.writeTodos([]todoItem{{Task: "a", Status: "doing"}, {Task: "b", Status: "doing"}})
	if !strings.Contains(out, "one at a time") {
		t.Errorf("two current steps should be called out:\n%s", out)
	}
}

func TestTodoRejectsAnEmptyList(t *testing.T) {
	a := planner(t, false)
	if _, isErr := a.writeTodos(nil); !isErr {
		t.Error("an empty list is a call that says nothing")
	}
	if _, isErr := a.writeTodos([]todoItem{{Status: "pending"}}); !isErr {
		t.Error("an item with no text is nothing to do")
	}
}

// The rename kept the old name working. A model that learned `delegate`, or a
// session resumed from before the change, should not lose a turn to it.
func TestTheOldDelegateNameStillWorks(t *testing.T) {
	ag, err := New(Config{Name: "t", Model: "m", Workdir: t.TempDir()}, &scriptedProvider{},
		WithWorker(&scriptedProvider{}, "w"))
	if err != nil {
		t.Fatal(err)
	}
	if got := ag.resolveToolName("delegate", json.RawMessage(`{"task":"x"}`)); got != "task" {
		t.Errorf("resolveToolName(delegate) = %q, want task", got)
	}
	if got := ag.resolveToolName("todowrite", json.RawMessage(`{}`)); got != "todo" {
		t.Errorf("resolveToolName(todowrite) = %q, want todo", got)
	}
}
