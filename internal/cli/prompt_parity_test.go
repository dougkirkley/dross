package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// TestPromptParityWithClaudeCode checks dross's prompt intake against the thing
// it is copying, by asking the real Claude Code CLI what it would have sent.
//
// The CLI is pointed at a stand-in for the Anthropic API that records the
// request and answers with a canned "ok", so this costs no tokens and needs no
// credentials, and it reads the composed prompt off the wire rather than
// trusting either program's documentation. The composition it confirms —
// argument first, pipe second, one newline between, nothing trimmed, no
// separator for an empty pipe — is written down nowhere else.
//
// Skipped when Claude Code is not installed, and under -short: it starts a
// node process per case.
func TestPromptParityWithClaudeCode(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a claude process per case")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not on PATH")
	}

	spy := newAPISpy()
	srv := httptest.NewServer(spy)
	defer srv.Close()

	for _, tc := range []struct {
		name  string
		args  []string
		stdin string
	}{
		{name: "argument alone", args: []string{"PROMPT_ONLY"}},
		{name: "pipe alone", stdin: "PIPED_ONLY\n"},
		{name: "argument then pipe", args: []string{"ARG"}, stdin: "PIPED_ONE\nPIPED_TWO\n"},
		{name: "empty pipe", args: []string{"ARG_EMPTY_PIPE"}},
		{name: "blank pipe", args: []string{"ARG_BLANK_PIPE"}, stdin: "  \n"},
		// The unquoted prompt, which both drop after the first word. Ugly, and
		// the point: parity has to cover the shapes people get wrong too.
		{name: "unquoted words", args: []string{"UNQUOTED_FIRST", "second", "third"}},
		{name: "unquoted words with a pipe", args: []string{"UNQUOTED_PIPED", "second"}, stdin: "PIPED\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spy.reset()
			argv := append([]string{"-p", "--bare"}, tc.args...)
			cmd := exec.Command("claude", argv...)
			cmd.Dir = t.TempDir()
			cmd.Stdin = strings.NewReader(tc.stdin)
			cmd.Env = append(os.Environ(),
				"ANTHROPIC_BASE_URL="+srv.URL,
				"ANTHROPIC_API_KEY=parity-test",
			)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Skipf("claude would not run against the stand-in API (%v): %s", err, out)
			}

			sent, ok := spy.prompt()
			if !ok {
				t.Fatal("claude sent no turn carrying tools; nothing to compare against")
			}
			if got := composePrompt(promptArg(tc.args), tc.stdin); got != sent {
				t.Fatalf("composed %q, claude sent %q", got, sent)
			}
		})
	}
}

// apiSpy is enough of the Anthropic Messages API to let Claude Code complete
// one turn: it records what was asked and streams back a one-word answer.
type apiSpy struct {
	mu   sync.Mutex
	sent []string // the user's prompt from each turn that carried tools
}

func newAPISpy() *apiSpy { return &apiSpy{} }

func (s *apiSpy) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = nil
}

// prompt returns the prompt from the first real turn.
//
// "Real" means the request that carried the tool definitions. Claude Code also
// makes a side call to name the session, which quotes the prompt inside a
// wrapper of its own; that one has no tools, and mistaking it for the turn
// would compare against the wrong string.
func (s *apiSpy) prompt() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sent) == 0 {
		return "", false
	}
	return s.sent[0], true
}

func (s *apiSpy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.Contains(r.URL.Path, "/v1/messages") {
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, "{}")
		return
	}
	var body struct {
		Stream   bool              `json:"stream"`
		Tools    []json.RawMessage `json:"tools"`
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err == nil && len(body.Tools) > 0 && len(body.Messages) > 0 {
		// The prompt is the last text block of the first user message; the
		// blocks before it are the harness's own context.
		last := ""
		for _, blk := range body.Messages[0].Content {
			if blk.Type == "text" {
				last = blk.Text
			}
		}
		s.mu.Lock()
		s.sent = append(s.sent, last)
		s.mu.Unlock()
	}

	if body.Stream {
		w.Header().Set("content-type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, ev := range streamedOK {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.name, ev.data)
			if flusher != nil {
				flusher.Flush()
			}
		}
		return
	}
	w.Header().Set("content-type", "application/json")
	fmt.Fprint(w, `{"id":"msg_1","type":"message","role":"assistant","model":"m",`+
		`"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn",`+
		`"usage":{"input_tokens":1,"output_tokens":1}}`)
}

// streamedOK is the shortest complete assistant turn in the streaming shape.
var streamedOK = []struct{ name, data string }{
	{"message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}}`},
	{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
	{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`},
	{"content_block_stop", `{"type":"content_block_stop","index":0}`},
	{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}`},
	{"message_stop", `{"type":"message_stop"}`},
}
