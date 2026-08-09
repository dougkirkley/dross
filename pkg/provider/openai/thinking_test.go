package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dougkirkley/dross/pkg/provider"
)

// captureBody stands in for a chat-completions endpoint and hands back the
// request it was sent, so what actually goes on the wire can be asserted rather
// than assumed.
func captureBody(t *testing.T) (*httptest.Server, *map[string]any) {
	t.Helper()
	got := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},` +
			`"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func send(t *testing.T, base string, thinking *bool) {
	t.Helper()
	p, err := provider.New("openai", provider.Config{BaseURL: base, APIKey: "x"})
	if err != nil {
		t.Fatal(err)
	}
	s := p.NewSession(provider.SessionConfig{Model: "qwen3:14b", Thinking: thinking})
	if _, err := s.Send(context.Background(), provider.Input{Text: "hi"}); err != nil {
		t.Fatal(err)
	}
}

// TestThinkingIsAbsentUnlessAsked is the important one.
//
// Hosted OpenAI rejects a request carrying parameters it does not know, so a
// switch that is always present would break every session against it to make
// local sessions faster. Nil has to mean the wire is exactly as it was.
func TestThinkingIsAbsentUnlessAsked(t *testing.T) {
	srv, got := captureBody(t)
	send(t, srv.URL, nil)

	for _, key := range []string{"think", "chat_template_kwargs"} {
		if _, present := (*got)[key]; present {
			t.Errorf("%q was sent without being configured; that breaks hosted OpenAI", key)
		}
	}
}

// Both spellings go out together, because there is no reliable way to ask a
// server which one it speaks: ollama reads `think`, and vLLM, SGLang and
// llama.cpp read enable_thinking out of chat_template_kwargs.
func TestThinkingOffSendsBothSpellings(t *testing.T) {
	srv, got := captureBody(t)
	off := false
	send(t, srv.URL, &off)

	if v, ok := (*got)["think"].(bool); !ok || v {
		t.Errorf("think = %v, want false", (*got)["think"])
	}
	kwargs, ok := (*got)["chat_template_kwargs"].(map[string]any)
	if !ok {
		t.Fatalf("chat_template_kwargs missing: %v", *got)
	}
	if v, ok := kwargs["enable_thinking"].(bool); !ok || v {
		t.Errorf("enable_thinking = %v, want false", kwargs["enable_thinking"])
	}
}

// And true is a real setting, not merely the absence of false: a reasoning
// model whose server defaults thinking off still has to be switchable on.
func TestThinkingOnIsSentExplicitly(t *testing.T) {
	srv, got := captureBody(t)
	on := true
	send(t, srv.URL, &on)

	if v, ok := (*got)["think"].(bool); !ok || !v {
		t.Errorf("think = %v, want true", (*got)["think"])
	}
	kwargs, _ := (*got)["chat_template_kwargs"].(map[string]any)
	if v, ok := kwargs["enable_thinking"].(bool); !ok || !v {
		t.Errorf("enable_thinking = %v, want true", kwargs["enable_thinking"])
	}
}
