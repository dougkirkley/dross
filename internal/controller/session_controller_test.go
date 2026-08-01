package controller

import (
	"context"
	"testing"

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

// Reconciling a Session must produce exactly one pod and one volume, both owned
// by it so deletion garbage-collects them.
func TestReconcileCreatesOwnedPodAndVolume(t *testing.T) {
	s := newSession()
	sc := scheme(t)
	c := fake.NewClientBuilder().WithScheme(sc).
		WithObjects(s).WithStatusSubresource(s).Build()
	r := &SessionReconciler{Client: c}

	key := types.NamespacedName{Namespace: s.Namespace, Name: s.Name}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	var pod corev1.Pod
	if err := c.Get(context.Background(), key, &pod); err != nil {
		t.Fatalf("pod not created: %v", err)
	}
	var pvc corev1.PersistentVolumeClaim
	if err := c.Get(context.Background(), key, &pvc); err != nil {
		t.Fatalf("volume not created: %v", err)
	}

	// Ownership is what makes cleanup impossible to leak.
	for _, o := range []metav1.Object{&pod, &pvc} {
		refs := o.GetOwnerReferences()
		if len(refs) != 1 || refs[0].Kind != "Session" || refs[0].UID != "u-1" {
			t.Errorf("%T is not owned by its Session: %+v", o, refs)
		}
	}

	// The credential comes from a Secret, never the pod spec.
	env := pod.Spec.Containers[0].Env
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
	if pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
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
	var pods corev1.PodList
	if err := c.List(context.Background(), &pods); err != nil {
		t.Fatal(err)
	}
	if len(pods.Items) != 1 {
		t.Errorf("created %d pods; want exactly 1", len(pods.Items))
	}
}

// Pod phase must surface on the Session, so the API can report it without
// polling pods itself.
func TestStatusMirrorsThePod(t *testing.T) {
	s := newSession()
	sc := scheme(t)
	c := fake.NewClientBuilder().WithScheme(sc).WithObjects(s).WithStatusSubresource(s).Build()
	r := &SessionReconciler{Client: c}
	key := types.NamespacedName{Namespace: s.Namespace, Name: s.Name}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatal(err)
	}
	// Pretend the kubelet started it.
	var pod corev1.Pod
	if err := c.Get(context.Background(), key, &pod); err != nil {
		t.Fatal(err)
	}
	pod.Status.Phase = corev1.PodRunning
	if err := c.Status().Update(context.Background(), &pod); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatal(err)
	}

	var got dross.Session
	if err := c.Get(context.Background(), key, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Phase != "Running" || got.Status.PodName != s.Name {
		t.Errorf("status = %+v; want Running/%s", got.Status, s.Name)
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
