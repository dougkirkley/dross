package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A session started on a repository used to open blind: its first moves were
// ls -R, a glob and a read of main.go, several turns spent working out where it
// was. Asked what to do with "this repo", it could only ask back.
func TestPromptSaysWhatTheWorkspaceIs(t *testing.T) {
	p := systemPrompt(Config{
		Workdir: t.TempDir(),
		Repo:    "dougkirkley/dross",
		Branch:  "dross/olive-juniper",
	}, false, false, nil)

	for _, want := range []string{"dougkirkley/dross", "dross/olive-juniper"} {
		if !strings.Contains(p, want) {
			t.Errorf("the prompt never mentions %q", want)
		}
	}
	// Asked to build the project it was already standing in, a model answered
	// with `git clone` and a placeholder URL.
	if !strings.Contains(p, "never clone it") {
		t.Error("nothing tells it the code is already there")
	}
}

// A session with no repository behind it says nothing about one, rather than
// inventing a workspace section with a blank in it.
func TestPromptWithoutARepositorySaysNothing(t *testing.T) {
	p := systemPrompt(Config{Workdir: t.TempDir()}, false, false, nil)
	if strings.Contains(p, "This workspace") {
		t.Errorf("invented a workspace section:\n%s", p)
	}
}

// Telling a session to read the README is not the same as it having read one.
// A model that opens by asking what the project is has already spent the turn
// that mattered.
func TestPromptQuotesTheProjectsOwnDocs(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "README.md", "# Dross\n\nA coding agent you can drive from a phone.")
	write(t, dir, "AGENTS.md", "Run `go test ./...` before committing.")

	p := systemPrompt(Config{Workdir: dir, Repo: "doug/dross"}, false, false, nil)

	if !strings.Contains(p, "A coding agent you can drive from a phone.") {
		t.Error("the README is not in the prompt")
	}
	if !strings.Contains(p, "Run `go test ./...` before committing.") {
		t.Error("AGENTS.md is not in the prompt")
	}
}

// A README can be a book, and a system prompt is not the place for one. What
// earns its place is the top of it, cut on a line so the quote does not read as
// though the file itself stops mid-sentence.
func TestLongDocsAreCutAndSaidToBeCut(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "README.md", "# Big\n\n"+strings.Repeat("a line of documentation\n", 2000))

	p := systemPrompt(Config{Workdir: dir}, false, false, nil)

	if len(p) > 20000 {
		t.Errorf("the prompt grew to %d characters", len(p))
	}
	if !strings.Contains(p, "truncated") {
		t.Error("the quote was cut without saying so")
	}
	if strings.Contains(p, "a line of documentation\na line of documentation\nx") {
		t.Error("cut mid-line")
	}
}

// A workspace with nothing to say adds nothing.
func TestNoDocsAddNothing(t *testing.T) {
	p := systemPrompt(Config{Workdir: t.TempDir()}, false, false, nil)
	if strings.Contains(p, "explains itself") {
		t.Error("announced documents that are not there")
	}
}

// A chat session has no workspace at all, so neither half applies: it would be
// describing a directory it cannot read.
func TestChatPromptStaysBare(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "README.md", "secret project notes")

	p := systemPrompt(Config{Workdir: dir, Repo: "doug/dross", Chat: true}, false, false, nil)

	if strings.Contains(p, "secret project notes") || strings.Contains(p, "doug/dross") {
		t.Error("a chat session was told about a workspace it has no access to")
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Asked to install the project it was standing in, a model answered with a
// three-column table comparing ways it could be installed. "Do it" has one
// answer and it is not a list of options.
func TestPromptSaysToActOnTheProject(t *testing.T) {
	p := systemPrompt(Config{Workdir: t.TempDir(), Repo: "doug/dross"}, false, false, nil)
	if !strings.Contains(p, `is not an answer to "do it"`) {
		t.Error("nothing tells it to act on the project it is in")
	}
}

// The same answer's table cells held the literal characters "<br>", because
// nothing downstream renders HTML — and a table on a phone is three words wide.
func TestPromptRulesOutHTMLAndTables(t *testing.T) {
	p := systemPrompt(Config{Workdir: t.TempDir()}, false, false, nil)
	if !strings.Contains(p, "no HTML") {
		t.Error("nothing rules out HTML")
	}
	if !strings.Contains(p, "tables") {
		t.Error("nothing steers away from tables")
	}
}
