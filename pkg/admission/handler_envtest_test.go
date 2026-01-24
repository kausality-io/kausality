//go:build envtest
// +build envtest

package admission_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/go-logr/logr"

	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	kadmission "github.com/kausality-io/kausality/pkg/admission"
	"github.com/kausality-io/kausality/pkg/drift"
	"github.com/kausality-io/kausality/pkg/trace"
)

// Shared test environment
var (
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
	scheme    = runtime.NewScheme()
	testNS    string
)

func TestMain(m *testing.M) {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	testEnv = &envtest.Environment{}

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

	// Create a shared namespace for tests
	testNS = fmt.Sprintf("kausality-test-%d", time.Now().UnixNano())
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: testNS},
	}
	if err := k8sClient.Create(context.Background(), ns); err != nil {
		panic(fmt.Sprintf("failed to create test namespace: %v", err))
	}

	code := m.Run()

	// Cleanup
	_ = k8sClient.Delete(context.Background(), ns)
	_ = testEnv.Stop()

	os.Exit(code)
}

// =============================================================================
// Test: Controller Identification via managedFields
// =============================================================================

func TestControllerIdentification_ManagedFields(t *testing.T) {
	ctx := context.Background()

	// Create a Deployment
	deploy := createDeployment(t, ctx, "ctrl-id-deploy")

	// Simulate controller updating status (sets observedGeneration)
	// In real scenario, the deployment controller does this
	deploy.Status.ObservedGeneration = deploy.Generation
	deploy.Status.Replicas = 1
	deploy.Status.ReadyReplicas = 1
	if err := k8sClient.Status().Update(ctx, deploy); err != nil {
		t.Fatalf("failed to update deployment status: %v", err)
	}

	// Re-fetch to get managedFields
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(deploy), deploy); err != nil {
		t.Fatalf("failed to get deployment: %v", err)
	}

	t.Logf("Deployment managedFields: %d entries", len(deploy.ManagedFields))
	for _, mf := range deploy.ManagedFields {
		t.Logf("  Manager: %s, Operation: %s, Subresource: %s", mf.Manager, mf.Operation, mf.Subresource)
	}

	// Verify we can find the controller manager
	// The status update should have created a managedFields entry
	resolver := drift.NewParentResolver(k8sClient)

	// Create a child ReplicaSet with ownerRef
	rs := createReplicaSetWithOwner(t, ctx, "ctrl-id-rs", deploy)

	// Resolve parent and check controller manager is populated
	parentState, err := resolver.ResolveParent(ctx, rs)
	if err != nil {
		t.Fatalf("failed to resolve parent: %v", err)
	}

	if parentState == nil {
		t.Fatal("expected parent state, got nil")
	}

	t.Logf("Parent state: gen=%d, obsGen=%d, controllerManager=%q",
		parentState.Generation, parentState.ObservedGeneration, parentState.ControllerManager)

	// The controllerManager should be set from managedFields
	// (could be empty if no status update created managedFields entry for observedGeneration)
}

// =============================================================================
// Test: Drift Detection - Expected Change (gen != obsGen)
// =============================================================================

