//go:build e2e

package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/util/retry"

	ktesting "github.com/kausality-io/kausality/pkg/testing"
)

// =============================================================================
// Trace Origin vs Propagation Tests
// =============================================================================

// TestTraceExtendOnControllerReconcile verifies that when the Deployment controller
// creates a ReplicaSet in response to a Deployment update, the ReplicaSet gets a
// multi-hop trace extending from the Deployment.
func TestTraceExtendOnControllerReconcile(t *testing.T) {
	ctx := context.Background()
	name := fmt.Sprintf("trace-extend-%s", rand.String(4))

	t.Log("=== Testing Trace Extension on Controller Reconcile ===")
	t.Log("When a Deployment is updated, the controller creates a new ReplicaSet.")
	t.Log("The ReplicaSet should get a multi-hop trace extending from the Deployment.")

	// Step 1: Create a Deployment
	t.Log("")
	t.Logf("Step 1: Creating Deployment %q...", name)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
			Annotations: map[string]string{
				"kausality.io/trace-ticket": "EXTEND-001",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr(int32(1)),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": name},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": name},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "nginx",
						Image: "nginx:1.24-alpine",
					}},
				},
			},
		},
	}

	_, err := clientset.AppsV1().Deployments(testNamespace).Create(ctx, deployment, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = clientset.AppsV1().Deployments(testNamespace).Delete(ctx, name, metav1.DeleteOptions{})
	})

	// Step 2: Wait for Deployment to stabilize
	t.Log("")
	t.Log("Step 2: Waiting for Deployment to stabilize...")
	ktesting.Eventually(t, func() (bool, string) {
		dep, err := clientset.AppsV1().Deployments(testNamespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Sprintf("error getting deployment: %v", err)
		}
		if dep.Status.ObservedGeneration != dep.Generation || dep.Status.AvailableReplicas < 1 {
			return false, fmt.Sprintf("not stable: gen=%d, obsGen=%d, available=%d",
				dep.Generation, dep.Status.ObservedGeneration, dep.Status.AvailableReplicas)
		}
		return true, "deployment stabilized"
	}, defaultTimeout, defaultInterval, "deployment should stabilize")

	// Step 3: Check that the ReplicaSet has a multi-hop trace
	t.Log("")
	t.Log("Step 3: Checking ReplicaSet for multi-hop trace (extended from Deployment)...")

	ktesting.Eventually(t, func() (bool, string) {
		rsList, err := clientset.AppsV1().ReplicaSets(testNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("app=%s", name),
		})
		if err != nil {
			return false, fmt.Sprintf("error listing replicasets: %v", err)
		}
		if len(rsList.Items) == 0 {
			return false, "no replicaset found"
		}

		rs := rsList.Items[0]
		traceStr := rs.Annotations["kausality.io/trace"]
		if traceStr == "" {
			return false, fmt.Sprintf("no trace annotation on replicaset %s", rs.Name)
		}

		var hops []map[string]interface{}
		if err := json.Unmarshal([]byte(traceStr), &hops); err != nil {
			return false, fmt.Sprintf("failed to parse trace: %v", err)
		}

		if len(hops) < 2 {
			return false, fmt.Sprintf("expected multi-hop trace (>=2 hops), got %d hops: %s", len(hops), traceStr)
		}

		// First hop should be the Deployment (origin)
		firstKind, _ := hops[0]["kind"].(string)
		if firstKind != "Deployment" {
			return false, fmt.Sprintf("first hop kind=%s, expected Deployment", firstKind)
		}

		// Last hop should be the ReplicaSet
		lastKind, _ := hops[len(hops)-1]["kind"].(string)
		if lastKind != "ReplicaSet" {
			return false, fmt.Sprintf("last hop kind=%s, expected ReplicaSet", lastKind)
		}

		return true, fmt.Sprintf("replicaset %s has %d-hop trace: Deployment -> ReplicaSet", rs.Name, len(hops))
	}, annotationTimeout, defaultInterval, "ReplicaSet should have multi-hop trace")

	t.Log("")
	t.Log("SUCCESS: Controller-created ReplicaSet has extended (multi-hop) trace from Deployment")
}

