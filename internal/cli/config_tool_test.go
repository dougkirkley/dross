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

func TestDecodeSetArgs(t *testing.T) {
	// patch object.
	p, err := decodeSetArgs([]byte(`{"name":"x"}`), "", nil)
	if err != nil || p["name"] != "x" {
		t.Fatalf("patch form: %v %v", p, err)
	}
	// key + value form (what the model used).
	p, err = decodeSetArgs(nil, "name", []byte(`"dross"`))
	if err != nil || p["name"] != "dross" {
		t.Fatalf("key/value form: %v %v", p, err)
	}
	// key with no value errors.
	if _, err := decodeSetArgs(nil, "name", nil); err == nil {
		t.Fatal("key without value should error")
	}
	// nothing errors.
	if _, err := decodeSetArgs(nil, "", nil); err == nil {
		t.Fatal("empty set should error")
	}
}
