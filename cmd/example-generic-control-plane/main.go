// Command example-generic-control-plane demonstrates embedding kausality
// in a generic Kubernetes-style API server using k8s.io/apiserver.
//
// This example uses kcp-dev/embeddedetcd for storage and hardcodes a policy
// that tracks all resources in enforce mode.
//
// Usage:
//
//	go run . --data-dir=/tmp/example-control-plane
//
// For a full implementation, see:
//   - github.com/kcp-dev/generic-controlplane - Complete generic control plane
//   - github.com/kubernetes/sample-apiserver - Kubernetes sample apiserver
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/kcp-dev/embeddedetcd"
	"github.com/kcp-dev/embeddedetcd/options"
	genericoptions "k8s.io/apiserver/pkg/server/options"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

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
	var dataDir string

	flag.StringVar(&dataDir, "data-dir", "/tmp/example-control-plane", "Data directory for etcd and server state")

	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	log := zap.New(zap.UseFlagOptions(&opts))

	log.Info("starting example-generic-control-plane",
		"dataDir", dataDir,
	)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Create in-memory policy resolver that enforces all resources
	policyResolver := policy.NewStaticResolver(kausalityv1alpha1.ModeEnforce)
	log.Info("policy resolver created", "mode", kausalityv1alpha1.ModeEnforce)

	// Start server with embedded etcd
	if err := run(ctx, log, dataDir, policyResolver); err != nil {
		log.Error(err, "server failed")
		os.Exit(1)
	}
}

func run(ctx context.Context, log logr.Logger, dataDir string, policyResolver policy.Resolver) error {
	log.Info("starting embedded etcd server")

	// Create embedded etcd options with root directory
	etcdOpts := options.NewOptions(dataDir)
	etcdOpts.Enabled = true

	// Create standard EtcdOptions for completion
	// In a real apiserver, these come from the apiserver's configuration
	genericEtcdOpts := genericoptions.NewEtcdOptions(nil)
	genericEtcdOpts.StorageConfig.Transport.ServerList = []string{"embedded"}

	// Complete the embedded etcd options
	completedEtcdOpts := etcdOpts.Complete(genericEtcdOpts)

	// Create etcd config
	etcdConfig, err := embeddedetcd.NewConfig(completedEtcdOpts, true)
	if err != nil {
		return fmt.Errorf("failed to create etcd config: %w", err)
	}

	// Start etcd server
	etcdServer := embeddedetcd.NewServer(etcdConfig.Complete())
	if etcdServer == nil {
		return fmt.Errorf("failed to create etcd server")
	}

	// Run etcd server in background
	etcdDone := make(chan struct{})
	go func() {
		defer close(etcdDone)
		if err := etcdServer.Run(ctx); err != nil {
			log.Error(err, "embedded etcd server error")
		}
	}()

	// Wait for etcd to be ready
	log.Info("waiting for embedded etcd to be ready")
	select {
	case <-time.After(60 * time.Second):
		return fmt.Errorf("etcd server startup timed out")
	case <-etcdDone:
		return fmt.Errorf("etcd server exited unexpectedly")
	case <-ctx.Done():
		return ctx.Err()
	default:
		// Give etcd some time to start
		time.Sleep(2 * time.Second)
	}

	log.Info("embedded etcd started successfully")

	// Demonstrate kausality policy resolution
	demoContext := policy.ResourceContext{
		GVR:       schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"},
		Namespace: "default",
	}
	mode := policyResolver.ResolveMode(demoContext, nil, nil)
	log.Info("kausality policy resolution demo",
		"resource", "apps/v1/deployments",
		"namespace", "default",
		"resolvedMode", mode,
	)

	// In a full implementation, the admission handler would be wired into
	// the apiserver's admission chain like this:
	//
	// admissionHandler := admission.NewHandler(admission.Config{
	//     Client:         client,
	//     Log:            log,
	//     PolicyResolver: policyResolver,
	// })
	//
	// Then register it in the admission chain:
	// genericConfig.AdmissionControl = admissionHandler
	//
	// The admission handler intercepts all mutations and:
	// 1. Records controller identity (via user hash tracking)
	// 2. Detects drift (controller changing child when parent is stable)
	// 3. Propagates causal traces
	// 4. Enforces or logs based on policy

	log.Info("example server running",
		"note", "In a full implementation, this would serve the API with kausality admission",
	)

	// Block until context is done
	<-ctx.Done()
	log.Info("shutting down")

	// Wait for etcd to shut down
	select {
	case <-etcdDone:
	case <-time.After(5 * time.Second):
		log.Info("etcd shutdown timed out")
	}

	return nil
}
