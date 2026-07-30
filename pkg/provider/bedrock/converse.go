package bedrock

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	brdoc "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"github.com/dougkirkley/dross/pkg/provider"
)

// isAnthropicModel reports whether a Bedrock model id is a Claude model, which
// the anthropic-over-bedrock path handles (with prompt caching). Everything else
// (Llama, Nova, Mistral, Cohere, …) goes through the unified Converse API.
func isAnthropicModel(model string) bool {
	m := strings.ToLower(model)
	return strings.Contains(m, "anthropic.") || strings.Contains(m, "claude")
}

const converseMaxTokens = 8192

// converseSession talks to any Bedrock model via the Converse API.
type converseSession struct {
	brc      *bedrockruntime.Client
	model    string
	region   string
	system   []brtypes.SystemContentBlock
	toolCfg  *brtypes.ToolConfiguration
	maxTok   int32
	messages []brtypes.Message
}

// geoPrefix maps an AWS region to the cross-region inference-profile prefix.
func geoPrefix(region string) string {
	switch {
	case strings.HasPrefix(region, "us-gov-"):
		return "us-gov"
	case strings.HasPrefix(region, "us-"):
		return "us"
	case strings.HasPrefix(region, "eu-"):
		return "eu"
	case strings.HasPrefix(region, "ap-"):
		return "apac"
	default:
		return ""
	}
}

// fixModelRegion rewrites a us./eu./apac. inference-profile prefix to match the
// client's region, so selecting "us.amazon.nova-pro-v1:0" while in eu-west-1
// becomes "eu.amazon.nova-pro-v1:0" instead of failing as an invalid id.
func fixModelRegion(model, region string) string {
	geo := geoPrefix(region)
	if geo == "" {
		return model
	}
	for _, p := range []string{"us-gov.", "us.", "eu.", "apac."} {
		if strings.HasPrefix(model, p) {
			if p != geo+"." {
				return geo + "." + strings.TrimPrefix(model, p)
			}
			return model
		}
	}
	return model
}

func newConverseSession(brc *bedrockruntime.Client, region string, cfg provider.SessionConfig) provider.Session {
	maxTok := int32(cfg.MaxTokens)
	if maxTok <= 0 {
		maxTok = converseMaxTokens
	}
	s := &converseSession{brc: brc, region: region, model: fixModelRegion(cfg.Model, region), maxTok: maxTok}
	if strings.TrimSpace(cfg.System) != "" {
		s.system = []brtypes.SystemContentBlock{&brtypes.SystemContentBlockMemberText{Value: cfg.System}}
	}
	if len(cfg.Tools) > 0 {
		s.toolCfg = &brtypes.ToolConfiguration{Tools: convertConverseTools(cfg.Tools)}
	}
	return s
}

func (s *converseSession) Send(ctx context.Context, in provider.Input) (*provider.Response, error) {
	switch {
	case len(in.ToolResults) > 0:
		var blocks []brtypes.ContentBlock
		for _, r := range in.ToolResults {
			status := brtypes.ToolResultStatusSuccess
			if r.IsError {
				status = brtypes.ToolResultStatusError
			}
			blocks = append(blocks, &brtypes.ContentBlockMemberToolResult{Value: brtypes.ToolResultBlock{
				ToolUseId: aws.String(r.ID),
				Status:    status,
				Content:   []brtypes.ToolResultContentBlock{&brtypes.ToolResultContentBlockMemberText{Value: r.Content}},
			}})
		}
		s.messages = append(s.messages, brtypes.Message{Role: brtypes.ConversationRoleUser, Content: blocks})
	case strings.TrimSpace(in.Text) != "":
		s.messages = append(s.messages, brtypes.Message{
			Role:    brtypes.ConversationRoleUser,
			Content: []brtypes.ContentBlock{&brtypes.ContentBlockMemberText{Value: in.Text}},
		})
	default:
		return &provider.Response{StopReason: provider.StopEndTurn}, nil
	}

	out, err := s.brc.Converse(ctx, &bedrockruntime.ConverseInput{
		ModelId:         aws.String(s.model),
		Messages:        s.messages,
		System:          s.system,
		ToolConfig:      s.toolCfg,
		InferenceConfig: &brtypes.InferenceConfiguration{MaxTokens: aws.Int32(s.maxTok)},
	})
	if err != nil {
		// "invalid model identifier" almost always means the id isn't available
		// in this AWS region / account — a us.* inference profile needs a us-*
		// region and the model enabled in Bedrock ("Model access").
		if strings.Contains(err.Error(), "model identifier is invalid") {
			region := s.region
			if region == "" {
				region = "unset"
			}
			return nil, fmt.Errorf("bedrock converse: model %q not available in region %s — enable it under Bedrock → Model access in that region, or run `aws bedrock list-inference-profiles` to see valid ids: %w", s.model, region, err)
		}
		return nil, fmt.Errorf("bedrock converse: %w", err)
	}

	msgOut, ok := out.Output.(*brtypes.ConverseOutputMemberMessage)
	if !ok {
		return &provider.Response{StopReason: provider.StopEndTurn}, nil
	}
	s.messages = append(s.messages, msgOut.Value) // keep the assistant turn

	resp := &provider.Response{}
	if out.Usage != nil {
		resp.Usage = provider.Usage{
			InputTokens:  int64(aws.ToInt32(out.Usage.InputTokens)),
			OutputTokens: int64(aws.ToInt32(out.Usage.OutputTokens)),
		}
	}
	for _, block := range msgOut.Value.Content {
		switch b := block.(type) {
		case *brtypes.ContentBlockMemberText:
			resp.Text += b.Value
		case *brtypes.ContentBlockMemberToolUse:
			input := json.RawMessage("{}")
			if b.Value.Input != nil {
				if raw, err := b.Value.Input.MarshalSmithyDocument(); err == nil && len(raw) > 0 {
					input = raw
				}
			}
			resp.ToolCalls = append(resp.ToolCalls, provider.ToolCall{
				ID:    aws.ToString(b.Value.ToolUseId),
				Name:  aws.ToString(b.Value.Name),
				Input: input,
			})
		}
	}
	resp.StopReason = mapConverseStop(out.StopReason, len(resp.ToolCalls))
	return resp, nil
}

func mapConverseStop(reason brtypes.StopReason, nCalls int) string {
	if nCalls > 0 {
		return provider.StopToolUse
	}
	switch reason {
	case brtypes.StopReasonEndTurn, brtypes.StopReasonStopSequence:
		return provider.StopEndTurn
	case brtypes.StopReasonMaxTokens:
		return provider.StopMaxTokens
	default:
		return provider.StopOther
	}
}

func convertConverseTools(defs []provider.ToolDef) []brtypes.Tool {
	tools := make([]brtypes.Tool, 0, len(defs))
	for _, d := range defs {
		schema := map[string]any{"type": "object"}
		if len(d.Properties) > 0 {
			schema["properties"] = d.Properties
		}
		if len(d.Required) > 0 {
			schema["required"] = d.Required
		}
		spec := brtypes.ToolSpecification{
			Name:        aws.String(d.Name),
			InputSchema: &brtypes.ToolInputSchemaMemberJson{Value: brdoc.NewLazyDocument(schema)},
		}
		if d.Description != "" {
			spec.Description = aws.String(d.Description)
		}
		tools = append(tools, &brtypes.ToolMemberToolSpec{Value: spec})
	}
	return tools
}
