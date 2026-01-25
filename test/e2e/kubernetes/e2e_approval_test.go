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

	"github.com/kausality-io/kausality/pkg/approval"
	ktesting "github.com/kausality-io/kausality/pkg/testing"
)

// =============================================================================
// Drift and Approval Tests
//
// Drift = controller modifying a child's spec when the parent is stable (gen == obsGen).
// We trigger drift by directly editing the ReplicaSet's spec.replicas, causing
// the Deployment controller to try to "fix" it back to the desired count.
//
// Note: Kausality only intercepts spec changes - metadata/status changes are ignored.
// =============================================================================

// TestDriftBlockedInEnforceMode verifies that drift is blocked in enforce mode
// when there is no approval annotation.
func TestDriftBlockedInEnforceMode(t *testing.T) {
	if clientset == nil {
		t.Fatal("clientset is nil - TestMain did not initialize properly")
	}
	ctx := context.Background()

	t.Log("=== Testing Drift Blocked in Enforce Mode ===")
	t.Log("When we directly modify a ReplicaSet's spec and the Deployment controller tries to fix it,")
	t.Log("that fix attempt is drift (controller updating when parent is stable).")
	t.Log("In enforce mode without approval, drift should be blocked.")

	// Step 1: Create a namespace with enforce mode
	enforceNS := fmt.Sprintf("drift-block-%s", rand.String(4))
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: enforceNS,
			Annotations: map[string]string{
				"kausality.io/mode": "enforce",
			},
		},
	}
	_, err := clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		t.Logf("Cleanup: Deleting namespace %s", enforceNS)
		_ = clientset.CoreV1().Namespaces().Delete(ctx, enforceNS, metav1.DeleteOptions{})
	})
	t.Logf("Created namespace %s with enforce mode", enforceNS)

	// Step 2: Create a Deployment with 1 replica
	name := fmt.Sprintf("drift-block-%s", rand.String(4))
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: enforceNS,
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

	_, err = clientset.AppsV1().Deployments(enforceNS).Create(ctx, deployment, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Logf("Created Deployment %s with 1 replica", name)

	// Step 3: Wait for stabilization (gen == obsGen)
	ktesting.Eventually(t, func() (bool, string) {
		dep, err := clientset.AppsV1().Deployments(enforceNS).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Sprintf("error getting deployment: %v", err)
		}
		if dep.Status.ObservedGeneration != dep.Generation {
			return false, fmt.Sprintf("not stable: gen=%d, obsGen=%d", dep.Generation, dep.Status.ObservedGeneration)
		}
		if dep.Status.AvailableReplicas < 1 {
			return false, fmt.Sprintf("not available: replicas=%d", dep.Status.AvailableReplicas)
		}
		return true, "deployment stabilized"
	}, defaultTimeout, defaultInterval, "deployment should stabilize")
	t.Log("Deployment stabilized (gen == obsGen)")

	// Step 4: Get the ReplicaSet
	var rs *appsv1.ReplicaSet
	ktesting.Eventually(t, func() (bool, string) {
		rsList, err := clientset.AppsV1().ReplicaSets(enforceNS).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("app=%s", name),
		})
		if err != nil {
			return false, fmt.Sprintf("error listing replicasets: %v", err)
		}
		if len(rsList.Items) == 0 {
			return false, "no replicaset found"
		}
		rs = &rsList.Items[0]
		return true, fmt.Sprintf("found replicaset %s with %d replicas", rs.Name, *rs.Spec.Replicas)
	}, defaultTimeout, defaultInterval, "replicaset should exist")

	// Step 5: Directly modify the ReplicaSet's spec.replicas (simulate external drift)
	// Change from 1 to 2 - the Deployment controller will want to set it back to 1
	// Use a specific fieldManager so kausality knows this isn't the controller
	rs.Spec.Replicas = ptr(int32(2))
	_, err = clientset.AppsV1().ReplicaSets(enforceNS).Update(ctx, rs, metav1.UpdateOptions{
		FieldManager: "e2e-test",
	})
	require.NoError(t, err)
	t.Log("Directly modified ReplicaSet spec.replicas from 1 to 2")

	// Step 6: Wait for controller to attempt reconciliation
	// The controller will try to set replicas back to 1, but that's drift and should be blocked
	t.Log("Waiting for controller to attempt reconciliation (which should be blocked)...")

	// Step 7: Verify our modification persists (controller couldn't fix it)
	// The ReplicaSet should still have 2 replicas because the controller's fix was blocked
	ktesting.Eventually(t, func() (bool, string) {
		rs, err := clientset.AppsV1().ReplicaSets(enforceNS).Get(ctx, rs.Name, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Sprintf("error getting replicaset: %v", err)
		}
		if *rs.Spec.Replicas != 2 {
			return false, fmt.Sprintf("replicas changed to %d (drift was allowed!)", *rs.Spec.Replicas)
		}
		return true, "replicas still 2 (drift blocked)"
	}, defaultTimeout, defaultInterval, "drift should be blocked")

	t.Log("")
	t.Log("SUCCESS: Drift was blocked - our modification to the ReplicaSet persisted")
}

