// Package azure is a backend for Azure OpenAI. It speaks the OpenAI
// chat-completions schema, but against Azure's per-deployment URL
// (…/openai/deployments/{deployment}/chat/completions?api-version=…) with the
// api-key header. The "model" is the Azure deployment name. Import for side
// effects to register:
//
//	import _ "github.com/dougkirkley/dross/pkg/provider/azure"
//
// Endpoint, key, and api-version come from Config or the env: AZURE_OPENAI_ENDPOINT
// (e.g. https://my-resource.openai.azure.com), AZURE_OPENAI_API_KEY, and
// AZURE_OPENAI_API_VERSION (default 2024-10-21).
package azure

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/dougkirkley/dross/pkg/provider"
)

const (
	defaultAPIVersion = "2024-10-21"
	defaultMaxTokens  = 16000
)

func init() {
	provider.Register("azure", func(cfg provider.Config) (provider.Provider, error) {
		base := cfg.BaseURL
		if base == "" {
			base = os.Getenv("AZURE_OPENAI_ENDPOINT")
		}
		if base == "" {
			return nil, fmt.Errorf("azure: set AZURE_OPENAI_ENDPOINT (https://<resource>.openai.azure.com)")
		}
		key := cfg.APIKey
		if key == "" {
			key = os.Getenv("AZURE_OPENAI_API_KEY")
		}
		ver := os.Getenv("AZURE_OPENAI_API_VERSION")
		if ver == "" {
			ver = defaultAPIVersion
		}
		// Normalize to the resource root (strip a trailing /openai or /).
		root := strings.TrimRight(base, "/")
		root = strings.TrimSuffix(root, "/openai")
		return &prov{root: root, apiKey: key, apiVersion: ver, hc: &http.Client{}}, nil
	})
}

type prov struct {
	root       string
	apiKey     string
	apiVersion string
	hc         *http.Client
}

func (p *prov) Name() string { return "azure" }

func (p *prov) NewSession(cfg provider.SessionConfig) provider.Session {
	maxTok := cfg.MaxTokens
	if maxTok <= 0 {
		maxTok = defaultMaxTokens
	}
	s := &session{prov: p, model: cfg.Model, tools: convertTools(cfg.Tools), maxTok: maxTok}
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
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type toolDef struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description,omitempty"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

type chatReq struct {
	Messages  []chatMessage `json:"messages"`
	Tools     []toolDef     `json:"tools,omitempty"`
	MaxTokens int           `json:"max_tokens,omitempty"`
}

type chatResp struct {
	Choices []struct {
		Message struct {
			Content   string     `json:"content"`
			ToolCalls []toolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type session struct {
	prov     *prov
	model    string // Azure deployment name
	tools    []toolDef
	maxTok   int
	messages []chatMessage
}

func (s *session) Send(ctx context.Context, in provider.Input) (*provider.Response, error) {
	switch {
	case len(in.ToolResults) > 0:
		for _, r := range in.ToolResults {
			s.messages = append(s.messages, chatMessage{Role: "tool", ToolCallID: r.ID, Content: r.Content})
		}
	case strings.TrimSpace(in.Text) != "":
		s.messages = append(s.messages, chatMessage{Role: "user", Content: in.Text})
	default:
		return &provider.Response{StopReason: provider.StopEndTurn}, nil
	}

	reqBody, err := json.Marshal(chatReq{Messages: s.messages, Tools: s.tools, MaxTokens: s.maxTok})
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=%s", s.prov.root, s.model, s.prov.apiVersion)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("api-key", s.prov.apiKey)

	resp, err := s.prov.hc.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var cr chatResp
	if err := json.Unmarshal(raw, &cr); err != nil {
		return nil, fmt.Errorf("azure: bad response (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if cr.Error != nil {
		return nil, fmt.Errorf("azure: %s", cr.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("azure: http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if len(cr.Choices) == 0 {
		return &provider.Response{StopReason: provider.StopEndTurn}, nil
	}

	ch := cr.Choices[0]
	// Record the assistant turn (with any tool calls) so tool results attach.
	s.messages = append(s.messages, chatMessage{Role: "assistant", Content: ch.Message.Content, ToolCalls: ch.Message.ToolCalls})

	out := &provider.Response{
		Text:  ch.Message.Content,
		Usage: provider.Usage{InputTokens: cr.Usage.PromptTokens, OutputTokens: cr.Usage.CompletionTokens},
	}
	for _, tc := range ch.Message.ToolCalls {
		args := tc.Function.Arguments
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		out.ToolCalls = append(out.ToolCalls, provider.ToolCall{ID: tc.ID, Name: tc.Function.Name, Input: json.RawMessage(args)})
	}
	out.StopReason = mapFinish(ch.FinishReason, len(out.ToolCalls))
	return out, nil
}

func mapFinish(reason string, nCalls int) string {
	if nCalls > 0 {
		return provider.StopToolUse
	}
	switch reason {
	case "stop":
		return provider.StopEndTurn
	case "length":
		return provider.StopMaxTokens
	default:
		return provider.StopOther
	}
}

func convertTools(defs []provider.ToolDef) []toolDef {
	out := make([]toolDef, 0, len(defs))
	for _, d := range defs {
		var td toolDef
		td.Type = "function"
		td.Function.Name = d.Name
		td.Function.Description = d.Description
		params := map[string]any{"type": "object", "properties": map[string]any{}}
		if len(d.Properties) > 0 {
			params["properties"] = d.Properties
		}
		if len(d.Required) > 0 {
			params["required"] = d.Required
		}
		td.Function.Parameters = params
		out = append(out, td)
	}
	return out
}
