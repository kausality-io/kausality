//go:build e2e

package kubernetes

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"

	ktesting "github.com/kausality-io/kausality/pkg/testing"
)

// =============================================================================
// Backend Tests
// =============================================================================

// TestBackendPodReady verifies that the kausality backend pod is running.
func TestBackendPodReady(t *testing.T) {
	ctx := context.Background()

	t.Log("=== Testing Backend Pod ===")
	t.Log("Checking that the kausality-backend pod is running...")

	ktesting.Eventually(t, func() (bool, string) {
		pods, err := clientset.CoreV1().Pods(kausalityNS).List(ctx, metav1.ListOptions{
			LabelSelector: "app.kubernetes.io/name=kausality-backend",
		})
		if err != nil {
			return false, fmt.Sprintf("error listing backend pods: %v", err)
		}
		if len(pods.Items) == 0 {
			return false, "no backend pods found yet"
		}
		for _, pod := range pods.Items {
			if pod.Status.Phase != corev1.PodRunning {
				return false, fmt.Sprintf("backend pod %s phase=%s, waiting for Running", pod.Name, pod.Status.Phase)
			}
		}
		return true, fmt.Sprintf("backend pod is running")
	}, defaultTimeout, defaultInterval, "backend pod should be ready")

	t.Log("")
	t.Log("SUCCESS: Backend pod is running")
}

// TestBackendReceivesDriftReports verifies that DriftReports are sent to the backend
// when drift is detected. This test triggers a drift scenario by modifying a ReplicaSet
// directly, causing the Deployment controller to try to fix it (which is drift).
func TestBackendReceivesDriftReports(t *testing.T) {
	ctx := context.Background()
	name := fmt.Sprintf("drift-backend-%s", rand.String(4))

	t.Log("=== Testing Backend DriftReport Reception ===")
	t.Log("When drift is detected, the webhook should send a DriftReport to the backend.")

	// Step 1: Create a Deployment and wait for it to stabilize
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
						Image: "nginx:alpine",
					}},
				},
			},
		},
	}

	_, err := clientset.AppsV1().Deployments(testNamespace).Create(ctx, deployment, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		t.Logf("Cleanup: Deleting deployment %s", name)
		_ = clientset.AppsV1().Deployments(testNamespace).Delete(ctx, name, metav1.DeleteOptions{})
	})

	// Wait for stabilization
	ktesting.Eventually(t, func() (bool, string) {
		dep, err := clientset.AppsV1().Deployments(testNamespace).Get(ctx, name, metav1.GetOptions{})
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

	// Step 2: Find the ReplicaSet and modify it directly to trigger drift
	t.Log("")
	t.Log("Step 2: Modifying ReplicaSet directly to trigger drift...")
	var rsName string
	ktesting.Eventually(t, func() (bool, string) {
		rsList, err := clientset.AppsV1().ReplicaSets(testNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("app=%s", name),
		})
		if err != nil || len(rsList.Items) == 0 {
			return false, "no replicaset found"
		}
		rsName = rsList.Items[0].Name
		return true, fmt.Sprintf("found replicaset %s", rsName)
	}, defaultTimeout, defaultInterval, "replicaset should exist")

	// Modify the ReplicaSet's spec.replicas (this is a user modification)
	rs, err := clientset.AppsV1().ReplicaSets(testNamespace).Get(ctx, rsName, metav1.GetOptions{})
	require.NoError(t, err)
	rs.Spec.Replicas = ptr(int32(2))
	_, err = clientset.AppsV1().ReplicaSets(testNamespace).Update(ctx, rs, metav1.UpdateOptions{})
	require.NoError(t, err)
	t.Log("Modified ReplicaSet replicas to 2 - controller will try to fix this (drift)")

	// Wait for controller to detect and fix the drift
	// In log mode, drift is detected but allowed, so the controller can fix it
	ktesting.Eventually(t, func() (bool, string) {
		rs, err := clientset.AppsV1().ReplicaSets(testNamespace).Get(ctx, rsName, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Sprintf("error getting replicaset: %v", err)
		}
		if *rs.Spec.Replicas != 1 {
			return false, fmt.Sprintf("replicas=%d, waiting for controller to fix", *rs.Spec.Replicas)
		}
		return true, "controller fixed replicas (drift detected and allowed in log mode)"
	}, defaultTimeout, defaultInterval, "controller should fix drift")

	// Step 3: Check backend logs for DriftReport
	t.Log("")
	t.Log("Step 3: Checking backend logs for DriftReport...")

	ktesting.Eventually(t, func() (bool, string) {
		pods, err := clientset.CoreV1().Pods(kausalityNS).List(ctx, metav1.ListOptions{
			LabelSelector: "app.kubernetes.io/name=kausality-backend",
		})
		if err != nil {
			return false, fmt.Sprintf("error listing backend pods: %v", err)
		}
		if len(pods.Items) == 0 {
			return false, "no backend pods found"
		}

		// Get logs from the backend pod
		podName := pods.Items[0].Name
		req := clientset.CoreV1().Pods(kausalityNS).GetLogs(podName, &corev1.PodLogOptions{
			TailLines: ptr(int64(1000)),
		})
		logs, err := req.Do(ctx).Raw()
		if err != nil {
			return false, fmt.Sprintf("error getting logs: %v", err)
		}

		logStr := string(logs)

		// Check for DriftReport markers in the logs
		if !contains(logStr, "apiVersion: kausality.io") && !contains(logStr, "kind: DriftReport") {
			return false, "no DriftReport found in backend logs yet"
		}

		// Check for the specific deployment name or phase
		if !contains(logStr, "phase: Detected") && !contains(logStr, "phase: Resolved") {
			return false, "DriftReport found but no phase detected"
		}

		return true, "DriftReport found in backend logs"
	}, annotationTimeout, defaultInterval, "backend should receive DriftReport")

	t.Log("")
	t.Log("SUCCESS: Backend received DriftReport from webhook")
}