// TestTraceOriginOnDirectUserEdit verifies that when a user directly edits a
// ReplicaSet's spec (bypassing the Deployment), the ReplicaSet gets a fresh
// 1-hop origin trace instead of extending from the Deployment.
func TestTraceOriginOnDirectUserEdit(t *testing.T) {
	ctx := context.Background()
	name := fmt.Sprintf("trace-origin-%s", rand.String(4))

	t.Log("=== Testing Fresh Origin on Direct User Edit ===")
	t.Log("When a user directly edits a ReplicaSet's spec (not via Deployment),")
	t.Log("the trace should be a fresh 1-hop origin, not extended from the Deployment.")

	// Step 1: Create a Deployment and wait for stabilization
	t.Log("")
	t.Logf("Step 1: Creating Deployment %q and waiting for stabilization...", name)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr(int32(1)),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": name},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": name},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "nginx",
						Image: "nginx:1.24-alpine",
					}},
				},
			},
		},
	}

	_, err := clientset.AppsV1().Deployments(testNamespace).Create(ctx, deployment, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = clientset.AppsV1().Deployments(testNamespace).Delete(ctx, name, metav1.DeleteOptions{})
	})

	ktesting.Eventually(t, func() (bool, string) {
		dep, err := clientset.AppsV1().Deployments(testNamespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Sprintf("error: %v", err)
		}
		if dep.Status.ObservedGeneration != dep.Generation || dep.Status.AvailableReplicas < 1 {
			return false, fmt.Sprintf("not stable: gen=%d, obsGen=%d, available=%d",
				dep.Generation, dep.Status.ObservedGeneration, dep.Status.AvailableReplicas)
		}
		return true, "deployment stabilized"
	}, defaultTimeout, defaultInterval, "deployment should stabilize")
	t.Log("Deployment is stable")

	// Step 2: Verify the ReplicaSet has a multi-hop trace (baseline)
	t.Log("")
	t.Log("Step 2: Verifying ReplicaSet has initial multi-hop trace (baseline)...")

	var rsName string
	ktesting.Eventually(t, func() (bool, string) {
		rsList, err := clientset.AppsV1().ReplicaSets(testNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("app=%s", name),
		})
		if err != nil {
			return false, fmt.Sprintf("error: %v", err)
		}
		if len(rsList.Items) == 0 {
			return false, "no replicaset found"
		}

		rs := rsList.Items[0]
		rsName = rs.Name
		traceStr := rs.Annotations["kausality.io/trace"]
		if traceStr == "" {
			return false, "no trace annotation yet"
		}

		var hops []map[string]interface{}
		if err := json.Unmarshal([]byte(traceStr), &hops); err != nil {
			return false, fmt.Sprintf("failed to parse trace: %v", err)
		}
		if len(hops) < 2 {
			return false, fmt.Sprintf("expected >=2 hops (baseline), got %d", len(hops))
		}

		return true, fmt.Sprintf("baseline: %d-hop trace on %s", len(hops), rs.Name)
	}, annotationTimeout, defaultInterval, "ReplicaSet should have initial multi-hop trace")
	t.Logf("Baseline: ReplicaSet %s has multi-hop trace", rsName)

	// Step 3: Directly edit the ReplicaSet's spec (user edit, not via Deployment)
	t.Log("")
	t.Logf("Step 3: Directly editing ReplicaSet %s spec (user edit)...", rsName)
	t.Log("This bypasses the Deployment controller - user is a different actor.")

	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		rs, err := clientset.AppsV1().ReplicaSets(testNamespace).Get(ctx, rsName, metav1.GetOptions{})
		if err != nil {
			return err
		}

		// Change the template annotation to trigger a spec change
		if rs.Spec.Template.Annotations == nil {
			rs.Spec.Template.Annotations = make(map[string]string)
		}
		rs.Spec.Template.Annotations["user-edit"] = "true"

		_, err = clientset.AppsV1().ReplicaSets(testNamespace).Update(ctx, rs, metav1.UpdateOptions{})
		return err
	})
	require.NoError(t, err, "direct user edit of ReplicaSet should succeed")
	t.Log("Direct user edit succeeded")

	// Step 4: Verify the trace is now a fresh origin (1-hop)
	t.Log("")
	t.Log("Step 4: Checking that ReplicaSet now has a fresh 1-hop origin trace...")

	ktesting.Eventually(t, func() (bool, string) {
		rs, err := clientset.AppsV1().ReplicaSets(testNamespace).Get(ctx, rsName, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Sprintf("error: %v", err)
		}

		traceStr := rs.Annotations["kausality.io/trace"]
		if traceStr == "" {
			return false, "no trace annotation"
		}

		var hops []map[string]interface{}
		if err := json.Unmarshal([]byte(traceStr), &hops); err != nil {
			return false, fmt.Sprintf("failed to parse trace: %v", err)
		}

		if len(hops) != 1 {
			return false, fmt.Sprintf("expected 1-hop origin trace, got %d hops: %s", len(hops), traceStr)
		}

		// The single hop should be for the ReplicaSet (this object)
		hopKind, _ := hops[0]["kind"].(string)
		if hopKind != "ReplicaSet" {
			return false, fmt.Sprintf("origin hop kind=%s, expected ReplicaSet", hopKind)
		}

		return true, "ReplicaSet has 1-hop origin trace (fresh origin)"
	}, annotationTimeout, defaultInterval, "ReplicaSet should have fresh 1-hop origin trace after user edit")

	t.Log("")
	t.Log("SUCCESS: Direct user edit creates fresh origin trace (not extended from Deployment)")
	t.Log("This confirms the IsControllerByHash cross-validation and isOrigin fixes work end-to-end:")
	t.Log("  - Controller-created ReplicaSet: multi-hop trace (extended)")
	t.Log("  - User-edited ReplicaSet: 1-hop trace (fresh origin)")
}

