//go:build envtest
// +build envtest

package admission_test

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/kausality-io/kausality/pkg/admission"
)

// Shared test environment
var (
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
	scheme    = runtime.NewScheme()
	testNS    string
	ctx       context.Context
	cancel    context.CancelFunc
)

func TestMain(m *testing.M) {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	ctx, cancel = context.WithCancel(context.Background())

	// Build mutating webhook configuration
	failPolicy := admissionv1.Fail
	sideEffects := admissionv1.SideEffectClassNone
	matchPolicy := admissionv1.Equivalent
	webhookPath := "/mutate"

	mutatingWebhook := &admissionv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "kausality-webhook",
		},
		Webhooks: []admissionv1.MutatingWebhook{
			{
				Name:                    "mutate.admission.kausality.io",
				AdmissionReviewVersions: []string{"v1"},
				SideEffects:             &sideEffects,
				FailurePolicy:           &failPolicy,
				MatchPolicy:             &matchPolicy,
				ClientConfig: admissionv1.WebhookClientConfig{
					Service: &admissionv1.ServiceReference{
						Path: &webhookPath,
					},
				},
				Rules: []admissionv1.RuleWithOperations{
					{
						Operations: []admissionv1.OperationType{
							admissionv1.Create,
							admissionv1.Update,
							admissionv1.Delete,
						},
						Rule: admissionv1.Rule{
							APIGroups:   []string{"apps"},
							APIVersions: []string{"v1"},
							Resources:   []string{"deployments", "replicasets", "statefulsets", "daemonsets"},
						},
					},
					{
						Operations: []admissionv1.OperationType{
							admissionv1.Update,
						},
						Rule: admissionv1.Rule{
							APIGroups:   []string{"apps"},
							APIVersions: []string{"v1"},
							Resources:   []string{"deployments/status", "replicasets/status", "statefulsets/status", "daemonsets/status"},
						},
					},
					{
						Operations: []admissionv1.OperationType{
							admissionv1.Create,
							admissionv1.Update,
							admissionv1.Delete,
						},
						Rule: admissionv1.Rule{
							APIGroups:   []string{""},
							APIVersions: []string{"v1"},
							Resources:   []string{"configmaps", "secrets", "services"},
						},
					},
				},
			},
		},
	}

	testEnv = &envtest.Environment{
		WebhookInstallOptions: envtest.WebhookInstallOptions{
			MutatingWebhooks: []*admissionv1.MutatingWebhookConfiguration{mutatingWebhook},
		},
	}

	var err error
	cfg, err = testEnv.Start()
	if err != nil {
		panic(fmt.Sprintf("failed to start envtest: %v", err))
	}

	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		panic(fmt.Sprintf("failed to create client: %v", err))
	}

	// Start webhook server
	webhookInstallOptions := &testEnv.WebhookInstallOptions
	webhookServer := webhook.NewServer(webhook.Options{
		Host:    webhookInstallOptions.LocalServingHost,
		Port:    webhookInstallOptions.LocalServingPort,
		CertDir: webhookInstallOptions.LocalServingCertDir,
	})

	// Create and register the admission handler
	handler := admission.NewHandler(admission.Config{
		Client: k8sClient,
		Log:    ctrl.Log,
	})
	webhookServer.Register("/mutate", &webhook.Admission{Handler: handler})

	// Start webhook server in background
	go func() {
		if err := webhookServer.Start(ctx); err != nil {
			panic(fmt.Sprintf("failed to start webhook server: %v", err))
		}
	}()

	// Wait for webhook server to be ready
	dialer := &net.Dialer{Timeout: time.Second}
	addrPort := fmt.Sprintf("%s:%d", webhookInstallOptions.LocalServingHost, webhookInstallOptions.LocalServingPort)

	// Wait for the server to accept connections
	timeout := time.After(10 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

waitLoop:
	for {
		select {
		case <-timeout:
			panic("timed out waiting for webhook server to be ready")
		case <-ticker.C:
			conn, err := tls.DialWithDialer(dialer, "tcp", addrPort, &tls.Config{InsecureSkipVerify: true})
			if err == nil {
				conn.Close()
				break waitLoop
			}
		}
	}

	// Create a shared namespace for tests
	testNS = fmt.Sprintf("kausality-test-%d", time.Now().UnixNano())
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: testNS},
	}
	if err := k8sClient.Create(ctx, ns); err != nil {
		panic(fmt.Sprintf("failed to create test namespace: %v", err))
	}

	code := m.Run()

	// Cleanup
	cancel()
	_ = k8sClient.Delete(context.Background(), ns)
	_ = testEnv.Stop()

	os.Exit(code)
}

// certDir returns the webhook cert directory for tests that need it.
func certDir() string {
	return filepath.Join(testEnv.WebhookInstallOptions.LocalServingCertDir)
}
