// Package relay defines the wire protocol for device linking: a laptop dials
// OUT to the dross server over a WebSocket, registers as a device, and the
// server proxies the phone's session traffic to it. Frames are JSON, one per
// WebSocket message.
package relay

// SessionInfo mirrors a device session on the server (a subset of the server's
// sessionView, sent by the device so the phone can list it).
type SessionInfo struct {
	ID        string  `json:"id"`
	Model     string  `json:"model"`
	Provider  string  `json:"provider"`
	Title     string  `json:"title,omitempty"`
	Workspace string  `json:"workspace,omitempty"`
	Last      string  `json:"last,omitempty"`
	Status    string  `json:"status"`
	Cost      float64 `json:"cost"`
}

// Frame is a single relay message. Type determines which fields are set.
//
// Device → server:
//   - hello:   Device (name); the device announces itself
//   - session: Session (upsert); a session's current snapshot
//   - event:   SessionID, Seq, Kind, Data; one agent event
//   - ask:     SessionID, AskID, Data; the run needs an answer before it can go on
//
// Server → device:
//   - create:    Model, Worker; start a new session
//   - prompt:    SessionID, Text; run a prompt in a session
//   - interrupt: SessionID; cancel a running prompt
//   - delete:    SessionID; drop a session
//   - answer:    SessionID, AskID, Text; the reply to an ask
type Frame struct {
	Type   string `json:"type"`
	Device string `json:"device,omitempty"`

	Session *SessionInfo `json:"session,omitempty"`

	SessionID string `json:"session_id,omitempty"`
	Seq       int    `json:"seq,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Data      string `json:"data,omitempty"`

	// AskID pairs an ask with its answer. A run can only have one outstanding
	// ask, but the id makes a late answer to an abandoned question ignorable
	// rather than applied to the next one.
	AskID string `json:"ask_id,omitempty"`

	Text   string `json:"text,omitempty"`
	Model  string `json:"model,omitempty"`
	Worker string `json:"worker,omitempty"`
}
