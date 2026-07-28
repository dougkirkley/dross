package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dougkirkley/holocron/internal/provider"
)

// TestSend_ToolCall spins a mock OpenAI-compatible server and verifies the
// provider parses tool calls, maps the finish reason, and reports usage.
func TestSend_ToolCall(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{
			"choices":[{"message":{"role":"assistant","content":"",
				"tool_calls":[{"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":\"a.go\"}"}}]},
				"finish_reason":"tool_calls"}],
			"usage":{"prompt_tokens":100,"completion_tokens":20}}`)
	}))
	defer srv.Close()

	p, err := provider.New("openai", provider.Config{BaseURL: srv.URL, APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	sess := p.NewSession(provider.SessionConfig{
		System: "sys", Model: "m",
		Tools: []provider.ToolDef{{Name: "read", Description: "read", Properties: map[string]any{"path": map[string]any{"type": "string"}}, Required: []string{"path"}}},
	})

	resp, err := sess.Send(context.Background(), provider.Input{Text: "read a.go"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != provider.StopToolUse {
		t.Fatalf("stop = %q", resp.StopReason)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "read" {
		t.Fatalf("tool calls = %v", resp.ToolCalls)
	}
	var args struct {
		Path string `json:"path"`
	}
	json.Unmarshal(resp.ToolCalls[0].Input, &args)
	if args.Path != "a.go" {
		t.Fatalf("args = %v", args)
	}
	if resp.Usage.InputTokens != 100 || resp.Usage.OutputTokens != 20 {
		t.Fatalf("usage = %+v", resp.Usage)
	}

	// Request shape: system seeded, tools converted, model set.
	if gotBody["model"] != "m" {
		t.Fatalf("model in request = %v", gotBody["model"])
	}
	tools, _ := gotBody["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools in request = %v", gotBody["tools"])
	}
}

// TestSend_ToolResultRoundTrip verifies tool results are sent as role:tool.
func TestSend_ToolResultRoundTrip(t *testing.T) {
	var msgs []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []map[string]any `json:"messages"`
		}
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &body)
		msgs = body.Messages
		io.WriteString(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{}}`)
	}))
	defer srv.Close()

	p, _ := provider.New("openai", provider.Config{BaseURL: srv.URL})
	sess := p.NewSession(provider.SessionConfig{Model: "m"})
	resp, err := sess.Send(context.Background(), provider.Input{
		ToolResults: []provider.ToolResult{{ID: "call_1", Content: "file body"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != provider.StopEndTurn || resp.Text != "ok" {
		t.Fatalf("resp = %+v", resp)
	}
	last := msgs[len(msgs)-1]
	if last["role"] != "tool" || last["tool_call_id"] != "call_1" || last["content"] != "file body" {
		t.Fatalf("tool msg = %v", last)
	}
}

// TestSend_HTTPError surfaces non-200 as an error.
func TestSend_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	p, _ := provider.New("openai", provider.Config{BaseURL: srv.URL})
	sess := p.NewSession(provider.SessionConfig{Model: "m"})
	if _, err := sess.Send(context.Background(), provider.Input{Text: "x"}); err == nil {
		t.Fatal("expected error on 500")
	}
}

// TestSend_XMLToolCallFallback verifies XML-style tool calls (qwen3-coder) in
// the content are parsed into real tool calls and stripped from the text.
func TestSend_XMLToolCallFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":
			"I'll check.\n<tool_call>\n<function=bash>\n<parameter=command>\nwhich go\n</parameter>\n</function>\n</tool_call>"},
			"finish_reason":"stop"}],"usage":{}}`)
	}))
	defer srv.Close()

	p, _ := provider.New("openai", provider.Config{BaseURL: srv.URL})
	sess := p.NewSession(provider.SessionConfig{Model: "m"})
	resp, err := sess.Send(context.Background(), provider.Input{Text: "install go"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != provider.StopToolUse {
		t.Fatalf("stop = %q, want tool_use", resp.StopReason)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "bash" {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	var args struct {
		Command string `json:"command"`
	}
	json.Unmarshal(resp.ToolCalls[0].Input, &args)
	if args.Command != "which go" {
		t.Fatalf("command = %q", args.Command)
	}
	if strings.Contains(resp.Text, "<function=") {
		t.Fatalf("XML not stripped: %q", resp.Text)
	}
}

func TestNormStop(t *testing.T) {
	cases := map[string]string{
		"stop":       provider.StopEndTurn,
		"tool_calls": provider.StopToolUse,
		"length":     provider.StopMaxTokens,
		"weird":      provider.StopOther,
	}
	for in, want := range cases {
		if got := normStop(in); got != want {
			t.Errorf("normStop(%q) = %q, want %q", in, got, want)
		}
	}
}
