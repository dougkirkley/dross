package memory

import (
	"strings"
	"testing"
)

func open(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestWriteListAndDelete(t *testing.T) {
	st := open(t)
	if got := st.Prompt(); got != "" {
		t.Fatalf("an empty store should contribute nothing to the prompt, got %q", got)
	}
	if err := st.Write("prefers-table-tests", "The user wants table-driven tests. Why: they read as a spec."); err != nil {
		t.Fatal(err)
	}
	notes := st.List()
	if len(notes) != 1 || notes[0].Name != "prefers-table-tests" {
		t.Fatalf("List = %+v", notes)
	}
	if !strings.Contains(st.Prompt(), "table-driven") {
		t.Fatalf("the memory should reach the prompt:\n%s", st.Prompt())
	}
	if err := st.Delete("prefers-table-tests"); err != nil {
		t.Fatal(err)
	}
	if len(st.List()) != 0 {
		t.Fatal("delete left the memory behind")
	}
	// Deleting what is not there is what the caller wanted, not an error.
	if err := st.Delete("prefers-table-tests"); err != nil {
		t.Fatalf("second delete: %v", err)
	}
}

// Reusing a name replaces the note, which is how a wrong memory gets corrected
// rather than accumulating a contradiction beside it.
func TestWriteReplaces(t *testing.T) {
	st := open(t)
	must(t, st.Write("deploy-target", "Deploys go to staging."))
	must(t, st.Write("deploy-target", "Deploys go to production since the migration."))
	notes := st.List()
	if len(notes) != 1 {
		t.Fatalf("want one memory, got %d", len(notes))
	}
	if strings.Contains(notes[0].Body, "staging") {
		t.Fatalf("the old note survived: %q", notes[0].Body)
	}
}

// An empty body deletes, so a model can retract with the tool it already has.
func TestEmptyBodyRetracts(t *testing.T) {
	st := open(t)
	must(t, st.Write("wrong", "something untrue"))
	must(t, st.Write("wrong", "   "))
	if len(st.List()) != 0 {
		t.Fatal("an empty write should have removed the memory")
	}
}

// TestNamesAreConstrained: the name becomes a filename. A model that can pick
// any string has been handed a write primitive, not a notebook.
func TestNamesAreConstrained(t *testing.T) {
	st := open(t)
	for _, bad := range []string{
		"../escape", "/etc/passwd", "has space", "Upper", "", "sub/dir",
		strings.Repeat("x", 65),
	} {
		if err := st.Write(bad, "body"); err == nil {
			t.Errorf("name %q was accepted", bad)
		}
	}
	for _, ok := range []string{"a", "kebab-case-name", "has-9-digits"} {
		if err := st.Write(ok, "body"); err != nil {
			t.Errorf("name %q was refused: %v", ok, err)
		}
	}
}

// TestProjectsDoNotShareMemories: a fact about one checkout is usually wrong
// about another, and a shared pool would leak one project's details into an
// unrelated one.
func TestProjectsDoNotShareMemories(t *testing.T) {
	cfg := t.TempDir()
	a, err := Open(cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	must(t, a.Write("secret", "the client is Acme"))
	if len(b.List()) != 0 {
		t.Fatal("a second workspace can see the first's memories")
	}
	if a.Dir() == b.Dir() {
		t.Fatal("two workspaces share a directory")
	}
}

// Two checkouts of the same repository in different places are different
// workspaces. Keying on the basename alone would silently merge them.
func TestSameNameDifferentPathsStaySeparate(t *testing.T) {
	cfg := t.TempDir()
	a, _ := Open(cfg, t.TempDir()+"/dross")
	b, _ := Open(cfg, t.TempDir()+"/dross")
	if a.Dir() == b.Dir() {
		t.Fatal("same basename, different paths, same directory")
	}
}

// TestPromptIsBudgeted: memories are meant to be few. A session that writes one
// every turn must not be able to crowd out the rest of the system prompt, and
// what is dropped has to be admitted rather than silently lost.
func TestPromptIsBudgeted(t *testing.T) {
	st := open(t)
	body := strings.Repeat("this is a long memory. ", 60) // ~1.4k each
	for _, n := range []string{"one", "two", "three", "four", "five"} {
		must(t, st.Write(n, body))
	}
	p := st.Prompt()
	if len(p) > Budget*2 {
		t.Fatalf("prompt is %d chars, well past the %d budget", len(p), Budget)
	}
	if !strings.Contains(p, "not shown") {
		t.Fatalf("dropped memories must be admitted:\n%s", p[:200])
	}
}

// The prompt has to say these notes are stale-able, or a session trusts a note
// over the file in front of it.
func TestPromptWarnsThatMemoriesAge(t *testing.T) {
	st := open(t)
	must(t, st.Write("a-fact", "the build needs the sqlite tag"))
	p := st.Prompt()
	if !strings.Contains(p, "still exists") {
		t.Fatalf("the prompt should tell the model to verify before relying:\n%s", p)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
