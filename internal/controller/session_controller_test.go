package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	dross "github.com/dougkirkley/dross/api/v1alpha1"
)

func scheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := appsv1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := dross.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func newSession() *dross.Session {
	return &dross.Session{
		ObjectMeta: metav1.ObjectMeta{Namespace: "dross-sessions", Name: "dross-abc", UID: "u-1"},
		Spec: dross.SessionSpec{
			Owner: "doug@example.com", SessionID: "ses_abc", Image: "img",
			ServerURL: "http://dross-web.media.svc:8080", TokenSecret: "dross-abc-token",
		},
	}
}

// Reconciling a Session must produce a StatefulSet owned by it, so deletion
// garbage-collects the workload and its volume.
func TestReconcileCreatesOwnedStatefulSet(t *testing.T) {
	s := newSession()
	sc := scheme(t)
	c := fake.NewClientBuilder().WithScheme(sc).
		WithObjects(s).WithStatusSubresource(s).Build()
	r := &SessionReconciler{Client: c}

	key := types.NamespacedName{Namespace: s.Namespace, Name: s.Name}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	var set appsv1.StatefulSet
	if err := c.Get(context.Background(), key, &set); err != nil {
		t.Fatalf("statefulset not created: %v", err)
	}

	// Ownership is what makes cleanup impossible to leak.
	refs := set.GetOwnerReferences()
	if len(refs) != 1 || refs[0].Kind != "Session" || refs[0].UID != "u-1" {
		t.Errorf("statefulset is not owned by its Session: %+v", refs)
	}

	// The workspace comes from a volumeClaimTemplate — that is what survives a
	// restart — and is reclaimed when the session is deleted.
	if len(set.Spec.VolumeClaimTemplates) != 1 ||
		set.Spec.VolumeClaimTemplates[0].Name != "workspace" {
		t.Fatalf("expected a workspace volumeClaimTemplate, got %+v", set.Spec.VolumeClaimTemplates)
	}
	if p := set.Spec.PersistentVolumeClaimRetentionPolicy; p == nil ||
		p.WhenDeleted != appsv1.DeletePersistentVolumeClaimRetentionPolicyType {
		t.Error("volume must be reclaimed when the session is deleted, or every session leaks a disk")
	}
	// Restarting is the whole point of using a StatefulSet.
	if got := set.Spec.Template.Spec.RestartPolicy; got != corev1.RestartPolicyAlways {
		t.Errorf("restart policy = %q; a session must come back after a crash", got)
	}

	// The credential comes from a Secret, never the pod spec.
	env := set.Spec.Template.Spec.Containers[0].Env
	var fromSecret bool
	for _, e := range env {
		if e.Name == "DROSS_TOKEN" && e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
			fromSecret = true
		}
		if e.Value != "" && len(e.Value) > 40 {
			t.Errorf("a long literal env value looks like an inlined credential: %s", e.Name)
		}
	}
	if !fromSecret {
		t.Error("device token is not sourced from a Secret")
	}
	if a := set.Spec.Template.Spec.AutomountServiceAccountToken; a == nil || *a {
		t.Error("session pod must not receive a service-account token")
	}
}

// The loop must be idempotent: running it repeatedly creates nothing extra.
func TestReconcileIsIdempotent(t *testing.T) {
	s := newSession()
	sc := scheme(t)
	c := fake.NewClientBuilder().WithScheme(sc).WithObjects(s).WithStatusSubresource(s).Build()
	r := &SessionReconciler{Client: c}
	key := types.NamespacedName{Namespace: s.Namespace, Name: s.Name}

	for i := 0; i < 3; i++ {
		if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatalf("reconcile %d: %v", i, err)
		}
	}
	var sets appsv1.StatefulSetList
	if err := c.List(context.Background(), &sets); err != nil {
		t.Fatal(err)
	}
	if len(sets.Items) != 1 {
		t.Errorf("created %d statefulsets; want exactly 1", len(sets.Items))
	}
}

// Readiness must surface on the Session, so the API can report it without
// watching workloads itself.
func TestStatusMirrorsTheWorkload(t *testing.T) {
	s := newSession()
	sc := scheme(t)
	c := fake.NewClientBuilder().WithScheme(sc).WithObjects(s).WithStatusSubresource(s).Build()
	r := &SessionReconciler{Client: c}
	key := types.NamespacedName{Namespace: s.Namespace, Name: s.Name}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatal(err)
	}
	// Pretend the StatefulSet controller brought the pod up.
	var set appsv1.StatefulSet
	if err := c.Get(context.Background(), key, &set); err != nil {
		t.Fatal(err)
	}
	set.Status.ReadyReplicas = 1
	if err := c.Status().Update(context.Background(), &set); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatal(err)
	}

	var got dross.Session
	if err := c.Get(context.Background(), key, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Phase != "Running" || got.Status.PodName != s.Name+"-0" {
		t.Errorf("status = %+v; want Running/%s-0", got.Status, s.Name)
	}
	var ready bool
	for _, cond := range got.Status.Conditions {
		if cond.Type == "Ready" && cond.Status == metav1.ConditionTrue {
			ready = true
		}
	}
	if !ready {
		t.Error("Ready condition not set once the pod is running")
	}
}

// A Session that no longer exists must end the pass quietly: its workload is
// already being garbage-collected through ownerReferences, and recreating one
// here would resurrect a session the server deleted.
func TestReconcileOnMissingSessionCreatesNothing(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(scheme(t)).Build()
	r := &SessionReconciler{Client: c}
	key := types.NamespacedName{Namespace: "dross-sessions", Name: "dross-gone"}

	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.Requeue {
		t.Error("requeued for a session that does not exist")
	}
	var sets appsv1.StatefulSetList
	if err := c.List(context.Background(), &sets); err != nil {
		t.Fatal(err)
	}
	if len(sets.Items) != 0 {
		t.Errorf("created %d statefulsets for a missing session; want 0", len(sets.Items))
	}
}

// Once a Session is being deleted, the controller must keep its hands off:
// creating a workload during teardown would leak one past the collection of
// its owner.
func TestReconcileDuringDeletionCreatesNothing(t *testing.T) {
	s := newSession()
	now := metav1.Now()
	s.DeletionTimestamp = &now
	s.Finalizers = []string{"dross.sh/test"} // the fake client requires one to hold a deleted object
	sc := scheme(t)
	c := fake.NewClientBuilder().WithScheme(sc).WithObjects(s).WithStatusSubresource(s).Build()
	r := &SessionReconciler{Client: c}
	key := types.NamespacedName{Namespace: s.Namespace, Name: s.Name}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	var sets appsv1.StatefulSetList
	if err := c.List(context.Background(), &sets); err != nil {
		t.Fatal(err)
	}
	if len(sets.Items) != 0 {
		t.Errorf("created %d statefulsets while deleting; want 0", len(sets.Items))
	}
}
