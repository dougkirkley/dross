package server

// SetLeadEndpoint records the endpoint the lead provider talks to, so a session
// can ask whether its model is loaded. Only ollama answers that question; for
// anything else this is unused and harmless.
func (m *Manager) SetLeadEndpoint(url string) { m.leadURL = url }
