package controller

import (
	"context"
	"fmt"
	"reflect"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	dross "github.com/dougkirkley/dross/api/v1alpha1"
	"github.com/dougkirkley/dross/internal/subreconciler"
)

// DrossReconciler runs a dross server from a Dross resource.
//
// The server is reconciled by the same operator that reconciles the sessions it
// asks for, which is the point: the namespace sessions go in, the image they
// run and the address they dial back to are all derived here and written into
// the server's environment, so the two halves cannot be configured out of step.
type DrossReconciler struct {
	client.Client
}

// +kubebuilder:rbac:groups=dross.sh,resources=drosses,verbs=get;list;watch
// +kubebuilder:rbac:groups=dross.sh,resources=drosses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch

// Reconcile brings the server up to what the Dross resource asks for.
func (r *DrossReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return subreconciler.Run(ctx, req,
		r.drossPresent,
		r.ensureService,
		r.ensureDeployment,
		r.reflectServerStatus,
	)
}

func (r *DrossReconciler) load(ctx context.Context, req ctrl.Request) (*dross.Dross, error) {
	var d dross.Dross
	if err := r.Get(ctx, req.NamespacedName, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// drossPresent ends the pass when the server has been deleted or is going away.
func (r *DrossReconciler) drossPresent(ctx context.Context, req ctrl.Request) (*ctrl.Result, error) {
	d, err := r.load(ctx, req)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return subreconciler.Done()
		}
		return subreconciler.Fail(err)
	}
	if !d.DeletionTimestamp.IsZero() {
		return subreconciler.Done()
	}
	if d.Spec.Image == "" {
		// Nothing to run, and defaulting an image would be a guess at which
		// version of dross the user meant.
		return subreconciler.Fail(fmt.Errorf("spec.image is required"))
	}
	return subreconciler.ContinueReconciling()
}

func (r *DrossReconciler) ensureService(ctx context.Context, req ctrl.Request) (*ctrl.Result, error) {
	d, err := r.load(ctx, req)
	if err != nil {
		return subreconciler.Fail(client.IgnoreNotFound(err))
	}
	want := serviceFor(d)
	if err := ctrl.SetControllerReference(d, want, r.Scheme()); err != nil {
		return subreconciler.Fail(err)
	}

	var have corev1.Service
	err = r.Get(ctx, client.ObjectKeyFromObject(want), &have)
	if apierrors.IsNotFound(err) {
		if err := r.Create(ctx, want); err != nil {
			return subreconciler.Fail(fmt.Errorf("service: %w", err))
		}
		return subreconciler.ContinueReconciling()
	}
	if err != nil {
		return subreconciler.Fail(err)
	}
	// ClusterIP is assigned by the API server and cannot be written back.
	if !reflect.DeepEqual(have.Spec.Ports, want.Spec.Ports) ||
		!reflect.DeepEqual(have.Spec.Selector, want.Spec.Selector) {
		have.Spec.Ports, have.Spec.Selector = want.Spec.Ports, want.Spec.Selector
		if err := r.Update(ctx, &have); err != nil {
			return subreconciler.Fail(fmt.Errorf("service: %w", err))
		}
	}
	return subreconciler.ContinueReconciling()
}

func (r *DrossReconciler) ensureDeployment(ctx context.Context, req ctrl.Request) (*ctrl.Result, error) {
	d, err := r.load(ctx, req)
	if err != nil {
		return subreconciler.Fail(client.IgnoreNotFound(err))
	}
	want := deploymentFor(d)
	if err := ctrl.SetControllerReference(d, want, r.Scheme()); err != nil {
		return subreconciler.Fail(err)
	}

	var have appsv1.Deployment
	err = r.Get(ctx, client.ObjectKeyFromObject(want), &have)
	if apierrors.IsNotFound(err) {
		if err := r.Create(ctx, want); err != nil {
			return subreconciler.Fail(fmt.Errorf("deployment: %w", err))
		}
		log.FromContext(ctx).Info("created server", "image", d.Spec.Image)
		return subreconciler.ContinueReconciling()
	}
	if err != nil {
		return subreconciler.Fail(err)
	}

	// Unlike a Session's workload, this one is updated in place: an image bump
	// or a change to the session settings has to reach a server that is already
	// running, which is the whole reason for managing it here.
	if reflect.DeepEqual(have.Spec, want.Spec) {
		return subreconciler.ContinueReconciling()
	}
	have.Spec = want.Spec
	if err := r.Update(ctx, &have); err != nil {
		if apierrors.IsConflict(err) {
			return subreconciler.Retry()
		}
		return subreconciler.Fail(fmt.Errorf("deployment: %w", err))
	}
	log.FromContext(ctx).Info("updated server", "image", d.Spec.Image)
	return subreconciler.ContinueReconciling()
}

func (r *DrossReconciler) reflectServerStatus(ctx context.Context, req ctrl.Request) (*ctrl.Result, error) {
	d, err := r.load(ctx, req)
	if err != nil {
		return subreconciler.Fail(client.IgnoreNotFound(err))
	}
	var dep appsv1.Deployment
	if err := r.Get(ctx, req.NamespacedName, &dep); err != nil {
		if apierrors.IsNotFound(err) {
			return subreconciler.Retry()
		}
		return subreconciler.Fail(err)
	}

	phase, message := "Pending", ""
	switch {
	case dep.Status.ReadyReplicas > 0:
		phase = "Running"
	case dep.Spec.Replicas != nil && *dep.Spec.Replicas == 0:
		phase = "Paused"
	default:
		message = deploymentMessage(&dep)
	}
	mode := "in-process"
	if d.Spec.Sessions.Namespace != "" {
		mode = "pods"
	}

	if d.Status.Phase == phase && d.Status.ReadyReplicas == dep.Status.ReadyReplicas &&
		d.Status.SessionMode == mode && d.Status.Message == message {
		return subreconciler.ContinueReconciling()
	}
	d.Status.Phase, d.Status.ReadyReplicas = phase, dep.Status.ReadyReplicas
	d.Status.SessionMode, d.Status.Message = mode, message
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
	setCondition(&d.Status.Conditions, cond)

	if err := r.Status().Update(ctx, d); err != nil {
		if apierrors.IsConflict(err) {
			return subreconciler.Retry()
		}
		return subreconciler.Fail(err)
	}
	return subreconciler.ContinueReconciling()
}

// deploymentMessage surfaces why a server is not up yet, so the cause shows on
// the Dross object rather than only in the Deployment's conditions.
func deploymentMessage(dep *appsv1.Deployment) string {
	for _, c := range dep.Status.Conditions {
		if c.Type == appsv1.DeploymentProgressing && c.Status == corev1.ConditionFalse {
			return c.Reason + ": " + c.Message
		}
		if c.Type == appsv1.DeploymentReplicaFailure && c.Status == corev1.ConditionTrue {
			return c.Reason + ": " + c.Message
		}
	}
	return ""
}

// SetupWithManager registers the controller and watches what it owns, so a
// change to the server's workload re-runs the loop immediately.
func (r *DrossReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dross.Dross{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Complete(r)
}
