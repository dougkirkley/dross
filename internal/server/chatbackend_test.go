package server

import (
	"context"
	"testing"
)

// A chat backend is optional. Without one, chat has to behave exactly as it did
// before there was such a thing — same provider, same model, same endpoint.
func TestChatBackendFallsBackToLead(t *testing.T) {
	lead := &fakeProvider{}
	m := NewManager(lead, nil, "qwen3-coder:30b", t.TempDir())
	m.SetLeadEndpoint("http://lead:11434/v1")

	p, model, url := m.chatBackend()
	if p != lead {
		t.Error("chat should run on the lead provider")
	}
	if model != "qwen3-coder:30b" {
		t.Errorf("model = %q, want the lead's default", model)
	}
	if url != "http://lead:11434/v1" {
		t.Errorf("url = %q, want the lead endpoint", url)
	}
}

// Configured, chat answers somewhere else entirely — and the endpoint travels
// with it, because the cold-model check has to ask the server the turn will
// actually run on. Pointing it at the lead's endpoint on a two-server
// deployment reports "cold" forever for a model that is warm on the other one.
func TestChatBackendOverrides(t *testing.T) {
	lead, chat := &fakeProvider{}, &fakeProvider{}
	m := NewManager(lead, nil, "qwen3-coder:30b", t.TempDir())
	m.SetLeadEndpoint("http://lead:11434/v1")
	m.SetChat(chat, "gemma2:9b", "http://small:11434/v1")

	p, model, url := m.chatBackend()
	if p != chat {
		t.Error("chat should run on the chat provider")
	}
	if model != "gemma2:9b" {
		t.Errorf("model = %q, want the chat model", model)
	}
	if url != "http://small:11434/v1" {
		t.Errorf("url = %q, want the chat endpoint", url)
	}
}

// A half-configured chat block (a provider with no model) is not a chat
// backend. Running chat against an endpoint with no model named would fail
// every turn, where falling back answers.
func TestChatBackendIgnoresModellessOverride(t *testing.T) {
	lead := &fakeProvider{}
	m := NewManager(lead, nil, "qwen3-coder:30b", t.TempDir())
	m.SetChat(&fakeProvider{}, "", "http://small:11434/v1")

	if p, model, _ := m.chatBackend(); p != lead || model != "qwen3-coder:30b" {
		t.Error("a chat backend with no model should fall back to the lead")
	}
}

// A new chat is labelled with the model it will run on. Naming the lead model
// on a session that never touches it makes the client's model chip offer to
// change a model that is not in use.
func TestChatSessionDefaultsToChatModel(t *testing.T) {
	m := NewManager(&fakeProvider{}, nil, "qwen3-coder:30b", t.TempDir())
	m.SetChat(&fakeProvider{}, "gemma2:9b", "http://small:11434/v1")

	chat := m.create(CreateReq{Chat: true}, "someone@example.com")
	if chat.Model != "gemma2:9b" {
		t.Errorf("chat session model = %q, want the chat model", chat.Model)
	}
	// Code sessions are untouched by any of this.
	code := m.create(CreateReq{}, "someone@example.com")
	if code.Model != "qwen3-coder:30b" {
		t.Errorf("code session model = %q, want the lead model", code.Model)
	}
	// An explicit choice still wins over both defaults.
	picked := m.create(CreateReq{Chat: true, Model: "llama3.1:8b"}, "someone@example.com")
	if picked.Model != "llama3.1:8b" {
		t.Errorf("picked model = %q, want what the client asked for", picked.Model)
	}
}

// The picker has to offer models the session's own backend actually has.
func TestModelsForSessionKind(t *testing.T) {
	m := NewManager(&fakeProvider{}, nil, "qwen3-coder:30b", t.TempDir())
	m.SetModels([]string{"qwen3-coder:30b", "qwen2.5-coder:14b"})

	// No chat backend: chat and code are the same server, so the same list.
	if got, cur := m.modelsFor(context.Background(), true); len(got) != 2 || cur != "qwen3-coder:30b" {
		t.Errorf("without a chat backend, chat should get the lead's list, got %v/%q", got, cur)
	}

	// With one and nothing configured to offer, the only model that server is
	// known to have is the one it was configured with. Offering the lead's
	// list here would load an 18 GB model onto a machine sized for smaller
	// ones the moment somebody picked one.
	m.SetChat(&fakeProvider{}, "gemma2:9b", "http://small:11434/v1")
	got, cur := m.modelsFor(context.Background(), true)
	if len(got) != 1 || got[0] != "gemma2:9b" || cur != "gemma2:9b" {
		t.Errorf("chat picker = %v/%q, want just the chat model", got, cur)
	}
	// Code is unaffected by any of it.
	if got, cur := m.modelsFor(context.Background(), false); len(got) != 2 || cur != "qwen3-coder:30b" {
		t.Errorf("code picker = %v/%q, want the lead's list", got, cur)
	}

	// An explicit list wins, for a chat server that does have several.
	m.SetChatModels([]string{"gemma2:9b", "llama3.1:8b"})
	if got, _ := m.modelsFor(context.Background(), true); len(got) != 2 || got[1] != "llama3.1:8b" {
		t.Errorf("chat picker = %v, want the configured list", got)
	}
}
