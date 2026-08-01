// Package controller reconciles Session resources into running pods.
//
// The loop is deliberately dumb: for a Session, make sure a volume and a pod
// exist, then copy the pod's phase onto the Session's status. Everything harder
// is delegated to Kubernetes — ownerReferences garbage-collect the pod and
// volume when the Session goes away, so no delete path in dross can leak them,
// and the watch re-runs the loop whenever reality drifts from the spec.
package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
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
// +kubebuilder:rbac:groups="",resources=pods;persistentvolumeclaims,verbs=get;list;watch;create;delete

// Reconcile ensures the pod and volume for a Session exist, and reports back
// what the pod is doing.
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

	if err := r.ensurePVC(ctx, &s); err != nil {
		return ctrl.Result{}, fmt.Errorf("volume: %w", err)
	}
	pod, err := r.ensurePod(ctx, &s)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("pod: %w", err)
	}

	if err := r.updateStatus(ctx, &s, pod); err != nil {
		// A conflict just means someone else wrote first; the watch will re-run.
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, err
	}
	lg.V(1).Info("reconciled", "session", s.Spec.SessionID, "phase", s.Status.Phase)
	return ctrl.Result{}, nil
}

func (r *SessionReconciler) ensurePVC(ctx context.Context, s *dross.Session) error {
	var have corev1.PersistentVolumeClaim
	err := r.Get(ctx, types.NamespacedName{Namespace: s.Namespace, Name: s.Name}, &have)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	size := s.Spec.Storage
	if size == "" {
		size = "5Gi"
	}
	qty, err := resource.ParseQuantity(size)
	if err != nil {
		return fmt.Errorf("storage %q: %w", size, err)
	}
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Namespace: s.Namespace, Name: s.Name, Labels: labelsFor(s)},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: qty},
			},
		},
	}
	if r.StorageClass != "" {
		pvc.Spec.StorageClassName = &r.StorageClass
	}
	// Owned by the Session, so deleting the Session reclaims the volume.
	if err := ctrl.SetControllerReference(s, pvc, r.Scheme()); err != nil {
		return err
	}
	return r.Create(ctx, pvc)
}

func (r *SessionReconciler) ensurePod(ctx context.Context, s *dross.Session) (*corev1.Pod, error) {
	var have corev1.Pod
	err := r.Get(ctx, types.NamespacedName{Namespace: s.Namespace, Name: s.Name}, &have)
	if err == nil {
		return &have, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}
	// A finished session is not restarted: restartPolicy is Never, so a
	// Succeeded or Failed pod is a terminal state, not drift to heal.
	if s.Status.Phase == string(corev1.PodSucceeded) || s.Status.Phase == string(corev1.PodFailed) {
		return nil, nil
	}
	pod := podFor(s)
	if err := ctrl.SetControllerReference(s, pod, r.Scheme()); err != nil {
		return nil, err
	}
	if err := r.Create(ctx, pod); err != nil {
		return nil, err
	}
	return pod, nil
}

func (r *SessionReconciler) updateStatus(ctx context.Context, s *dross.Session, pod *corev1.Pod) error {
	phase, message, podName := "", "", ""
	if pod != nil {
		phase, podName = string(pod.Status.Phase), pod.Name
		// Surface why a pod is stuck, so the cause shows without reading events.
		for _, cs := range pod.Status.ContainerStatuses {
			if w := cs.State.Waiting; w != nil && w.Reason != "" {
				message = w.Reason
				if w.Message != "" {
					message += ": " + w.Message
				}
			}
		}
	}
	if s.Status.Phase == phase && s.Status.PodName == podName && s.Status.Message == message {
		return nil // nothing changed; don't write
	}
	s.Status.Phase, s.Status.PodName, s.Status.Message = phase, podName, message
	meta := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		Reason:             "PodNotRunning",
		Message:            message,
		LastTransitionTime: metav1.Now(),
	}
	if phase == string(corev1.PodRunning) {
		meta.Status, meta.Reason, meta.Message = metav1.ConditionTrue, "PodRunning", ""
	}
	setCondition(&s.Status.Conditions, meta)
	return r.Status().Update(ctx, s)
}

// SetupWithManager registers the controller and tells it to watch the pods it
// owns, so a pod dying re-runs the loop immediately rather than on resync.
func (r *SessionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dross.Session{}).
		Owns(&corev1.Pod{}).
		Owns(&corev1.PersistentVolumeClaim{}).
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
