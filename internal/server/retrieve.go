package server

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dougkirkley/dross/pkg/agent"
)

// Retrieval before the model, rather than if the model asks.
//
// Asking a local model to decide whether to search produces two failures. It
// often does not — a code-tuned model answers from memory because that is what
// it is for — and when it does not, it fills the gap with a citation it made up,
// which is the worst of both. Searching first removes the decision: the answer
// is written from results that exist, and the reader gets the same list the
// model was given.
//
// The cost is a search on turns that did not need one, which is why there is a
// guard below rather than an unconditional call.

// retrievalTimeout bounds the wait before the model starts. A search that is
// slow is worse than one that is missing: the person is watching a spinner
// either way, and without a bound they are watching it for the search *and*
// then the model.
const retrievalTimeout = 12 * time.Second

// retrieveFor searches the web for a chat prompt and returns the context block
// to give the model plus the sources behind it. Both are empty when the prompt
// does not warrant a search or the search finds nothing.
func (m *Manager) retrieveFor(prompt string) (string, []agent.Source) {
	if !worthSearching(prompt) {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), retrievalTimeout)
	defer cancel()

	// The open web plus the places worth asking directly. Reddit for anything
	// people argue about, and for a buying question the two that are actually
	// read before a purchase. Run together, because four searches in sequence is
	// four seconds somebody spends watching a spinner.
	queries := []string{prompt, prompt + " reddit"}
	if looksLikeShopping(prompt) {
		queries = append(queries, prompt+" amazon", prompt+" wirecutter")
	}

	type found struct {
		text    string
		sources []agent.Source
	}
	out := make([]found, len(queries))
	var wg sync.WaitGroup
	for i, q := range queries {
		wg.Add(1)
		go func(i int, q string) {
			defer wg.Done()
			text, src, err := agent.SearchWeb(ctx, q)
			if err != nil {
				return
			}
			out[i] = found{text: text, sources: src}
		}(i, q)
	}
	wg.Wait()

	var b strings.Builder
	var sources []agent.Source
	for i, f := range out {
		if strings.TrimSpace(f.text) == "" {
			continue
		}
		fmt.Fprintf(&b, "Results for %q:\n%s\n\n", queries[i], f.text)
		sources = append(sources, f.sources...)
	}
	results := strings.TrimSpace(b.String())
	// Only the text is required. Sources make the answer checkable and their
	// absence is worth fixing, but bailing without them meant a backend that
	// searched fine and recorded nothing switched retrieval off entirely -
	// which is exactly what happened when the Exa key started working.
	if results == "" {
		// A failed search is not worth surfacing here. The model still answers,
		// it simply answers without help, and saying "the search failed" in the
		// middle of someone's question is noise they cannot act on.
		return "", nil
	}
	block := fmt.Sprintf(`Web results retrieved just now for %q:

%s

Use these to answer. They are current; your training data is not. Cite the ones
you use, and say so if they do not actually answer the question rather than
filling the gap from memory.`, prompt, results)
	return block, sources
}

// looksLikeShopping reports whether a prompt is about buying or choosing
// something, which is when Amazon and Wirecutter are worth asking and when they
// are otherwise noise. Nobody troubleshooting a database wants a product page,
// and results that do not belong make an answer worse, not longer.
func looksLikeShopping(prompt string) bool {
	p := strings.ToLower(prompt)
	for _, w := range []string{
		"best ", "buy", "worth it", "worth buying", "should i get", "recommend",
		" vs ", "versus", "review", "cheapest", "cheap ", "price", "deal",
		"which one", "alternatives to", "instead of",
	} {
		if strings.Contains(p, w) {
			return true
		}
	}
	return false
}

// worthSearching decides whether a message is a question about the world.
//
// Deliberately generous — searching is cheap and being wrong in that direction
// costs a second, while being wrong the other way costs the whole feature. What
// it skips is the small set that is plainly conversational: an acknowledgement,
// or a follow-up that refers back to what was just said and would search for
// the wrong thing anyway.
func worthSearching(prompt string) bool {
	p := strings.ToLower(strings.TrimSpace(prompt))
	if p == "" {
		return false
	}
	// Short acknowledgements. Length alone is not the test — "who is the PM?"
	// is short and very much a question — so this is matched, not measured.
	for _, ack := range []string{
		"thanks", "thank you", "ta", "cheers", "ok", "okay", "k", "got it",
		"nice", "cool", "great", "perfect", "yes", "no", "yep", "nope", "sure",
	} {
		if p == ack || p == ack+"!" || p == ack+"." {
			return false
		}
	}
	// A follow-up about the conversation rather than about the world. Searching
	// the literal text would look up the wrong thing: "explain that again" is
	// not a query anybody means.
	for _, ref := range []string{
		"explain that", "say that again", "what do you mean", "what did you mean",
		"rephrase", "simpler", "shorter", "in short", "summarize that",
		"continue", "go on", "keep going",
	} {
		if strings.HasPrefix(p, ref) {
			return false
		}
	}
	return true
}
