package main

import (
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/volcano-sh/agentcube/pkg/agentd"
)

var (
	schemeBuilder = runtime.NewScheme()
)

func init() {
	utilruntime.Must(scheme.AddToScheme(schemeBuilder))
	utilruntime.Must(sandboxv1alpha1.AddToScheme(schemeBuilder))
}

func main() {
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: schemeBuilder,
		Metrics: metricsserver.Options{
			BindAddress: "0", // Disable metrics server
		},
		HealthProbeBindAddress: "0", // Disable health probe server

	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "unable to start manager: %v\n", err)
		os.Exit(1)
	}

	err = ctrl.NewControllerManagedBy(mgr).
		For(&sandboxv1alpha1.Sandbox{}).
		Complete(&agentd.AgentdReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
		})
	if err != nil {
		fmt.Fprintf(os.Stderr, "unable to create controller: %v\n", err)
		os.Exit(1)
	}

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		fmt.Fprintf(os.Stderr, "problem running manager: %v\n", err)
		os.Exit(1)
	}
}
