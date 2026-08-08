package claudecode

import (
	"strings"
	"testing"
)

func TestNormalizeToolName(t *testing.T) {
	cases := map[string]string{
		"Read": "read", "GREP": "grep", "Glob": "glob",
		"Bash": "bash", "WebFetch": "web_fetch", "edit": "edit",
	}
	for in, want := range cases {
		if got := normalizeToolName(in); got != want {
			t.Errorf("normalizeToolName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseCallsExtractsAndCleans(t *testing.T) {
	// A valid call plus echoed protocol scaffolding the model shouldn't emit.
	s := "Let me read it.\n<function=Read>\n<parameter=path>go.mod</parameter>\n</function>\n" +
		"<tool_result>\nstale echoed output\n</tool_result>\n</parameter>"
	calls, text := parseCalls(s, nil)
	if len(calls) != 1 || calls[0].Name != "read" {
		t.Fatalf("calls = %+v", calls)
	}
	for _, leak := range []string{"<function", "</function", "<parameter", "</parameter", "<tool_result", "function_results"} {
		if strings.Contains(text, leak) {
			t.Fatalf("protocol leaked into text %q: %q", leak, text)
		}
	}
	if strings.Contains(text, "stale echoed output") {
		t.Fatalf("echoed tool result not stripped: %q", text)
	}
}

func TestStripProtocolNoCalls(t *testing.T) {
	s := "Here is the answer.\n<function_results>7 CLAUDE.md</function_results>\n</function>"
	if got := stripProtocol(s); strings.Contains(got, "<") {
		t.Fatalf("stray tags remain: %q", got)
	}
}

func TestStripProtocolDropsSystemReminder(t *testing.T) {
	s := "Done.\n<system-reminder>\nWarning: this tool call took a long time to run.\n</system-reminder>"
	got := stripProtocol(s)
	if strings.Contains(got, "system-reminder") || strings.Contains(got, "took a long time") {
		t.Fatalf("system-reminder leaked: %q", got)
	}
	if got != "Done." {
		t.Fatalf("want %q, got %q", "Done.", got)
	}
}

func TestParseCallsNativeInvokeFormat(t *testing.T) {
	// Opus-style native tool XML (attribute form + <invoke>) must parse.
	s := `I'll read it.
<invoke name="read">
<parameter name="path">go.mod</parameter>
</invoke>`
	calls, _ := parseCalls(s, nil)
	if len(calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(calls))
	}
	if calls[0].Name != "read" {
		t.Fatalf("name = %q", calls[0].Name)
	}
	if !strings.Contains(string(calls[0].Input), `"path":"go.mod"`) {
		t.Fatalf("path arg dropped: %s", calls[0].Input)
	}
}
