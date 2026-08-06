package anthropic

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/dougkirkley/dross/pkg/provider"
)

// sse writes one server-sent event.
func sse(w http.ResponseWriter, event, data string) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// streamBody replays a full message as events: some prose, then a tool call
// whose arguments arrive in fragments — the case a hand-rolled reader gets
// wrong.
func streamBody(w http.ResponseWriter) {
	sse(w, "message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5","content":[],"stop_reason":null,"usage":{"input_tokens":10,"output_tokens":0,"cache_read_input_tokens":7,"cache_creation_input_tokens":3}}}`)

	sse(w, "content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
	sse(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Reading "}}`)
	sse(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"the file."}}`)
	sse(w, "content_block_stop", `{"type":"content_block_stop","index":0}`)

	sse(w, "content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"read","input":{}}}`)
	sse(w, "content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"pa"}}`)
	sse(w, "content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"th\": \"util.go\"}"}}`)
	sse(w, "content_block_stop", `{"type":"content_block_stop","index":1}`)

	sse(w, "message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":25}}`)
	sse(w, "message_stop", `{"type":"message_stop"}`)
}

const bufferedBody = `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5",
"content":[{"type":"text","text":"Reading the file."},
{"type":"tool_use","id":"toolu_1","name":"read","input":{"path":"util.go"}}],
"stop_reason":"tool_use",
"usage":{"input_tokens":10,"output_tokens":25,"cache_read_input_tokens":7,"cache_creation_input_tokens":3}}`

func fakeSession(t *testing.T, h http.HandlerFunc) provider.Session {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	client := sdk.NewClient(option.WithBaseURL(srv.URL), option.WithAPIKey("test"))
	p := NewWithClient(client, "anthropic")
	return p.NewSession(provider.SessionConfig{Model: "claude-opus-5", System: "sys"})
}

// TestStreamingMatchesBuffered is the drift guard: two code paths that must
// produce the same answer, checked against each other rather than against a
// hand-written expectation that could be wrong twice.
func TestStreamingMatchesBuffered(t *testing.T) {
	streamed := fakeSession(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		streamBody(w)
	})
	buffered := fakeSession(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, bufferedBody)
	})

	var chunks []string
	got, err := streamed.Send(context.Background(), provider.Input{
		Text:   "read util.go",
		OnText: func(s string) { chunks = append(chunks, s) },
	})
	if err != nil {
		t.Fatal(err)
	}
	want, err := buffered.Send(context.Background(), provider.Input{Text: "read util.go"})
	if err != nil {
		t.Fatal(err)
	}

	if got.Text != want.Text {
		t.Errorf("text: streamed %q, buffered %q", got.Text, want.Text)
	}
	if got.StopReason != want.StopReason {
		t.Errorf("stop: streamed %q, buffered %q", got.StopReason, want.StopReason)
	}
	if got.Usage != want.Usage {
		t.Errorf("usage: streamed %+v, buffered %+v", got.Usage, want.Usage)
	}
	if len(got.ToolCalls) != 1 || len(want.ToolCalls) != 1 {
		t.Fatalf("tool calls: streamed %d, buffered %d", len(got.ToolCalls), len(want.ToolCalls))
	}
	g, wnt := got.ToolCalls[0], want.ToolCalls[0]
	if g.ID != wnt.ID || g.Name != wnt.Name {
		t.Errorf("tool call: streamed %+v, buffered %+v", g, wnt)
	}
	// The fragmented arguments must reassemble into the same JSON.
	if !strings.Contains(string(g.Input), `"path"`) || !strings.Contains(string(g.Input), `util.go`) {
		t.Errorf("streamed tool input did not reassemble: %s", g.Input)
	}
}

// TestStreamingEmitsProseAsItArrives is the reason to stream at all.
func TestStreamingEmitsProseAsItArrives(t *testing.T) {
	sess := fakeSession(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		streamBody(w)
	})
	var chunks []string
	if _, err := sess.Send(context.Background(), provider.Input{
		Text:   "hi",
		OnText: func(s string) { chunks = append(chunks, s) },
	}); err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 2 {
		t.Fatalf("got %d chunks (%q), want the prose to arrive in pieces", len(chunks), chunks)
	}
	if strings.Join(chunks, "") != "Reading the file." {
		t.Fatalf("chunks joined = %q", strings.Join(chunks, ""))
	}
	// Tool-call fragments are not prose and must not be streamed as text.
	for _, c := range chunks {
		if strings.Contains(c, "partial_json") || strings.Contains(c, "util.go") {
			t.Errorf("tool arguments leaked into the prose stream: %q", c)
		}
	}
}

// TestNoOnTextStaysBuffered: nothing to watch, so no reason to stream.
func TestNoOnTextStaysBuffered(t *testing.T) {
	var streamed bool
	sess := fakeSession(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Accept"), "event-stream") {
			streamed = true
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, bufferedBody)
	})
	if _, err := sess.Send(context.Background(), provider.Input{Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if streamed {
		t.Error("a request with no OnText should not ask for a stream")
	}
}
