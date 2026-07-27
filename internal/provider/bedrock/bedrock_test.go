package bedrock

import (
	"testing"

	"github.com/dougkirkley/holocron/internal/provider"
)

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
