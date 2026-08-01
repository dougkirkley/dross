package server

import "testing"

// A session that runs for days must not grow until the pod dies, and the
// sequence numbers clients reconnect with have to stay monotonic across the
// trim.
func TestEventHistoryIsBounded(t *testing.T) {
	m := NewManager(&fakeProvider{}, nil, "m", t.TempDir())
	s := m.create(CreateReq{}, "doug@example.com")

	const n = maxSessionEvents + 500
	for i := 0; i < n; i++ {
		s.emit("text", "x")
	}

	s.mu.Lock()
	kept := len(s.events)
	first, last := s.events[0].Seq, s.events[len(s.events)-1].Seq
	seq := s.seq
	s.mu.Unlock()

	if kept > maxSessionEvents {
		t.Errorf("retained %d events; cap is %d", kept, maxSessionEvents)
	}
	if seq != n {
		t.Errorf("seq = %d; want %d — trimming must not reset the counter", seq, n)
	}
	if last != n {
		t.Errorf("last event seq = %d; want %d", last, n)
	}
	if first <= 0 || first >= last {
		t.Errorf("window looks wrong: first=%d last=%d", first, last)
	}
	// A client reconnecting with ?after= gets only newer events.
	if got := s.history(last - 10); len(got) != 10 {
		t.Errorf("history(after=last-10) = %d events; want 10", len(got))
	}
}
