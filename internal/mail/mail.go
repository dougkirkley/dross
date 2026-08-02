// Package mail sends the handful of account mails dross owes people: we have
// your request, and you are in.
//
// Sending is an interface with one implementation (Resend) so the transport is
// a swap, not a rewrite — the composed message is the part worth keeping. A nil
// Sender is a working configuration: it delivers nothing, which is what a local
// run and a server with no API key should do.
package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Message is one composed mail, ready for any transport.
type Message struct {
	To      string
	Subject string
	HTML    string
	Text    string // plain-text alternative; some clients only show this
}

// Sender delivers a message. Implementations must be safe for concurrent use.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// --- Resend ----------------------------------------------------------------

// Resend delivers through resend.com's HTTP API.
type Resend struct {
	key    string
	from   string // "Dross <dross@dross.sh>"
	client *http.Client
}

// NewResend returns a sender, or a nil Sender when the key or from address is
// missing — an unconfigured server delivers nothing rather than failing an
// approval. The return type is the interface on purpose: handing back a typed
// nil pointer would make every "is mail configured?" check answer yes.
func NewResend(key, from string) Sender {
	key, from = strings.TrimSpace(key), strings.TrimSpace(from)
	if key == "" || from == "" {
		return nil
	}
	return &Resend{key: key, from: from, client: &http.Client{Timeout: 15 * time.Second}}
}

func (r *Resend) Send(ctx context.Context, m Message) error {
	body, err := json.Marshal(map[string]any{
		"from":    r.from,
		"to":      []string{m.To},
		"subject": m.Subject,
		"html":    m.HTML,
		"text":    m.Text,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.resend.com/emails", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		// The response body carries Resend's reason (unverified domain, bad
		// key); without it every failure looks the same.
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(http.MaxBytesReader(nil, resp.Body, 4<<10))
		return fmt.Errorf("resend: %s: %s", resp.Status, strings.TrimSpace(buf.String()))
	}
	return nil
}
