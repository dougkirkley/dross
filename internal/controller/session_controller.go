// Package controller reconciles Session resources into running workloads.
//
// The loop is deliberately dumb: for a Session, make sure a StatefulSet exists,
// then copy its readiness onto the Session's status. Everything harder is
// delegated to Kubernetes. ownerReferences garbage-collect the workload when the
// Session goes away, so no delete path in dross can leak one. The StatefulSet
// keeps the session alive across a crash or a node reboot, and gives it back the
// same volume — a bare pod has no controller behind it and simply stays dead.
package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	dross "github.com/dougkirkley/dross/api/v1alpha1"
)

// SessionReconciler drives Session resources towards a running pod.
type SessionReconciler struct {
	client.Client
	// StorageClass for session volumes; empty uses the cluster default.
	StorageClass string
}

// +kubebuilder:rbac:groups=dross.sh,resources=sessions,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups=dross.sh,resources=sessions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

// Reconcile ensures the workload for a Session exists, and reports back what it
// is doing.
func (r *SessionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	lg := log.FromContext(ctx)

	var s dross.Session
	if err := r.Get(ctx, req.NamespacedName, &s); err != nil {
		// Gone: ownerReferences have already taken the pod and volume with it.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !s.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil // deleting; garbage collection handles the children
	}

	set, err := r.ensureStatefulSet(ctx, &s)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("statefulset: %w", err)
	}

	if err := r.updateStatus(ctx, &s, set); err != nil {
		// A conflict just means someone else wrote first; the watch will re-run.
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, err
	}
	lg.V(1).Info("reconciled", "session", s.Spec.SessionID, "phase", s.Status.Phase)
	return ctrl.Result{}, nil
}

func (r *SessionReconciler) ensureStatefulSet(ctx context.Context, s *dross.Session) (*appsv1.StatefulSet, error) {
	var have appsv1.StatefulSet
	err := r.Get(ctx, types.NamespacedName{Namespace: s.Namespace, Name: s.Name}, &have)
	if err == nil {
		return &have, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}
	set := statefulSetFor(s)
	if set.Spec.VolumeClaimTemplates[0].Spec.StorageClassName == nil && r.StorageClass != "" {
		set.Spec.VolumeClaimTemplates[0].Spec.StorageClassName = &r.StorageClass
	}
	// Owned by the Session, so deleting the Session reclaims the workload and,
	// through the retention policy, its volume.
	if err := ctrl.SetControllerReference(s, set, r.Scheme()); err != nil {
		return nil, err
	}
	if err := r.Create(ctx, set); err != nil {
		return nil, err
	}
	return set, nil
}

func (r *SessionReconciler) updateStatus(ctx context.Context, s *dross.Session, set *appsv1.StatefulSet) error {
	phase, message, podName := "", "", ""
	if set != nil {
		podName = set.Name + "-0" // StatefulSet ordinal naming: stable across restarts
		switch {
		case set.Status.ReadyReplicas > 0:
			phase = "Running"
		case set.Spec.Replicas != nil && *set.Spec.Replicas == 0:
			phase = "Paused"
		default:
			phase = "Pending"
			message = podMessage(ctx, r, s)
		}
	}
	if s.Status.Phase == phase && s.Status.PodName == podName && s.Status.Message == message {
		return nil // nothing changed; don't write
	}
	s.Status.Phase, s.Status.PodName, s.Status.Message = phase, podName, message
	cond := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		Reason:             "NotRunning",
		Message:            message,
		LastTransitionTime: metav1.Now(),
	}
	if phase == "Running" {
		cond.Status, cond.Reason, cond.Message = metav1.ConditionTrue, "Running", ""
	}
	setCondition(&s.Status.Conditions, cond)
	return r.Status().Update(ctx, s)
}

// podMessage surfaces why a session is not running yet — an image pull failure
// or an unschedulable pod — so the cause shows without digging through events.
func podMessage(ctx context.Context, r *SessionReconciler, s *dross.Session) string {
	var pod corev1.Pod
	if err := r.Get(ctx, types.NamespacedName{Namespace: s.Namespace, Name: s.Name + "-0"}, &pod); err != nil {
		return ""
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if w := cs.State.Waiting; w != nil && w.Reason != "" {
			if w.Message != "" {
				return w.Reason + ": " + w.Message
			}
			return w.Reason
		}
	}
	return ""
}

// SetupWithManager registers the controller and watches the StatefulSets it
// owns, so a change re-runs the loop immediately rather than on resync.
func (r *SessionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dross.Session{}).
		Owns(&appsv1.StatefulSet{}).
		Complete(r)
}

func setCondition(conds *[]metav1.Condition, c metav1.Condition) {
	for i := range *conds {
		if (*conds)[i].Type == c.Type {
			if (*conds)[i].Status == c.Status {
				c.LastTransitionTime = (*conds)[i].LastTransitionTime
			}
			(*conds)[i] = c
			return
		}
	}
	*conds = append(*conds, c)
}

func labelsFor(s *dross.Session) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":      "dross-session",
		"app.kubernetes.io/component": "agent",
		"dross.sh/session":            s.Name,
	}
}
