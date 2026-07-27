package agent

import "github.com/dougkirkley/holocron/internal/provider"

// pricing is USD per 1,000,000 tokens. cacheRead is the discounted rate for
// cache-read input; cacheWrite is the 5-minute-TTL write premium (1.25x input).
// The whole cost-routing thesis rides on making this legible, so it is a plain
// table, updated when prices change.
type pricing struct {
	in, out, cacheRead, cacheWrite float64
}

var priceTable = map[string]pricing{
	// Anthropic (first-party rates).
	"claude-opus-5":    {in: 5.0, out: 25.0, cacheRead: 0.5, cacheWrite: 6.25},
	"claude-sonnet-5":  {in: 3.0, out: 15.0, cacheRead: 0.3, cacheWrite: 3.75},
	"claude-haiku-4-5": {in: 1.0, out: 5.0, cacheRead: 0.1, cacheWrite: 1.25},
	// Local models are free after the hardware.
	"qwen3-coder:30b": {},
}

// Cost estimates the USD cost of usage for the given model. Unknown models cost
// 0 (treated as free/local) rather than guessing.
func Cost(model string, u provider.Usage) float64 {
	p, ok := priceTable[model]
	if !ok {
		return 0
	}
	perM := func(tokens int64, rate float64) float64 {
		return float64(tokens) / 1_000_000 * rate
	}
	return perM(u.InputTokens, p.in) +
		perM(u.OutputTokens, p.out) +
		perM(u.CacheReadTokens, p.cacheRead) +
		perM(u.CacheWriteTokens, p.cacheWrite)
}
