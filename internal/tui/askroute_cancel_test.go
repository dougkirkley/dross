package tui

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The bug: ctrl+c did nothing to a run parked on a question sent to a phone.
// The relay's wait watches the connection's context, which is still alive, so
// the agent goroutine sat there — "interrupting…" printed thirteen times while
// the run kept counting.
func TestRouteAbandonsAQuestionWhenTheRunIsCancelled(t *testing.T) {
	r := NewAskRouter()
	blocked := make(chan struct{})
	r.SetRemote(true, func(string) (string, error) {
		<-blocked // never answers, like a phone nobody is holding
		return "", nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	r.SetRun(ctx)

	done := make(chan error, 1)
	go func() {
		_, err := r.Route("q", func() (string, error) { return "", nil })
		done <- err
	}()

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Route did not return after the run was cancelled")
	}
	close(blocked)
}

// An answer that arrives before any cancellation is returned normally.
func TestRouteReturnsTheAnswer(t *testing.T) {
	r := NewAskRouter()
	r.SetRemote(true, func(string) (string, error) { return "yes", nil })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.SetRun(ctx)

	got, err := r.Route("q", func() (string, error) { return "local", nil })
	if err != nil || got != "yes" {
		t.Errorf("got %q, %v; want the remote answer", got, err)
	}
}

// With no run recorded the router behaves exactly as before, so a local TUI
// session is unaffected.
func TestRouteWithoutARunIsUnchanged(t *testing.T) {
	r := NewAskRouter()
	got, err := r.Route("q", func() (string, error) { return "local", nil })
	if err != nil || got != "local" {
		t.Errorf("got %q, %v; want the local answer", got, err)
	}
}
