package server

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	dross "github.com/dougkirkley/dross/api/v1alpha1"
)

func podSessions(t *testing.T) *PodSessions {
	t.Helper()
	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := dross.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return &PodSessions{
		c:          fake.NewClientBuilder().WithScheme(s).Build(),
		namespace:  "dross",
		NamePrefix: "dross-web",
		Provider:   "openai",
		APIKey:     "k",
		MintToken: func(string, string, time.Duration) (string, error) {
			return "dv1.tok", nil
		},
	}
}

// The workload's name no longer encodes the session id, so deleting has to find
// it by label. Getting this wrong strands a pod and its volume for every
// session ever deleted.
func TestDeleteFindsTheSessionByLabel(t *testing.T) {
	p := podSessions(t)
	ctx := context.Background()

	name, err := p.Create(ctx, "ses_abc", "doug@example.com", "m")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// The session is called what its pod is called, so the two are the same
	// thing to look at rather than a name and a hex id.
	if name == "" {
		t.Error("create returned no name for the session to take")
	}

	var list dross.SessionList
	if err := p.c.List(ctx, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("created %d sessions; want 1", len(list.Items))
	}
	got := list.Items[0]
	if got.Name == "dross-abc" || got.Labels[sessionIDLabel] != "ses_abc" {
		t.Errorf("name %q / labels %v: expected a generated name tagged with the session id",
			got.Name, got.Labels)
	}

	if err := p.Delete(ctx, "ses_abc"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := p.c.List(ctx, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 0 {
		t.Errorf("%d sessions left after delete", len(list.Items))
	}
	// The credential is not owned by the Session, so it has to go explicitly.
	var secs corev1.SecretList
	if err := p.c.List(ctx, &secs); err != nil {
		t.Fatal(err)
	}
	if len(secs.Items) != 0 {
		t.Errorf("%d secrets left after delete", len(secs.Items))
	}
}

// Deleting a session that never had a workload is not an error: pod mode may
// have been switched on after it was created.
func TestDeleteWithNoWorkloadIsFine(t *testing.T) {
	if err := podSessions(t).Delete(context.Background(), "ses_never"); err != nil {
		t.Errorf("delete: %v", err)
	}
}

// A pod-backed session takes the name of the pod running it, so a list of them
// reads as distinct sessions rather than "New session" repeated.
func TestCreateNamesTheSessionAfterItsPod(t *testing.T) {
	p := podSessions(t)
	ctx := context.Background()

	friendly, err := p.Create(ctx, "ses_abc", "doug@example.com", "m")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	var list dross.SessionList
	if err := p.c.List(ctx, &list); err != nil {
		t.Fatal(err)
	}
	// The workload is prefixed with the server; the session's name is the half
	// that distinguishes it, since every session here shares the prefix.
	want := "dross-web-" + friendly
	if list.Items[0].Name != want {
		t.Errorf("workload %q; want %q", list.Items[0].Name, want)
	}
	if strings.Contains(friendly, "dross-web") {
		t.Errorf("session name %q repeats the server prefix", friendly)
	}
}

// A workload created before the label existed is named after the session id.
// Delete has to still find it, or upgrading strands every pod and volume that
// predates the change while reporting success.
func TestDeleteFindsAPreLabelWorkload(t *testing.T) {
	p := podSessions(t)
	ctx := context.Background()

	old := &dross.Session{ObjectMeta: metav1.ObjectMeta{
		Namespace: "dross", Name: "dross-abc", // no session-id label
	}}
	if err := p.c.Create(ctx, old); err != nil {
		t.Fatal(err)
	}

	if err := p.Delete(ctx, "ses_abc"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	var list dross.SessionList
	if err := p.c.List(ctx, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 0 {
		t.Errorf("pre-label workload survived deletion: %v", list.Items[0].Name)
	}
}
