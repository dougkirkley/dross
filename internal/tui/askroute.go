package tui

import "sync"

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
}

// NewAskRouter returns a router that answers locally until told otherwise.
func NewAskRouter() *AskRouter { return &AskRouter{} }

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
	remote, ask := r.remote, r.ask
	r.mu.Unlock()
	if remote && ask != nil {
		return ask(payload)
	}
	return local()
}
