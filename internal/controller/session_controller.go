// Package controller reconciles Session resources into running workloads.
//
// The loop is deliberately dumb: for a Session, make sure a StatefulSet exists,
// then copy its readiness onto the Session's status. Everything harder is
// delegated to Kubernetes. ownerReferences garbage-collect the workload when the
// Session goes away, so no delete path in dross can leak one. The StatefulSet
// keeps the session alive across a crash or a node reboot, and gives it back the
// same volume — a bare pod has no controller behind it and simply stays dead.
//
// Reconcile is written as subreconcilers: a list of phases, each of which
// either says "carry on" or ends the pass with a result. The phases are
// independently readable and independently testable, and adding one — a
// finalizer, a Service, a network policy — is an entry in the list rather than
// another branch threaded through one long function.
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
	"github.com/dougkirkley/dross/internal/subreconciler"
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

// Reconcile runs the phases in order and stops at the first one that wants to
// end the pass.
func (r *SessionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return subreconciler.Run(ctx, req,
		r.sessionPresent,
		r.ensureStatefulSet,
		r.reflectStatus,
	)
}

// load fetches the Session a phase is working on.
//
// Every phase loads it again rather than passing one copy along. A phase may
// write to the Session, so a copy read at the top of the pass is stale by the
// time a later phase uses it, and writing it back would clobber what the
// earlier phase stored.
func (r *SessionReconciler) load(ctx context.Context, req ctrl.Request) (*dross.Session, error) {
	var s dross.Session
	if err := r.Get(ctx, req.NamespacedName, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// sessionPresent ends the pass when there is nothing to reconcile: the Session
// is gone, or it is being deleted and garbage collection owns the cleanup.
func (r *SessionReconciler) sessionPresent(ctx context.Context, req ctrl.Request) (*ctrl.Result, error) {
	s, err := r.load(ctx, req)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// ownerReferences have already taken the workload and its volume.
			return subreconciler.Done()
		}
		return subreconciler.Fail(err)
	}
	if !s.DeletionTimestamp.IsZero() {
		return subreconciler.Done()
	}
	return subreconciler.ContinueReconciling()
}

// ensureStatefulSet creates the workload for a Session if it does not exist.
func (r *SessionReconciler) ensureStatefulSet(ctx context.Context, req ctrl.Request) (*ctrl.Result, error) {
	s, err := r.load(ctx, req)
	if err != nil {
		return subreconciler.Fail(client.IgnoreNotFound(err))
	}

	var have appsv1.StatefulSet
	err = r.Get(ctx, req.NamespacedName, &have)
	if err == nil {
		// Only the image is reconciled onto an existing workload. Everything
		// else in the template is fixed when the session is created, and
		// rewriting the lot would restart a session for a change to a resource
		// limit or a label; the image is rolled precisely because the binary
		// running is the wrong one.
		c := have.Spec.Template.Spec.Containers
		if len(c) > 0 && c[0].Image != s.Spec.Image {
			c[0].Image = s.Spec.Image
			if err := r.Update(ctx, &have); err != nil {
				if apierrors.IsConflict(err) {
					return subreconciler.Retry()
				}
				return subreconciler.Fail(fmt.Errorf("statefulset image: %w", err))
			}
			log.FromContext(ctx).Info("session image updated",
				"session", s.Spec.SessionID, "image", s.Spec.Image)
		}
		return subreconciler.ContinueReconciling()
	}
	if !apierrors.IsNotFound(err) {
		return subreconciler.Fail(fmt.Errorf("statefulset: %w", err))
	}

	set := statefulSetFor(s)
	if set.Spec.VolumeClaimTemplates[0].Spec.StorageClassName == nil && r.StorageClass != "" {
		set.Spec.VolumeClaimTemplates[0].Spec.StorageClassName = &r.StorageClass
	}
	// Owned by the Session, so deleting the Session reclaims the workload and,
	// through the retention policy, its volume.
	if err := ctrl.SetControllerReference(s, set, r.Scheme()); err != nil {
		return subreconciler.Fail(err)
	}
	if err := r.Create(ctx, set); err != nil {
		if apierrors.IsAlreadyExists(err) {
			// Raced with our own cache; the next pass sees it.
			return subreconciler.Retry()
		}
		return subreconciler.Fail(fmt.Errorf("statefulset: %w", err))
	}
	log.FromContext(ctx).Info("created workload", "session", s.Spec.SessionID)
	return subreconciler.ContinueReconciling()
}

// reflectStatus copies the workload's readiness onto the Session.
func (r *SessionReconciler) reflectStatus(ctx context.Context, req ctrl.Request) (*ctrl.Result, error) {
	s, err := r.load(ctx, req)
	if err != nil {
		return subreconciler.Fail(client.IgnoreNotFound(err))
	}
	var set appsv1.StatefulSet
	if err := r.Get(ctx, req.NamespacedName, &set); err != nil {
		if apierrors.IsNotFound(err) {
			// The previous phase only just created it; come back for the status.
			return subreconciler.Retry()
		}
		return subreconciler.Fail(err)
	}

	phase, message := "Pending", ""
	switch {
	case set.Status.ReadyReplicas > 0:
		phase = "Running"
	case set.Spec.Replicas != nil && *set.Spec.Replicas == 0:
		phase = "Paused"
	default:
		message = podMessage(ctx, r, s)
	}
	podName := set.Name + "-0" // StatefulSet ordinal naming: stable across restarts

	if s.Status.Phase == phase && s.Status.PodName == podName && s.Status.Message == message {
		return subreconciler.ContinueReconciling() // nothing changed; don't write
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

	if err := r.Status().Update(ctx, s); err != nil {
		if apierrors.IsConflict(err) {
			// Someone else wrote first; re-read and try again.
			return subreconciler.Retry()
		}
		return subreconciler.Fail(err)
	}
	log.FromContext(ctx).V(1).Info("reconciled", "session", s.Spec.SessionID, "phase", phase)
	return subreconciler.ContinueReconciling()
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
