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

// maxQueries bounds how many searches one question turns into.
const maxQueries = 5

// retrieveFor searches the web for a chat prompt and returns the context block
// to give the model plus the sources behind it. Both are empty when the prompt
// does not warrant a search or the search finds nothing.
func (m *Manager) retrieveFor(runCtx context.Context, s *Session, prompt string) (string, []agent.Source) {
	if !worthSearching(prompt) {
		return "", nil
	}
	// Bounded, but derived from the run - so pressing Stop cancels the searches
	// instead of waiting them out.
	ctx, cancel := context.WithTimeout(runCtx, retrievalTimeout)
	defer cancel()

	// The open web plus the places worth asking directly. Reddit for anything
	// people argue about, and for a buying question the two that are actually
	// read before a purchase. Run together, because four searches in sequence is
	// four seconds somebody spends watching a spinner.
	queries := []string{prompt, prompt + " reddit"}
	if looksLikeShopping(prompt) {
		queries = append(queries, prompt+" amazon", prompt+" wirecutter")
	}
	// A ceiling, so adding a site here later is a decision rather than a drift.
	// Each query is a request, a share of the rate limit, and a second of
	// somebody's wait.
	if len(queries) > maxQueries {
		queries = queries[:maxQueries]
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
			// Say what is being looked up while it happens. A spinner that
			// names the thing it is waiting on is the difference between
			// "working" and "searching reddit for best mattresses" - the second
			// is the same wait and a fraction of the doubt.
			s.emit("search", q)
			text, src, err := agent.SearchWeb(ctx, q)
			if err != nil {
				return
			}
			out[i] = found{text: text, sources: src}
		}(i, q)
	}
	wg.Wait()
	// Searching is over; what follows is the model. Leaving the last search on
	// screen makes a two-minute wait look like a stuck search, which is the
	// opposite of what naming the search was for.
	s.emit("searched", "")

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

// contentWords strips the words that carry no query of their own, leaving what
// a search would actually be for.
func contentWords(p string) []string {
	filler := map[string]bool{
		"a": true, "an": true, "the": true, "is": true, "are": true, "was": true,
		"be": true, "to": true, "of": true, "for": true, "in": true, "on": true,
		"how": true, "what": true, "where": true, "which": true, "who": true,
		"why": true, "when": true, "do": true, "does": true, "did": true,
		"can": true, "could": true, "should": true, "would": true, "i": true,
		"me": true, "my": true, "you": true, "get": true, "much": true,
		"any": true, "some": true, "good": true, "and": true, "or": true,
		"about": true, "from": true, "with": true,
		// The referential ones: the whole point is that they stand for
		// something said earlier.
		"it": true, "them": true, "they": true, "that": true, "this": true,
		"those": true, "these": true, "one": true, "ones": true,
	}
	var out []string
	for _, w := range strings.Fields(strings.Map(func(r rune) rune {
		if r == '?' || r == '.' || r == ',' || r == '!' || r == '\'' {
			return ' '
		}
		return r
	}, p)) {
		if !filler[w] {
			out = append(out, w)
		}
	}
	return out
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
	// A question that leans on the last answer. "where to get them?" searched
	// literally looks up the word "them", which is nobody's question - the
	// meaning is in the conversation, and the model already has that.
	//
	// The test is not whether a pronoun appears: "is the framework laptop worth
	// it" ends in one and is a perfectly good query. It is whether anything is
	// left to search for once the filler is removed. A follow-up is a question
	// with its subject somewhere else.
	if content := contentWords(p); len(content) < 2 {
		return false
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
