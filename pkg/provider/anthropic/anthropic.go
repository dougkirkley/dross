// Package anthropic is the Anthropic backend for the provider registry. Import
// it for side effects to register the "anthropic" provider:
//
//	import _ "github.com/dougkirkley/dross/pkg/provider/anthropic"
package anthropic

import (
	"context"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/dougkirkley/dross/pkg/provider"
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
		return NewWithClient(sdk.NewClient(opts...), "anthropic"), nil
	})
}

const defaultMaxTokens = 16000

// NewWithClient wraps an already-constructed Anthropic SDK client as a provider
// under the given name. This lets other backends that speak the Anthropic
// Messages API over a different transport (e.g. AWS Bedrock) reuse the exact
// same session logic instead of duplicating it.
func NewWithClient(client sdk.Client, name string) provider.Provider {
	return &prov{client: client, name: name}
}

// NewWithClientPinned is like NewWithClient but prepends pin as the first system
// block on every request. Some auth scopes (e.g. subscription OAuth) require a
// specific first system block; the caller supplies it.
func NewWithClientPinned(client sdk.Client, name, pin string) provider.Provider {
	return &prov{client: client, name: name, pin: pin}
}

type prov struct {
	client sdk.Client
	name   string
	pin    string
}

func (p *prov) Name() string { return p.name }

func (p *prov) NewSession(cfg provider.SessionConfig) provider.Session {
	maxTok := cfg.MaxTokens
	if maxTok <= 0 {
		maxTok = defaultMaxTokens
	}
	return &session{
		client:       p.client,
		model:        cfg.Model,
		system:       cfg.System,
		pin:          p.pin,
		tools:        convertTools(cfg.Tools),
		maxTokens:    int64(maxTok),
		lastCacheIdx: -1,
	}
}

type session struct {
	client       sdk.Client
	model        string
	system       string
	pin          string
	tools        []sdk.ToolUnionParam
	maxTokens    int64
	messages     []sdk.MessageParam // provider-native history
	lastCacheIdx int                // message holding the rolling cache breakpoint
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

	// Roll the conversation cache breakpoint onto the newest user turn (a stable
	// input — text or tool results, never a shifting assistant output), clearing
	// the previous one so we stay within Anthropic's 4-breakpoint limit. Combined
	// with the cached system + tools prefix, each turn re-reads the whole prefix
	// at ~0.1x.
	if s.lastCacheIdx >= 0 && s.lastCacheIdx < len(s.messages) {
		setBlockCache(&s.messages[s.lastCacheIdx], sdk.CacheControlEphemeralParam{})
	}
	setBlockCache(&s.messages[len(s.messages)-1], sdk.NewCacheControlEphemeralParam())
	s.lastCacheIdx = len(s.messages) - 1

	params := sdk.MessageNewParams{
		Model:     sdk.Model(s.model),
		MaxTokens: s.maxTokens,
		Messages:  s.messages,
		Tools:     s.tools,
	}
	var sys []sdk.TextBlockParam
	if s.pin != "" {
		sys = append(sys, sdk.TextBlockParam{Text: s.pin})
	}
	if s.system != "" {
		// Cache the (stable) system prompt so repeated turns pay ~0.1x on it.
		sys = append(sys, sdk.TextBlockParam{
			Text:         s.system,
			CacheControl: sdk.NewCacheControlEphemeralParam(),
		})
	}
	if len(sys) > 0 {
		params.System = sys
	}

	resp, err := s.send(ctx, params, in.OnText)
	if err != nil {
		return nil, err
	}
	// Append the assistant response verbatim so tool_use blocks are preserved
	// for the next turn.
	s.messages = append(s.messages, resp.ToParam())
	return shape(resp), nil
}

// send performs one request, streaming when the caller wants to watch.
//
// Both paths end with the same *sdk.Message, so everything downstream — the
// history append, the block extraction, the usage — is shared and cannot drift
// between them. Streaming exists for perceived latency only: a whole generation
// buffered is a long silence followed by a wall of text, which reads as a hang
// and then a dump.
func (s *session) send(ctx context.Context, params sdk.MessageNewParams, onText func(string)) (*sdk.Message, error) {
	if onText == nil {
		return s.client.Messages.New(ctx, params)
	}
	stream := s.client.Messages.NewStreaming(ctx, params)
	var msg sdk.Message
	for stream.Next() {
		event := stream.Current()
		// Accumulate rather than hand-assembling: a tool call's input arrives as
		// a run of partial-JSON fragments, and only the accumulator knows when
		// they add up to something parseable.
		if err := msg.Accumulate(event); err != nil {
			return nil, err
		}
		if d, ok := event.AsAny().(sdk.ContentBlockDeltaEvent); ok {
			if t, ok := d.Delta.AsAny().(sdk.TextDelta); ok && t.Text != "" {
				onText(t.Text)
			}
		}
	}
	if err := stream.Err(); err != nil {
		return nil, err
	}
	return &msg, nil
}

// shape converts a completed message into the provider-agnostic response.
func shape(resp *sdk.Message) *provider.Response {
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
	return out
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
	// Cache the (stable) tool definitions: a cache_control on the last tool marks
	// the whole tools + system prefix as cacheable, so repeated turns pay ~0.1x
	// on it.
	if n := len(out); n > 0 && out[n-1].OfTool != nil {
		out[n-1].OfTool.CacheControl = sdk.NewCacheControlEphemeralParam()
	}
	return out
}

// setBlockCache sets (or, with a zero value, clears) cache_control on the last
// content block of a message.
func setBlockCache(msg *sdk.MessageParam, cc sdk.CacheControlEphemeralParam) {
	if len(msg.Content) == 0 {
		return
	}
	b := &msg.Content[len(msg.Content)-1]
	switch {
	case b.OfText != nil:
		b.OfText.CacheControl = cc
	case b.OfToolResult != nil:
		b.OfToolResult.CacheControl = cc
	}
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
