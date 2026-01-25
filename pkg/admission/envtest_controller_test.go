//go:build envtest
// +build envtest

package admission_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kausality-io/kausality/pkg/controller"
	"github.com/kausality-io/kausality/pkg/drift"
)

// =============================================================================
// Test: Controller Identification - Single Updater
// =============================================================================

func TestControllerIdentification_SingleUpdater(t *testing.T) {
	ctx := context.Background()

	// Create parent deployment
	deploy := createDeployment(t, ctx, "ctrl-single-deploy")

	// Set up parent as stable (gen == obsGen)
	deploy.Status.ObservedGeneration = deploy.Generation
	require.NoError(t, k8sClient.Status().Update(ctx, deploy))

	// Re-fetch to get latest state
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(deploy), deploy))

	// Create child ReplicaSet with a single updater hash
	rs := createReplicaSetWithOwner(t, ctx, "ctrl-single-rs", deploy)

	// Add a single updater annotation (simulating first CREATE)
	user1 := "system:serviceaccount:kube-system:deployment-controller"
	annotations := controller.RecordUpdater(rs, user1)
	rs.SetAnnotations(annotations)
	require.NoError(t, k8sClient.Update(ctx, rs))

	// Re-fetch
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(rs), rs))

	t.Logf("Child updaters annotation: %s", rs.Annotations[controller.UpdatersAnnotation])

	// Detect drift with same user - should be identified as controller
	detector := drift.NewDetector(k8sClient)
	childUpdaters := drift.ParseUpdaterHashes(rs)
	result, err := detector.DetectWithUsername(ctx, rs, user1, childUpdaters)
	require.NoError(t, err)

	t.Logf("Result with same user: drift=%v, reason=%s", result.DriftDetected, result.Reason)

	// Single updater = that's the controller
	// gen == obsGen + controller = drift
	assert.True(t, result.DriftDetected, "expected drift when single updater (controller) updates stable parent")

	// Now try with a different user - should NOT be controller
	user2 := "kubectl-user@example.com"
	result2, err := detector.DetectWithUsername(ctx, rs, user2, childUpdaters)
	require.NoError(t, err)

	t.Logf("Result with different user: drift=%v, reason=%s", result2.DriftDetected, result2.Reason)

	// Different user than the single updater = not controller = not drift
	assert.False(t, result2.DriftDetected, "expected no drift when different user updates (new causal origin)")
}

// =============================================================================
// Test: Controller Identification - Multiple Updaters with Intersection
// =============================================================================

func TestControllerIdentification_MultipleUpdatersIntersection(t *testing.T) {
	ctx := context.Background()

	// Create parent deployment
	deploy := createDeployment(t, ctx, "ctrl-multi-deploy")

	// Add controller hash to parent (simulating status update recording)
	controllerUser := "system:serviceaccount:kube-system:deployment-controller"
	controllerHash := controller.HashUsername(controllerUser)

	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(deploy), deploy); err != nil {
			return err
		}
		annotations := deploy.GetAnnotations()
		if annotations == nil {
			annotations = make(map[string]string)
		}
		annotations[controller.ControllersAnnotation] = controllerHash
		deploy.SetAnnotations(annotations)
		return k8sClient.Update(ctx, deploy)
	})
	require.NoError(t, err)

	// Re-fetch and set up parent as stable AFTER all annotation updates
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(deploy), deploy))
	deploy.Status.ObservedGeneration = deploy.Generation
	require.NoError(t, k8sClient.Status().Update(ctx, deploy))
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(deploy), deploy))
	t.Logf("Parent controllers annotation: %s", deploy.Annotations[controller.ControllersAnnotation])
	t.Logf("Parent generation: %d, observedGeneration: %d", deploy.Generation, deploy.Status.ObservedGeneration)

	// Create child with multiple updaters (controller + user)
	rs := createReplicaSetWithOwner(t, ctx, "ctrl-multi-rs", deploy)

	regularUser := "kubectl-user@example.com"
	userHash := controller.HashUsername(regularUser)

	// Set multiple updaters on child
	err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(rs), rs); err != nil {
			return err
		}
		annotations := rs.GetAnnotations()
		if annotations == nil {
			annotations = make(map[string]string)
		}
		annotations[controller.UpdatersAnnotation] = controllerHash + "," + userHash
		rs.SetAnnotations(annotations)
		return k8sClient.Update(ctx, rs)
	})
	require.NoError(t, err)

	// Re-fetch
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(rs), rs))
	t.Logf("Child updaters annotation: %s", rs.Annotations[controller.UpdatersAnnotation])

	// Detect drift with controller user - intersection should identify as controller
	detector := drift.NewDetector(k8sClient)
	childUpdaters := drift.ParseUpdaterHashes(rs)

	result, err := detector.DetectWithUsername(ctx, rs, controllerUser, childUpdaters)
	require.NoError(t, err)

	t.Logf("Result with controller user: drift=%v, reason=%s", result.DriftDetected, result.Reason)

	// Controller user in intersection = controller = drift
	assert.True(t, result.DriftDetected, "expected drift when controller updates stable parent")

	// Detect drift with regular user - not in intersection
	result2, err := detector.DetectWithUsername(ctx, rs, regularUser, childUpdaters)
	require.NoError(t, err)

	t.Logf("Result with regular user: drift=%v, reason=%s", result2.DriftDetected, result2.Reason)

	// Regular user not in parent controllers = not controller = not drift
	assert.False(t, result2.DriftDetected, "expected no drift when non-controller user updates")
}