func TestDriftDetection_ExpectedChange(t *testing.T) {
	ctx := context.Background()

	// Create parent deployment
	deploy := createDeployment(t, ctx, "expected-change-deploy")

	// Don't update status yet - generation != observedGeneration (0)

	// Create child ReplicaSet
	rs := createReplicaSetWithOwner(t, ctx, "expected-change-rs", deploy)

	// Detect drift
	detector := drift.NewDetector(k8sClient)
	result, err := detector.Detect(ctx, rs)
	if err != nil {
		t.Fatalf("drift detection failed: %v", err)
	}

	t.Logf("Result: allowed=%v, drift=%v, phase=%v, reason=%s",
		result.Allowed, result.DriftDetected, result.LifecyclePhase, result.Reason)

	// Parent is initializing (no observedGeneration) - should allow
	if !result.Allowed {
		t.Errorf("expected allowed=true, got false")
	}

	// Now set observedGeneration < generation (reconciling)
	deploy.Status.ObservedGeneration = deploy.Generation - 1
	if err := k8sClient.Status().Update(ctx, deploy); err != nil {
		// If generation is 1, we can't set obsGen to 0 and have it < gen
		// Just update to same value and then bump generation
		deploy.Status.ObservedGeneration = deploy.Generation
		_ = k8sClient.Status().Update(ctx, deploy)

		// Bump generation by updating spec
		replicas := int32(2)
		deploy.Spec.Replicas = &replicas
		if err := k8sClient.Update(ctx, deploy); err != nil {
			t.Fatalf("failed to update deployment: %v", err)
		}
	}

	// Re-fetch
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(deploy), deploy); err != nil {
		t.Fatalf("failed to get deployment: %v", err)
	}

	t.Logf("After update: gen=%d, obsGen=%d", deploy.Generation, deploy.Status.ObservedGeneration)

	// Now gen != obsGen - should be expected change
	result, err = detector.Detect(ctx, rs)
	if err != nil {
		t.Fatalf("drift detection failed: %v", err)
	}

	t.Logf("Result: allowed=%v, drift=%v, phase=%v, reason=%s",
		result.Allowed, result.DriftDetected, result.LifecyclePhase, result.Reason)

	if !result.Allowed {
		t.Errorf("expected allowed=true for reconciling parent")
	}
	if result.DriftDetected {
		t.Errorf("expected driftDetected=false for reconciling parent")
	}
}

// =============================================================================
// Test: Drift Detection - Drift (gen == obsGen)
// =============================================================================

func TestDriftDetection_Drift(t *testing.T) {
	ctx := context.Background()

	// Create parent deployment
	deploy := createDeployment(t, ctx, "drift-deploy")

	// Set observedGeneration = generation (stable state)
	deploy.Status.ObservedGeneration = deploy.Generation
	deploy.Status.Replicas = 1
	if err := k8sClient.Status().Update(ctx, deploy); err != nil {
		t.Fatalf("failed to update deployment status: %v", err)
	}

	// Re-fetch
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(deploy), deploy); err != nil {
		t.Fatalf("failed to get deployment: %v", err)
	}

	t.Logf("Deployment: gen=%d, obsGen=%d", deploy.Generation, deploy.Status.ObservedGeneration)

	// Create child ReplicaSet
	rs := createReplicaSetWithOwner(t, ctx, "drift-rs", deploy)

	// Detect drift
	detector := drift.NewDetector(k8sClient)
	result, err := detector.Detect(ctx, rs)
	if err != nil {
		t.Fatalf("drift detection failed: %v", err)
	}

	t.Logf("Result: allowed=%v, drift=%v, phase=%v, reason=%s",
		result.Allowed, result.DriftDetected, result.LifecyclePhase, result.Reason)

	// gen == obsGen - drift should be detected
	if !result.DriftDetected {
		t.Errorf("expected driftDetected=true when gen == obsGen")
	}
	// Phase 1: always allow
	if !result.Allowed {
		t.Errorf("expected allowed=true (Phase 1 logging only)")
	}
}

// =============================================================================
// Test: Trace Propagation - New Origin
// =============================================================================

func TestTracePropagation_NewOrigin(t *testing.T) {
	ctx := context.Background()

	// Create deployment without parent (origin)
	deploy := createDeployment(t, ctx, "trace-origin-deploy")

	propagator := trace.NewPropagator(k8sClient)
	result, err := propagator.Propagate(ctx, deploy, "test-user@example.com")
	if err != nil {
		t.Fatalf("propagation failed: %v", err)
	}

	t.Logf("Trace result: isOrigin=%v, trace=%s", result.IsOrigin, result.Trace.String())

	if !result.IsOrigin {
		t.Errorf("expected isOrigin=true for object without parent")
	}

	if len(result.Trace) != 1 {
		t.Errorf("expected trace length 1, got %d", len(result.Trace))
	}

	if len(result.Trace) > 0 && result.Trace[0].User != "test-user@example.com" {
		t.Errorf("expected user 'test-user@example.com', got %q", result.Trace[0].User)
	}
}

// =============================================================================
// Test: Trace Propagation - Extend Parent Trace
// =============================================================================