// TestApprovalAllowsDrift verifies that kausality.io/approvals annotation allows
// drift to pass in enforce mode.
func TestApprovalAllowsDrift(t *testing.T) {
	ctx := context.Background()

	t.Log("=== Testing Approval Allows Drift ===")
	t.Log("When we directly modify a ReplicaSet's spec and the Deployment controller tries to fix it,")
	t.Log("with an approval annotation on the Deployment, the drift should be allowed.")

	// Step 1: Create a namespace with enforce mode
	enforceNS := fmt.Sprintf("approve-drift-%s", rand.String(4))
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: enforceNS,
			Annotations: map[string]string{
				"kausality.io/mode": "enforce",
			},
		},
	}
	_, err := clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		t.Logf("Cleanup: Deleting namespace %s", enforceNS)
		_ = clientset.CoreV1().Namespaces().Delete(ctx, enforceNS, metav1.DeleteOptions{})
	})
	t.Logf("Created namespace %s with enforce mode", enforceNS)

	// Step 2: Create a Deployment with approval for all ReplicaSets
	name := fmt.Sprintf("approve-drift-%s", rand.String(4))

	approvals := []approval.Approval{{
		APIVersion: "apps/v1",
		Kind:       "ReplicaSet",
		Name:       "*", // Approve all ReplicaSets
		Mode:       approval.ModeAlways,
	}}
	approvalData, err := json.Marshal(approvals)
	require.NoError(t, err)

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: enforceNS,
			Annotations: map[string]string{
				approval.ApprovalsAnnotation: string(approvalData),
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

	_, err = clientset.AppsV1().Deployments(enforceNS).Create(ctx, deployment, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Logf("Created Deployment %s with approval annotation", name)

	// Step 3: Wait for stabilization (gen == obsGen)
	ktesting.Eventually(t, func() (bool, string) {
		dep, err := clientset.AppsV1().Deployments(enforceNS).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Sprintf("error getting deployment: %v", err)
		}
		if dep.Status.ObservedGeneration != dep.Generation {
			return false, fmt.Sprintf("not stable: gen=%d, obsGen=%d", dep.Generation, dep.Status.ObservedGeneration)
		}
		if dep.Status.AvailableReplicas < 1 {
			return false, fmt.Sprintf("not available: replicas=%d", dep.Status.AvailableReplicas)
		}
		return true, "deployment stabilized"
	}, defaultTimeout, defaultInterval, "deployment should stabilize")
	t.Log("Deployment stabilized (gen == obsGen)")

	// Step 4: Get the ReplicaSet
	var rsName string
	ktesting.Eventually(t, func() (bool, string) {
		rsList, err := clientset.AppsV1().ReplicaSets(enforceNS).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("app=%s", name),
		})
		if err != nil {
			return false, fmt.Sprintf("error listing replicasets: %v", err)
		}
		if len(rsList.Items) == 0 {
			return false, "no replicaset found"
		}
		rsName = rsList.Items[0].Name
		return true, fmt.Sprintf("found replicaset %s", rsName)
	}, defaultTimeout, defaultInterval, "replicaset should exist")

	// Step 5: Directly modify the ReplicaSet's spec.replicas (simulate external drift)
	rs, err := clientset.AppsV1().ReplicaSets(enforceNS).Get(ctx, rsName, metav1.GetOptions{})
	require.NoError(t, err)

	rs.Spec.Replicas = ptr(int32(2))
	_, err = clientset.AppsV1().ReplicaSets(enforceNS).Update(ctx, rs, metav1.UpdateOptions{})
	require.NoError(t, err)
	t.Log("Directly modified ReplicaSet spec.replicas from 1 to 2")

	// Step 6: Wait for controller to fix the drift (should be allowed with approval)
	t.Log("Waiting for controller to reconcile (drift should be allowed with approval)...")

	// Step 7: Verify the controller was able to fix it (replicas should be back to 1)
	ktesting.Eventually(t, func() (bool, string) {
		rs, err := clientset.AppsV1().ReplicaSets(enforceNS).Get(ctx, rsName, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Sprintf("error getting replicaset: %v", err)
		}
		if *rs.Spec.Replicas != 1 {
			return false, fmt.Sprintf("replicas still %d (controller hasn't reconciled yet)", *rs.Spec.Replicas)
		}
		return true, "replicas back to 1 (controller reconciled successfully)"
	}, defaultTimeout, defaultInterval, "controller should fix the drift")

	t.Log("")
	t.Log("SUCCESS: Drift was allowed - controller successfully fixed the ReplicaSet")
}

