package cli

import (
	"bytes"
	"strings"
	"testing"
)

// TestComposePrompt pins the composition rules against the shapes read off the
// wire from the real Claude Code CLI. TestPromptParityWithClaudeCode re-derives
// the same table from the binary itself; this one runs everywhere and in
// milliseconds, and is what fails first if the rules are edited by hand.
func TestComposePrompt(t *testing.T) {
	for _, tc := range []struct {
		name, arg, piped, want string
	}{
		{"argument alone", "fix the build", "", "fix the build"},
		{"pipe alone", "", "explain this\n", "explain this\n"},
		{"argument then pipe", "explain", "line one\nline two\n", "explain\nline one\nline two\n"},
		// An empty pipe contributes nothing, not a blank line: `dross -p x
		// </dev/null` must ask the same question as `dross -p x`.
		{"empty pipe adds no separator", "explain", "", "explain"},
		// A pipe carrying only whitespace is not empty. It is what `git diff`
		// prints when there is nothing to say, and the model should see that
		// rather than a question with the evidence quietly removed.
		{"blank pipe is still a pipe", "explain", "  \n", "explain\n  \n"},
		{"nothing at all", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := composePrompt(tc.arg, tc.piped); got != tc.want {
				t.Fatalf("composePrompt(%q, %q) = %q, want %q", tc.arg, tc.piped, got, tc.want)
			}
		})
	}
}

func TestPromptArg(t *testing.T) {
	if got := promptArg(nil); got != "" {
		t.Fatalf("no args should give no prompt, got %q", got)
	}
	if got := promptArg([]string{"fix the build"}); got != "fix the build" {
		t.Fatalf("promptArg = %q", got)
	}
	// Unquoted, and so dropped after the first word — the same answer Claude
	// Code gives, wart and all. Parity is the requirement; being nicer about it
	// would be a difference.
	if got := promptArg([]string{"fix", "the", "build"}); got != "fix" {
		t.Fatalf("promptArg = %q, want the first word alone", got)
	}
}

// TestWantsPrint covers the three ways a run turns out to be non-interactive.
// nil stands for "not a terminal" here, which is what a redirected stream is.
func TestWantsPrint(t *testing.T) {
	if !wantsPrint(true, nil, nil) {
		t.Fatal("-p must print")
	}
	if !wantsPrint(false, nil, nil) {
		t.Fatal("a redirected stdout must print")
	}
}

// TestRootTakesAnyWordAsAPrompt: the root refuses no argument shape, because
// Claude Code refuses none. A word that names no subcommand is a prompt, and
// extra words are dropped later rather than rejected here.
func TestRootTakesAnyWordAsAPrompt(t *testing.T) {
	for _, args := range [][]string{{"agnet"}, {"fix", "the", "build"}, {"nope"}} {
		if err := rootArgs(NewRootCmd(), args); err != nil {
			t.Fatalf("rootArgs(%q) = %v, want it accepted as a prompt", args, err)
		}
	}
}

// TestSlashCommandsAreAnsweredWithoutAModel covers the prompt shape that is not
// a prompt.
//
// Claude Code answers these locally — `claude -p /help` and `claude -p /nope`
// both print a line, exit 0, and open no session; that was checked against the
// binary with a capturing endpoint, and zero model turns arrived. The costly
// failure is the other behaviour: sending "/model" to a model, waiting a minute
// on a local one, and being told it is not a command.
func TestSlashCommandsAreAnsweredWithoutAModel(t *testing.T) {
	known, ok := slashReply("/model")
	if !ok || !strings.Contains(known, "/model") {
		t.Fatalf("slashReply(/model) = %q, %v", known, ok)
	}
	if !strings.Contains(known, "--print") {
		t.Fatalf("a known command should say where it does work, got %q", known)
	}

	unknown, ok := slashReply("/notacommand")
	if !ok || unknown != "Unknown command: /notacommand" {
		t.Fatalf("slashReply(/notacommand) = %q, %v", unknown, ok)
	}

	// A prompt is not a command, and neither is a path.
	for _, prompt := range []string{
		"fix the build",
		"/usr/local/bin/thing is missing, install it",
		"/",
		"",
	} {
		if reply, ok := slashReply(prompt); ok {
			t.Errorf("slashReply(%q) answered %q; that is a prompt", prompt, reply)
		}
	}
}

// And end to end: the print path answers before it resolves a provider, so it
// works with nothing configured at all.
func TestPrintAnswersSlashWithNoProviderConfigured(t *testing.T) {
	root := NewRootCmd()
	var out bytes.Buffer
	root.SetArgs([]string{"--print", "/notacommand"})
	root.SetIn(strings.NewReader(""))
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	if err := root.Execute(); err != nil {
		t.Fatalf("a slash command should not be an error: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "Unknown command: /notacommand" {
		t.Fatalf("printed %q", got)
	}
}

// TestPrintWithNothingToSay: `dross -p` with no argument and no pipe has no
// prompt, and Claude Code treats that as an error rather than a session.
func TestPrintWithNothingToSay(t *testing.T) {
	root := NewRootCmd()
	root.SetArgs([]string{"--print"})
	root.SetIn(strings.NewReader("")) // stdin is not a terminal under go test
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	err := root.Execute()
	if err == nil {
		t.Fatal("--print with no input should be an error")
	}
	if !strings.Contains(err.Error(), "stdin") {
		t.Fatalf("unexpected error: %v", err)
	}
}
