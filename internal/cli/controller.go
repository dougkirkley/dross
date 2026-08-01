package cli

import (
	"fmt"

	"github.com/spf13/cobra"
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

// newControllerCmd is `dross controller`: the reconciler that turns Session
// resources into running pods.
//
// It runs apart from `dross serve` on purpose. The server is internet-facing and
// executes model output; the controller is the component holding permission to
// create pods. Keeping them in separate processes — and separate namespaces —
// means a compromise of the web surface does not come with the ability to
// schedule workloads.
func newControllerCmd() *cobra.Command {
	var (
		metricsAddr, probeAddr string
		sessionNamespace       string
		storageClass           string
	)
	cmd := &cobra.Command{
		Use:   "controller",
		Short: "Reconcile dross Session resources into per-session pods",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// klog's text logger rather than zap: klog is already in the tree via
			// client-go, so this adds no dependency for the same output.
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
				// One replica: leader election would add a Lease and a failure
				// mode for no benefit at this size.
				LeaderElection: false,
			}
			if sessionNamespace != "" {
				// Watch only where sessions live, so the controller needs no
				// cluster-wide read on pods.
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
			fmt.Fprintf(cmd.OutOrStdout(),
				"dross controller watching Sessions in %s\n", orAll(sessionNamespace))
			return mgr.Start(ctrl.SetupSignalHandler())
		},
	}
	f := cmd.Flags()
	f.StringVar(&metricsAddr, "metrics-bind-address", "0", "metrics endpoint (0 disables)")
	f.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "health probe endpoint")
	f.StringVar(&sessionNamespace, "session-namespace", "dross-sessions", "namespace session pods run in")
	f.StringVar(&storageClass, "storage-class", "", "storage class for session volumes")
	return cmd
}

func orAll(ns string) string {
	if ns == "" {
		return "all namespaces"
	}
	return ns
}
