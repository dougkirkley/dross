package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ws(t *testing.T, files map[string]string) (*workspace, string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w, err := newWorkspace(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	return w, dir
}

const limiter = `package bench

// Clamp returns v limited to the range [lo, hi].
func Clamp(v, lo, hi int) int {
	if v > hi {
		return hi
	}
	return v
}

func use(v int) int { return Clamp(v, 1, 10) }
`

// TestReplaceAllRenames is the case that cost the benchmark a handful of turns:
// a rename hits several sites, the natural old_str is the bare name, and
// requiring uniqueness turned one intent into an error plus a retry per site.
func TestReplaceAllRenames(t *testing.T) {
	w, dir := ws(t, map[string]string{"limiter.go": limiter})

	out, err := w.applyEdits("limiter.go", []edit{{OldStr: "Clamp", NewStr: "Bound", ReplaceAll: true}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "3 replacements") {
		t.Errorf("should report every replacement: %q", out)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "limiter.go"))
	if strings.Contains(string(got), "Clamp") {
		t.Errorf("Clamp survived:\n%s", got)
	}
	if n := strings.Count(string(got), "Bound"); n != 3 {
		t.Errorf("got %d Bound, want 3:\n%s", n, got)
	}
}

// TestAmbiguousEditNamesTheSites: without this the model re-reads the whole
// file to build a unique old_str — a turn spent recovering what this call
// already knew.
func TestAmbiguousEditNamesTheSites(t *testing.T) {
	w, _ := ws(t, map[string]string{"limiter.go": limiter})

	_, err := w.applyEdits("limiter.go", []edit{{OldStr: "Clamp", NewStr: "Bound"}})
	if err == nil {
		t.Fatal("want an error for an ambiguous edit")
	}
	msg := err.Error()
	if !strings.Contains(msg, "replace_all") {
		t.Errorf("error should offer replace_all: %s", msg)
	}
	for _, want := range []string{"limiter.go:3", "limiter.go:4", "limiter.go:11"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error should name the site %s:\n%s", want, msg)
		}
	}
}

// TestIndentationMismatchStillApplies is the failure this was watched doing on
// a real run: read separates the line number from the content with a tab, Go
// code is indented with tabs, and a model reconstructing that boundary guesses
// wrong. Refusing the edit is technically correct and useless — the intent was
// unambiguous, so it lands, in the file's own indentation.
func TestIndentationMismatchStillApplies(t *testing.T) {
	w, dir := ws(t, map[string]string{"limiter.go": limiter})

	// Right text, wrong indentation — spaces where the file has tabs.
	out, err := w.applyEdits("limiter.go", []edit{{OldStr: "    return hi", NewStr: "    return lo"}})
	if err != nil {
		t.Fatalf("an unambiguous edit should land despite the indentation: %v", err)
	}
	if !strings.Contains(out, "ignoring indentation") {
		t.Errorf("an adjusted edit must say so rather than land silently: %q", out)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "limiter.go"))
	if !strings.Contains(string(got), "\t\treturn lo") {
		t.Errorf("the file's own indentation should be kept, not the model's guess:\n%q", got)
	}
	if strings.Contains(string(got), "    return lo") {
		t.Errorf("the model's spaces leaked into a tab-indented file:\n%q", got)
	}
}

