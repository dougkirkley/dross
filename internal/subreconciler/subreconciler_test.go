package subreconciler

import (
	"context"
	"errors"
	"testing"

	ctrl "sigs.k8s.io/controller-runtime"
)

func req() ctrl.Request { return ctrl.Request{} }

// The point of the package: phases run in order, and the first one to end the
// pass stops the ones behind it.
func TestRunStopsAtTheFirstHaltingPhase(t *testing.T) {
	var ran []string
	res, err := Run(context.Background(), req(),
		func(context.Context, ctrl.Request) (*ctrl.Result, error) {
			ran = append(ran, "first")
			return ContinueReconciling()
		},
		func(context.Context, ctrl.Request) (*ctrl.Result, error) {
			ran = append(ran, "second")
			return Done()
		},
		func(context.Context, ctrl.Request) (*ctrl.Result, error) {
			ran = append(ran, "third")
			return ContinueReconciling()
		},
	)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Errorf("RequeueAfter = %v; Done must not requeue", res.RequeueAfter)
	}
	if len(ran) != 2 || ran[0] != "first" || ran[1] != "second" {
		t.Errorf("ran %v; want the first two phases only", ran)
	}
}

// Running every phase to completion is success with no requeue.
func TestRunToCompletion(t *testing.T) {
	count := 0
	carryOn := func(context.Context, ctrl.Request) (*ctrl.Result, error) {
		count++
		return ContinueReconciling()
	}
	res, err := Run(context.Background(), req(), carryOn, carryOn, carryOn)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if count != 3 {
		t.Errorf("ran %d phases; want 3", count)
	}
	if res.RequeueAfter != 0 {
		t.Errorf("RequeueAfter = %v; want none", res.RequeueAfter)
	}
}

// An error has to reach Reconcile, or the controller cannot back off and
// retry, and the failure is silently swallowed.
func TestRunSurfacesTheError(t *testing.T) {
	boom := errors.New("boom")
	res, err := Run(context.Background(), req(),
		func(context.Context, ctrl.Request) (*ctrl.Result, error) { return Fail(boom) },
		func(context.Context, ctrl.Request) (*ctrl.Result, error) {
			t.Error("ran a phase after a failure")
			return ContinueReconciling()
		},
	)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v; want boom", err)
	}
	// Returning an error requeues on its own; also asking for a requeue would
	// make a persistently failing object spin instead of backing off.
	if res.RequeueAfter != 0 {
		t.Errorf("RequeueAfter = %v; an error must not also set a requeue", res.RequeueAfter)
	}
}

// Retry is the "look again shortly" case and must actually schedule one.
func TestRetryRequeues(t *testing.T) {
	res, err := Run(context.Background(), req(),
		func(context.Context, ctrl.Request) (*ctrl.Result, error) { return Retry() },
	)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.RequeueAfter != RetryDelay {
		t.Errorf("RequeueAfter = %v; want %v", res.RequeueAfter, RetryDelay)
	}
}

func TestShouldHalt(t *testing.T) {
	for _, tc := range []struct {
		name string
		res  *ctrl.Result
		err  error
		want bool
	}{
		{"continue", nil, nil, false},
		{"result", &ctrl.Result{}, nil, true},
		{"error", nil, errors.New("x"), true},
		{"both", &ctrl.Result{}, errors.New("x"), true},
	} {
		if got := ShouldHalt(tc.res, tc.err); got != tc.want {
			t.Errorf("%s: ShouldHalt = %v; want %v", tc.name, got, tc.want)
		}
	}
}
