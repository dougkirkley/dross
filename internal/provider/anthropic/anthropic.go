// Package anthropic is the Anthropic backend for the provider registry. Import
// it for side effects to register the "anthropic" provider:
//
//	import _ "github.com/dougkirkley/holocron/internal/provider/anthropic"
package anthropic

import (
	"context"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/dougkirkley/holocron/internal/provider"
)

func init() {
	provider.Register("anthropic", func(cfg provider.Config) (provider.Provider, error) {
		var opts []option.RequestOption
		if cfg.APIKey != "" {
			opts = append(opts, option.WithAPIKey(cfg.APIKey))
		}
		if cfg.BaseURL != "" {
			opts = append(opts, option.WithBaseURL(cfg.BaseURL))
		}
		// Zero-arg client also resolves an `ant auth login` profile, so a
		// missing APIKey is not an error here.
		return &prov{client: sdk.NewClient(opts...)}, nil
	})
}

const defaultMaxTokens = 16000

type prov struct {
	client sdk.Client
}

func (p *prov) Name() string { return "anthropic" }

func (p *prov) NewSession(cfg provider.SessionConfig) provider.Session {
	maxTok := cfg.MaxTokens
	if maxTok <= 0 {
		maxTok = defaultMaxTokens
	}
	return &session{
		client:    p.client,
		model:     cfg.Model,
		system:    cfg.System,
		tools:     convertTools(cfg.Tools),
		maxTokens: int64(maxTok),
	}
}

type session struct {
	client    sdk.Client
	model     string
	system    string
	tools     []sdk.ToolUnionParam
	maxTokens int64
	messages  []sdk.MessageParam // provider-native history
}

func (s *session) Send(ctx context.Context, in provider.Input) (*provider.Response, error) {
	// Append the user turn: tool results if present, else free text.
	if len(in.ToolResults) > 0 {
		blocks := make([]sdk.ContentBlockParamUnion, 0, len(in.ToolResults))
		for _, r := range in.ToolResults {
			blocks = append(blocks, sdk.NewToolResultBlock(r.ID, r.Content, r.IsError))
		}
		s.messages = append(s.messages, sdk.NewUserMessage(blocks...))
	} else {
		s.messages = append(s.messages, sdk.NewUserMessage(sdk.NewTextBlock(in.Text)))
	}

	params := sdk.MessageNewParams{
		Model:     sdk.Model(s.model),
		MaxTokens: s.maxTokens,
		Messages:  s.messages,
		Tools:     s.tools,
	}
	if s.system != "" {
		// Cache the (stable) system prompt so repeated turns pay ~0.1x on it.
		params.System = []sdk.TextBlockParam{{
			Text:         s.system,
			CacheControl: sdk.NewCacheControlEphemeralParam(),
		}}
	}

	resp, err := s.client.Messages.New(ctx, params)
	if err != nil {
		return nil, err
	}
	// Append the assistant response verbatim so tool_use blocks are preserved
	// for the next turn.
	s.messages = append(s.messages, resp.ToParam())

	out := &provider.Response{
		StopReason: normStop(resp.StopReason),
		Usage: provider.Usage{
			InputTokens:      resp.Usage.InputTokens,
			OutputTokens:     resp.Usage.OutputTokens,
			CacheReadTokens:  resp.Usage.CacheReadInputTokens,
			CacheWriteTokens: resp.Usage.CacheCreationInputTokens,
		},
	}
	for _, block := range resp.Content {
		switch v := block.AsAny().(type) {
		case sdk.TextBlock:
			out.Text += v.Text
		case sdk.ToolUseBlock:
			out.ToolCalls = append(out.ToolCalls, provider.ToolCall{
				ID:    v.ID,
				Name:  v.Name,
				Input: []byte(v.JSON.Input.Raw()),
			})
		}
	}
	return out, nil
}

func convertTools(defs []provider.ToolDef) []sdk.ToolUnionParam {
	out := make([]sdk.ToolUnionParam, 0, len(defs))
	for _, d := range defs {
		t := sdk.ToolParam{
			Name:        d.Name,
			Description: sdk.String(d.Description),
			InputSchema: sdk.ToolInputSchemaParam{
				Properties: d.Properties,
				Required:   d.Required,
			},
		}
		out = append(out, sdk.ToolUnionParam{OfTool: &t})
	}
	return out
}

func normStop(r sdk.StopReason) string {
	switch r {
	case sdk.StopReasonEndTurn:
		return provider.StopEndTurn
	case sdk.StopReasonToolUse:
		return provider.StopToolUse
	case sdk.StopReasonMaxTokens:
		return provider.StopMaxTokens
	default:
		return provider.StopOther
	}
}
