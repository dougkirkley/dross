package anthropic

import (
	"testing"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/dougkirkley/holocron/internal/provider"
)

// convertTools should cache only the last tool (marks the whole tools+system
// prefix cacheable with a single breakpoint).
func TestConvertToolsCachesLastOnly(t *testing.T) {
	tools := convertTools([]provider.ToolDef{{Name: "a"}, {Name: "b"}})
	if got := string(tools[1].OfTool.CacheControl.Type); got != "ephemeral" {
		t.Fatalf("last tool cache = %q, want ephemeral", got)
	}
	if got := string(tools[0].OfTool.CacheControl.Type); got == "ephemeral" {
		t.Fatalf("only the last tool should be cached, tool[0] is too")
	}
}

// setBlockCache sets and clears cache_control on a message's last block.
func TestSetBlockCache(t *testing.T) {
	m := sdk.NewUserMessage(sdk.NewTextBlock("hi"))
	setBlockCache(&m, sdk.NewCacheControlEphemeralParam())
	if got := string(m.Content[0].OfText.CacheControl.Type); got != "ephemeral" {
		t.Fatalf("set: cache = %q, want ephemeral", got)
	}
	setBlockCache(&m, sdk.CacheControlEphemeralParam{})
	if got := string(m.Content[0].OfText.CacheControl.Type); got != "" {
		t.Fatalf("clear: cache = %q, want empty", got)
	}
}