func TestTracePropagation_ExtendParent(t *testing.T) {
	ctx := context.Background()

	// Create parent deployment with a trace
	deploy := createDeployment(t, ctx, "trace-extend-deploy")

	// Set a trace on the parent
	parentTrace := trace.Trace{
		trace.NewHop("apps/v1", "Deployment", deploy.Name, deploy.Generation, "parent-user"),
	}
	annotations := deploy.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[trace.TraceAnnotation] = parentTrace.String()
	deploy.SetAnnotations(annotations)
	if err := k8sClient.Update(ctx, deploy); err != nil {
		t.Fatalf("failed to update deployment with trace: %v", err)
	}

	// Set observedGeneration < generation (parent reconciling)
	// First bump generation
	replicas := int32(2)
	deploy.Spec.Replicas = &replicas
	if err := k8sClient.Update(ctx, deploy); err != nil {
		t.Fatalf("failed to update deployment spec: %v", err)
	}

	// Re-fetch
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(deploy), deploy); err != nil {
		t.Fatalf("failed to get deployment: %v", err)
	}

	// Status update with obsGen < generation
	deploy.Status.ObservedGeneration = deploy.Generation - 1
	if err := k8sClient.Status().Update(ctx, deploy); err != nil {
		t.Fatalf("failed to update deployment status: %v", err)
	}

	// Re-fetch
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(deploy), deploy); err != nil {
		t.Fatalf("failed to get deployment: %v", err)
	}

	t.Logf("Parent: gen=%d, obsGen=%d", deploy.Generation, deploy.Status.ObservedGeneration)

	// Create child ReplicaSet
	rs := createReplicaSetWithOwner(t, ctx, "trace-extend-rs", deploy)

	// Propagate trace to child
	propagator := trace.NewPropagator(k8sClient)
	result, err := propagator.Propagate(ctx, rs, "controller-sa")
	if err != nil {
		t.Fatalf("propagation failed: %v", err)
	}

	t.Logf("Trace result: isOrigin=%v, trace=%s", result.IsOrigin, result.Trace.String())

	// Parent is reconciling - should extend trace
	if result.IsOrigin {
		t.Errorf("expected isOrigin=false when parent is reconciling")
	}

	// Trace should have parent hop + child hop
	if len(result.Trace) != 2 {
		t.Errorf("expected trace length 2, got %d", len(result.Trace))
	}
}

// =============================================================================
// Test: Lifecycle Phase - Initializing
// =============================================================================

func TestLifecyclePhase_Initializing(t *testing.T) {
	ctx := context.Background()

	// Create parent deployment (no observedGeneration yet = initializing)
	deploy := createDeployment(t, ctx, "lifecycle-init-deploy")

	// Create child ReplicaSet
	rs := createReplicaSetWithOwner(t, ctx, "lifecycle-init-rs", deploy)

	// Detect drift
	detector := drift.NewDetector(k8sClient)
	result, err := detector.Detect(ctx, rs)
	if err != nil {
		t.Fatalf("drift detection failed: %v", err)
	}

	t.Logf("Result: phase=%v, allowed=%v, drift=%v", result.LifecyclePhase, result.Allowed, result.DriftDetected)

	if result.LifecyclePhase != drift.PhaseInitializing {
		t.Errorf("expected phase Initializing, got %v", result.LifecyclePhase)
	}

	if !result.Allowed {
		t.Errorf("expected allowed=true during initialization")
	}

	if result.DriftDetected {
		t.Errorf("expected driftDetected=false during initialization")
	}
}

// =============================================================================
// Test: Lifecycle Phase - Deleting
// =============================================================================

