package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/dougkirkley/dross/pkg/provider"
)

// serve stands up a fake completions endpoint and returns a session pointed at
// it, plus a counter of how many requests it received. The handler is told
// which attempt it is answering, so a test can fail the first and pass the
// second.
func serve(t *testing.T, h func(w http.ResponseWriter, attempt int32)) (provider.Session, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		h(w, hits.Add(1))
	}))
	t.Cleanup(srv.Close)
	p := &prov{base: srv.URL, hc: &http.Client{}}
	return p.NewSession(provider.SessionConfig{Model: "m"}), &hits
}

func okBody(text string) string {
	var cr chatResponse
	cr.Choices = append(cr.Choices, struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	}{Message: chatMessage{Content: text}, FinishReason: "stop"})
	b, _ := json.Marshal(cr)
	return string(b)
}

// TestToolCallParseFailureIsReportedNotRetried is the case that killed a real
// benchmark run: ollama answers 500 when the model emits a tool call it cannot
// parse.
//
// Retrying was tried first and watched failing three times running — the same
// context produces the same confusion, so the attempts are spent for nothing
// and the run dies anyway. It comes back tagged instead, because this is the
// one failure the model can repair once it is told about it.
func TestToolCallParseFailureIsReportedNotRetried(t *testing.T) {
	sess, hits := serve(t, func(w http.ResponseWriter, _ int32) {
		http.Error(w, `{"error":{"message":"error parsing tool call: raw='{...'"}}`, http.StatusInternalServerError)
	})
	_, err := sess.Send(context.Background(), provider.Input{Text: "hi"})
	if !errors.Is(err, provider.ErrMalformedToolCall) {
		t.Fatalf("want ErrMalformedToolCall, got %v", err)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("requests = %d, want 1 — re-asking gets the same broken sample", got)
	}
}

// TestEmptyCompletionIsReAsked: a 200 with no text, no tool call and no tokens
// is a failed turn, not a finished one. Taken at face value it ended a task in
// one turn with the work undone.
func TestEmptyCompletionIsReAsked(t *testing.T) {
	sess, hits := serve(t, func(w http.ResponseWriter, attempt int32) {
		if attempt == 1 {
			fmt.Fprint(w, okBody(""))
			return
		}
		fmt.Fprint(w, okBody("actually here is the answer"))
	})
	resp, err := sess.Send(context.Background(), provider.Input{Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "actually here is the answer" {
		t.Fatalf("text = %q", resp.Text)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("requests = %d, want 2", got)
	}
}

// TestPersistentlyEmptyGivesUp keeps that from looping forever.
func TestPersistentlyEmptyGivesUp(t *testing.T) {
	sess, hits := serve(t, func(w http.ResponseWriter, _ int32) {
		fmt.Fprint(w, okBody(""))
	})
	if _, err := sess.Send(context.Background(), provider.Input{Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if got := hits.Load(); got != sendAttempts {
		t.Fatalf("requests = %d, want %d", got, sendAttempts)
	}
}

// TestGivesUpAfterAttempts bounds it: a server that always fails is reported,
// not retried forever.
func TestGivesUpAfterAttempts(t *testing.T) {
	sess, hits := serve(t, func(w http.ResponseWriter, _ int32) {
		http.Error(w, "upstream on fire", http.StatusBadGateway)
	})
	_, err := sess.Send(context.Background(), provider.Input{Text: "hi"})
	if err == nil {
		t.Fatal("want an error once the attempts are spent")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Fatalf("error should name the status: %v", err)
	}
	if got := hits.Load(); got != sendAttempts {
		t.Fatalf("requests = %d, want %d", got, sendAttempts)
	}
}

// TestDoesNotRetryClientErrors keeps a bad request fast: the server will refuse
// it identically, so asking twice only makes the failure slower.
func TestDoesNotRetryClientErrors(t *testing.T) {
	sess, hits := serve(t, func(w http.ResponseWriter, _ int32) {
		http.Error(w, `{"error":{"message":"model not found"}}`, http.StatusNotFound)
	})
	if _, err := sess.Send(context.Background(), provider.Input{Text: "hi"}); err == nil {
		t.Fatal("want an error")
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("requests = %d, want 1 — a 404 is not retryable", got)
	}
}

// TestRetriesRateLimit covers the other half of retryable: a 429 is a "later",
// not a "no".
func TestRetriesRateLimit(t *testing.T) {
	sess, hits := serve(t, func(w http.ResponseWriter, attempt int32) {
		if attempt == 1 {
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, okBody("ok"))
	})
	if _, err := sess.Send(context.Background(), provider.Input{Text: "hi"}); err != nil {
		t.Fatalf("429 should be retried: %v", err)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("requests = %d, want 2", got)
	}
}

// TestCancelStopsRetrying: a user who pressed ctrl-c is not asking for two more
// attempts.
func TestCancelStopsRetrying(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sess, hits := serve(t, func(w http.ResponseWriter, _ int32) {
		cancel()
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	if _, err := sess.Send(ctx, provider.Input{Text: "hi"}); err == nil {
		t.Fatal("want an error")
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("requests = %d, want 1 after cancellation", got)
	}
}
