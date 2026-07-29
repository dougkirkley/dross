package cli

import "testing"

func TestDecodePatch(t *testing.T) {
	// Plain object.
	p, err := decodePatch([]byte(`{"theme":"ember","max_tokens":100}`))
	if err != nil || p["theme"] != "ember" {
		t.Fatalf("object patch: %v %v", p, err)
	}
	// Double-encoded: patch passed as a JSON string.
	p, err = decodePatch([]byte(`"{\"phrases\":[\"a\",\"b\"]}"`))
	if err != nil {
		t.Fatalf("stringified patch: %v", err)
	}
	if got, ok := p["phrases"].([]any); !ok || len(got) != 2 {
		t.Fatalf("stringified patch not parsed: %v", p)
	}
	// Junk.
	if _, err := decodePatch([]byte(`123`)); err == nil {
		t.Fatal("non-object/non-string patch should error")
	}
}
