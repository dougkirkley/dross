package agent

import "github.com/dougkirkley/dross/pkg/provider"

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

// aliases are the short names the Claude Code CLI takes for a model, mapped to
// the id the price table is keyed by.
//
// Without these, a session run as --model opus priced at zero. "Unknown means
// free" is the right default for a local model and exactly the wrong one for
// the most expensive model on the list: a benchmark comparing dross-through-
// Claude-Code against Claude Code itself reported $0.0000 against $1.27, which
// reads as a saving and was a missing table entry.
var aliases = map[string]string{
	"opus":   "claude-opus-5",
	"sonnet": "claude-sonnet-5",
	"haiku":  "claude-haiku-4-5",
}

// Cost estimates the USD cost of usage for the given model. Unknown models cost
// 0 (treated as free/local) rather than guessing.
func Cost(model string, u provider.Usage) float64 {
	if id, ok := aliases[model]; ok {
		model = id
	}
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
