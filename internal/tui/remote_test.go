package tui

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dougkirkley/dross/pkg/agent"
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// exec runs a command the way the Bubble Tea runtime would: a batch yields a
// BatchMsg holding more commands, which have to be run in turn.
func runCmd(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range batch {
			runCmd(c)
		}
	}
}

// feed is everything rendered into the transcript, with styling removed:
// lipgloss splits a wrapped line into many colour spans, so asserting on the
// raw string would be asserting on where the wrap happened.
func feed(m Model) string {
	return ansiRE.ReplaceAllString(strings.Join(m.lines, "\n"), "")
}

// A prompt from the phone runs here, through the local agent, and the work
// appears in the local feed. Before this the relay ran it on a second,
// unconfigured agent and the terminal showed nothing.
func TestRemotePromptRunsLocallyAndRenders(t *testing.T) {
	ran := make(chan string, 1)
	m := New("dross", "m", "openai", func(_ context.Context, prompt, _ string, emit func(agent.Event)) (*agent.Result, error) {
		ran <- prompt
		emit(agent.Event{Kind: "tool", Data: "bash df -h"})
		emit(agent.Event{Kind: "text", Data: "node2 is at 58%, no action needed."})
		return &agent.Result{}, nil
	})
	m.width, m.height = 100, 30
	m.layout()

	next, cmd := m.Update(remotePromptMsg{"ses_x", "check the disk usage on node2"})
	m = next.(Model)
	runCmd(cmd)
	select {
	case got := <-ran:
		if got != "check the disk usage on node2" {
			t.Fatalf("ran %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the remote prompt never reached the local agent")
	}
	for i := 0; i < 2; i++ {
		select {
		case msg := <-m.msgs:
			if ev, ok := msg.(EventMsg); ok {
				m.applyEvent(agent.Event(ev))
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no events")
		}
	}

	out := feed(m)
	for _, want := range []string{
		"phone",                         // attributed to the remote, not to "you"
		"check the disk usage on node2", // the inbound prompt
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

	next, _ := m.Update(remotePromptMsg{"ses_x", "deploy it"})
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

// One agent means one run at a time. A remote prompt arriving mid-run has to
// queue exactly as typing during a run does — starting it would put two runs on
// the single shared conversation.
func TestRemotePromptQueuesWhileRunning(t *testing.T) {
	m := New("dross", "m", "openai", noRun(t))
	m.width, m.height = 100, 30
	m.layout()
	m.running = true // a run is already in flight

	next, _ := m.Update(remotePromptMsg{"ses_x", "and then deploy"})
	m = next.(Model)

	if len(m.queue) != 1 || m.queue[0] != "and then deploy" {
		t.Errorf("queue = %v; the remote prompt should have been queued", m.queue)
	}
}

// Stop on the phone has to cancel the run happening here.
func TestRemoteInterruptCancelsTheRun(t *testing.T) {
	m := New("dross", "m", "openai", noRun(t))
	m.width, m.height = 100, 30
	m.layout()
	cancelled := false
	m.running, m.runCancel = true, func() { cancelled = true }

	next, _ := m.Update(remoteInterruptMsg{})
	m = next.(Model)

	if !cancelled {
		t.Error("a remote interrupt did not cancel the run")
	}
	if !strings.Contains(feed(m), "interrupted from the remote") {
		t.Error("the interrupt was not reported in the transcript")
	}
}

// Choosing Allow must actually allow. approve() matches the offered option
// exactly, so the labelled "header: answer" form denied every gate it was
// asked to approve.
func TestSingleQuestionAnswersWithTheChoiceAlone(t *testing.T) {
	m := New("dross", "m", "openai", noRun(t))
	m.width, m.height = 100, 30
	m.layout()
	st := &askState{
		questions: []agent.AskQuestion{{
			Header: "bash", Question: "Allow this bash?", Options: []string{"Allow", "Deny"},
		}},
		sel:    [][]bool{{true, false}},
		cursor: []int{0},
		reply:  make(chan string, 1),
	}
	m.pendingAsk = st
	m.submitAsk()

	if got := <-st.reply; got != "Allow" {
		t.Errorf("agent received %q; approve() only accepts the bare option", got)
	}
}

// Several questions still need labels, or the answers cannot be told apart.
func TestMultiQuestionKeepsItsLabels(t *testing.T) {
	m := New("dross", "m", "openai", noRun(t))
	m.width, m.height = 100, 30
	m.layout()
	st := &askState{
		questions: []agent.AskQuestion{
			{Header: "lang", Options: []string{"Go", "Rust"}},
			{Header: "tests", Options: []string{"Yes", "No"}},
		},
		sel:    [][]bool{{true, false}, {false, true}},
		cursor: []int{0, 0},
		reply:  make(chan string, 1),
	}
	m.pendingAsk = st
	m.submitAsk()

	got := <-st.reply
	if !strings.Contains(got, "lang: Go") || !strings.Contains(got, "tests: No") {
		t.Errorf("multi-question answer lost its labels: %q", got)
	}
}
