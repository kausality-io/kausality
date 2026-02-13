//go:build e2e

package crossplane

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/rand"

	ktesting "github.com/kausality-io/kausality/pkg/testing"
)

// =============================================================================
// Trace Origin vs Propagation Tests for Crossplane
// =============================================================================

// TestTraceExtendOnCompositionReconcile verifies that when the Crossplane
// composition controller creates a NopResource from a composite resource,
// the NopResource gets a multi-hop trace extending from the parent XService.
func TestTraceExtendOnCompositionReconcile(t *testing.T) {
	ctx := context.Background()
	suffix := rand.String(4)

	t.Log("=== Testing Trace Extension on Composition Reconcile ===")
	t.Log("When Crossplane creates a NopResource from a composition,")
	t.Log("the NopResource should get a multi-hop trace extending from the parent.")

	// Step 1: Ensure XRDs exist
	t.Log("")
	t.Log("Step 1: Ensuring XRDs exist...")

	xserviceXRD := makeXServiceXRD()
	_, err := dynamicClient.Resource(xrdGVR).Create(ctx, xserviceXRD, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		require.NoError(t, err)
	}

	xplatformXRD := makeXPlatformXRD()
	_, err = dynamicClient.Resource(xrdGVR).Create(ctx, xplatformXRD, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		require.NoError(t, err)
	}

	waitForXRDEstablished(t, ctx, "xservices.test.kausality.io")
	waitForXRDEstablished(t, ctx, "xplatforms.test.kausality.io")
	t.Log("XRDs are established")

	// Step 2: Create compositions
	t.Log("")
	t.Log("Step 2: Creating compositions...")

	xserviceComp := makeXServiceComposition(suffix)
	_, err = dynamicClient.Resource(compositionGVR).Create(ctx, xserviceComp, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = dynamicClient.Resource(compositionGVR).Delete(ctx, xserviceComp.GetName(), metav1.DeleteOptions{})
	})

	xplatformComp := makeXPlatformComposition(suffix)
	_, err = dynamicClient.Resource(compositionGVR).Create(ctx, xplatformComp, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = dynamicClient.Resource(compositionGVR).Delete(ctx, xplatformComp.GetName(), metav1.DeleteOptions{})
	})

	// Step 3: Create XPlatform with trace label
	t.Log("")
	t.Log("Step 3: Creating XPlatform with trace label...")

	xplatformGVR := schema.GroupVersionResource{
		Group:    "test.kausality.io",
		Version:  "v1alpha1",
		Resource: "xplatforms",
	}

	xplatform := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "test.kausality.io/v1alpha1",
			"kind":       "XPlatform",
			"metadata": map[string]interface{}{
				"name": "trace-extend-" + suffix,
				"annotations": map[string]interface{}{
					"kausality.io/trace-ticket": "EXTEND-XP-001",
				},
			},
			"spec": map[string]interface{}{
				"platformName": "trace-extend-test",
			},
		},
	}

	_, err = dynamicClient.Resource(xplatformGVR).Create(ctx, xplatform, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = dynamicClient.Resource(xplatformGVR).Delete(ctx, "trace-extend-"+suffix, metav1.DeleteOptions{})
		time.Sleep(2 * time.Second)
	})

	// Step 4: Wait for composition hierarchy to be created
	t.Log("")
	t.Log("Step 4: Waiting for composition hierarchy...")

	xserviceGVR := schema.GroupVersionResource{
		Group:    "test.kausality.io",
		Version:  "v1alpha1",
		Resource: "xservices",
	}

	var xserviceName string
	ktesting.Eventually(t, func() (bool, string) {
		list, err := dynamicClient.Resource(xserviceGVR).List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, fmt.Sprintf("error: %v", err)
		}
		for _, item := range list.Items {
			for _, owner := range item.GetOwnerReferences() {
				if owner.Kind == "XPlatform" && owner.Name == "trace-extend-"+suffix {
					xserviceName = item.GetName()
					return true, fmt.Sprintf("found XService %s", xserviceName)
				}
			}
		}
		return false, "waiting for XService"
	}, 90*time.Second, 2*time.Second, "XService should be created")

	var nopResourceName string
	ktesting.Eventually(t, func() (bool, string) {
		list, err := dynamicClient.Resource(nopResourceGVR).List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, fmt.Sprintf("error: %v", err)
		}
		for _, item := range list.Items {
			for _, owner := range item.GetOwnerReferences() {
				if owner.Kind == "XService" && owner.Name == xserviceName {
					nopResourceName = item.GetName()
					return true, fmt.Sprintf("found NopResource %s", nopResourceName)
				}
			}
		}
		return false, "waiting for NopResource"
	}, 120*time.Second, 2*time.Second, "NopResource should be created")

	t.Logf("Hierarchy: XPlatform -> %s -> %s", xserviceName, nopResourceName)

	// Step 5: Wait for NopResource to become Ready
	t.Log("")
	t.Log("Step 5: Waiting for NopResource to become Ready...")

	ktesting.Eventually(t, func() (bool, string) {
		obj, err := dynamicClient.Resource(nopResourceGVR).Get(ctx, nopResourceName, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Sprintf("error: %v", err)
		}
		conditions, found, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
		if !found {
			return false, "no conditions"
		}
		for _, c := range conditions {
			cond, _ := c.(map[string]interface{})
			cType, _, _ := unstructured.NestedString(cond, "type")
			cStatus, _, _ := unstructured.NestedString(cond, "status")
			if cType == "Ready" && cStatus == "True" {
				return true, "Ready"
			}
		}
		return false, "not Ready"
	}, 60*time.Second, 2*time.Second, "NopResource should be Ready")

	// Step 6: Check NopResource has a multi-hop trace
	t.Log("")
	t.Log("Step 6: Checking NopResource for multi-hop trace (extended from parent)...")

	ktesting.Eventually(t, func() (bool, string) {
		obj, err := dynamicClient.Resource(nopResourceGVR).Get(ctx, nopResourceName, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Sprintf("error: %v", err)
		}

		traceStr, found, _ := unstructured.NestedString(obj.Object, "metadata", "annotations", "kausality.io/trace")
		if !found || traceStr == "" {
			return false, "no trace annotation"
		}

		var hops []map[string]interface{}
		if err := json.Unmarshal([]byte(traceStr), &hops); err != nil {
			return false, fmt.Sprintf("parse error: %v", err)
		}

		if len(hops) < 2 {
			return false, fmt.Sprintf("expected >=2 hops, got %d: %s", len(hops), traceStr)
		}

		// Log the trace hops for debugging
		for i, hop := range hops {
			kind, _ := hop["kind"].(string)
			name, _ := hop["name"].(string)
			t.Logf("  Hop %d: %s/%s", i, kind, name)
		}

		return true, fmt.Sprintf("NopResource has %d-hop trace (extended from parent)", len(hops))
	}, annotationTimeout, defaultInterval, "NopResource should have multi-hop trace")

	t.Log("")
	t.Log("SUCCESS: Composition-created NopResource has extended (multi-hop) trace")
}

