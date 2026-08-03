package cli

import (
	"testing"

	"github.com/dougkirkley/dross/internal/config"
)

func TestChatSlot(t *testing.T) {
	lead := config.Provider{
		Provider: "openai",
		Model:    "qwen3-coder:30b",
		BaseURL:  "http://ollama:11434/v1",
		APIKey:   "ollama",
	}

	t.Run("no block is no chat backend", func(t *testing.T) {
		if got := chatSlot(nil, lead); got.Model != "" {
			t.Errorf("got %+v, want the zero value so chat runs on the lead", got)
		}
	})

	// The case nearly every deployment wants: one provider, one endpoint, a
	// different model for chat.
	t.Run("a model alone inherits the rest", func(t *testing.T) {
		got := chatSlot(&config.Provider{Model: "gemma2:9b"}, lead)
		if got.Model != "gemma2:9b" {
			t.Errorf("model = %q", got.Model)
		}
		if got.Provider != "openai" || got.BaseURL != "http://ollama:11434/v1" || got.APIKey != "ollama" {
			t.Errorf("should have inherited the lead's backend, got %+v", got)
		}
	})

	// A second server on the same provider: state the endpoint, keep the key.
	t.Run("a base URL alone keeps the key", func(t *testing.T) {
		got := chatSlot(&config.Provider{Model: "gemma2:9b", BaseURL: "http://ollama-small:11434/v1"}, lead)
		if got.BaseURL != "http://ollama-small:11434/v1" {
			t.Errorf("base URL = %q, want the stated one", got.BaseURL)
		}
		if got.APIKey != "ollama" || got.Provider != "openai" {
			t.Errorf("should have inherited provider and key, got %+v", got)
		}
	})

	// A different provider is a different service. Inheriting a local
	// endpoint and the string "ollama" as an API key would be confidently
	// wrong in a way that only shows up on the first chat turn.
	t.Run("a different provider inherits nothing but the model", func(t *testing.T) {
		got := chatSlot(&config.Provider{Provider: "anthropic"}, lead)
		if got.Provider != "anthropic" {
			t.Errorf("provider = %q", got.Provider)
		}
		if got.BaseURL != "" || got.APIKey != "" {
			t.Errorf("should not have inherited the lead's endpoint or key, got %+v", got)
		}
		// The model is still inherited: it is the one field where "the same as
		// the lead" is a coherent answer for any provider, and a chat block
		// that names an endpoint but no model is asking for the same model
		// somewhere else.
		if got.Model != "qwen3-coder:30b" {
			t.Errorf("model = %q, want the lead's", got.Model)
		}
	})

	t.Run("a full block is left alone", func(t *testing.T) {
		block := &config.Provider{
			Provider: "openai", Model: "gemma2:9b",
			BaseURL: "http://elsewhere:11434/v1", APIKey: "k",
		}
		if got := chatSlot(block, lead); got != *block {
			t.Errorf("got %+v, want %+v", got, *block)
		}
	})
}
