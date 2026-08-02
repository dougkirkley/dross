package tui

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dougkirkley/dross/pkg/agent"
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

// The other direction: work started at the keyboard has to reach the session
// the phone is watching, or the two ends show different halves of the same
// conversation.
func TestLocalRunIsPublishedToTheLinkedSession(t *testing.T) {
	var mu sync.Mutex
	var got []string
	publish := func(kind, data string) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, kind+":"+data)
	}

	m := New("dross", "m", "openai", func(_ context.Context, prompt, _ string, emit func(agent.Event)) (*agent.Result, error) {
		emit(agent.Event{Kind: "tool", Data: "bash ls"})
		emit(agent.Event{Kind: "text", Data: "two files"})
		return &agent.Result{}, nil
	})
	m.width, m.height = 100, 30
	m.layout()
	m.remoteLinked, m.remotePublish = true, publish

	// startRun spawns the run; drain until it reports completion.
	m.startRun(context.Background(), "list the files")()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case msg := <-m.msgs:
			if _, done := msg.(doneMsg); done {
				goto check
			}
		case <-deadline:
			t.Fatal("run did not finish")
		}
	}

check:
	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(got, "\n")
	for _, want := range []string{
		"prompt:list the files", // the request, before the work
		"tool:bash ls",          // each event as it happens
		"text:two files",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("never published %q; published:\n%s", want, joined)
		}
	}
	// Without a terminal event the phone shows the session running forever.
	if !strings.Contains(joined, "done:") {
		t.Errorf("run was never closed out remotely; published:\n%s", joined)
	}
	if i, j := strings.Index(joined, "prompt:"), strings.Index(joined, "tool:"); i > j {
		t.Error("published the work before the prompt that caused it")
	}
}

// A dropped link must stop publishing, rather than writing into a dead socket.
func TestLinkDropClearsThePublisher(t *testing.T) {
	m := New("dross", "m", "openai", noRun(t))
	m.width, m.height = 100, 30
	m.layout()
	m.remoteLinked = true
	m.remotePublish = func(string, string) { t.Error("published after the link closed") }

	next, _ := m.Update(remoteStatusMsg{"link closed"})
	m = next.(Model)

	if m.remotePublish != nil {
		t.Error("publisher outlived the link")
	}
	if m.remoteLinked {
		t.Error("still marked as linked after the link closed")
	}
}
