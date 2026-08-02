package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// First sign-in is the sign-up; later ones only update the record. Signing in
// is how you ask for access, so the registry never rejects anyone — it records
// them as pending and lets the gate decide.
func TestFirstSignInRegistersTheAccount(t *testing.T) {
	dir := t.TempDir()
	u, err := NewUsers(dir)
	if err != nil {
		t.Fatal(err)
	}

	got, isNew := u.SignIn("Doug@Example.com", "Doug")
	if !isNew || got.Logins != 1 {
		t.Fatalf("first sign-in: isNew=%v logins=%d", isNew, got.Logins)
	}
	// Same account, different capitalisation, must not create a second user.
	got, isNew = u.SignIn("doug@example.com", "Doug")
	if isNew || got.Logins != 2 {
		t.Fatalf("second sign-in: isNew=%v logins=%d", isNew, got.Logins)
	}
	if got.FirstSeen.After(got.LastSeen) {
		t.Error("first_seen should precede last_seen")
	}
	if len(u.List()) != 1 {
		t.Errorf("List = %d users; want 1", len(u.List()))
	}

	// The registry survives a restart.
	u2, err := NewUsers(dir)
	if err != nil {
		t.Fatal(err)
	}
	x, ok := u2.Get("DOUG@example.com")
	if !ok || x.Logins != 2 || x.Name != "Doug" {
		t.Fatalf("reloaded user = %+v ok=%v", x, ok)
	}
	if x.Status != StatusPending || x.Approved() {
		t.Errorf("a new account must be pending, got %q", x.Status)
	}
}

// The upgrade that introduces the approval gate must not lock out the people
// already using the server: a record written before the field existed was, by
// definition, someone who was allowed in.
func TestAccountsWrittenBeforeTheGateAreApproved(t *testing.T) {
	dir := t.TempDir()
	old := `[{"email":"doug@example.com","name":"Doug","first_seen":"2026-01-01T00:00:00Z","last_seen":"2026-01-02T00:00:00Z","logins":7}]`
	if err := os.WriteFile(filepath.Join(dir, "users.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}

	u, err := NewUsers(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !u.IsApproved("doug@example.com") {
		t.Fatal("an account from before the gate should have migrated to approved")
	}

	// And the migration is written down, not re-derived on every start: the
	// file on disk must now say so.
	b, err := os.ReadFile(filepath.Join(dir, "users.json"))
	if err != nil {
		t.Fatal(err)
	}
	var list []User
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Status != StatusApproved {
		t.Errorf("saved registry = %+v; want one approved account", list)
	}
}

// Approving is what opens the door, and it happens exactly once: a second
// approval changes nothing and must not send a second welcome.
func TestDecideIsIdempotentAndWelcomesOnce(t *testing.T) {
	u, err := NewUsers(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	u.SignIn("new@example.com", "New Person")
	if u.IsApproved("new@example.com") {
		t.Fatal("a pending account must not be approved")
	}

	got, changed := u.Decide("NEW@example.com", StatusApproved, "doug@example.com")
	if !changed || !got.Approved() || got.DecidedBy != "doug@example.com" {
		t.Fatalf("first approval: changed=%v user=%+v", changed, got)
	}
	if _, changed := u.Decide("new@example.com", StatusApproved, "doug@example.com"); changed {
		t.Error("approving an approved account reported a change")
	}

	if !u.MarkWelcomed("new@example.com") {
		t.Error("first welcome should be claimed")
	}
	if u.MarkWelcomed("new@example.com") {
		t.Error("the welcome mail was claimed twice")
	}

	// Denying revokes access on the next request, without deleting the record.
	if _, changed := u.Decide("new@example.com", StatusDenied, "doug@example.com"); !changed {
		t.Error("denying an approved account should change it")
	}
	if u.IsApproved("new@example.com") {
		t.Error("a denied account is still approved")
	}

	// An account nobody has ever seen is not approved by omission.
	if u.IsApproved("ghost@example.com") {
		t.Error("unknown account reported as approved")
	}
}

// The admin has to be approved before anyone exists to approve them.
func TestEnsureSeedsTheAdmin(t *testing.T) {
	dir := t.TempDir()
	u, err := NewUsers(dir)
	if err != nil {
		t.Fatal(err)
	}
	u.Ensure("doug@example.com", StatusApproved, RoleAdmin)
	if !u.IsApproved("doug@example.com") {
		t.Fatal("the admin should be approved before their first sign-in")
	}

	// Signing in later fills the record in without demoting them.
	u.SignIn("doug@example.com", "Doug")
	if !u.IsApproved("doug@example.com") {
		t.Error("signing in demoted the admin")
	}
	// An empty admin email must not create a ghost account.
	u.Ensure("", StatusApproved, RoleAdmin)
	if len(u.List()) != 1 {
		t.Errorf("registry has %d accounts; want 1", len(u.List()))
	}
}
