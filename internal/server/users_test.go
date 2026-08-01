package server

import "testing"

// First sign-in is the sign-up; later ones only update the record. Access is
// decided by the identity provider, so the registry must never reject anyone.
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
}
