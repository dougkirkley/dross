// Package openai is an OpenAI-compatible backend for the provider registry. It
// speaks the /chat/completions API, so it works against OpenAI itself and any
// compatible server — notably the homelab's ollama, reached by pointing
// Config.BaseURL at its /v1 endpoint. Import for side effects to register:
//
//	import _ "github.com/dougkirkley/holocron/internal/provider/openai"
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/dougkirkley/holocron/internal/provider"
)

func init() {
	provider.Register("openai", func(cfg provider.Config) (provider.Provider, error) {
		base := cfg.BaseURL
		if base == "" {
			base = "https://api.openai.com/v1"
		}
		return &prov{
			base:   strings.TrimRight(base, "/"),
			apiKey: cfg.APIKey,
			hc:     &http.Client{}, // no timeout: generations are long
		}, nil
	})
}

const defaultMaxTokens = 16000

type prov struct {
	base   string
	apiKey string
	hc     *http.Client
}

func (p *prov) Name() string { return "openai" }

func (p *prov) NewSession(cfg provider.SessionConfig) provider.Session {
	maxTok := cfg.MaxTokens
	if maxTok <= 0 {
		maxTok = defaultMaxTokens
	}
	s := &session{prov: p, model: cfg.Model, tools: convertTools(cfg.Tools), maxTokens: maxTok}
	if cfg.System != "" {
		s.messages = append(s.messages, chatMessage{Role: "system", Content: cfg.System})
	}
	return s
}

// --- wire types (OpenAI chat-completions) ---------------------------------

type chatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON encoded as a string
	} `json:"function"`
}

type toolSpec struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

type chatRequest struct {
	Model     string        `json:"model"`
	Messages  []chatMessage `json:"messages"`
	Tools     []toolSpec    `json:"tools,omitempty"`
	MaxTokens int           `json:"max_tokens,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// --- session --------------------------------------------------------------

type session struct {
	prov      *prov
	model     string
	tools     []toolSpec
	maxTokens int
	messages  []chatMessage
}

func (s *session) Send(ctx context.Context, in provider.Input) (*provider.Response, error) {
	if len(in.ToolResults) > 0 {
		for _, r := range in.ToolResults {
			s.messages = append(s.messages, chatMessage{Role: "tool", ToolCallID: r.ID, Content: r.Content})
		}
	} else {
		s.messages = append(s.messages, chatMessage{Role: "user", Content: in.Text})
	}

	reqBody, _ := json.Marshal(chatRequest{
		Model:     s.model,
		Messages:  s.messages,
		Tools:     s.tools,
		MaxTokens: s.maxTokens,
	})

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.prov.base+"/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if s.prov.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+s.prov.apiKey)
	}

	resp, err := s.prov.hc.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai: %s: %s", resp.Status, truncate(body, 300))
	}

	var cr chatResponse
	if err := json.Unmarshal(body, &cr); err != nil {
		return nil, fmt.Errorf("openai: decode: %w", err)
	}
	if cr.Error != nil {
		return nil, fmt.Errorf("openai: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return nil, fmt.Errorf("openai: no choices in response")
	}

	msg := cr.Choices[0].Message
	msg.Role = "assistant" // normalize before appending to history
	s.messages = append(s.messages, msg)

	out := &provider.Response{
		Text:       msg.Content,
		StopReason: normStop(cr.Choices[0].FinishReason),
		Usage: provider.Usage{
			InputTokens:  cr.Usage.PromptTokens,
			OutputTokens: cr.Usage.CompletionTokens,
		},
	}
	for _, tc := range msg.ToolCalls {
		args := tc.Function.Arguments
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		out.ToolCalls = append(out.ToolCalls, provider.ToolCall{
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: json.RawMessage(args),
		})
	}
	return out, nil
}

func convertTools(defs []provider.ToolDef) []toolSpec {
	out := make([]toolSpec, 0, len(defs))
	for _, d := range defs {
		var t toolSpec
		t.Type = "function"
		t.Function.Name = d.Name
		t.Function.Description = d.Description
		t.Function.Parameters = map[string]any{
			"type":       "object",
			"properties": d.Properties,
			"required":   d.Required,
		}
		out = append(out, t)
	}
	return out
}

func normStop(r string) string {
	switch r {
	case "stop":
		return provider.StopEndTurn
	case "tool_calls":
		return provider.StopToolUse
	case "length":
		return provider.StopMaxTokens
	default:
		return provider.StopOther
	}
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