// TestTraceOriginOnDirectNopResourceEdit verifies that when a user directly
// edits a NopResource's spec (bypassing the composition), the NopResource
// gets a fresh 1-hop origin trace.
func TestTraceOriginOnDirectNopResourceEdit(t *testing.T) {
	ctx := context.Background()
	suffix := rand.String(4)

	t.Log("=== Testing Fresh Origin on Direct NopResource Edit ===")
	t.Log("When a user directly edits a NopResource's spec,")
	t.Log("the trace should be a fresh 1-hop origin, not extended from the composition parent.")

	// Step 1: Set up composition hierarchy
	t.Log("")
	t.Log("Step 1: Setting up composition hierarchy...")

	xserviceXRD := makeXServiceXRD()
	_, _ = dynamicClient.Resource(xrdGVR).Create(ctx, xserviceXRD, metav1.CreateOptions{})
	xplatformXRD := makeXPlatformXRD()
	_, _ = dynamicClient.Resource(xrdGVR).Create(ctx, xplatformXRD, metav1.CreateOptions{})
	waitForXRDEstablished(t, ctx, "xservices.test.kausality.io")
	waitForXRDEstablished(t, ctx, "xplatforms.test.kausality.io")

	xserviceComp := makeXServiceComposition(suffix)
	_, err := dynamicClient.Resource(compositionGVR).Create(ctx, xserviceComp, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = dynamicClient.Resource(compositionGVR).Delete(ctx, xserviceComp.GetName(), metav1.DeleteOptions{})
	})

	xplatformComp := makeXPlatformComposition(suffix)
	_, err = dynamicClient.Resource(compositionGVR).Create(ctx, xplatformComp, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = dynamicClient.Resource(compositionGVR).Delete(ctx, xplatformComp.GetName(), metav1.DeleteOptions{})
	})

	xplatformGVR := schema.GroupVersionResource{
		Group:    "test.kausality.io",
		Version:  "v1alpha1",
		Resource: "xplatforms",
	}

	xplatform := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "test.kausality.io/v1alpha1",
			"kind":       "XPlatform",
			"metadata": map[string]interface{}{
				"name": "trace-origin-" + suffix,
			},
			"spec": map[string]interface{}{
				"platformName": "trace-origin-test",
			},
		},
	}

	_, err = dynamicClient.Resource(xplatformGVR).Create(ctx, xplatform, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = dynamicClient.Resource(xplatformGVR).Delete(ctx, "trace-origin-"+suffix, metav1.DeleteOptions{})
		time.Sleep(2 * time.Second)
	})

	// Wait for hierarchy
	xserviceGVR := schema.GroupVersionResource{
		Group:    "test.kausality.io",
		Version:  "v1alpha1",
		Resource: "xservices",
	}

	var xserviceName string
	ktesting.Eventually(t, func() (bool, string) {
		list, err := dynamicClient.Resource(xserviceGVR).List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, fmt.Sprintf("error: %v", err)
		}
		for _, item := range list.Items {
			for _, owner := range item.GetOwnerReferences() {
				if owner.Kind == "XPlatform" && owner.Name == "trace-origin-"+suffix {
					xserviceName = item.GetName()
					return true, fmt.Sprintf("found XService %s", xserviceName)
				}
			}
		}
		return false, "waiting for XService"
	}, 90*time.Second, 2*time.Second, "XService should be created")

	var nopResourceName string
	ktesting.Eventually(t, func() (bool, string) {
		list, err := dynamicClient.Resource(nopResourceGVR).List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, fmt.Sprintf("error: %v", err)
		}
		for _, item := range list.Items {
			for _, owner := range item.GetOwnerReferences() {
				if owner.Kind == "XService" && owner.Name == xserviceName {
					nopResourceName = item.GetName()
					return true, fmt.Sprintf("found NopResource %s", nopResourceName)
				}
			}
		}
		return false, "waiting for NopResource"
	}, 120*time.Second, 2*time.Second, "NopResource should be created")

	// Wait for Ready and stability
	ktesting.Eventually(t, func() (bool, string) {
		obj, err := dynamicClient.Resource(nopResourceGVR).Get(ctx, nopResourceName, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Sprintf("error: %v", err)
		}
		conditions, found, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
		if !found {
			return false, "no conditions"
		}
		for _, c := range conditions {
			cond, _ := c.(map[string]interface{})
			cType, _, _ := unstructured.NestedString(cond, "type")
			cStatus, _, _ := unstructured.NestedString(cond, "status")
			if cType == "Ready" && cStatus == "True" {
				return true, "Ready"
			}
		}
		return false, "not Ready"
	}, 60*time.Second, 2*time.Second, "NopResource should be Ready")

	t.Logf("Hierarchy stable: XPlatform -> %s -> %s", xserviceName, nopResourceName)

	// Step 2: Verify NopResource has initial multi-hop trace (baseline)
	t.Log("")
	t.Log("Step 2: Verifying NopResource has initial multi-hop trace (baseline)...")

	ktesting.Eventually(t, func() (bool, string) {
		obj, err := dynamicClient.Resource(nopResourceGVR).Get(ctx, nopResourceName, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Sprintf("error: %v", err)
		}
		traceStr, found, _ := unstructured.NestedString(obj.Object, "metadata", "annotations", "kausality.io/trace")
		if !found || traceStr == "" {
			return false, "no trace annotation"
		}
		var hops []map[string]interface{}
		if err := json.Unmarshal([]byte(traceStr), &hops); err != nil {
			return false, fmt.Sprintf("parse error: %v", err)
		}
		if len(hops) < 2 {
			return false, fmt.Sprintf("expected >=2 hops (baseline), got %d: %s", len(hops), traceStr)
		}
		return true, fmt.Sprintf("baseline: %d-hop trace", len(hops))
	}, annotationTimeout, defaultInterval, "baseline multi-hop trace")
	t.Log("Baseline verified: NopResource has multi-hop trace")

	// Step 3: User directly edits NopResource spec
	t.Log("")
	t.Log("Step 3: User directly edits NopResource spec (bypass composition)...")

	nopResource, err := dynamicClient.Resource(nopResourceGVR).Get(ctx, nopResourceName, metav1.GetOptions{})
	require.NoError(t, err)

	err = unstructured.SetNestedField(nopResource.Object, []interface{}{
		map[string]interface{}{
			"time":            "42s", // User-chosen value
			"conditionType":   "Ready",
			"conditionStatus": "True",
		},
	}, "spec", "forProvider", "conditionAfter")
	require.NoError(t, err)

	_, err = dynamicClient.Resource(nopResourceGVR).Update(ctx, nopResource, metav1.UpdateOptions{})
	require.NoError(t, err, "user edit should succeed")
	t.Log("User edit succeeded")

	// Step 4: Verify the trace is now a fresh origin (1-hop)
	t.Log("")
	t.Log("Step 4: Checking that NopResource now has a fresh 1-hop origin trace...")

	ktesting.Eventually(t, func() (bool, string) {
		obj, err := dynamicClient.Resource(nopResourceGVR).Get(ctx, nopResourceName, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Sprintf("error: %v", err)
		}

		traceStr, found, _ := unstructured.NestedString(obj.Object, "metadata", "annotations", "kausality.io/trace")
		if !found || traceStr == "" {
			return false, "no trace annotation"
		}

		var hops []map[string]interface{}
		if err := json.Unmarshal([]byte(traceStr), &hops); err != nil {
			return false, fmt.Sprintf("parse error: %v", err)
		}

		if len(hops) != 1 {
			return false, fmt.Sprintf("expected 1-hop origin, got %d hops: %s", len(hops), traceStr)
		}

		// The single hop should be for the NopResource
		hopKind, _ := hops[0]["kind"].(string)
		assert.Equal(t, "NopResource", hopKind, "origin hop should be NopResource")

		return true, "NopResource has 1-hop origin trace (fresh origin)"
	}, annotationTimeout, defaultInterval, "NopResource should have fresh 1-hop origin after user edit")

	t.Log("")
	t.Log("SUCCESS: Direct user edit creates fresh origin trace (not extended from composition)")
	t.Log("This confirms the IsControllerByHash cross-validation and isOrigin fixes work")
	t.Log("end-to-end with Crossplane composition hierarchies:")
	t.Log("  - Composition-created NopResource: multi-hop trace (extended)")
	t.Log("  - User-edited NopResource: 1-hop trace (fresh origin)")
}
