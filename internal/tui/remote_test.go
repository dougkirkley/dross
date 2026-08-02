package tui

import (
	"regexp"
	"strings"
	"testing"
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// feed is everything rendered into the transcript, with styling removed:
// lipgloss splits a wrapped line into many colour spans, so asserting on the
// raw string would be asserting on where the wrap happened.
func feed(m Model) string {
	return ansiRE.ReplaceAllString(strings.Join(m.lines, "\n"), "")
}

// A linked terminal must show the work being driven through it. Before this,
// /rc reported that a link was open and then went silent while the agent ran
// invisibly inside it.
func TestRemoteEventsRenderInTheFeed(t *testing.T) {
	m := New("dross", "m", "openai", noRun(t))
	m.width, m.height = 100, 30
	m.layout()

	for _, ev := range []remoteEventMsg{
		{"prompt", "check the disk usage on node2"},
		{"tool", "bash df -h"},
		{"tool-result", "/dev/sda1  40G  22G  16G  58% /"},
		{"text", "node2 is at 58%, no action needed."},
		{"done", "$0.0021 · 2 turns"},
	} {
		next, _ := m.Update(ev)
		m = next.(Model)
	}

	out := feed(m)
	for _, want := range []string{
		"phone",                        // attributed to the remote, not to "you"
		"check the disk usage on node2", // the inbound prompt
		"58%",                           // the tool's output
		"no action needed",              // the reply
	} {
		if !strings.Contains(out, want) {
			t.Errorf("feed is missing %q:\n%s", want, out)
		}
	}
}

// The prompt came from elsewhere, so labelling it "you" would make the
// transcript claim the person at the keyboard asked for it.
func TestRemotePromptIsNotAttributedToYou(t *testing.T) {
	m := New("dross", "m", "openai", noRun(t))
	m.width, m.height = 100, 30
	m.layout()

	next, _ := m.Update(remoteEventMsg{"prompt", "deploy it"})
	m = next.(Model)

	line := feed(m)
	if !strings.Contains(line, "phone") {
		t.Errorf("remote prompt is not labelled as remote:\n%s", line)
	}
	if strings.Contains(line, "you") {
		t.Errorf("remote prompt is attributed to the local user:\n%s", line)
	}
}

// Linking must not take the terminal over: the person sitting at it keeps
// working while someone else drives.
func TestLinkedTerminalStillAcceptsInput(t *testing.T) {
	m := New("dross", "m", "openai", noRun(t))
	m.width, m.height = 100, 30
	m.layout()
	m.remoteLinked = true

	m.input.SetValue("still typing")
	if got := m.input.Value(); got != "still typing" {
		t.Errorf("input = %q; a linked terminal must stay usable", got)
	}
}