// =============================================================================
// Test: Controller Identification - Can't Determine (No Parent Controllers)
// =============================================================================

func TestControllerIdentification_CantDetermine(t *testing.T) {
	ctx := context.Background()

	// Create parent deployment WITHOUT controllers annotation
	deploy := createDeployment(t, ctx, "ctrl-unknown-deploy")

	// Set up parent as stable
	deploy.Status.ObservedGeneration = deploy.Generation
	require.NoError(t, k8sClient.Status().Update(ctx, deploy))
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(deploy), deploy))

	// NO controllers annotation on parent

	// Create child with MULTIPLE updaters
	rs := createReplicaSetWithOwner(t, ctx, "ctrl-unknown-rs", deploy)

	user1Hash := controller.HashUsername("user1")
	user2Hash := controller.HashUsername("user2")

	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(rs), rs); err != nil {
			return err
		}
		annotations := rs.GetAnnotations()
		if annotations == nil {
			annotations = make(map[string]string)
		}
		annotations[controller.UpdatersAnnotation] = user1Hash + "," + user2Hash
		rs.SetAnnotations(annotations)
		return k8sClient.Update(ctx, rs)
	})
	require.NoError(t, err)

	require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(rs), rs))
	t.Logf("Child updaters: %s", rs.Annotations[controller.UpdatersAnnotation])
	t.Logf("Parent controllers: %s", deploy.Annotations[controller.ControllersAnnotation])

	// Multiple updaters + no parent controllers = can't determine
	detector := drift.NewDetector(k8sClient)
	childUpdaters := drift.ParseUpdaterHashes(rs)

	result, err := detector.DetectWithUsername(ctx, rs, "user1", childUpdaters)
	require.NoError(t, err)

	t.Logf("Result: drift=%v, reason=%s", result.DriftDetected, result.Reason)

	// Can't determine = be lenient = no drift detection
	assert.False(t, result.DriftDetected, "expected no drift when controller can't be determined")
	assert.Contains(t, result.Reason, "cannot determine", "reason should indicate can't determine")
}

// =============================================================================
// Test: Controller Identification - CREATE (First Updater)
// =============================================================================

func TestControllerIdentification_CreateFirstUpdater(t *testing.T) {
	ctx := context.Background()

	// Create parent deployment
	deploy := createDeployment(t, ctx, "ctrl-create-deploy")

	// Set up parent as stable
	deploy.Status.ObservedGeneration = deploy.Generation
	require.NoError(t, k8sClient.Status().Update(ctx, deploy))
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(deploy), deploy))

	// Create child WITHOUT any updaters annotation (simulating CREATE)
	rs := createReplicaSetWithOwner(t, ctx, "ctrl-create-rs", deploy)

	// Ensure no updaters annotation
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(rs), rs))
	t.Logf("Child updaters (should be empty): %s", rs.Annotations[controller.UpdatersAnnotation])

	// Detect with empty childUpdaters (CREATE scenario)
	detector := drift.NewDetector(k8sClient)
	var childUpdaters []string // Empty = CREATE

	result, err := detector.DetectWithUsername(ctx, rs, "creating-user", childUpdaters)
	require.NoError(t, err)

	t.Logf("Result for CREATE: drift=%v, reason=%s", result.DriftDetected, result.Reason)

	// CREATE = first updater = that's the controller = check drift
	// gen == obsGen + controller = drift
	assert.True(t, result.DriftDetected, "expected drift detection for CREATE when parent is stable")
}

// =============================================================================
// Test: Hash Recording Functions
// =============================================================================

func TestRecordUpdater_AddsHash(t *testing.T) {
	ctx := context.Background()

	// Create a deployment to use as test object
	deploy := createDeployment(t, ctx, "record-updater-deploy")

	// Record first updater
	user1 := "user1@example.com"
	annotations := controller.RecordUpdater(deploy, user1)

	hash1 := controller.HashUsername(user1)
	assert.Equal(t, hash1, annotations[controller.UpdatersAnnotation])

	// Apply and re-fetch
	deploy.SetAnnotations(annotations)
	require.NoError(t, k8sClient.Update(ctx, deploy))
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(deploy), deploy))

	// Record second updater
	user2 := "user2@example.com"
	annotations2 := controller.RecordUpdater(deploy, user2)

	hash2 := controller.HashUsername(user2)
	expected := hash1 + "," + hash2
	assert.Equal(t, expected, annotations2[controller.UpdatersAnnotation])

	t.Logf("After two updates: %s", annotations2[controller.UpdatersAnnotation])
}

func TestHashUsername_Deterministic(t *testing.T) {
	username := "system:serviceaccount:kube-system:deployment-controller"

	hash1 := controller.HashUsername(username)
	hash2 := controller.HashUsername(username)

	assert.Equal(t, hash1, hash2, "hash should be deterministic")
	assert.Len(t, hash1, 5, "hash should be 5 characters")

	t.Logf("Hash of %q = %s", username, hash1)
}