// TestMultiLineIndentationMismatch is the exact shape of the observed failure:
// a three-line old_str where the model dropped the leading tab on the first and
// last lines but kept it on the middle one.
func TestMultiLineIndentationMismatch(t *testing.T) {
	const src = "package bench\n\nfunc Clamp(v, lo, hi int) int {\n\tif v > hi {\n\t\treturn hi\n\t}\n\tif v < lo {\n\t\treturn hi\n\t}\n\treturn v\n}\n"
	w, dir := ws(t, map[string]string{"util.go": src})

	_, err := w.applyEdits("util.go", []edit{{
		OldStr: "if v < lo {\n\t\treturn hi\n}",
		NewStr: "if v < lo {\n\t\treturn lo\n}",
	}})
	if err != nil {
		t.Fatalf("should have applied: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "util.go"))
	want := "package bench\n\nfunc Clamp(v, lo, hi int) int {\n\tif v > hi {\n\t\treturn hi\n\t}\n\tif v < lo {\n\t\treturn lo\n\t}\n\treturn v\n}\n"
	if string(got) != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

// TestLooseMatchRefusesAmbiguity: forgiving indentation must not become
// guessing which of two places was meant.
func TestLooseMatchRefusesAmbiguity(t *testing.T) {
	const src = "package bench\n\nfunc a() int {\n\treturn hi\n}\n\nfunc b() int {\n\treturn hi\n}\n"
	w, _ := ws(t, map[string]string{"util.go": src})

	_, err := w.applyEdits("util.go", []edit{{OldStr: "return hi", NewStr: "return lo"}})
	if err == nil {
		t.Fatal("two candidate sites is not unambiguous; want an error")
	}
}

// TestNoMatchQuotesTheWholeRegion: quoting one line of a three-line old_str
// leaves the model reconstructing the other two from the memory that already
// failed it.
func TestNoMatchQuotesTheWholeRegion(t *testing.T) {
	const src = "package bench\n\nfunc a() int {\n\tif x {\n\t\treturn 1\n\t}\n\treturn 2\n}\n"
	w, _ := ws(t, map[string]string{"util.go": src})

	// Matches nothing loosely (the middle line differs), so it must fail well.
	_, err := w.applyEdits("util.go", []edit{{
		OldStr: "if x {\n\t\treturn 9\n}",
		NewStr: "if x {\n\t\treturn 3\n}",
	}})
	if err == nil {
		t.Fatal("want an error")
	}
	msg := err.Error()
	// The whole region, so the model can see the line it got wrong (return 1,
	// not return 9) instead of guessing again.
	for _, want := range []string{"line 4", "\tif x {", "\t\treturn 1", "\t}"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message should quote the real region, missing %q:\n%s", want, msg)
		}
	}
}

// TestBatchEditIsAllOrNothing: a half-applied batch leaves the file in a state
// nobody asked for and the model reasoning about a diff it did not make.
func TestBatchEditIsAllOrNothing(t *testing.T) {
	w, dir := ws(t, map[string]string{"limiter.go": limiter})

	_, err := w.applyEdits("limiter.go", []edit{
		{OldStr: "return hi", NewStr: "return lo", ReplaceAll: true},
		{OldStr: "this text is not in the file", NewStr: "x"},
	})
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "nothing was written") {
		t.Errorf("error should say the batch was abandoned: %s", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "limiter.go"))
	if string(got) != limiter {
		t.Errorf("the first edit leaked despite the batch failing:\n%s", got)
	}
}

// TestBatchEditApplies collapses several changes to one file into one call.
func TestBatchEditApplies(t *testing.T) {
	w, dir := ws(t, map[string]string{"limiter.go": limiter})

	out, err := w.applyEdits("limiter.go", []edit{
		{OldStr: "Clamp", NewStr: "Bound", ReplaceAll: true},
		{OldStr: "\t\treturn hi", NewStr: "\t\treturn lo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "4 replacements") {
		t.Errorf("out = %q", out)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "limiter.go"))
	if strings.Contains(string(got), "Clamp") || !strings.Contains(string(got), "return lo") {
		t.Errorf("batch did not apply in order:\n%s", got)
	}
}

// TestEditReturnsTheResult removes the model's reason to re-read the file to
// confirm what it just did.
func TestEditReturnsTheResult(t *testing.T) {
	w, _ := ws(t, map[string]string{"limiter.go": limiter})

	out, err := w.applyEdits("limiter.go", []edit{{OldStr: "\t\treturn hi", NewStr: "\t\treturn lo"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "return lo") {
		t.Errorf("edit should echo the changed region: %q", out)
	}
	if !strings.Contains(out, "     6\t") {
		t.Errorf("echoed region should be line-numbered: %q", out)
	}
	// Bounded: a one-line edit must not reprint the file.
	if strings.Count(out, "\n") > 20 {
		t.Errorf("echoed region is not bounded: %q", out)
	}
}

// TestEditRejectsNoOps: an edit that changes nothing used to succeed and report
// a replacement, which reads to the model as progress.
func TestEditRejectsNoOps(t *testing.T) {
	w, _ := ws(t, map[string]string{"limiter.go": limiter})

	if _, err := w.applyEdits("limiter.go", []edit{{OldStr: "return v", NewStr: "return v"}}); err == nil {
		t.Error("an edit identical to what it replaces should be an error")
	}
	if _, err := w.applyEdits("limiter.go", []edit{{OldStr: "", NewStr: "x"}}); err == nil {
		t.Error("an empty old_str should be an error")
	}
}

// TestEditFileStillWorks: the single-edit entry point is unchanged for callers.
func TestEditFileStillWorks(t *testing.T) {
	w, dir := ws(t, map[string]string{"a.txt": "hello world\n"})
	if _, err := w.editFile("a.txt", "world", "there"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(got) != "hello there\n" {
		t.Fatalf("got %q", got)
	}
}
