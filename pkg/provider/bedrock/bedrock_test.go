package bedrock

import (
	"testing"

	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"github.com/dougkirkley/dross/pkg/provider"
)

func TestIsAnthropicModel(t *testing.T) {
	claude := []string{"us.anthropic.claude-sonnet-4-5-20250929-v1:0", "anthropic.claude-3-haiku"}
	other := []string{"us.amazon.nova-pro-v1:0", "us.meta.llama3-3-70b-instruct-v1:0", "mistral.mistral-large-2407-v1:0"}
	for _, m := range claude {
		if !isAnthropicModel(m) {
			t.Errorf("%q should route to the anthropic path", m)
		}
	}
	for _, m := range other {
		if isAnthropicModel(m) {
			t.Errorf("%q should route to Converse", m)
		}
	}
}

func TestConvertConverseTools(t *testing.T) {
	tools := convertConverseTools([]provider.ToolDef{{
		Name: "read", Description: "read a file",
		Properties: map[string]any{"path": map[string]any{"type": "string"}},
		Required:   []string{"path"},
	}})
	if len(tools) != 1 {
		t.Fatalf("want 1 tool, got %d", len(tools))
	}
	spec, ok := tools[0].(*brtypes.ToolMemberToolSpec)
	if !ok || spec.Value.Name == nil || *spec.Value.Name != "read" {
		t.Fatalf("tool spec wrong: %+v", tools[0])
	}
	if _, ok := spec.Value.InputSchema.(*brtypes.ToolInputSchemaMemberJson); !ok {
		t.Fatalf("input schema should be JSON member")
	}
}

func TestMapConverseStop(t *testing.T) {
	if mapConverseStop(brtypes.StopReasonEndTurn, 1) != provider.StopToolUse {
		t.Fatal("tool calls -> tool_use")
	}
	if mapConverseStop(brtypes.StopReasonEndTurn, 0) != provider.StopEndTurn {
		t.Fatal("end_turn")
	}
	if mapConverseStop(brtypes.StopReasonMaxTokens, 0) != provider.StopMaxTokens {
		t.Fatal("max_tokens")
	}
}

func TestBedrockRegistered(t *testing.T) {
	names := provider.Names()
	found := false
	for _, n := range names {
		if n == "bedrock" {
			found = true
		}
	}
	if !found {
		t.Fatalf("bedrock not registered; have %v", names)
	}
}

func TestBedrockNewReturnsNamedProvider(t *testing.T) {
	p, err := provider.New("bedrock", provider.Config{})
	if err != nil {
		t.Fatalf("New(bedrock): %v", err)
	}
	if p.Name() != "bedrock" {
		t.Fatalf("Name() = %q, want bedrock", p.Name())
	}
}

func TestFixModelRegion(t *testing.T) {
	cases := []struct{ model, region, want string }{
		{"us.amazon.nova-pro-v1:0", "eu-west-1", "eu.amazon.nova-pro-v1:0"},
		{"us.amazon.nova-pro-v1:0", "us-east-1", "us.amazon.nova-pro-v1:0"},
		{"us.amazon.nova-pro-v1:0", "ap-southeast-2", "apac.amazon.nova-pro-v1:0"},
		{"amazon.nova-pro-v1:0", "eu-west-1", "amazon.nova-pro-v1:0"}, // no prefix, unchanged
		{"us.amazon.nova-pro-v1:0", "", "us.amazon.nova-pro-v1:0"},    // unknown region, unchanged
	}
	for _, c := range cases {
		if got := fixModelRegion(c.model, c.region); got != c.want {
			t.Errorf("fixModelRegion(%q,%q) = %q, want %q", c.model, c.region, got, c.want)
		}
	}
}