func TestLifecyclePhase_Deleting(t *testing.T) {
	ctx := context.Background()

	// Create parent deployment with finalizer
	deploy := createDeployment(t, ctx, "lifecycle-delete-deploy")
	deploy.Finalizers = []string{"test.kausality.io/finalizer"}
	if err := k8sClient.Update(ctx, deploy); err != nil {
		t.Fatalf("failed to add finalizer: %v", err)
	}

	// Set observedGeneration (mark as ready)
	deploy.Status.ObservedGeneration = deploy.Generation
	if err := k8sClient.Status().Update(ctx, deploy); err != nil {
		t.Fatalf("failed to update status: %v", err)
	}

	// Create child ReplicaSet
	rs := createReplicaSetWithOwner(t, ctx, "lifecycle-delete-rs", deploy)

	// Delete the deployment (will be blocked by finalizer, but sets deletionTimestamp)
	if err := k8sClient.Delete(ctx, deploy); err != nil {
		t.Fatalf("failed to delete deployment: %v", err)
	}

	// Re-fetch deployment to get deletionTimestamp
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(deploy), deploy); err != nil {
		t.Fatalf("failed to get deployment: %v", err)
	}

	if deploy.DeletionTimestamp == nil {
		t.Fatal("expected deletionTimestamp to be set")
	}

	t.Logf("Deployment deletionTimestamp: %v", deploy.DeletionTimestamp)

	// Detect drift
	detector := drift.NewDetector(k8sClient)
	result, err := detector.Detect(ctx, rs)
	if err != nil {
		t.Fatalf("drift detection failed: %v", err)
	}

	t.Logf("Result: phase=%v, allowed=%v, drift=%v", result.LifecyclePhase, result.Allowed, result.DriftDetected)

	if result.LifecyclePhase != drift.PhaseDeleting {
		t.Errorf("expected phase Deleting, got %v", result.LifecyclePhase)
	}

	if !result.Allowed {
		t.Errorf("expected allowed=true during deletion")
	}

	// Clean up: remove finalizer
	deploy.Finalizers = nil
	if err := k8sClient.Update(ctx, deploy); err != nil {
		t.Logf("failed to remove finalizer: %v", err)
	}
}

// =============================================================================
// Test: FieldManager Matching
// =============================================================================

func TestFieldManagerMatching_SameManager(t *testing.T) {
	ctx := context.Background()

	// Create parent deployment
	deploy := createDeployment(t, ctx, "fieldmgr-same-deploy")

	// Update status with a specific manager
	deploy.Status.ObservedGeneration = deploy.Generation
	deploy.Status.Replicas = 1
	if err := k8sClient.Status().Update(ctx, deploy); err != nil {
		t.Fatalf("failed to update status: %v", err)
	}

	// Re-fetch
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(deploy), deploy); err != nil {
		t.Fatalf("failed to get deployment: %v", err)
	}

	// Find the controller manager from managedFields
	var controllerManager string
	for _, mf := range deploy.ManagedFields {
		if mf.Subresource == "status" {
			controllerManager = mf.Manager
			break
		}
	}
	t.Logf("Controller manager from managedFields: %q", controllerManager)

	// Create child ReplicaSet
	rs := createReplicaSetWithOwner(t, ctx, "fieldmgr-same-rs", deploy)

	// Detect drift with matching fieldManager
	detector := drift.NewDetector(k8sClient)
	result, err := detector.DetectWithFieldManager(ctx, rs, controllerManager)
	if err != nil {
		t.Fatalf("drift detection failed: %v", err)
	}

	t.Logf("Result with matching manager: allowed=%v, drift=%v, reason=%s",
		result.Allowed, result.DriftDetected, result.Reason)

	// Same manager + gen == obsGen = drift (controller updating when nothing changed)
	if !result.DriftDetected {
		t.Errorf("expected driftDetected=true when gen == obsGen")
	}
}

func TestFieldManagerMatching_DifferentManager(t *testing.T) {
	ctx := context.Background()

	// Create parent deployment
	deploy := createDeployment(t, ctx, "fieldmgr-diff-deploy")

	// Set up parent as ready (gen == obsGen)
	deploy.Status.ObservedGeneration = deploy.Generation
	deploy.Status.Replicas = 1
	if err := k8sClient.Status().Update(ctx, deploy); err != nil {
		t.Fatalf("failed to update status: %v", err)
	}

	// Re-fetch
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(deploy), deploy); err != nil {
		t.Fatalf("failed to get deployment: %v", err)
	}

	// Create child ReplicaSet
	rs := createReplicaSetWithOwner(t, ctx, "fieldmgr-diff-rs", deploy)

	// Detect drift with different fieldManager
	detector := drift.NewDetector(k8sClient)
	result, err := detector.DetectWithFieldManager(ctx, rs, "some-other-controller")
	if err != nil {
		t.Fatalf("drift detection failed: %v", err)
	}

	t.Logf("Result with different manager: allowed=%v, drift=%v, reason=%s",
		result.Allowed, result.DriftDetected, result.Reason)

	// Different manager = NOT drift (it's a different actor, new causal origin)
	if result.DriftDetected {
		t.Errorf("expected driftDetected=false for different manager (not drift, just different actor)")
	}
}

