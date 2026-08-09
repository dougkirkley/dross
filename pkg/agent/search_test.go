package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// searchWorkspace seeds a tree with the shape that makes searching hard: files
// below the root, and a symbol whose case is not what you would guess.
func searchWorkspace(t *testing.T) *workspace {
	t.Helper()
	dir := t.TempDir()
	for p, content := range map[string]string{
		"main.go":                   "package main\n\nfunc main() { limiter.Apply(3) }\n",
		"internal/limit/limiter.go": "package limit\n\n// Clamp bounds v.\nfunc Clamp(v, lo, hi int) int { return v }\n",
		"internal/limit/doc.go":     "package limit\n\n// HTTPTimeout is the deadline.\nconst HTTPTimeout = 30\n",
		"docs/notes.md":             "the clamp helper lives in internal/limit\n",
	} {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w, err := newWorkspace(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// TestGrepRetriesIgnoringCase is the fix for the search that finds nothing and
// stops the run.
//
// A model looking for a symbol it half-remembers gets the case wrong, is told
// "[no matches]", and concludes the thing does not exist. The results were
// there the whole time.
func TestGrepRetriesIgnoringCase(t *testing.T) {
	w := searchWorkspace(t)
	out, err := w.grep("CLAMP", grepOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "limiter.go") {
		t.Fatalf("the case-insensitive retry should have found Clamp:\n%s", out)
	}
	// And it says what it did, so the model is not misled about what matched.
	if !strings.Contains(out, "ignoring case") {
		t.Fatalf("the retry must announce itself:\n%s", out)
	}
}

// A search that was already case-insensitive is not retried — there is nothing
// left to try, and the answer is the honest dead end with a suggestion on it.
func TestGrepDeadEndSuggestsSomething(t *testing.T) {
	w := searchWorkspace(t)
	out, err := w.grep("nothinglikethis", grepOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "with or without case") {
		t.Fatalf("a true dead end should say both were tried:\n%s", out)
	}
}

// TestGrepBlamesTheGlobWhenTheGlobIsWrong: the pattern is innocent when nothing
// was searched. A model told only "no matches" rewrites the pattern forever.
func TestGrepBlamesTheGlobWhenTheGlobIsWrong(t *testing.T) {
	w := searchWorkspace(t)
	out, err := w.grep("Clamp", grepOpts{Glob: "**/*.rs"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "matched no files") {
		t.Fatalf("should point at the glob, not the pattern:\n%s", out)
	}
}

// TestGrepGlobMatchesAtAnyDepth: the filter has the same trap the standalone
// glob had. "*.go" as a filter must not silently mean "the root only", or a
// search restricted to Go files searches one file and reports nothing.
func TestGrepGlobMatchesAtAnyDepth(t *testing.T) {
	w := searchWorkspace(t)
	out, err := w.grep("Clamp", grepOpts{Glob: "*.go"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "limiter.go") {
		t.Fatalf("a bare *.go filter should reach nested files:\n%s", out)
	}
}

// TestGrepFallsBackToLiteral: "Rate()" and "map[string]any" are ordinary things
// to search for and neither is valid regexp. Refusing them spends a turn
// teaching the model to escape.
func TestGrepFallsBackToLiteral(t *testing.T) {
	w := searchWorkspace(t)
	out, err := w.grep("main(", grepOpts{})
	if err != nil {
		t.Fatalf("an unparseable pattern should be searched literally, not refused: %v", err)
	}
	if !strings.Contains(out, "main.go") {
		t.Fatalf("literal search missed the match:\n%s", out)
	}
	if !strings.Contains(out, "literally") {
		t.Fatalf("the fallback must say it happened, so a real regexp can be fixed:\n%s", out)
	}
}

// TestGrepIgnoreCaseIsExplicitlyAvailable: the automatic retry is a safety net,
// not the only way in. A model that knows it wants case-insensitivity should be
// able to say so and get clean output with no apology attached.
func TestGrepIgnoreCaseIsExplicitlyAvailable(t *testing.T) {
	w := searchWorkspace(t)
	out, err := w.grep("clamp", grepOpts{IgnoreCase: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "limiter.go") || !strings.Contains(out, "notes.md") {
		t.Fatalf("ignore_case should find both spellings:\n%s", out)
	}
	if strings.Contains(out, "ignoring case") {
		t.Fatalf("an asked-for search should not be reported as a retry:\n%s", out)
	}
}

func TestAnyDepth(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"*.go", "**/*.go"},
		{"*_test.go", "**/*_test.go"},
		{"cmd/*.go", "cmd/*.go"},   // a slash means a path, and is left alone
		{"**/*.go", "**/*.go"},     // already explicit
		{"./*.go", "*.go"},         // the root, and only the root
		{"./cmd/*.go", "cmd/*.go"}, // anchored, still a path
		{"", ""},
	} {
		if got := anyDepth(tc.in); got != tc.want {
			t.Errorf("anyDepth(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestSearchHintFiresOnceForATreeReader is the turn-count gap, reproduced.
//
// A traced find-in-a-tree run opened 28 files in eight batched reads to find
// the one component whose Rate() disagreed with its doc comment. Two greps
// would have answered it. That run took 15 turns against Claude Code's 7, and
// almost all of the difference was reading a tree it could have searched.
func TestSearchHintFiresOnceForATreeReader(t *testing.T) {
	ag, err := New(Config{Name: "t", Model: "m", Workdir: t.TempDir()}, &scriptedProvider{})
	if err != nil {
		t.Fatal(err)
	}
	// Under the threshold: nothing said, because opening a package to read it
	// is ordinary and this must not nag.
	if got := ag.searchHint(6); got != "" {
		t.Fatalf("hinted at 6 files: %q", got)
	}
	// Crossing it: said once, with the count and the alternative.
	got := ag.searchHint(6)
	if !strings.Contains(got, "grep") || !strings.Contains(got, "12 files") {
		t.Fatalf("the hint should name the count and the tool: %q", got)
	}
	// And never again — a run already deep in a tree does not need telling
	// every turn.
	if again := ag.searchHint(6); again != "" {
		t.Fatalf("hinted twice: %q", again)
	}
}

// A run that has already searched is doing the right thing. Reading the hits a
// grep returned is not tree-enumeration, and interrupting it would be advice to
// do what it just did.
func TestSearchHintStaysQuietOnceARunHasSearched(t *testing.T) {
	ag, err := New(Config{Name: "t", Model: "m", Workdir: t.TempDir()}, &scriptedProvider{})
	if err != nil {
		t.Fatal(err)
	}
	ag.searched = true
	if got := ag.searchHint(50); got != "" {
		t.Fatalf("hinted at a run that had already grepped: %q", got)
	}
}

// TestBatchingHintCatchesHandfuls is the bug two screenshots made obvious.
//
// The hint required CONSECUTIVE single-file reads and cleared its count the
// moment a call passed two paths. A traced session read 1, 1, 2, 1, 2, 1, 3, 1
// files across fifteen turns and was never hinted once: every two-file read
// counted as batching. Two of a possible twenty is not batching.
func TestBatchingHintCatchesHandfuls(t *testing.T) {
	ag, err := New(Config{Name: "t", Model: "m", Workdir: t.TempDir()}, &scriptedProvider{})
	if err != nil {
		t.Fatal(err)
	}
	one := toolCall("1", "read", map[string]any{"path": "a.go"})
	two := toolCall("2", "read", map[string]any{"paths": []string{"a.go", "b.go"}})

	// The exact pattern from the trace. Under the old rule this hinted never.
	if got := ag.batchingHint(one); got != "" {
		t.Fatalf("hinted on the first call: %q", got)
	}
	if got := ag.batchingHint(one); got != "" {
		t.Fatalf("hinted on the second call: %q", got)
	}
	got := ag.batchingHint(two)
	if !strings.Contains(got, "read calls") || !strings.Contains(got, "per call") {
		t.Fatalf("a run averaging %v files a call should be hinted, got %q", 4.0/3.0, got)
	}
	// Once only — nagging every turn teaches nothing the first one did not.
	if again := ag.batchingHint(one); again != "" {
		t.Fatalf("hinted twice: %q", again)
	}
}

// A run that asks for a real batch is doing the right thing and must not be
// lectured about it, however many calls it makes.
func TestBatchingHintLeavesRealBatchesAlone(t *testing.T) {
	ag, err := New(Config{Name: "t", Model: "m", Workdir: t.TempDir()}, &scriptedProvider{})
	if err != nil {
		t.Fatal(err)
	}
	big := toolCall("1", "read", map[string]any{
		"paths": []string{"a.go", "b.go", "c.go", "d.go", "e.go", "f.go"}})
	for i := 0; i < 5; i++ {
		if got := ag.batchingHint(big); got != "" {
			t.Fatalf("hinted at a run reading six at a time: %q", got)
		}
	}
}
