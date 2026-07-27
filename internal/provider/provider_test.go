package provider

import (
	"context"
	"testing"
)

type stubProvider struct{ name string }

func (s *stubProvider) Name() string                     { return s.name }
func (s *stubProvider) NewSession(SessionConfig) Session { return nil }

func TestRegistry(t *testing.T) {
	Register("stub-test", func(Config) (Provider, error) { return &stubProvider{"stub-test"}, nil })

	p, err := New("stub-test", Config{})
	if err != nil || p.Name() != "stub-test" {
		t.Fatalf("New: %v %v", p, err)
	}
	if _, err := New("does-not-exist", Config{}); err == nil {
		t.Fatal("expected error for unknown provider")
	}

	found := false
	for _, n := range Names() {
		if n == "stub-test" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Names() missing stub-test: %v", Names())
	}

	// Duplicate registration panics (driver semantics).
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("duplicate Register should panic")
			}
		}()
		Register("stub-test", func(Config) (Provider, error) { return nil, nil })
	}()
}

func TestUsageAdd(t *testing.T) {
	u := Usage{InputTokens: 1, OutputTokens: 2, CacheReadTokens: 3, CacheWriteTokens: 4}
	u.Add(Usage{InputTokens: 10, OutputTokens: 20, CacheReadTokens: 30, CacheWriteTokens: 40})
	if u != (Usage{11, 22, 33, 44}) {
		t.Fatalf("Add = %+v", u)
	}
}

var _ = context.Background
