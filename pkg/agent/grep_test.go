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

	w, err := newWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}

	// grep
	out, err := w.grep("func main", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "main.go:2:") {
		t.Fatalf("grep missed match:\n%s", out)
	}
	if empty, _ := w.grep("zzz-nope", ""); empty != "[no matches]" {
		t.Fatalf("expected no matches, got %q", empty)
	}

	// glob **/*.go finds both mains; *.go finds only the root one.
	all, _ := w.glob("**/*.go")
	if !strings.Contains(all, "main.go") || !strings.Contains(all, "cmd/tool/main.go") {
		t.Fatalf("glob **/*.go missing files:\n%s", all)
	}
	root, _ := w.glob("*.go")
	if strings.Contains(root, "cmd/tool/main.go") {
		t.Fatalf("*.go should not cross directories:\n%s", root)
	}
}
