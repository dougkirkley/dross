package mail

import (
	"context"
	"strings"
	"testing"
)

type capture struct{ sent []Message }

func (c *capture) Send(_ context.Context, m Message) error {
	c.sent = append(c.sent, m)
	return nil
}

// The copy is worth testing precisely because it is the part a user reads and
// nobody re-reads. Rendering it needs no network.
func TestTheThreeMailsRender(t *testing.T) {
	c := &capture{}
	n := &Templated{Send: c, AppURL: "https://dross.sh/"}
	ctx := context.Background()

	if err := n.RequestReceived(ctx, Account{Email: "new@example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := n.AdminRequest(ctx, "doug@example.com", Account{Email: "new@example.com", Name: "New Person"}); err != nil {
		t.Fatal(err)
	}
	if err := n.Welcome(ctx, Account{Email: "new@example.com", Name: "New Person"}); err != nil {
		t.Fatal(err)
	}
	if len(c.sent) != 3 {
		t.Fatalf("sent %d mails; want 3", len(c.sent))
	}

	// The acknowledgement goes to the person, addressed by the local part when
	// the provider gave us no name.
	got := c.sent[0]
	if got.To != "new@example.com" || !strings.Contains(got.Text, "Hi new,") {
		t.Errorf("request mail: to=%q text=%.80q", got.To, got.Text)
	}

	// The admin notice goes to the admin, not the applicant, and links to the
	// page that decides it.
	got = c.sent[1]
	if got.To != "doug@example.com" ||
		!strings.Contains(got.Text, "https://dross.sh/admin") ||
		!strings.Contains(got.Subject, "new@example.com") {
		t.Errorf("admin mail: to=%q subject=%q text=%.120q", got.To, got.Subject, got.Text)
	}

	// The welcome carries a way in, and every mail has both alternatives so a
	// plain-text client is not handed an empty message.
	got = c.sent[2]
	if !strings.Contains(got.Text, "https://dross.sh") || !strings.Contains(got.HTML, "Open Dross") {
		t.Errorf("welcome mail: text=%.120q", got.Text)
	}
	for i, m := range c.sent {
		if m.Subject == "" || m.HTML == "" || m.Text == "" {
			t.Errorf("mail %d is missing a subject or a body alternative", i)
		}
	}
}

// A server with no mail configured has to keep working: approving somebody
// still has to succeed, it just tells nobody.
func TestUnconfiguredMailIsASilentNoOp(t *testing.T) {
	n := &Templated{Send: NewResend("", ""), AppURL: "https://dross.sh"}
	if n.Enabled() {
		t.Fatal("a notifier with no key reports itself enabled")
	}
	if err := n.Welcome(context.Background(), Account{Email: "new@example.com"}); err != nil {
		t.Errorf("Welcome with no sender = %v; want nil", err)
	}

	// Nil all the way down is also a working configuration.
	var none *Templated
	if none.Enabled() {
		t.Error("a nil notifier reports itself enabled")
	}
	if err := none.Welcome(context.Background(), Account{Email: "new@example.com"}); err != nil {
		t.Errorf("Welcome on a nil notifier = %v; want nil", err)
	}
}

// With nobody to tell - a server whose directory holds no administrator yet -
// the notice is skipped rather than sent to an empty address.
func TestNoAdminMeansNoAdminNotice(t *testing.T) {
	c := &capture{}
	n := &Templated{Send: c, AppURL: "https://dross.sh"}
	if err := n.AdminRequest(context.Background(), "", Account{Email: "new@example.com"}); err != nil {
		t.Fatal(err)
	}
	if len(c.sent) != 0 {
		t.Errorf("sent %d mails with no admin configured; want 0", len(c.sent))
	}
}