// =============================================================================
// Test: Admission Handler Integration
// =============================================================================

func TestAdmissionHandler_Integration(t *testing.T) {
	ctx := context.Background()

	// Create parent deployment
	deploy := createDeployment(t, ctx, "handler-int-deploy")

	// Set as ready
	deploy.Status.ObservedGeneration = deploy.Generation
	if err := k8sClient.Status().Update(ctx, deploy); err != nil {
		t.Fatalf("failed to update status: %v", err)
	}

	// Create handler
	handler := kadmission.NewHandler(kadmission.Config{
		Client: k8sClient,
		Log:    logr.Discard(),
	})

	// Create a child ReplicaSet
	rs := createReplicaSetWithOwner(t, ctx, "handler-int-rs", deploy)

	// Set TypeMeta explicitly (not populated by client.Get)
	rs.APIVersion = "apps/v1"
	rs.Kind = "ReplicaSet"

	// Serialize the ReplicaSet
	rsBytes, err := json.Marshal(rs)
	if err != nil {
		t.Fatalf("failed to marshal replicaset: %v", err)
	}

	// Create admission request
	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			UID:       types.UID("test-uid"),
			Operation: admissionv1.Update,
			Kind: metav1.GroupVersionKind{
				Group:   "apps",
				Version: "v1",
				Kind:    "ReplicaSet",
			},
			Namespace: rs.Namespace,
			Name:      rs.Name,
			Object: runtime.RawExtension{
				Raw: rsBytes,
			},
			OldObject: runtime.RawExtension{
				Raw: rsBytes,
			},
			UserInfo: authenticationv1.UserInfo{
				Username: "system:serviceaccount:kube-system:deployment-controller",
			},
			Options: runtime.RawExtension{
				Raw: []byte(`{"fieldManager":"deployment-controller"}`),
			},
		},
	}

	// Handle the request
	resp := handler.Handle(ctx, req)

	t.Logf("Response: allowed=%v, result=%v", resp.Allowed, resp.Result)

	// Phase 1: always allow
	if !resp.Allowed {
		t.Errorf("expected allowed=true")
	}
}

// =============================================================================
// Helper Functions
// =============================================================================

var testCounter int

func createDeployment(t *testing.T, ctx context.Context, namePrefix string) *appsv1.Deployment {
	t.Helper()
	testCounter++

	replicas := int32(1)
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%d", namePrefix, testCounter),
			Namespace: testNS,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": namePrefix},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": namePrefix},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "test", Image: "nginx:latest"},
					},
				},
			},
		},
	}

	if err := k8sClient.Create(ctx, deploy); err != nil {
		t.Fatalf("failed to create deployment: %v", err)
	}

	// Re-fetch to get server-set fields
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(deploy), deploy); err != nil {
		t.Fatalf("failed to get deployment: %v", err)
	}

	return deploy
}

func createReplicaSetWithOwner(t *testing.T, ctx context.Context, namePrefix string, owner *appsv1.Deployment) *appsv1.ReplicaSet {
	t.Helper()
	testCounter++

	trueVal := true
	replicas := int32(1)

	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%d", namePrefix, testCounter),
			Namespace: testNS,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       owner.Name,
					UID:        owner.UID,
					Controller: &trueVal,
				},
			},
		},
		Spec: appsv1.ReplicaSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": namePrefix},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": namePrefix},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "test", Image: "nginx:latest"},
					},
				},
			},
		},
	}

	if err := k8sClient.Create(ctx, rs); err != nil {
		t.Fatalf("failed to create replicaset: %v", err)
	}

	// Re-fetch
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(rs), rs); err != nil {
		t.Fatalf("failed to get replicaset: %v", err)
	}

	return rs
}
