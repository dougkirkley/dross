package claudecode

import (
	"encoding/json"
	"testing"
)

// TestEditKeepsIndentation is the bug this file exists for: an old_str that
// begins with a tab — which is to say nearly every edit inside a Go function —
// used to arrive with the tab stripped, so it never matched the file and the
// agent retried the identical edit until it ran out of turns.
func TestEditKeepsIndentation(t *testing.T) {
	old := "\tif v < lo {\n\t\treturn hi\n\t}"
	new := "\tif v < lo {\n\t\treturn lo\n\t}"
	s := "<function=edit>\n" +
		"<parameter=path>\n util.go \n</parameter>\n" +
		"<parameter=old_str>\n" + old + "\n</parameter>\n" +
		"<parameter=new_str>\n" + new + "\n</parameter>\n" +
		"</function>"

	calls, _ := parseCalls(s, nil)
	if len(calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(calls))
	}
	var got map[string]string
	if err := json.Unmarshal(calls[0].Input, &got); err != nil {
		t.Fatal(err)
	}
	if got["old_str"] != old {
		t.Errorf("old_str = %q, want %q", got["old_str"], old)
	}
	if got["new_str"] != new {
		t.Errorf("new_str = %q, want %q", got["new_str"], new)
	}
	// A path is not whitespace-significant, so it still comes back clean.
	if got["path"] != "util.go" {
		t.Errorf("path = %q, want %q", got["path"], "util.go")
	}
}

// TestWriteKeepsTrailingNewline: a Go file without its final newline is a file
// gofmt will rewrite and a diff will complain about, and it used to be silent.
func TestWriteKeepsTrailingNewline(t *testing.T) {
	content := "package main\n\nfunc main() {\n}\n"
	s := "<function=write>\n<parameter=path>main.go</parameter>\n" +
		"<parameter=content>\n" + content + "</parameter>\n</function>"

	calls, _ := parseCalls(s, nil)
	if len(calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(calls))
	}
	var got map[string]string
	if err := json.Unmarshal(calls[0].Input, &got); err != nil {
		t.Fatal(err)
	}
	if got["content"] != content {
		t.Errorf("content = %q, want %q", got["content"], content)
	}
}

// TestParamValue pins the framing rules directly.
func TestParamValue(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
	}{
		{"old_str", "\n\tx\n", "\tx"},                         // one framing newline each end
		{"old_str", "\n\n\tx\n\n", "\n\tx\n"},                 // a real blank line survives
		{"old_str", "\r\n\tx\r\n", "\tx"},                     // CRLF framing
		{"content", "\nline\n", "line\n"},                     // a whole file keeps its final newline
		{"content", "\nline\n\n", "line\n\n"},                 // and any blank line after it
		{"path", "\n  main.go  \n", "main.go"},                // not verbatim: trimmed
		{"command", "\n go build ./... \n", "go build ./..."}, // not verbatim: trimmed
		{"old_str", "no newlines", "no newlines"},
	} {
		if got := paramValue(tc.name, tc.in); got != tc.want {
			t.Errorf("paramValue(%q, %q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}
