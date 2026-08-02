// Command dross-operator reconciles dross Session resources into per-session
// pods.
//
// It is a separate binary from dross itself, not a subcommand, because it is
// the component that holds permission to create pods. Shipping it apart means
// its image carries none of what the agent needs — no shell, no Go toolchain,
// no web bundle — so the most privileged component has the smallest attack
// surface, and it can be released without rolling the user-facing app.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2/textlogger"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	dross "github.com/dougkirkley/dross/api/v1alpha1"
	"github.com/dougkirkley/dross/internal/controller"
)

func main() {
	var (
		metricsAddr  string
		probeAddr    string
		storageClass string
	)
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "metrics endpoint (0 disables)")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "health probe endpoint")
	flag.StringVar(&storageClass, "storage-class", "", "storage class for session volumes")
	flag.Parse()

	if err := run(metricsAddr, probeAddr, watchNamespaces(), storageClass); err != nil {
		fmt.Fprintln(os.Stderr, "dross-operator:", err)
		os.Exit(1)
	}
}

// watchNamespaces reads WATCH_NAMESPACES, the convention for scoping an
// operator: a comma-separated list, empty meaning every namespace.
//
// It has to be a list rather than one namespace, because the operator watches
// two different things in two different places — Dross servers beside itself,
// and the Sessions they ask for in a namespace of their own.
func watchNamespaces() []string {
	var out []string
	for _, ns := range strings.Split(os.Getenv("WATCH_NAMESPACES"), ",") {
		if ns = strings.TrimSpace(ns); ns != "" {
			out = append(out, ns)
		}
	}
	return out
}

func run(metricsAddr, probeAddr string, namespaces []string, storageClass string) error {
	// klog's text logger: already in the tree via client-go, so this costs no
	// extra dependency for the same structured output.
	ctrl.SetLogger(textlogger.NewLogger(textlogger.NewConfig()))

	// Only the three groups the reconciler touches: Sessions, the StatefulSets
	// it creates for them, and the pods it reads status from. Registering the
	// full client-go scheme would work too, but every registered kind is a kind
	// the cache is willing to watch.
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		return err
	}
	if err := appsv1.AddToScheme(scheme); err != nil {
		return err
	}
	if err := dross.AddToScheme(scheme); err != nil {
		return err
	}

	cfg, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("kubernetes config: %w", err)
	}
	opts := ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		// One replica: leader election would add a Lease and a failure mode for
		// no benefit at this size.
		LeaderElection: false,
	}
	if len(namespaces) > 0 {
		// Watch only where dross lives, so the operator needs no cluster-wide
		// read on pods or secrets. Unset means every namespace, which is the
		// convention, but not what this deployment's RBAC grants.
		scoped := make(map[string]cache.Config, len(namespaces))
		for _, ns := range namespaces {
			scoped[ns] = cache.Config{}
		}
		opts.Cache = cache.Options{DefaultNamespaces: scoped}
	}
	mgr, err := ctrl.NewManager(cfg, opts)
	if err != nil {
		return fmt.Errorf("manager: %w", err)
	}
	if err := (&controller.SessionReconciler{
		Client:       mgr.GetClient(),
		StorageClass: storageClass,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("session controller: %w", err)
	}
	if err := (&controller.DrossReconciler{
		Client: mgr.GetClient(),
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("dross controller: %w", err)
	}
	if err := mgr.AddHealthzCheck("ping", healthz.Ping); err != nil {
		return err
	}
	if err := mgr.AddReadyzCheck("ping", healthz.Ping); err != nil {
		return err
	}
	return mgr.Start(ctrl.SetupSignalHandler())
}
