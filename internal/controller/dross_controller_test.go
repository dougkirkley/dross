package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	dross "github.com/dougkirkley/dross/api/v1alpha1"
)

func newDross() *dross.Dross {
	return &dross.Dross{
		ObjectMeta: metav1.ObjectMeta{Namespace: "dross-system", Name: "dross-web", UID: "u-2"},
		Spec: dross.DrossSpec{
			Image: "registry.example/dross-web:1.2.3",
			Args:  []string{"serve", "--addr", ":8080"},
			Sessions: dross.SessionDefaults{
				Namespace: "dross", StorageClass: "iscsi",
			},
		},
	}
}

func envOf(c corev1.Container) map[string]string {
	m := map[string]string{}
	for _, e := range c.Env {
		m[e.Name] = e.Value
	}
	return m
}

func reconcileDross(t *testing.T, d *dross.Dross) (*DrossReconciler, types.NamespacedName) {
	t.Helper()
	sc := scheme(t)
	c := fake.NewClientBuilder().WithScheme(sc).WithObjects(d).WithStatusSubresource(d).Build()
	r := &DrossReconciler{Client: c}
	key := types.NamespacedName{Namespace: d.Namespace, Name: d.Name}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	return r, key
}

// The whole reason the operator owns the server: it writes the session settings
// into the server's environment, so the two halves cannot be configured apart.
func TestServerGetsSessionEnvironment(t *testing.T) {
	d := newDross()
	r, key := reconcileDross(t, d)

	var dep appsv1.Deployment
	if err := r.Get(context.Background(), key, &dep); err != nil {
		t.Fatalf("deployment: %v", err)
	}
	env := envOf(dep.Spec.Template.Spec.Containers[0])

	if got := env["DROSS_SESSION_NAMESPACE"]; got != "dross" {
		t.Errorf("DROSS_SESSION_NAMESPACE = %q; want dross", got)
	}
	// The session image is the server's own: one binary serves the API and runs
	// a session, so a separate field could only ever drift.
	if got := env["DROSS_SESSION_IMAGE"]; got != d.Spec.Image {
		t.Errorf("DROSS_SESSION_IMAGE = %q; want the server's own image %q", got, d.Spec.Image)
	}
	// Derived from the Service this same reconcile creates.
	if want := "http://dross-web.dross-system.svc:8080"; env["DROSS_SESSION_SERVER_URL"] != want {
		t.Errorf("DROSS_SESSION_SERVER_URL = %q; want %q", env["DROSS_SESSION_SERVER_URL"], want)
	}
	if got := env["DROSS_SESSION_STORAGE_CLASS"]; got != "iscsi" {
		t.Errorf("DROSS_SESSION_STORAGE_CLASS = %q; want iscsi", got)
	}
}

// With no session namespace the server must be left in its in-process mode.
// Setting the variable to an empty string is not the same as not setting it,
// and would be read as "pod mode, namespace unset".
func TestNoSessionNamespaceLeavesPodModeOff(t *testing.T) {
	d := newDross()
	d.Spec.Sessions = dross.SessionDefaults{}
	r, key := reconcileDross(t, d)

	var dep appsv1.Deployment
	if err := r.Get(context.Background(), key, &dep); err != nil {
		t.Fatalf("deployment: %v", err)
	}
	for name := range envOf(dep.Spec.Template.Spec.Containers[0]) {
		if len(name) > 14 && name[:15] == "DROSS_SESSION_N" {
			t.Errorf("%s set with no session namespace", name)
		}
	}
}

// The Service has to select the pods the Deployment creates, or the server is
// unreachable however healthy it is.
func TestServiceSelectsTheServerPods(t *testing.T) {
	d := newDross()
	r, key := reconcileDross(t, d)

	var svc corev1.Service
	if err := r.Get(context.Background(), key, &svc); err != nil {
		t.Fatalf("service: %v", err)
	}
	var dep appsv1.Deployment
	if err := r.Get(context.Background(), key, &dep); err != nil {
		t.Fatalf("deployment: %v", err)
	}
	for k, v := range svc.Spec.Selector {
		if dep.Spec.Template.Labels[k] != v {
			t.Errorf("service selects %s=%s, which the pods do not carry", k, v)
		}
	}
	if len(svc.Spec.Selector) == 0 {
		t.Error("service has no selector")
	}
}

