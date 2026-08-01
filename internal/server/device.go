package server

import (
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/dougkirkley/dross/internal/auth"
	"github.com/dougkirkley/dross/internal/relay"
)

// deviceHub tracks laptops linked over the relay WebSocket and routes frames to
// them. Device sessions are mirrored into the Manager so the phone lists and
// prompts them like pod sessions; prompts/interrupts are proxied to the device.
type deviceHub struct {
	mgr   *Manager
	mu    sync.Mutex
	conns map[string]*deviceConn
}

type deviceConn struct {
	id   string
	send chan relay.Frame
}

func newDeviceHub(mgr *Manager) *deviceHub {
	return &deviceHub{mgr: mgr, conns: map[string]*deviceConn{}}
}

// toDevice sends a frame to a connected device; false if it isn't linked.
func (h *deviceHub) toDevice(id string, f relay.Frame) bool {
	h.mu.Lock()
	c := h.conns[id]
	h.mu.Unlock()
	if c == nil {
		return false
	}
	select {
	case c.send <- f:
		return true
	default:
		return false // backed-up device; drop
	}
}

func (h *deviceHub) add(c *deviceConn) {
	h.mu.Lock()
	h.conns[c.id] = c
	h.mu.Unlock()
}

func (h *deviceHub) remove(id string) {
	h.mu.Lock()
	if h.conns[id] != nil {
		delete(h.conns, id)
	}
	h.mu.Unlock()
	h.mgr.setDeviceConnected(id, false)
}

// handleDeviceConnect is the relay endpoint a device dials into. It reads the
// hello, then pumps frames both ways until the connection drops.
func (s *Server) handleDeviceConnect(w http.ResponseWriter, r *http.Request) {
	// Require a real SSO identity for device linking, so a session is always
	// tied to the logged-in user. Checking only for an "@" was dead code: the
	// machine credential's identity (service@dross) contains one, so a token
	// meant for automated checks could register a device and receive prompts.
	if s.auth != nil {
		email := auth.Identity(r)
		if email == "" || s.auth.IsServiceIdentity(email) {
			http.Error(w, "device linking requires SSO login", http.StatusForbidden)
			return
		}
	}
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	ctx := r.Context()
	c.SetReadLimit(4 << 20)

	var hello relay.Frame
	if err := wsjson.Read(ctx, c, &hello); err != nil || hello.Type != "hello" || hello.Device == "" {
		c.Close(websocket.StatusPolicyViolation, "expected hello")
		return
	}
	conn := &deviceConn{id: hello.Device, send: make(chan relay.Frame, 64)}
	s.hub.add(conn)
	defer func() {
		s.hub.remove(conn.id)
		c.Close(websocket.StatusNormalClosure, "")
	}()

	// Writer: server → device.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case f := <-conn.send:
				if wsjson.Write(ctx, c, f) != nil {
					return
				}
			}
		}
	}()

	// Reader: device → server.
	for {
		var f relay.Frame
		if err := wsjson.Read(ctx, c, &f); err != nil {
			return
		}
		switch f.Type {
		case "session":
			if f.Session != nil {
				s.mgr.upsertDeviceSession(conn.id, *f.Session)
			}
		case "event":
			s.mgr.deviceEvent(conn.id, f.SessionID, f.Kind, f.Data)
		}
	}
}

// --- Manager device-session mirroring -------------------------------------

// upsertDeviceSession creates or updates the pod-side mirror of a device
// session so the phone can list it.
func (m *Manager) upsertDeviceSession(device string, info relay.SessionInfo) {
	m.mu.Lock()
	s := m.sessions[info.ID]
	if s == nil {
		s = &Session{ID: info.ID, device: device}
		m.sessions[info.ID] = s
	}
	m.mu.Unlock()

	s.mu.Lock()
	s.Model, s.Provider = info.Model, info.Provider
	s.title, s.repo, s.last = info.Title, info.Workspace, info.Last
	s.status, s.cost = info.Status, info.Cost
	s.device, s.connected = device, true
	s.updated = time.Now()
	s.mu.Unlock()
}

// deviceEvent appends an event streamed from a device into its mirror session.
func (m *Manager) deviceEvent(device, sessionID, kind, data string) {
	s, ok := m.getAny(sessionID)
	if !ok || s.device != device {
		return
	}
	s.emit(kind, data)
	if kind == "done" || kind == "error" {
		s.setStatus("idle")
	}
}

// setDeviceConnected flips the connected flag for all of a device's sessions
// (e.g. when the device drops).
func (m *Manager) setDeviceConnected(device string, connected bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		if s.device == device {
			s.mu.Lock()
			s.connected = connected
			s.mu.Unlock()
		}
	}
}
