package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceGrepGlob(t *testing.T) {
	dir := t.TempDir()
	must := func(p, content string) {
		full := filepath.Join(dir, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("main.go", "package main\nfunc main() { println(\"hi\") }\n")
	must("cmd/tool/main.go", "package main\n// TODO: wire it\n")
	must("readme.md", "docs\n")

	w, err := newWorkspace(dir, false)
	if err != nil {
		t.Fatal(err)
	}

	// grep
	out, err := w.grep("func main", grepOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "main.go:2:") {
		t.Fatalf("grep missed match:\n%s", out)
	}
	// A search with nothing to find says what to try instead of just "no".
	empty, _ := w.grep("zzz-nope", grepOpts{})
	if !strings.Contains(empty, "no matches") || !strings.Contains(empty, "zzz-nope") {
		t.Fatalf("a dead end should name what was searched for, got %q", empty)
	}

	// glob **/*.go finds both mains, and so does a bare *.go — a slash-less
	// pattern matches at any depth, which is what people mean by it and what
	// Claude Code's equivalent does. "./*.go" is how to ask for the root alone.
	all, _ := w.glob("**/*.go")
	if !strings.Contains(all, "main.go") || !strings.Contains(all, "cmd/tool/main.go") {
		t.Fatalf("glob **/*.go missing files:\n%s", all)
	}
	bare, _ := w.glob("*.go")
	if !strings.Contains(bare, "cmd/tool/main.go") {
		t.Fatalf("a bare *.go should match at any depth:\n%s", bare)
	}
	root, _ := w.glob("./*.go")
	if strings.Contains(root, "cmd/tool/main.go") || !strings.Contains(root, "main.go") {
		t.Fatalf("./*.go should be the root alone:\n%s", root)
	}
}
