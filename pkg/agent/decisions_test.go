package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dougkirkley/dross/pkg/provider"
)

const clampFile = "package bench\n\n" +
	"// Clamp returns v limited to the range [lo, hi].\n" +
	"func Clamp(v, lo, hi int) int {\n" +
	"\tif v > hi {\n\t\treturn hi\n\t}\n" +
	"\tif v < lo {\n\t\treturn hi\n\t}\n" +
	"\treturn v\n}\n"

// An ambiguous old_str used to be answered with line numbers and the advice to
// "add surrounding context", which is a description of work rather than the
// work. Traced on find-the-bug with gpt-oss:20b: it re-read the file, re-read
// it a second time identically, then sent an edits array whose first entry
// replaced "return hi" with itself — four turns to build a string this call
// already had every byte of.
func TestAmbiguousEditOffersSomethingToPaste(t *testing.T) {
	msg := ambiguousMessage("util.go", clampFile, "return hi", 2, 0, 1)

	// Each candidate must be usable as old_str exactly as printed: present in
	// the file, and unique in it.
	for _, want := range []string{"\tif v > hi {\n\t\treturn hi", "\tif v < lo {\n\t\treturn hi"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message should offer %q:\n%s", want, msg)
		}
		if n := strings.Count(clampFile, want); n != 1 {
			t.Errorf("the offered old_str %q matches %d times, so it does not disambiguate", want, n)
		}
	}
	if !strings.Contains(msg, "replace_all") {
		t.Error("changing every site is still the other answer, and should still be offered")
	}
}

// A file that genuinely repeats itself has nothing unique to offer, and saying
// so beats printing windows that do not disambiguate.
func TestAmbiguousEditFallsBackToNamingSites(t *testing.T) {
	same := strings.Repeat("x\n", 40)
	msg := ambiguousMessage("f.txt", same, "x", 40, 0, 1)
	if !strings.Contains(msg, "matches at:") {
		t.Errorf("with no unique window it should name the sites:\n%s", msg)
	}
}

// uniqueWindow grows upward first: the line above a match is usually what tells
// two otherwise identical lines apart, and a window that reaches forward
// instead is bigger for no gain.
func TestUniqueWindowGrowsUpwardFirst(t *testing.T) {
	got, ok := uniqueWindow(clampFile, strings.Index(clampFile, "return hi"),
		strings.Index(clampFile, "return hi")+len("return hi"))
	if !ok {
		t.Fatal("this file has a unique window for the first match")
	}
	if !strings.HasSuffix(got, "return hi") {
		t.Errorf("the window should end at the match, not run past it: %q", got)
	}
	if !strings.Contains(got, "if v > hi") {
		t.Errorf("the window should have grown upward to the distinguishing line: %q", got)
	}
}

// A window that has swallowed the whole file is not something to paste into
// old_str; replace_all is the better answer and the caller falls back to it.
func TestUniqueWindowGivesUpOnAFileOfRepeats(t *testing.T) {
	same := strings.Repeat("x\n", 200)
	if _, ok := uniqueWindow(same, 0, 1); ok {
		t.Error("nothing in a file of identical lines is unique")
	}
}

// The other waste both models showed: fetching the same unchanged thing twice.
// gpt-oss:20b read one file in consecutive turns, and on the rename grepped for
// the same symbol at turn 1 and again at turn 4.
func TestRepeatedLookupSaysItHasNotChanged(t *testing.T) {
	a := &Agent{}
	tc := provider.ToolCall{Name: "read", Input: json.RawMessage(`{"path":"util.go"}`)}

	if note := a.repeatNote(tc, clampFile); note != "" {
		t.Errorf("the first call is not a repeat: %q", note)
	}
	note := a.repeatNote(tc, clampFile)
	if note == "" {
		t.Fatal("an identical result to an identical call should say so")
	}
	if !strings.Contains(note, "nothing has changed") {
		t.Errorf("the note should say why it is safe to reuse: %q", note)
	}
}

