package agent

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/dougkirkley/dross/pkg/provider"
)

// splittingProvider answers every session the same way: delegate once, then
// finish. Pointed at itself as its own worker, it is a model that would recurse
// forever, which is the only useful thing to test the limits against.
type splittingProvider struct{ sessions atomic.Int32 }

func (p *splittingProvider) Name() string { return "splitting" }
func (p *splittingProvider) NewSession(provider.SessionConfig) provider.Session {
	p.sessions.Add(1)
	return &scriptedSession{steps: []provider.Response{
		{
			StopReason: provider.StopToolUse,
			ToolCalls:  []provider.ToolCall{toolCall("1", "task", map[string]any{"task": "split this further"})},
		},
		{Text: "done", StopReason: provider.StopEndTurn},
	}}
}

// TestWorkersDoNotGetWorkers is the default arrangement: the lead splits the
// job, the workers do it. A worker that tries to pass its task on again is
// refused, so the tree is two levels however eagerly a model reaches for the
// tool.
//
// Two sessions, not three. The lead's, and the one worker's.
func TestWorkersDoNotGetWorkers(t *testing.T) {
	prov := &splittingProvider{}
	ag, err := New(Config{Name: "lead", Model: "m", Workdir: t.TempDir()},
		prov, WithWorker(prov, "worker-model"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ag.Run(context.Background(), "a job that is several jobs"); err != nil {
		t.Fatal(err)
	}
	if got := prov.sessions.Load(); got != 2 {
		t.Fatalf("opened %d sessions; want 2 (the lead and its worker)", got)
	}
	if got := ag.spawned.Load(); got != 1 {
		t.Fatalf("spawned %d sub-agents; want 1", got)
	}
}

// TestDeeperDelegationIsAvailableWhenAskedFor: the hierarchy is off, not gone.
// WithMaxTaskDepth is what a comparison between the two arrangements would use,
// so it stays wired and tested rather than rotting until someone needs it.
func TestDeeperDelegationIsAvailableWhenAskedFor(t *testing.T) {
	prov := &splittingProvider{}
	ag, err := New(Config{Name: "lead", Model: "m", Workdir: t.TempDir()},
		prov, WithWorker(prov, "worker-model"), WithMaxTaskDepth(2))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ag.Run(context.Background(), "a job that is several jobs"); err != nil {
		t.Fatal(err)
	}
	if got := prov.sessions.Load(); got != 3 {
		t.Fatalf("opened %d sessions; want 3 (lead, worker, worker's worker)", got)
	}
}

// TestTheLastLevelIsToldToDoItItself: the refusal has to say what to do next.
// "Delegation is not available" leaves a model that has just been handed a job
// with no move; naming the move is the difference between a wasted turn and a
// finished task.
func TestTheLastLevelIsToldToDoItItself(t *testing.T) {
	prov := &splittingProvider{}
	leaf, err := New(Config{Name: "leaf", Model: "m", Workdir: t.TempDir()},
		prov, WithWorker(prov, "worker-model"), withDepth(1, &atomic.Int32{}))
	if err != nil {
		t.Fatal(err)
	}
	if leaf.canDelegate() {
		t.Fatal("the last level should not be offered a task tool")
	}
	for _, tl := range leaf.enabledTools() {
		if tl.Name == "task" {
			t.Fatal("task offered at the depth limit")
		}
	}
	out, isErr := leaf.delegate(context.Background(), "split this further", &Result{})
	if !isErr || !strings.Contains(out, "yourself") {
		t.Fatalf("delegate at the limit returned %q, isErr=%v", out, isErr)
	}
}

// TestSubagentBudgetIsSharedByTheWholeTree removes the depth limit and checks
// the other one holds. Depth alone bounds nothing — a tree that fans out is
// bounded by its width, not its height — so the count is what stands between a
// high-level request and an unbounded bill.
func TestSubagentBudgetIsSharedByTheWholeTree(t *testing.T) {
	prov := &splittingProvider{}
	ag, err := New(Config{Name: "lead", Model: "m", Workdir: t.TempDir()},
		prov, WithWorker(prov, "worker-model"), WithMaxTaskDepth(1000))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ag.Run(context.Background(), "a job that never stops splitting"); err != nil {
		t.Fatal(err)
	}
	if got := int(ag.spawned.Load()); got != maxSubagents {
		t.Fatalf("spawned %d sub-agents; want the budget, %d", got, maxSubagents)
	}
}

// TestFanOutHintFiresOnSerialDelegation: with one level of workers, how wide
// the lead fans out is the whole of the parallelism, and a lead handing over one
// subtask at a time is running them end to end. The hint arrives on the second
// one, and stops the moment a list is used.
func TestFanOutHintFiresOnSerialDelegation(t *testing.T) {
	prov := &splittingProvider{}
	ag, err := New(Config{Name: "lead", Model: "m", Workdir: t.TempDir()},
		prov, WithWorker(prov, "worker-model"))
	if err != nil {
		t.Fatal(err)
	}
	one := toolCall("1", "task", map[string]any{"task": "a piece"})

	if got := ag.fanOutHint(one); got != "" {
		t.Fatalf("hinted on the first delegation: %q", got)
	}
	got := ag.fanOutHint(one)
	if !strings.Contains(got, "tasks=") {
		t.Fatalf("second delegation should name the list form, got %q", got)
	}
	if !strings.Contains(got, "in parallel") {
		t.Fatalf("the hint should say what it buys, got %q", got)
	}

	// A batch clears the count, and a model that has taken the point is not
	// told again.
	if got := ag.fanOutHint(toolCall("2", "task", map[string]any{"tasks": []string{"a", "b"}})); got != "" {
		t.Fatalf("hinted at a model already batching: %q", got)
	}
	if got := ag.fanOutHint(one); got != "" {
		t.Fatalf("the count should have restarted, got %q", got)
	}

	// And a different tool ends the run of them entirely.
	ag.singleTasks = singleTasksBeforeHint - 1
	if got := ag.fanOutHint(toolCall("3", "read", map[string]any{"path": "a.go"})); got != "" {
		t.Fatalf("a read is not a delegation: %q", got)
	}
	if ag.singleTasks != 0 {
		t.Fatalf("singleTasks = %d after another tool; want 0", ag.singleTasks)
	}
}

// TestNestWorkerKeepsOnePrefix pins the event shape a client renders the tree
// from: one "worker:" however deep the event came from, and the depth carried
// in the name instead.
func TestNestWorkerKeepsOnePrefix(t *testing.T) {
	kind, data := nestWorker("port the loader", "text", "reading config.go")
	if kind != "worker:text" {
		t.Fatalf("kind = %q", kind)
	}
	if data != "port the loader"+workerTagSep+"reading config.go" {
		t.Fatalf("data = %q", data)
	}

	// The same event arriving from one level further down.
	kind, data = nestWorker("port the loader", kind, data)
	if kind != "worker:text" {
		t.Fatalf("nested kind = %q; a second prefix would be unrenderable", kind)
	}
	if want := "port the loader/port the loader" + workerTagSep + "reading config.go"; data != want {
		t.Fatalf("nested data = %q, want %q", data, want)
	}
	if n := strings.Count(data, workerTagSep); n != 1 {
		t.Fatalf("%d separators in the payload; the client splits on the first", n)
	}
}

// TestWorkerIdentityDependsOnDepth: a worker is told it may split only while it
// still can. Describing a tool the agent does not have is how a run spends a
// turn discovering the refusal.
func TestWorkerIdentityDependsOnDepth(t *testing.T) {
	// The default: one level of worker, and it is not told about a tool it does
	// not have.
	if got := workerIdentityFor(1, maxTaskDepth); strings.Contains(got, "several jobs") {
		t.Error("a worker under the default depth should not be told it may split")
	}
	if got := workerIdentityFor(1, 2); !strings.Contains(got, "several jobs") {
		t.Error("a worker with depth beneath it should be told it may split")
	}
	if got := workerIdentityFor(2, 2); strings.Contains(got, "several jobs") {
		t.Error("the last level should not be told about a tool it does not have")
	}
}
