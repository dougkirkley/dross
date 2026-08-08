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