// Byte-identical output is the whole proof. When the answer differs the file
// has changed underneath, and saying "nothing has changed" would be a lie that
// stops the model reading the new content.
func TestChangedResultIsNotReportedAsUnchanged(t *testing.T) {
	a := &Agent{}
	tc := provider.ToolCall{Name: "read", Input: json.RawMessage(`{"path":"util.go"}`)}
	a.repeatNote(tc, clampFile)
	if note := a.repeatNote(tc, strings.Replace(clampFile, "return hi", "return lo", 1)); note != "" {
		t.Errorf("an edited file must not be reported as unchanged: %q", note)
	}
	// ...and the new content becomes the thing a later repeat is compared to.
	edited := strings.Replace(clampFile, "return hi", "return lo", 1)
	a.repeatNote(tc, edited)
	if note := a.repeatNote(tc, edited); note == "" {
		t.Error("a repeat of the new content is still a repeat")
	}
}

// Different arguments are a different question, however similar the answer.
func TestDifferentArgumentsAreNotRepeats(t *testing.T) {
	a := &Agent{}
	a.repeatNote(provider.ToolCall{Name: "read", Input: json.RawMessage(`{"path":"a.go"}`)}, "same bytes")
	if note := a.repeatNote(provider.ToolCall{Name: "read", Input: json.RawMessage(`{"path":"b.go"}`)}, "same bytes"); note != "" {
		t.Errorf("two files that happen to match are not a repeated call: %q", note)
	}
}

// A write is not a look-up: repeating one does something, and telling a model
// its second write was redundant would be wrong.
func TestOnlyLookupsAreCheckedForRepeats(t *testing.T) {
	a := &Agent{}
	tc := provider.ToolCall{Name: "bash", Input: json.RawMessage(`{"command":"go test ./..."}`)}
	a.repeatNote(tc, "ok  bench 0.16s")
	if note := a.repeatNote(tc, "ok  bench 0.16s"); note != "" {
		t.Errorf("running the tests twice is a real thing to do: %q", note)
	}
}

// A rename is one change to a codebase and several changes to files, and the
// tool could only say the second thing: every file cost its own turn. Traced on
// rename-symbol, gpt-oss:20b spent turns 5, 6 and 9 on three edits that were
// one intention.
func TestOneEditCallChangesSeveralFiles(t *testing.T) {
	w, _ := ws(t, map[string]string{
		"util.go":    "package bench\n\n// Clamp bounds v.\nfunc Clamp(v int) int { return v }\n",
		"limiter.go": "package bench\n\nfunc Apply(v int) int { return Clamp(v) }\n",
	})

	out, err := w.applyEditsAcross("", []edit{
		{Path: "util.go", OldStr: "// Clamp bounds v.\nfunc Clamp(", NewStr: "// Bound bounds v.\nfunc Bound("},
		{Path: "limiter.go", OldStr: "return Clamp(v)", NewStr: "return Bound(v)"},
	})
	if err != nil {
		t.Fatalf("a two-file rename should apply: %v", err)
	}
	for _, want := range []string{"util.go", "limiter.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report should name %s: %s", want, out)
		}
	}
	for path, want := range map[string]string{"util.go": "func Bound(", "limiter.go": "return Bound(v)"} {
		got, err := w.rawFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, want) {
			t.Errorf("%s = %q, want it to contain %q", path, got, want)
		}
	}
}

// All or nothing across every file. A half-applied rename does not compile, and
// leaving one file behind is worse than refusing: a refusal is visible, whereas
// a codebase that builds nowhere costs a turn to diagnose.
func TestAFailedEntryWritesNothingAnywhere(t *testing.T) {
	w, _ := ws(t, map[string]string{
		"a.go": "package a\n\nfunc Old() {}\n",
		"b.go": "package a\n\nfunc Other() {}\n",
	})
	before, _ := w.rawFile("a.go")

	_, err := w.applyEditsAcross("", []edit{
		{Path: "a.go", OldStr: "func Old()", NewStr: "func New()"},
		{Path: "b.go", OldStr: "func NotThere()", NewStr: "func Nope()"},
	})
	if err == nil {
		t.Fatal("an entry that cannot apply should fail the batch")
	}
	if got, _ := w.rawFile("a.go"); got != before {
		t.Errorf("the file that would have applied was written anyway:\n%s", got)
	}
	if !strings.Contains(err.Error(), "nothing was written") {
		t.Errorf("the error should say the batch was abandoned: %v", err)
	}
}

