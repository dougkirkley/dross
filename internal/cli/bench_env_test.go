package cli

import "testing"

// TestBenchSpecReadsTheEnvironment guards the failure that looked like a result.
//
// A --dross spec without base_url used to build a plain OpenAI client, so a
// benchmark pointed at a local endpoint by OPENAI_BASE_URL — the way every
// other part of dross is pointed at one — went to api.openai.com instead and
// reported "401 Unauthorized" for every task. That reads as an agent that
// cannot do the work, and it is a harness that never reached a model.
func TestBenchSpecReadsTheEnvironment(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "https://ollama.example/v1")
	t.Setenv("OPENAI_API_KEY", "from-env")

	spec, err := parseDrossSpec("provider=openai,model=qwen3:14b")
	if err != nil {
		t.Fatal(err)
	}
	base, key := spec.endpoint()
	if base != "https://ollama.example/v1" || key != "from-env" {
		t.Fatalf("endpoint = %q, %q; want the environment's", base, key)
	}
}

// The environment is a fallback, not an override: a spec written to measure one
// endpoint must not quietly measure another.
func TestBenchSpecPrefersWhatItWasGiven(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "https://ollama.example/v1")
	t.Setenv("OPENAI_API_KEY", "from-env")

	spec, err := parseDrossSpec("provider=openai,model=m,base_url=https://explicit/v1,api_key=explicit")
	if err != nil {
		t.Fatal(err)
	}
	base, key := spec.endpoint()
	if base != "https://explicit/v1" || key != "explicit" {
		t.Fatalf("endpoint = %q, %q; want the spec's", base, key)
	}
}