// TestTraceOriginOnUpdate verifies the combined flow:
// 1. Controller reconcile → extended trace
// 2. User edit → fresh origin
// 3. Controller reconcile again → extended trace again
func TestTraceOriginOnUpdate(t *testing.T) {
	ctx := context.Background()
	name := fmt.Sprintf("trace-cycle-%s", rand.String(4))

	t.Log("=== Testing Trace Origin/Extend Cycle ===")
	t.Log("Verifying the full cycle: controller extend -> user origin -> controller extend")

	// Step 1: Create Deployment and wait for stable state
	t.Log("")
	t.Logf("Step 1: Creating Deployment %q...", name)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr(int32(1)),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": name},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": name},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "nginx",
						Image: "nginx:1.24-alpine",
					}},
				},
			},
		},
	}

	_, err := clientset.AppsV1().Deployments(testNamespace).Create(ctx, deployment, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = clientset.AppsV1().Deployments(testNamespace).Delete(ctx, name, metav1.DeleteOptions{})
	})

	// Wait for stabilization
	ktesting.Eventually(t, func() (bool, string) {
		dep, err := clientset.AppsV1().Deployments(testNamespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Sprintf("error: %v", err)
		}
		if dep.Status.ObservedGeneration != dep.Generation || dep.Status.AvailableReplicas < 1 {
			return false, fmt.Sprintf("not stable: gen=%d, obsGen=%d", dep.Generation, dep.Status.ObservedGeneration)
		}
		return true, "stable"
	}, defaultTimeout, defaultInterval, "deployment should stabilize")

	// Find the ReplicaSet and verify multi-hop trace
	var rsName string
	ktesting.Eventually(t, func() (bool, string) {
		rsList, err := clientset.AppsV1().ReplicaSets(testNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("app=%s", name),
		})
		if err != nil || len(rsList.Items) == 0 {
			return false, "no replicaset"
		}
		rs := rsList.Items[0]
		rsName = rs.Name
		traceStr := rs.Annotations["kausality.io/trace"]
		if traceStr == "" {
			return false, "no trace"
		}
		var hops []map[string]interface{}
		if err := json.Unmarshal([]byte(traceStr), &hops); err != nil || len(hops) < 2 {
			return false, fmt.Sprintf("expected >=2 hops, got trace: %s", traceStr)
		}
		return true, fmt.Sprintf("initial: %d-hop trace", len(hops))
	}, annotationTimeout, defaultInterval, "initial multi-hop trace")
	t.Logf("Phase 1 PASS: ReplicaSet %s has multi-hop trace (controller extend)", rsName)

	// Step 2: User directly edits the ReplicaSet → fresh origin
	t.Log("")
	t.Log("Step 2: User directly edits ReplicaSet (expect fresh origin)...")
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		rs, err := clientset.AppsV1().ReplicaSets(testNamespace).Get(ctx, rsName, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if rs.Spec.Template.Annotations == nil {
			rs.Spec.Template.Annotations = make(map[string]string)
		}
		rs.Spec.Template.Annotations["user-cycle-edit"] = "true"
		_, err = clientset.AppsV1().ReplicaSets(testNamespace).Update(ctx, rs, metav1.UpdateOptions{})
		return err
	})
	require.NoError(t, err)

	// Verify 1-hop origin
	ktesting.Eventually(t, func() (bool, string) {
		rs, err := clientset.AppsV1().ReplicaSets(testNamespace).Get(ctx, rsName, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Sprintf("error: %v", err)
		}
		traceStr := rs.Annotations["kausality.io/trace"]
		var hops []map[string]interface{}
		if err := json.Unmarshal([]byte(traceStr), &hops); err != nil {
			return false, fmt.Sprintf("parse error: %v", err)
		}
		if len(hops) != 1 {
			return false, fmt.Sprintf("expected 1-hop, got %d: %s", len(hops), traceStr)
		}
		return true, "1-hop origin trace"
	}, annotationTimeout, defaultInterval, "fresh origin after user edit")
	t.Log("Phase 2 PASS: ReplicaSet has 1-hop origin trace (user edit)")

	// Step 3: Update Deployment to trigger controller reconcile → extended trace again
	t.Log("")
	t.Log("Step 3: Updating Deployment to trigger controller reconcile...")
	dep, err := clientset.AppsV1().Deployments(testNamespace).Get(ctx, name, metav1.GetOptions{})
	require.NoError(t, err)

	dep.Spec.Template.Spec.Containers[0].Image = "nginx:1.25-alpine"
	_, err = clientset.AppsV1().Deployments(testNamespace).Update(ctx, dep, metav1.UpdateOptions{})
	require.NoError(t, err)

	// Wait for rollout to complete
	ktesting.Eventually(t, func() (bool, string) {
		dep, err := clientset.AppsV1().Deployments(testNamespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Sprintf("error: %v", err)
		}
		if dep.Status.ObservedGeneration != dep.Generation || dep.Status.AvailableReplicas < 1 || dep.Status.UpdatedReplicas != *dep.Spec.Replicas {
			return false, fmt.Sprintf("rollout in progress: gen=%d, obsGen=%d, updated=%d",
				dep.Generation, dep.Status.ObservedGeneration, dep.Status.UpdatedReplicas)
		}
		return true, "rollout complete"
	}, defaultTimeout, defaultInterval, "deployment rollout should complete")

	// Find the new ReplicaSet (with the updated image) and verify multi-hop trace
	ktesting.Eventually(t, func() (bool, string) {
		rsList, err := clientset.AppsV1().ReplicaSets(testNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("app=%s", name),
		})
		if err != nil {
			return false, fmt.Sprintf("error: %v", err)
		}

		// Find the active ReplicaSet (the one with replicas > 0)
		for _, rs := range rsList.Items {
			if rs.Status.Replicas == 0 {
				continue
			}
			traceStr := rs.Annotations["kausality.io/trace"]
			if traceStr == "" {
				return false, fmt.Sprintf("no trace on active replicaset %s", rs.Name)
			}
			var hops []map[string]interface{}
			if err := json.Unmarshal([]byte(traceStr), &hops); err != nil {
				return false, fmt.Sprintf("parse error: %v", err)
			}
			if len(hops) < 2 {
				return false, fmt.Sprintf("expected >=2 hops on %s, got %d: %s", rs.Name, len(hops), traceStr)
			}

			// Verify first hop is Deployment
			firstKind, _ := hops[0]["kind"].(string)
			assert.Equal(t, "Deployment", firstKind, "first hop should be Deployment")

			return true, fmt.Sprintf("active RS %s has %d-hop trace", rs.Name, len(hops))
		}
		return false, "no active replicaset with multi-hop trace"
	}, annotationTimeout, defaultInterval, "new ReplicaSet should have multi-hop trace after Deployment update")

	t.Log("Phase 3 PASS: New ReplicaSet has multi-hop trace (controller extend)")

	t.Log("")
	t.Log("SUCCESS: Full origin/extend cycle works correctly:")
	t.Log("  1. Controller create  -> multi-hop trace (extend)")
	t.Log("  2. User direct edit   -> 1-hop trace (fresh origin)")
	t.Log("  3. Controller update  -> multi-hop trace (extend)")
}