// The single-file form is untouched: entries without a path use the call's.
func TestEntriesWithoutAPathUseTheCallsPath(t *testing.T) {
	w, _ := ws(t, map[string]string{"a.go": "package a\n\nvar X = 1\nvar Y = 2\n"})
	out, err := w.applyEditsAcross("a.go", []edit{
		{OldStr: "var X = 1", NewStr: "var X = 10"},
		{OldStr: "var Y = 2", NewStr: "var Y = 20"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "2 replacements") {
		t.Errorf("both should have applied to the one file: %s", out)
	}
}

// A batch with no path at all is a call that cannot be run, and saying which
// of the two ways to give one is faster than a bare rejection.
func TestAnEditWithNoPathSaysHowToGiveOne(t *testing.T) {
	w, _ := ws(t, map[string]string{"a.go": "package a\n"})
	_, err := w.applyEditsAcross("", []edit{{OldStr: "package a", NewStr: "package b"}})
	if err == nil {
		t.Fatal("there is no file to edit")
	}
	if !strings.Contains(err.Error(), "path") {
		t.Errorf("the error should name what is missing: %v", err)
	}
}

// editedPaths is what lets the caller emit one edit event per file, so a client
// showing changed files sees a three-file rename as three edits. It has to
// inherit paths the same way applyEditsAcross does, or the two disagree about
// which files a batch touched and an edit event goes missing.
func TestEditedPathsListsEachFileOnce(t *testing.T) {
	got := editedPaths("fallback.go", []edit{
		{Path: "a.go"}, {}, {Path: "b.go"}, {Path: "a.go"},
	})
	// The bare entry continues with a.go, the file before it — not the call's
	// path, which only applies until an entry names one.
	want := []string{"a.go", "b.go"}
	if len(got) != len(want) {
		t.Fatalf("editedPaths = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("editedPaths = %v, want %v (first-seen order)", got, want)
		}
	}
}

// A batch that names a file once and then keeps editing it is how the list gets
// written. Traced on rename-symbol: gpt-oss:20b put a path on the first entries
// and left it off a later one, and rejecting that cost a turn to be told
// something the batch already implied.
func TestAnEntryWithoutAPathStaysOnTheSameFile(t *testing.T) {
	w, _ := ws(t, map[string]string{
		"util.go":    "package a\n\n// Clamp bounds.\nfunc Clamp(v int) int { return v }\n",
		"limiter.go": "package a\n\nfunc Apply(v int) int { return Clamp(v) }\n",
	})
	out, err := w.applyEditsAcross("", []edit{
		{Path: "util.go", OldStr: "// Clamp bounds.", NewStr: "// Bound bounds."},
		{OldStr: "func Clamp(", NewStr: "func Bound("}, // still util.go
		{Path: "limiter.go", OldStr: "return Clamp(v)", NewStr: "return Bound(v)"},
	})
	if err != nil {
		t.Fatalf("the second entry should stay on util.go: %v", err)
	}
	got, _ := w.rawFile("util.go")
	if !strings.Contains(got, "// Bound bounds.") || !strings.Contains(got, "func Bound(") {
		t.Errorf("both util.go edits should have applied:\n%s", got)
	}
	if got, _ := w.rawFile("limiter.go"); !strings.Contains(got, "return Bound(v)") {
		t.Errorf("limiter.go should have been edited too:\n%s", got)
	}
	if !strings.Contains(out, "util.go") || !strings.Contains(out, "limiter.go") {
		t.Errorf("the report should name both files: %s", out)
	}
}

// With nothing before it and no call path, there is genuinely no file to edit,
// and the error says which of the two ways to name one.
func TestAFirstEntryWithNoPathStillFails(t *testing.T) {
	w, _ := ws(t, map[string]string{"a.go": "package a\n"})
	_, err := w.applyEditsAcross("", []edit{{OldStr: "package a", NewStr: "package b"}})
	if err == nil {
		t.Fatal("there is no file to edit")
	}
	if !strings.Contains(err.Error(), "no earlier entry named one") {
		t.Errorf("the error should say why there is no path to inherit: %v", err)
	}
}
