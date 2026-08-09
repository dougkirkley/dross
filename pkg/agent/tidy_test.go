package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTidyFormatsWhatTheAgentWrote is the fix for turns spent on whitespace.
//
// A small model writes Go at the wrong indentation and then edits it again to
// straighten it out. One observed run reached correct logic and spent eleven
// turns and twelve minutes on the shape of it. gofmt is deterministic and takes
// microseconds, so the model never has to spend a round trip on this.
func TestTidyFormatsWhatTheAgentWrote(t *testing.T) {
	w, dir := ws(t, map[string]string{})
	report, err := w.writeFile("mangled.go", "package x\n\nfunc F() int {\nreturn 1\n\t\t}\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report, "reformatted") {
		t.Errorf("the model should be told its file was tidied, got %q", report)
	}
	got, err := os.ReadFile(filepath.Join(dir, "mangled.go"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "package x\n\nfunc F() int {\n\treturn 1\n}\n"; string(got) != want {
		t.Fatalf("file on disk:\n%q\nwant:\n%q", got, want)
	}
}

// Already-tidy code is left alone and says nothing — a note on every write
// would be noise, and noise in a tool result is context spent for nothing.
func TestTidySaysNothingWhenNothingChanged(t *testing.T) {
	w, _ := ws(t, map[string]string{})
	report, err := w.writeFile("clean.go", "package x\n\nfunc F() int {\n\treturn 1\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(report, "reformatted") {
		t.Errorf("clean code should not be announced, got %q", report)
	}
}

// TestTidyReportsASyntaxErrorAndKeepsTheFile: the free half of the feature.
//
// gofmt has to parse, so a broken file is caught on the edit that broke it
// rather than whenever a build next happens. The file must survive exactly as
// written — silently reverting an edit the model believes it made is a worse
// turn than an unformatted file.
func TestTidyReportsASyntaxErrorAndKeepsTheFile(t *testing.T) {
	w, dir := ws(t, map[string]string{})
	broken := "package x\n\nfunc F() int {\n\treturn 1\n"
	report, err := w.writeFile("broken.go", broken)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report, "does not parse") {
		t.Errorf("a syntax error should be reported at once, got %q", report)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "broken.go"))
	if string(got) != broken {
		t.Fatalf("the file was altered despite not parsing:\n%q", got)
	}
}

// Only Go. Formatting someone's YAML or Markdown with a Go formatter would be
// vandalism, and there is no reason to read the file to find that out.
func TestTidyLeavesOtherLanguagesAlone(t *testing.T) {
	w, dir := ws(t, map[string]string{})
	yaml := "a:   1\n   b: 2\n"
	report, err := w.writeFile("conf.yaml", yaml)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(report, "reformatted") || strings.Contains(report, "does not parse") {
		t.Errorf("non-Go files should be untouched, got %q", report)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "conf.yaml"))
	if string(got) != yaml {
		t.Fatalf("yaml was rewritten: %q", got)
	}
}

// The edit path gets it too — that is where the churn was actually observed,
// since the model edits far more often than it writes whole files.
func TestTidyRunsAfterAnEdit(t *testing.T) {
	w, dir := ws(t, map[string]string{
		"a.go": "package x\n\nfunc F() int {\n\treturn 1\n}\n",
	})
	report, err := w.editFile("a.go", "\treturn 1", "        return 2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report, "reformatted") {
		t.Errorf("an edit that mis-indents should be tidied, got %q", report)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "a.go"))
	if want := "package x\n\nfunc F() int {\n\treturn 2\n}\n"; string(got) != want {
		t.Fatalf("after edit:\n%q\nwant:\n%q", got, want)
	}
}