// TestRejectionBlocksDrift verifies that kausality.io/rejections annotation blocks
// drift even when there might otherwise be approval.
func TestRejectionBlocksDrift(t *testing.T) {
	ctx := context.Background()

	t.Log("=== Testing Rejection Blocks Drift ===")
	t.Log("When a parent has a rejection for a child, drift should be blocked.")

	// Step 1: Create a namespace with enforce mode
	enforceNS := fmt.Sprintf("reject-drift-%s", rand.String(4))
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: enforceNS,
			Annotations: map[string]string{
				"kausality.io/mode": "enforce",
			},
		},
	}
	_, err := clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		t.Logf("Cleanup: Deleting namespace %s", enforceNS)
		_ = clientset.CoreV1().Namespaces().Delete(ctx, enforceNS, metav1.DeleteOptions{})
	})
	t.Logf("Created namespace %s with enforce mode", enforceNS)

	// Step 2: Create a Deployment (no rejection yet - add it after stabilization)
	name := fmt.Sprintf("reject-drift-%s", rand.String(4))
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: enforceNS,
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

	_, err = clientset.AppsV1().Deployments(enforceNS).Create(ctx, deployment, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Logf("Created Deployment %s", name)

	// Step 3: Wait for stabilization
	ktesting.Eventually(t, func() (bool, string) {
		dep, err := clientset.AppsV1().Deployments(enforceNS).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Sprintf("error getting deployment: %v", err)
		}
		if dep.Status.ObservedGeneration != dep.Generation {
			return false, fmt.Sprintf("not stable: gen=%d, obsGen=%d", dep.Generation, dep.Status.ObservedGeneration)
		}
		if dep.Status.AvailableReplicas < 1 {
			return false, fmt.Sprintf("not available: replicas=%d", dep.Status.AvailableReplicas)
		}
		return true, "deployment stabilized"
	}, defaultTimeout, defaultInterval, "deployment should stabilize")
	t.Log("Deployment stabilized")

	// Step 4: Get the ReplicaSet name
	var rsName string
	ktesting.Eventually(t, func() (bool, string) {
		rsList, err := clientset.AppsV1().ReplicaSets(enforceNS).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("app=%s", name),
		})
		if err != nil {
			return false, fmt.Sprintf("error listing replicasets: %v", err)
		}
		if len(rsList.Items) == 0 {
			return false, "no replicaset found"
		}
		rsName = rsList.Items[0].Name
		return true, fmt.Sprintf("found replicaset %s", rsName)
	}, defaultTimeout, defaultInterval, "replicaset should exist")

	// Step 5: Add rejection annotation to the Deployment
	dep, err := clientset.AppsV1().Deployments(enforceNS).Get(ctx, name, metav1.GetOptions{})
	require.NoError(t, err)

	rejections := []approval.Rejection{{
		APIVersion: "apps/v1",
		Kind:       "ReplicaSet",
		Name:       rsName,
		Reason:     "frozen by test",
	}}
	rejectionData, err := json.Marshal(rejections)
	require.NoError(t, err)

	if dep.Annotations == nil {
		dep.Annotations = make(map[string]string)
	}
	dep.Annotations[approval.RejectionsAnnotation] = string(rejectionData)
	_, err = clientset.AppsV1().Deployments(enforceNS).Update(ctx, dep, metav1.UpdateOptions{})
	require.NoError(t, err)
	t.Logf("Added rejection annotation to Deployment for ReplicaSet %s", rsName)

	// Step 6: Directly modify the ReplicaSet's spec.replicas
	// Use retry loop to handle conflicts with controller
	ktesting.Eventually(t, func() (bool, string) {
		rs, err := clientset.AppsV1().ReplicaSets(enforceNS).Get(ctx, rsName, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Sprintf("error getting replicaset: %v", err)
		}
		rs.Spec.Replicas = ptr(int32(2))
		_, err = clientset.AppsV1().ReplicaSets(enforceNS).Update(ctx, rs, metav1.UpdateOptions{})
		if err != nil {
			return false, fmt.Sprintf("error updating replicaset: %v", err)
		}
		return true, "successfully modified replicas to 2"
	}, defaultTimeout, defaultInterval, "should be able to modify replicaset")
	t.Log("Directly modified ReplicaSet spec.replicas from 1 to 2")

	// Step 7: Verify our modification persists (controller can't fix it due to rejection)
	ktesting.Eventually(t, func() (bool, string) {
		rs, err := clientset.AppsV1().ReplicaSets(enforceNS).Get(ctx, rsName, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Sprintf("error getting replicaset: %v", err)
		}
		if *rs.Spec.Replicas != 2 {
			return false, fmt.Sprintf("replicas changed to %d (drift was allowed despite rejection!)", *rs.Spec.Replicas)
		}
		return true, "replicas still 2 (drift blocked by rejection)"
	}, defaultTimeout, defaultInterval, "rejection should block drift")

	// Verify rejection annotation is still present
	dep, err = clientset.AppsV1().Deployments(enforceNS).Get(ctx, name, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Contains(t, dep.Annotations, approval.RejectionsAnnotation)

	t.Log("")
	t.Log("SUCCESS: Drift was blocked by rejection annotation")
}
