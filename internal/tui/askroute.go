package tui

import (
	"context"
	"sync"
)

// AskRouter decides where a permission prompt is answered.
//
// The agent's asker runs on the run's own goroutine and has no way to reach the
// model, which lives inside the Bubble Tea loop. This is the handoff between
// them: the model records how the current run started, and the asker reads it.
//
// It exists because the answer depends on who asked for the work. A prompt
// typed here is gated here. A prompt sent from a phone has to be gated on that
// phone — asking the terminal instead would stall the run until someone walks
// over to a machine they may not be near, and would let whoever is at the
// keyboard approve an action they did not request.
type AskRouter struct {
	mu     sync.Mutex
	remote bool
	ask    func(payload string) (string, error)
	// run is the context of the run now in flight, so a question can be
	// abandoned when that run is cancelled. Without it ctrl+c did nothing to a
	// run parked on a remote question: the relay's wait watches the
	// connection's context, which is still perfectly alive, so the agent
	// goroutine sat on a channel until the answer arrived or the link dropped.
	run context.Context
}

// NewAskRouter returns a router that answers locally until told otherwise.
func NewAskRouter() *AskRouter { return &AskRouter{} }

// SetRun records the context of the run in flight, or nil when none is. A
// question outlives nothing: cancelling the run abandons it.
func (r *AskRouter) SetRun(ctx context.Context) {
	r.mu.Lock()
	r.run = ctx
	r.mu.Unlock()
}

// SetRemote records whether the run now in flight came from the relay, and how
// to reach the device that sent it.
func (r *AskRouter) SetRemote(remote bool, ask func(payload string) (string, error)) {
	r.mu.Lock()
	r.remote, r.ask = remote, ask
	r.mu.Unlock()
}

// Route asks the device that started the run, falling back to local when the
// run began here or the link has gone.
func (r *AskRouter) Route(payload string, local func() (string, error)) (string, error) {
	r.mu.Lock()
	remote, ask, run := r.remote, r.ask, r.run
	r.mu.Unlock()
	answer := local
	if remote && ask != nil {
		answer = func() (string, error) { return ask(payload) }
	}
	if run == nil {
		return answer()
	}
	// Race the question against the run being cancelled. The goroutine is left
	// to finish on its own — the channel is buffered, so it cannot block, and
	// whatever it was waiting on ends when the link or the ask does. Waiting
	// for it here would defeat the point, since not returning is the problem.
	type result struct {
		text string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		text, err := answer()
		done <- result{text, err}
	}()
	select {
	case res := <-done:
		return res.text, res.err
	case <-run.Done():
		return "", run.Err()
	}
}