// Both children must be owned, so deleting the Dross takes the server with it
// rather than leaving an unmanaged Deployment behind.
func TestServerChildrenAreOwned(t *testing.T) {
	d := newDross()
	r, key := reconcileDross(t, d)

	var dep appsv1.Deployment
	if err := r.Get(context.Background(), key, &dep); err != nil {
		t.Fatal(err)
	}
	var svc corev1.Service
	if err := r.Get(context.Background(), key, &svc); err != nil {
		t.Fatal(err)
	}
	for name, refs := range map[string][]metav1.OwnerReference{
		"deployment": dep.OwnerReferences, "service": svc.OwnerReferences,
	} {
		owned := false
		for _, o := range refs {
			if o.Kind == "Dross" && o.Name == d.Name {
				owned = true
			}
		}
		if !owned {
			t.Errorf("%s is not owned by the Dross", name)
		}
	}
}

// An image bump has to reach a server that is already running; a create-only
// reconcile would leave it on the old image forever.
func TestServerImageIsUpdatedInPlace(t *testing.T) {
	d := newDross()
	sc := scheme(t)
	c := fake.NewClientBuilder().WithScheme(sc).WithObjects(d).WithStatusSubresource(d).Build()
	r := &DrossReconciler{Client: c}
	key := types.NamespacedName{Namespace: d.Namespace, Name: d.Name}
	ctx := context.Background()

	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	var live dross.Dross
	if err := c.Get(ctx, key, &live); err != nil {
		t.Fatal(err)
	}
	live.Spec.Image = "registry.example/dross-web:9.9.9"
	if err := c.Update(ctx, &live); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("reconcile after bump: %v", err)
	}

	var dep appsv1.Deployment
	if err := c.Get(ctx, key, &dep); err != nil {
		t.Fatal(err)
	}
	if got := dep.Spec.Template.Spec.Containers[0].Image; got != "registry.example/dross-web:9.9.9" {
		t.Errorf("image = %q; want the bumped one", got)
	}
	// And the session image follows it, since they are the same binary.
	if got := envOf(dep.Spec.Template.Spec.Containers[0])["DROSS_SESSION_IMAGE"]; got != "registry.example/dross-web:9.9.9" {
		t.Errorf("DROSS_SESSION_IMAGE = %q; want it to follow the server image", got)
	}
}

// A Dross with no image is a mistake worth reporting, not something to default.
func TestDrossWithoutImageFails(t *testing.T) {
	d := newDross()
	d.Spec.Image = ""
	sc := scheme(t)
	c := fake.NewClientBuilder().WithScheme(sc).WithObjects(d).WithStatusSubresource(d).Build()
	r := &DrossReconciler{Client: c}
	key := types.NamespacedName{Namespace: d.Namespace, Name: d.Name}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err == nil {
		t.Error("reconciled a Dross with no image")
	}
	var dep appsv1.Deployment
	if err := c.Get(context.Background(), key, &dep); err == nil {
		t.Error("created a deployment for a Dross with no image")
	}
}

