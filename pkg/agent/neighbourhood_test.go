package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dougkirkley/dross/pkg/provider"
)

func seedDir(t *testing.T, files map[string]string) *workspace {
	t.Helper()
	dir := t.TempDir()
	for p, body := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w, err := newWorkspace(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

const themeGo = `package ui

import "fmt"

// Palette is the colour set.
type Palette struct {
	Fg string
	Bg string
}

func Theme(name string) Palette {
	return Palette{}
}
`

// TestNeighbourhoodSendsShapeNotContents is the redesign.
//
// The first version sent whole sibling files and was wrong twice: an 8 KB
// per-file ceiling excluded every real neighbour in this repository, and sizing
// it to fit them would have put ~25k tokens of unrequested content into a
// context that was already full. Declarations came to 4.3% of the bytes across
// ten real files — the part a model needs from a package it has not opened.
func TestNeighbourhoodSendsShapeNotContents(t *testing.T) {
	w := seedDir(t, map[string]string{
		"ui/list.go":  "package ui\n",
		"ui/theme.go": themeGo,
	})
	got := w.neighbourhood([]string{"ui/list.go"}, nil)

	if !strings.Contains(got, "type Palette struct {") {
		t.Errorf("the declaration is missing:\n%s", got)
	}
	if !strings.Contains(got, "func Theme(name string) Palette {") {
		t.Errorf("the function signature is missing:\n%s", got)
	}
	// The body is what makes this cheap to leave out.
	if strings.Contains(got, "return Palette{}") {
		t.Errorf("a function body leaked into the outline:\n%s", got)
	}
	if strings.Contains(got, "Fg string") {
		t.Errorf("indented struct fields are not top-level declarations:\n%s", got)
	}
	// Comments are prose about the code, not the shape of it.
	if strings.Contains(got, "// Palette is the colour set.") {
		t.Errorf("a comment leaked into the outline:\n%s", got)
	}
	// And it says what it is, so the model does not mistake an outline for a file.
	if !strings.Contains(got, "declarations only") || !strings.Contains(got, "Read one for its body") {
		t.Errorf("the block should say what it is and what to do next:\n%s", got)
	}
}

// A model that asked for several files is already doing the thing this exists
// to encourage and gets exactly what it asked for.
func TestNeighbourhoodLeavesRealBatchesAlone(t *testing.T) {
	w := seedDir(t, map[string]string{
		"ui/a.go": "package ui\n", "ui/b.go": themeGo, "ui/c.go": themeGo,
	})
	if got := w.neighbourhood([]string{"ui/a.go", "ui/b.go"}, nil); got != "" {
		t.Fatalf("a multi-file read should get no outline:\n%s", got)
	}
}

// Files already returned are not outlined again — the model has the real thing.
func TestNeighbourhoodSkipsWhatWasAlreadyRead(t *testing.T) {
	w := seedDir(t, map[string]string{
		"ui/a.go": "package ui\n", "ui/b.go": themeGo, "ui/c.go": themeGo,
	})
	got := w.neighbourhood([]string{"ui/a.go"}, map[string]bool{"ui/b.go": true})
	if strings.Contains(got, "ui/b.go") {
		t.Errorf("outlined a file the model already has in full:\n%s", got)
	}
	if !strings.Contains(got, "ui/c.go") {
		t.Errorf("the unseen sibling should still be outlined:\n%s", got)
	}
}

// Only the same file type, and never across directories.
func TestNeighbourhoodStaysInTheDirectoryAndLanguage(t *testing.T) {
	w := seedDir(t, map[string]string{
		"ui/a.go":     "package ui\n",
		"ui/notes.md": "# heading\nprose\n",
		"other/x.go":  themeGo,
	})
	got := w.neighbourhood([]string{"ui/a.go"}, nil)
	if strings.Contains(got, "notes.md") {
		t.Errorf("crossed file types:\n%s", got)
	}
	if strings.Contains(got, "other/x.go") {
		t.Errorf("crossed directories:\n%s", got)
	}
}

// A generated or vendored directory is not a component, and outlining sixty
// files of it would be the context explosion this design exists to avoid.
func TestNeighbourhoodSkipsLargeDirectories(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < neighbourDirMax+5; i++ {
		files[filepath.Join("gen", string(rune('a'+i%26))+string(rune('a'+i/26))+".go")] = themeGo
	}
	w := seedDir(t, files)
	if got := w.neighbourhood([]string{"gen/aa.go"}, nil); got != "" {
		t.Fatalf("a %d-file directory should be skipped:\n%s", neighbourDirMax+5, got)
	}
}

// The budget is the guarantee that this cannot explode a context.
func TestNeighbourhoodIsBounded(t *testing.T) {
	files := map[string]string{"ui/a.go": "package ui\n"}
	big := "package ui\n" + strings.Repeat("func LongEnoughToMatter"+strings.Repeat("x", 80)+"() {}\n", 200)
	for i := 0; i < 20; i++ {
		files[filepath.Join("ui", "f"+string(rune('a'+i))+".go")] = big
	}
	w := seedDir(t, files)
	got := w.neighbourhood([]string{"ui/a.go"}, nil)
	if len(got) > neighbourBudget*2 {
		t.Fatalf("outline is %d chars, want it bounded near %d", len(got), neighbourBudget)
	}
	if n := strings.Count(got, ".go:\n"); n > neighbourFiles {
		t.Fatalf("outlined %d files, cap is %d", n, neighbourFiles)
	}
}

// The heuristic is language-agnostic on purpose: column zero, not a comment,
// not a closing bracket.
func TestDeclarationsAcrossLanguages(t *testing.T) {
	dart := "import 'x.dart';\n\nclass AppState extends ChangeNotifier {\n  void load() {\n    // work\n  }\n}\n"
	got := strings.Join(declarations(dart), "\n")
	if !strings.Contains(got, "class AppState extends ChangeNotifier {") {
		t.Errorf("missed the Dart class:\n%s", got)
	}
	if strings.Contains(got, "void load()") {
		t.Errorf("an indented method is not a top-level declaration:\n%s", got)
	}
	py := "import os\n\ndef main():\n    return 1\n\nclass Thing:\n    pass\n"
	got = strings.Join(declarations(py), "\n")
	for _, want := range []string{"def main():", "class Thing:"} {
		if !strings.Contains(got, want) {
			t.Errorf("missed %q in python:\n%s", want, got)
		}
	}
}

// TestNeighbourhoodSurvivesALargeRead is the bug that made this look broken.
//
// The outline was appended in the tool dispatch, and a large read is elided on
// the way back to the model — so reading tui.go at 137 KB produced a correct
// outline that was then truncated off the end of the result. Measured in a live
// run: four reads, three of them large, one outline delivered. The function was
// right and its output never arrived.
//
// It is appended after compaction now, next to the hints, which is why those
// always survived.
func TestNeighbourhoodSurvivesALargeRead(t *testing.T) {
	huge := "package ui\n" + strings.Repeat("// padding to force elision\n", 8000)
	dir := t.TempDir()
	for name, body := range map[string]string{
		"ui/big.go":   huge,
		"ui/theme.go": themeGo,
	} {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	prov := &scriptedProvider{steps: []provider.Response{
		{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{
			toolCall("1", "read", map[string]any{"path": "ui/big.go"}),
		}},
		{StopReason: provider.StopEndTurn, Text: "done"},
	}}
	ag, err := New(Config{Name: "t", Model: "m", Workdir: dir}, prov)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ag.Run(context.Background(), "read the big one"); err != nil {
		t.Fatal(err)
	}
	// What the MODEL received, not what the event carried. The tool-result
	// event is truncated to 16 KB for the client feed, so on a large read the
	// outline is missing from the event and present in the result — which is
	// how a live trace made this look broken when it was not.
	got := prov.last.seen()
	if !strings.Contains(got, "declarations only") {
		t.Fatal("the outline did not reach the model after a large read")
	}
	if !strings.Contains(got, "func Theme(name string) Palette {") {
		t.Error("the sibling's declarations are missing")
	}
}