// =============================================================================
// Test: Async Controller Recording
// =============================================================================

func TestRecordControllerAsync_AddsHashAfterDelay(t *testing.T) {
	ctx := context.Background()

	// Create a deployment
	deploy := createDeployment(t, ctx, "async-ctrl-deploy")

	// Create tracker with test logger
	tracker := controller.NewTracker(k8sClient, ctrl.Log)
	t.Logf("Created tracker, deploy name=%s ns=%s", deploy.GetName(), deploy.GetNamespace())

	// Record controller async
	user := "system:serviceaccount:test:controller"
	tracker.RecordControllerAsync(ctx, deploy, user)

	// Hash should NOT be there immediately
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(deploy), deploy))
	assert.Empty(t, deploy.Annotations[controller.ControllersAnnotation], "hash should not be immediate")

	// Wait for async update (5s delay + buffer)
	t.Log("Waiting for async annotation update...")
	time.Sleep(7 * time.Second)

	// Now hash should be there
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(deploy), deploy))
	expectedHash := controller.HashUsername(user)

	t.Logf("Controllers annotation after delay: %s", deploy.Annotations[controller.ControllersAnnotation])

	assert.Equal(t, expectedHash, deploy.Annotations[controller.ControllersAnnotation],
		"controller hash should be recorded after delay")
}

// =============================================================================
// Test: Integration - Full Flow
// =============================================================================

func TestControllerIdentification_FullFlow(t *testing.T) {
	ctx := context.Background()

	controllerUser := "system:serviceaccount:kube-system:deployment-controller"
	regularUser := "kubectl-admin@example.com"

	// Step 1: Create parent deployment
	t.Log("Step 1: Creating parent deployment")
	deploy := createDeployment(t, ctx, "full-flow-deploy")

	// Step 2: Add controller annotation (simulating async recording)
	t.Log("Step 2: Adding controller annotation to parent")
	controllerHash := controller.HashUsername(controllerUser)
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(deploy), deploy); err != nil {
			return err
		}
		annotations := deploy.GetAnnotations()
		if annotations == nil {
			annotations = make(map[string]string)
		}
		annotations[controller.ControllersAnnotation] = controllerHash
		deploy.SetAnnotations(annotations)
		return k8sClient.Update(ctx, deploy)
	})
	require.NoError(t, err)

	// Step 2b: Now set parent as stable AFTER annotation update
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(deploy), deploy))
	deploy.Status.ObservedGeneration = deploy.Generation
	require.NoError(t, k8sClient.Status().Update(ctx, deploy))
	t.Logf("Parent gen=%d, obsGen=%d", deploy.Generation, deploy.Status.ObservedGeneration)

	// Step 3: Controller creates child (first updater)
	t.Log("Step 3: Controller creates child ReplicaSet")
	rs := createReplicaSetWithOwner(t, ctx, "full-flow-rs", deploy)

	// Add controller as first updater
	err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(rs), rs); err != nil {
			return err
		}
		annotations := controller.RecordUpdater(rs, controllerUser)
		rs.SetAnnotations(annotations)
		return k8sClient.Update(ctx, rs)
	})
	require.NoError(t, err)

	require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(deploy), deploy))
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(rs), rs))

	t.Logf("Parent controllers: %s", deploy.Annotations[controller.ControllersAnnotation])
	t.Logf("Child updaters: %s", rs.Annotations[controller.UpdatersAnnotation])

	// Step 4: Regular user modifies child
	t.Log("Step 4: Regular user modifies child spec")

	// Add user to child updaters
	err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(rs), rs); err != nil {
			return err
		}
		annotations := controller.RecordUpdater(rs, regularUser)
		rs.SetAnnotations(annotations)
		return k8sClient.Update(ctx, rs)
	})
	require.NoError(t, err)

	require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(rs), rs))
	t.Logf("Child updaters after user edit: %s", rs.Annotations[controller.UpdatersAnnotation])

	// Step 5: Detect drift - regular user should NOT trigger drift
	t.Log("Step 5: Checking drift for regular user")
	detector := drift.NewDetector(k8sClient)
	childUpdaters := drift.ParseUpdaterHashes(rs)

	result, err := detector.DetectWithUsername(ctx, rs, regularUser, childUpdaters)
	require.NoError(t, err)

	t.Logf("Regular user result: drift=%v, reason=%s", result.DriftDetected, result.Reason)
	assert.False(t, result.DriftDetected, "regular user should not trigger drift")

	// Step 6: Controller tries to correct - SHOULD trigger drift
	t.Log("Step 6: Checking drift for controller correction")
	result2, err := detector.DetectWithUsername(ctx, rs, controllerUser, childUpdaters)
	require.NoError(t, err)

	t.Logf("Controller result: drift=%v, reason=%s", result2.DriftDetected, result2.Reason)
	assert.True(t, result2.DriftDetected, "controller correcting stable parent should trigger drift")

	t.Log("SUCCESS: Full flow works correctly")
}