// A session's image is fixed in its spec at creation, so without rolling, a fix
// to the agent reaches new sessions only and everything already running stays
// on the binary it started with — for as long as the session lives.
func TestBumpingTheServerRollsItsSessions(t *testing.T) {
	d := newDross()
	old := &dross.Session{
		ObjectMeta: metav1.ObjectMeta{Namespace: "dross", Name: "dross-web-old-one"},
		Spec:       dross.SessionSpec{SessionID: "ses_a", Image: "registry.example/dross-web:1.0.0"},
	}
	sc := scheme(t)
	c := fake.NewClientBuilder().WithScheme(sc).
		WithObjects(d, old).WithStatusSubresource(d).Build()
	r := &DrossReconciler{Client: c}
	key := types.NamespacedName{Namespace: d.Namespace, Name: d.Name}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	var got dross.Session
	if err := c.Get(context.Background(),
		types.NamespacedName{Namespace: "dross", Name: "dross-web-old-one"}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Spec.Image != d.Spec.Image {
		t.Errorf("session image = %q; want the server's %q", got.Spec.Image, d.Spec.Image)
	}
}

// A session already being deleted must be left alone: writing to it would only
// race the collection that is removing it.
func TestRollSkipsSessionsBeingDeleted(t *testing.T) {
	d := newDross()
	now := metav1.Now()
	going := &dross.Session{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "dross", Name: "dross-web-going",
			DeletionTimestamp: &now, Finalizers: []string{"dross.sh/test"},
		},
		Spec: dross.SessionSpec{SessionID: "ses_b", Image: "old"},
	}
	sc := scheme(t)
	c := fake.NewClientBuilder().WithScheme(sc).
		WithObjects(d, going).WithStatusSubresource(d).Build()
	r := &DrossReconciler{Client: c}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: d.Namespace, Name: d.Name},
	}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	var got dross.Session
	if err := c.Get(context.Background(),
		types.NamespacedName{Namespace: "dross", Name: "dross-web-going"}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Spec.Image != "old" {
		t.Error("rewrote a session that was being deleted")
	}
}

// The workload has to follow its Session, or the spec says one image and the
// pod goes on running another.
func TestSessionImageChangeUpdatesTheWorkload(t *testing.T) {
	s := newSession()
	s.Spec.Image = "registry.example/dross-web:1.0.0"
	sc := scheme(t)
	c := fake.NewClientBuilder().WithScheme(sc).WithObjects(s).WithStatusSubresource(s).Build()
	r := &SessionReconciler{Client: c}
	key := types.NamespacedName{Namespace: s.Namespace, Name: s.Name}
	ctx := context.Background()

	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatal(err)
	}
	var live dross.Session
	if err := c.Get(ctx, key, &live); err != nil {
		t.Fatal(err)
	}
	live.Spec.Image = "registry.example/dross-web:2.0.0"
	if err := c.Update(ctx, &live); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatal(err)
	}

	var set appsv1.StatefulSet
	if err := c.Get(ctx, key, &set); err != nil {
		t.Fatal(err)
	}
	if got := set.Spec.Template.Spec.Containers[0].Image; got != "registry.example/dross-web:2.0.0" {
		t.Errorf("workload image = %q; it did not follow the session", got)
	}
}

// A bump moves every outdated session in one pass. Each workload rolls its own
// pod, so pacing them here would only delay a fix reaching the ones at the back
// of the list.
func TestRollMovesEverySessionAtOnce(t *testing.T) {
	d := newDross()
	var objs []client.Object
	objs = append(objs, d)
	for _, n := range []string{"a", "b", "c"} {
		objs = append(objs, &dross.Session{
			ObjectMeta: metav1.ObjectMeta{Namespace: "dross", Name: "dross-web-" + n},
			Spec:       dross.SessionSpec{SessionID: "ses_" + n, Image: "old"},
		})
	}
	c := fake.NewClientBuilder().WithScheme(scheme(t)).
		WithObjects(objs...).WithStatusSubresource(d).Build()
	r := &DrossReconciler{Client: c}
	ctx := context.Background()

	if _, err := r.Reconcile(ctx, ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: d.Namespace, Name: d.Name},
	}); err != nil {
		t.Fatal(err)
	}

	var l dross.SessionList
	if err := c.List(ctx, &l); err != nil {
		t.Fatal(err)
	}
	for _, s := range l.Items {
		if s.Spec.Image != d.Spec.Image {
			t.Errorf("%s left on %q", s.Name, s.Spec.Image)
		}
	}
}
