package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLooksLikeSlashCommand(t *testing.T) {
	for _, cmd := range []string{"/model", "/help", "/model ollama", "/mdoel"} {
		if !looksLikeSlashCommand(cmd) {
			t.Errorf("%q should be a command", cmd)
		}
	}
	// What a terminal pastes when a file is dragged in. Routed to the command
	// handler it came back as "unknown command: /var/folders/...".
	for _, notCmd := range []string{
		"/var/folders/gd/T/Screenshot.png",
		"/Users/doug/Projects/dross/main.go",
		"/etc/hosts and /etc/passwd",
		"", " ", "/", "look at ./main.go", "no slash at all",
	} {
		if looksLikeSlashCommand(notCmd) {
			t.Errorf("%q should not be a command", notCmd)
		}
	}
}

func TestUnescapeDropped(t *testing.T) {
	dir := t.TempDir()
	spaced := filepath.Join(dir, "a file.txt")
	plain := filepath.Join(dir, "plain.txt")
	for _, f := range []string{spaced, plain} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Escaped, quoted, and several at once — all real files, so all rewritten.
	esc := filepath.Join(dir, `a\ file.txt`)
	if got := unescapeDropped(esc + " "); got != spaced {
		t.Errorf("escaped: got %q, want %q", got, spaced)
	}
	if got := unescapeDropped(`'` + spaced + `'`); got != spaced {
		t.Errorf("quoted: got %q, want %q", got, spaced)
	}
	if got := unescapeDropped(esc + " " + plain); got != spaced+" "+plain {
		t.Errorf("two paths: got %q", got)
	}

	// Prose keeps its punctuation: nothing here names a file that exists, so
	// nothing is rewritten.
	for _, keep := range []string{
		`what does C:\Users\bob do?`,
		`it's fine`,
		`the "quoted" word`,
		filepath.Join(dir, "missing.txt"),
		dir, // a directory is not a dropped file
		"plain text",
		"",
	} {
		if got := unescapeDropped(keep); got != keep {
			t.Errorf("unescapeDropped(%q) = %q, want it untouched", keep, got)
		}
	}
}
