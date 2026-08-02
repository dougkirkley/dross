// Package subreconciler splits a reconcile pass into ordered phases.
//
// A controller's Reconcile tends to grow into one long function where the
// interesting part — what happens, in what order, and what stops the pass — is
// buried in early returns. Here a pass is a list of phases. Each one either
// says "carry on" or returns the result that ends the pass, and Run walks them
// in order and stops at the first that wants to stop.
//
// The convention is the one the pattern is usually written with: a nil result
// with a nil error means keep going, and anything else ends the pass. That
// makes the two cases impossible to confuse — there is no "empty result that
// means continue" to get wrong.
//
// Written here rather than pulled in: it is four small functions and a loop,
// and the published implementations set Result.Requeue, which controller-runtime
// has deprecated in favour of RequeueAfter.
package subreconciler

import (
	"context"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
)

// RetryDelay is how soon Retry comes back. Short, because the things that use
// it are races that have already resolved by the time we look again: a create
// that has not reached our cache, a status write that lost to another writer.
const RetryDelay = time.Second

// Fn is one phase of a reconcile pass.
//
// Phases take the request rather than the object: each loads what it needs
// itself, so a phase that writes cannot be handed a copy that another phase
// has already made stale.
type Fn = func(context.Context, ctrl.Request) (*ctrl.Result, error)

// Run executes phases in order, stopping at the first one that ends the pass,
// and returns what Reconcile should return.
func Run(ctx context.Context, req ctrl.Request, phases ...Fn) (ctrl.Result, error) {
	for _, phase := range phases {
		res, err := phase(ctx, req)
		if ShouldHalt(res, err) {
			return Evaluate(res, err)
		}
	}
	return ctrl.Result{}, nil
}

// ContinueReconciling hands the pass to the next phase.
func ContinueReconciling() (*ctrl.Result, error) { return nil, nil }

// Done ends the pass successfully, with no requeue. The controller still sees
// the object again when something about it changes.
func Done() (*ctrl.Result, error) { return &ctrl.Result{}, nil }

// Retry ends the pass and comes back shortly. For races rather than failures:
// there is nothing wrong, we just looked a moment too early.
func Retry() (*ctrl.Result, error) { return RequeueAfter(RetryDelay) }

// RequeueAfter ends the pass and comes back once d has passed.
func RequeueAfter(d time.Duration) (*ctrl.Result, error) {
	return &ctrl.Result{RequeueAfter: d}, nil
}

// Fail ends the pass with an error. No requeue is set because returning an
// error already requeues, with the controller's backoff; asking for both makes
// a failing object spin.
func Fail(err error) (*ctrl.Result, error) { return &ctrl.Result{}, err }

// ShouldHalt reports whether a phase ended the pass.
func ShouldHalt(res *ctrl.Result, err error) bool { return res != nil || err != nil }

// Evaluate converts a phase's result into Reconcile's return values.
func Evaluate(res *ctrl.Result, err error) (ctrl.Result, error) {
	if res == nil {
		return ctrl.Result{}, err
	}
	return *res, err
}
