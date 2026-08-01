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
		metricsAddr      string
		probeAddr        string
		sessionNamespace string
		storageClass     string
	)
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "metrics endpoint (0 disables)")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "health probe endpoint")
	flag.StringVar(&sessionNamespace, "session-namespace", "dross-sessions",
		"namespace session pods run in")
	flag.StringVar(&storageClass, "storage-class", "", "storage class for session volumes")
	flag.Parse()

	if err := run(metricsAddr, probeAddr, sessionNamespace, storageClass); err != nil {
		fmt.Fprintln(os.Stderr, "dross-operator:", err)
		os.Exit(1)
	}
}

func run(metricsAddr, probeAddr, sessionNamespace, storageClass string) error {
	// klog's text logger: already in the tree via client-go, so this costs no
	// extra dependency for the same structured output.
	ctrl.SetLogger(textlogger.NewLogger(textlogger.NewConfig()))

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
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
	if sessionNamespace != "" {
		// Watch only where sessions live, so the operator needs no cluster-wide
		// read on pods.
		opts.Cache = cache.Options{
			DefaultNamespaces: map[string]cache.Config{sessionNamespace: {}},
		}
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
	if err := mgr.AddHealthzCheck("ping", healthz.Ping); err != nil {
		return err
	}
	if err := mgr.AddReadyzCheck("ping", healthz.Ping); err != nil {
		return err
	}
	return mgr.Start(ctrl.SetupSignalHandler())
}
