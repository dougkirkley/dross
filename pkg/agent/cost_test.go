package agent

import (
	"math"
	"testing"

	"github.com/dougkirkley/dross/pkg/provider"
)

func TestCost(t *testing.T) {
	u := provider.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000, CacheReadTokens: 1_000_000}

	// Opus 5: $5 in + $25 out + $0.50 cache-read per 1M.
	if got := Cost("claude-opus-5", u); math.Abs(got-30.5) > 1e-9 {
		t.Fatalf("opus-5 cost = %v, want 30.5", got)
	}
	// Local model is free.
	if got := Cost("qwen3-coder:30b", u); got != 0 {
		t.Fatalf("local cost = %v, want 0", got)
	}
	// Unknown model treated as free rather than guessed.
	if got := Cost("some-unknown-model", u); got != 0 {
		t.Fatalf("unknown cost = %v, want 0", got)
	}
}

func TestCost_Zero(t *testing.T) {
	if Cost("claude-opus-5", provider.Usage{}) != 0 {
		t.Fatal("zero usage should cost 0")
	}
}
