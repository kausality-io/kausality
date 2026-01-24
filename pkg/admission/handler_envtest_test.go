//go:build envtest
// +build envtest

package admission_test

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/kausality-io/kausality/pkg/admission"
	"github.com/kausality-io/kausality/pkg/drift"
)

var (
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
	scheme    = runtime.NewScheme()
)

func TestMain(m *testing.M) {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	testEnv = &envtest.Environment{}

	var err error
	cfg, err = testEnv.Start()
	if err != nil {
		panic(err)
	}

	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		panic(err)
	}

	code := m.Run()

	_ = testEnv.Stop()

	if code != 0 {
		panic("tests failed")
	}
}

func TestDriftDetection_ExpectedChange(t *testing.T) {
	ctx := context.Background()

	// Create a namespace
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-expected-" + randString(5),
		},
	}
	if err := k8sClient.Create(ctx, ns); err != nil {
		t.Fatalf("failed to create namespace: %v", err)
	}
	defer k8sClient.Delete(ctx, ns)

	// Create a Deployment (parent)
	replicas := int32(1)
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "parent-deploy",
			Namespace: ns.Name,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "test"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "test"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "test", Image: "nginx"},
					},
				},
			},
		},
	}
	if err := k8sClient.Create(ctx, deploy); err != nil {
		t.Fatalf("failed to create deployment: %v", err)
	}

	// Wait for deployment to get a generation
	time.Sleep(100 * time.Millisecond)
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(deploy), deploy); err != nil {
		t.Fatalf("failed to get deployment: %v", err)
	}

	// Create a ReplicaSet (child) with ownerReference to deployment
	trueVal := true
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "child-rs",
			Namespace: ns.Name,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       deploy.Name,
					UID:        deploy.UID,
					Controller: &trueVal,
				},
			},
		},
		Spec: appsv1.ReplicaSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "test"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "test"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "test", Image: "nginx"},
					},
				},
			},
		},
	}
	if err := k8sClient.Create(ctx, rs); err != nil {
		t.Fatalf("failed to create replicaset: %v", err)
	}

	// Test drift detection
	detector := drift.NewDetector(k8sClient)
	result, err := detector.Detect(ctx, rs)
	if err != nil {
		t.Fatalf("drift detection failed: %v", err)
	}

	// Parent just created, generation != observedGeneration (or no obsGen yet)
	// So this should be allowed as expected change or initialization
	if !result.Allowed {
		t.Errorf("expected allowed=true, got false: %s", result.Reason)
	}

	t.Logf("Result: allowed=%v, drift=%v, phase=%v, reason=%s",
		result.Allowed, result.DriftDetected, result.LifecyclePhase, result.Reason)
}

func TestAdmissionHandler(t *testing.T) {
	handler := admission.NewHandler(admission.Config{
		Client: k8sClient,
		Log:    logr.Discard(),
	})

	if handler == nil {
		t.Fatal("handler should not be nil")
	}
}

func randString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[time.Now().UnixNano()%int64(len(letters))]
		time.Sleep(time.Nanosecond)
	}
	return string(b)
}
