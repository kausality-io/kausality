// Command kausality-controller runs the Kausality policy controller.
// It watches Kausality CRD instances and reconciles webhook configuration.
package main

import (
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/discovery"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	kausalityv1alpha1 "github.com/kausality-io/kausality/api/v1alpha1"
	"github.com/kausality-io/kausality/pkg/policy"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(kausalityv1alpha1.AddToScheme(scheme))
}

func main() {
	var (
		metricsAddr            string
		healthProbeBindAddress string
		leaderElect            bool
		webhookName            string
		webhookNamespace       string
		webhookServiceName     string
	)

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address for the metrics endpoint")
	flag.StringVar(&healthProbeBindAddress, "health-probe-bind-address", ":8081", "The address for health probes")
	flag.BoolVar(&leaderElect, "leader-elect", false, "Enable leader election for controller manager")
	flag.StringVar(&webhookName, "webhook-name", "kausality", "Name of the MutatingWebhookConfiguration to manage")
	flag.StringVar(&webhookNamespace, "webhook-namespace", "kausality-system", "Namespace of the webhook service")
	flag.StringVar(&webhookServiceName, "webhook-service-name", "kausality-webhook", "Name of the webhook service")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	log := zap.New(zap.UseFlagOptions(&opts))
	ctrl.SetLogger(log)

	log.Info("starting kausality-controller",
		"webhookName", webhookName,
		"webhookNamespace", webhookNamespace,
		"webhookServiceName", webhookServiceName,
	)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: healthProbeBindAddress,
		LeaderElection:         leaderElect,
		LeaderElectionID:       "kausality-controller",
	})
	if err != nil {
		log.Error(err, "unable to create manager")
		os.Exit(1)
	}

	// Create discovery client for resource expansion
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(ctrl.GetConfigOrDie())
	if err != nil {
		log.Error(err, "unable to create discovery client")
		os.Exit(1)
	}

	// Set up the policy controller
	controller := &policy.Controller{
		Client:          mgr.GetClient(),
		Log:             log.WithName("controller"),
		Scheme:          mgr.GetScheme(),
		DiscoveryClient: discoveryClient,
		WebhookName:     webhookName,
		WebhookServiceRef: policy.WebhookServiceRef{
			Namespace: webhookNamespace,
			Name:      webhookServiceName,
			Port:      443,
			Path:      "/mutate",
		},
		ExcludedNamespaces: []string{"kube-system", "kube-public", "kube-node-lease"},
	}

	if err := controller.SetupWithManager(mgr); err != nil {
		log.Error(err, "unable to set up controller")
		os.Exit(1)
	}

	// Add health checks
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	log.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "manager exited with error")
		os.Exit(1)
	}
}
