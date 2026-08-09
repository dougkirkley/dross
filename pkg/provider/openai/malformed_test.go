package openai

import "testing"

// TestMalformedToolCallRecognisesEveryPhrasing is the rename-symbol failure.
//
// The detector matched only "parsing tool call". ollama rejects a bad
// qwen3-coder call with "XML syntax error on line 8: element <parameter>
// closed by </function>", which says nothing about tool calls at all — so it
// fell through to retryableStatus, and a 500 is retryable, so the same prompt
// went back three times and produced the same confusion each time before the
// run died. The repair path that would have fixed it in one turn was already
// written and simply never reached. rename-symbol scored 2/5 on this.
func TestMalformedToolCallRecognisesEveryPhrasing(t *testing.T) {
	for _, body := range []string{
		// The one that was already handled.
		`{"error":{"message":"error parsing tool call: unexpected token"}}`,
		// The one that cost three runs, verbatim from the bench.
		`{"error":{"message":"XML syntax error on line 8: element <parameter> closed by </function>","type":"api_error"}}`,
		`{"error":{"message":"invalid tool call syntax"}}`,
		`{"error":{"message":"failed to parse tool arguments"}}`,
		`{"error":{"message":"tool call parse failure"}}`,
		// Case is not something a server promises.
		`{"error":{"message":"XML Syntax Error in template output"}}`,
	} {
		if !malformedToolCall([]byte(body)) {
			t.Errorf("not recognised, so this run would retry and then die:\n  %s", body)
		}
	}
}

// The other half: a server that is genuinely unwell must stay retryable.
// Treating an overload as the model's fault would send a nudge to fix a tool
// call that was never wrong, and give up a retry that would have worked.
func TestMalformedToolCallLeavesRealFailuresAlone(t *testing.T) {
	for _, body := range []string{
		`{"error":{"message":"model requires more system memory than is available"}}`,
		`{"error":{"message":"server overloaded"}}`,
		`{"error":{"message":"context canceled"}}`,
		`{"error":{"message":"no such model"}}`,
		`{"error":{"message":"CUDA out of memory"}}`,
		``,
	} {
		if malformedToolCall([]byte(body)) {
			t.Errorf("misread as the model's fault, so it will not be retried:\n  %s", body)
		}
	}
}
