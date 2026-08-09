package cli

import (
	"errors"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/dougkirkley/dross/internal/tui"
)

// This file is dross's prompt intake, and it deliberately copies the Claude
// Code CLI's rather than inventing one. The shapes:
//
//	dross "do the thing"          interactive, with the prompt already sent
//	dross -p "do the thing"       run it, print the answer, exit
//	cat f | dross -p "explain"    the argument, then the piped text, one prompt
//	cat f | dross -p              the piped text is the whole prompt
//	dross "do the thing" | tee x  print mode: stdout is not a terminal
//	dross -p                      an error; there is nothing to say
//
// Every one of those was read off the wire from the real `claude` binary
// pointed at a local stand-in for the Anthropic API, so what follows is what
// Claude Code actually sends rather than what its help text implies —
// including the details the docs do not mention: the argument comes first, the
// piped text keeps its own trailing newline, an empty pipe adds no separator,
// and a second unquoted word is dropped rather than joined, so
// `dross -p fix the bug` asks about "fix". TestPromptParityWithClaudeCode
// re-runs that experiment against the binary, case for case.
//
// That last one is a wart, and it is here on purpose: the point of this file is
// that a prompt behaves identically whichever of the two you type it into, and
// a prompt shape that behaves better here than there is still a prompt shape
// that behaves differently.

// errNoPrompt is what --print reports when neither an argument nor a pipe gave
// it anything to do. Worded as Claude Code words it.
var errNoPrompt = errors.New("input must be provided either through stdin or as a prompt argument when using --print")

// composePrompt joins the positional prompt with piped stdin the way Claude
// Code does: the argument first, then the pipe, one newline between them, and
// no separator when either half is empty.
//
// Nothing is trimmed. Piped text is usually a file or a command's output, and
// its whitespace can be the point — a diff's trailing blank line, the blank
// last line of a log.
func composePrompt(arg, piped string) string {
	switch {
	case arg == "":
		return piped
	case piped == "":
		return arg
	}
	return arg + "\n" + piped
}

// promptArg is the positional prompt, if one was given: the first argument,
// and only the first. Words after it are dropped, which is what Claude Code
// does with them.
func promptArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

// pipedStdin reads standard input when it has been redirected, and returns ""
// when it is still a terminal.
//
// The terminal check is the whole point: without it an interactive `dross`
// would block on a read that only ends when the user, who is waiting for a
// prompt box, presses ctrl-d.
func pipedStdin(cmd *cobra.Command) (string, error) {
	if isTerminal(os.Stdin) {
		return "", nil
	}
	b, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// slashReply answers a "/command" typed at the non-interactive path, and
// reports whether it answered.
//
// Claude Code does this and it is worth copying exactly: `claude -p /help`
// prints "/help isn't available in this environment.", `claude -p /nope` prints
// "Unknown command: /nope", both exit 0, and neither opens a session — verified
// against the binary, zero model turns captured. dross used to hand the literal
// text to the model, which pays for a round trip to be told something the
// process already knows, and on a local model that is a minute of nothing.
//
// The two messages are kept apart because they mean different things: one says
// the command exists and this is the wrong place for it, the other says it does
// not exist. Collapsing them would send someone looking for a typo in a name
// that was right.
func slashReply(prompt string) (string, bool) {
	if !tui.LooksLikeSlashCommand(prompt) {
		return "", false
	}
	name := strings.Fields(strings.TrimSpace(prompt))[0]
	for _, known := range tui.SlashCommandNames() {
		if known == name {
			return name + " isn't available in --print; it needs the interactive client.", true
		}
	}
	return "Unknown command: " + name, true
}

// isTerminal reports whether f is a character device a person is sitting at.
func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// wantsPrint decides between the interactive client and one-shot output.
//
// Explicitly with -p, and implicitly when either end is not a terminal.
// `dross "..." > answer.txt` has nowhere to draw an interface and would fill
// the file with escape sequences; `cat notes | dross` has no keyboard to read
// the next keystroke from, only a pipe that is already at EOF. Claude Code
// makes the same call — its help puts it as "non-interactive mode (via -p, or
// when stdout is not a TTY, e.g. piped or redirected output)".
func wantsPrint(print bool, stdout, stdin *os.File) bool {
	return print || !isTerminal(stdout) || !isTerminal(stdin)
}
