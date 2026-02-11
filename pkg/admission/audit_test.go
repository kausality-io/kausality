package admission

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/kausality-io/kausality/pkg/controller"
)

var (
	configMapGVK  = schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"}
	deploymentGVK = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
	replicaSetGVK = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "ReplicaSet"}
)

// buildUnstructured creates an unstructured object with the given fields.
func buildUnstructured(gvk schema.GroupVersionKind, namespace, name string, spec map[string]interface{}, extras ...func(*unstructured.Unstructured)) *unstructured.Unstructured { //nolint:unparam
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": gvk.GroupVersion().String(),
		"kind":       gvk.Kind,
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
		},
	}}
	if spec != nil {
		obj.Object["spec"] = spec
	}
	for _, fn := range extras {
		fn(obj)
	}
	return obj
}

// withOwnerRef adds a controller ownerReference.
func withOwnerRef(gvk schema.GroupVersionKind, name string, uid types.UID) func(*unstructured.Unstructured) {
	return func(obj *unstructured.Unstructured) {
		obj.SetOwnerReferences([]metav1.OwnerReference{{
			APIVersion: gvk.GroupVersion().String(),
			Kind:       gvk.Kind,
			Name:       name,
			UID:        uid,
			Controller: boolPtr(true),
		}})
	}
}

// withStatus sets status fields on the object.
func withStatus(fields map[string]interface{}) func(*unstructured.Unstructured) {
	return func(obj *unstructured.Unstructured) {
		obj.Object["status"] = fields
	}
}

// withAnnotations sets annotations on the object.
func withAnnotations(ann map[string]string) func(*unstructured.Unstructured) {
	return func(obj *unstructured.Unstructured) {
		obj.SetAnnotations(ann)
	}
}

// withGeneration sets the generation on the object.
func withGeneration(gen int64) func(*unstructured.Unstructured) {
	return func(obj *unstructured.Unstructured) {
		obj.SetGeneration(gen)
	}
}

// withUID sets the UID on the object.
func withUID(uid types.UID) func(*unstructured.Unstructured) {
	return func(obj *unstructured.Unstructured) {
		obj.SetUID(uid)
	}
}

func boolPtr(b bool) *bool { return &b }

// buildAdmissionRequest constructs an admission.Request for testing.
func buildAdmissionRequest(op admissionv1.Operation, obj *unstructured.Unstructured, oldObj *unstructured.Unstructured, username string) admission.Request {
	raw, _ := json.Marshal(obj.Object)

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			UID:       "test-uid-1",
			Operation: op,
			Kind: metav1.GroupVersionKind{
				Group:   obj.GroupVersionKind().Group,
				Version: obj.GroupVersionKind().Version,
				Kind:    obj.GroupVersionKind().Kind,
			},
			Namespace: obj.GetNamespace(),
			Name:      obj.GetName(),
			Object:    runtime.RawExtension{Raw: raw},
			UserInfo:  testUserInfo(username),
		},
	}

	if oldObj != nil {
		oldRaw, _ := json.Marshal(oldObj.Object)
		req.OldObject = runtime.RawExtension{Raw: oldRaw}
	}

	return req
}

func testUserInfo(username string) authenticationv1.UserInfo {
	return authenticationv1.UserInfo{
		Username: username,
		UID:      username + "-uid",
	}
}

// newTestHandler creates a handler with a fake client containing the given objects.
func newTestHandler(objs ...runtime.Object) *Handler {
	scheme := runtime.NewScheme()
	// Register unstructured types - the fake client handles unstructured natively

	clientBuilder := fake.NewClientBuilder().WithScheme(scheme)
	for _, obj := range objs {
		clientBuilder = clientBuilder.WithRuntimeObjects(obj)
	}
	c := clientBuilder.Build()

	return NewHandler(Config{
		Client: c,
		Log:    logr.Discard(),
	})
}

func TestAuditAnnotations_CreateWithoutOwner(t *testing.T) {
	h := newTestHandler()
	ctx := context.Background()

	// CREATE a ConfigMap with no ownerRef
	obj := buildUnstructured(configMapGVK, "default", "test-cm",
		map[string]interface{}{"data": "value"})

	req := buildAdmissionRequest(admissionv1.Create, obj, nil, "admin")
	resp := h.Handle(ctx, req)

	require.True(t, resp.Allowed)
	audit := resp.AuditAnnotations
	assert.Equal(t, "allowed", audit[auditKeyDecision])
	assert.Equal(t, "false", audit[auditKeyDrift])
	assert.Equal(t, "log", audit[auditKeyMode])
	assert.NotEmpty(t, audit[auditKeyTrace], "trace should be set")

	// No drift → no drift-resolution
	assert.Empty(t, audit[auditKeyDriftResolution])
}

func TestAuditAnnotations_UpdateNoDrift(t *testing.T) {
	// Parent is reconciling (gen != obsGen) → child update is expected, not drift
	parent := buildUnstructured(deploymentGVK, "default", "parent-deploy",
		map[string]interface{}{"replicas": int64(1)},
		withUID("parent-uid-1"),
		withGeneration(2),
		withStatus(map[string]interface{}{
			"observedGeneration": int64(1), // gen != obsGen → reconciling
		}),
	)

	h := newTestHandler(parent)
	ctx := context.Background()

	// Child with ownerRef to parent
	child := buildUnstructured(replicaSetGVK, "default", "child-rs",
		map[string]interface{}{"replicas": int64(1)},
		withOwnerRef(deploymentGVK, "parent-deploy", "parent-uid-1"),
	)
	oldChild := buildUnstructured(replicaSetGVK, "default", "child-rs",
		map[string]interface{}{"replicas": int64(2)}, // different spec → spec changed
		withOwnerRef(deploymentGVK, "parent-deploy", "parent-uid-1"),
	)

	req := buildAdmissionRequest(admissionv1.Update, child, oldChild, "system:serviceaccount:kube-system:deployment-controller")
	resp := h.Handle(ctx, req)

	require.True(t, resp.Allowed)
	audit := resp.AuditAnnotations
	assert.Equal(t, "allowed", audit[auditKeyDecision])
	assert.Equal(t, "false", audit[auditKeyDrift], "parent reconciling → not drift")
	assert.Equal(t, "log", audit[auditKeyMode])
	assert.NotEmpty(t, audit[auditKeyTrace])
	assert.Empty(t, audit[auditKeyDriftResolution])
}

func TestAuditAnnotations_DriftDetectedLogMode(t *testing.T) {
	userHash := controller.HashUsername("system:serviceaccount:kube-system:deployment-controller")

	// Parent stable (gen == obsGen) and initialized
	parent := buildUnstructured(deploymentGVK, "default", "stable-deploy",
		map[string]interface{}{"replicas": int64(1)},
		withUID("stable-uid-1"),
		withGeneration(1),
		withAnnotations(map[string]string{
			controller.PhaseAnnotation: controller.PhaseValueInitialized,
		}),
		withStatus(map[string]interface{}{
			"observedGeneration": int64(1), // gen == obsGen → stable
		}),
	)

	h := newTestHandler(parent)
	ctx := context.Background()

	// Child with updater hash matching current user (single updater = controller)
	child := buildUnstructured(replicaSetGVK, "default", "drift-rs",
		map[string]interface{}{"replicas": int64(3)},
		withOwnerRef(deploymentGVK, "stable-deploy", "stable-uid-1"),
	)
	oldChild := buildUnstructured(replicaSetGVK, "default", "drift-rs",
		map[string]interface{}{"replicas": int64(1)},
		withOwnerRef(deploymentGVK, "stable-deploy", "stable-uid-1"),
		withAnnotations(map[string]string{
			controller.UpdatersAnnotation: userHash, // single updater = controller
		}),
	)

	req := buildAdmissionRequest(admissionv1.Update, child, oldChild,
		"system:serviceaccount:kube-system:deployment-controller")
	resp := h.Handle(ctx, req)

	require.True(t, resp.Allowed, "log mode allows drift")
	require.NotEmpty(t, resp.Warnings, "should have drift warning")
	audit := resp.AuditAnnotations
	assert.Equal(t, "allowed-with-warning", audit[auditKeyDecision])
	assert.Equal(t, "true", audit[auditKeyDrift])
	assert.Equal(t, "log", audit[auditKeyMode])
	assert.Equal(t, "Initialized", audit[auditKeyLifecyclePhase])
	assert.Equal(t, "unresolved", audit[auditKeyDriftResolution])
	assert.NotEmpty(t, audit[auditKeyTrace])
}

func TestAuditAnnotations_DriftDeniedEnforceMode(t *testing.T) {
	userHash := controller.HashUsername("system:serviceaccount:kube-system:deployment-controller")

	// Parent stable and initialized, with enforce mode annotation
	parent := buildUnstructured(deploymentGVK, "default", "enforce-deploy",
		map[string]interface{}{"replicas": int64(1)},
		withUID("enforce-uid-1"),
		withGeneration(1),
		withAnnotations(map[string]string{
			controller.PhaseAnnotation: controller.PhaseValueInitialized,
		}),
		withStatus(map[string]interface{}{
			"observedGeneration": int64(1),
		}),
	)

	h := newTestHandler(parent)
	ctx := context.Background()

	// Child with enforce mode annotation and updater hash
	child := buildUnstructured(replicaSetGVK, "default", "enforce-rs",
		map[string]interface{}{"replicas": int64(3)},
		withOwnerRef(deploymentGVK, "enforce-deploy", "enforce-uid-1"),
		withAnnotations(map[string]string{
			"kausality.io/mode": "enforce",
		}),
	)
	oldChild := buildUnstructured(replicaSetGVK, "default", "enforce-rs",
		map[string]interface{}{"replicas": int64(1)},
		withOwnerRef(deploymentGVK, "enforce-deploy", "enforce-uid-1"),
		withAnnotations(map[string]string{
			controller.UpdatersAnnotation: userHash,
			"kausality.io/mode":           "enforce",
		}),
	)

	req := buildAdmissionRequest(admissionv1.Update, child, oldChild,
		"system:serviceaccount:kube-system:deployment-controller")
	resp := h.Handle(ctx, req)

	require.False(t, resp.Allowed, "enforce mode denies drift")
	audit := resp.AuditAnnotations
	assert.Equal(t, "denied", audit[auditKeyDecision])
	assert.Equal(t, "true", audit[auditKeyDrift])
	assert.Equal(t, "enforce", audit[auditKeyMode])
	assert.Equal(t, "Initialized", audit[auditKeyLifecyclePhase])
	assert.Equal(t, "unresolved", audit[auditKeyDriftResolution])

	// Trace is NOT set on denied paths (mutation didn't happen, trace not computed)
	assert.Empty(t, audit[auditKeyTrace])
}

func TestAuditAnnotations_DeleteHasTrace(t *testing.T) {
	h := newTestHandler()
	ctx := context.Background()

	// DELETE a ConfigMap (no owner, no drift)
	obj := buildUnstructured(configMapGVK, "default", "delete-cm",
		map[string]interface{}{"data": "value"})

	req := buildAdmissionRequest(admissionv1.Delete, obj, nil, "admin")
	// For DELETE, the object is in OldObject, not Object
	req.OldObject = req.Object
	req.Object = runtime.RawExtension{}

	resp := h.Handle(ctx, req)

	require.True(t, resp.Allowed)
	audit := resp.AuditAnnotations
	assert.Equal(t, "allowed", audit[auditKeyDecision])
	assert.Equal(t, "false", audit[auditKeyDrift])
	assert.NotEmpty(t, audit[auditKeyTrace], "DELETE should have trace in audit (can't patch object)")
}

func TestAuditAnnotations_NoAuditOnStatusUpdate(t *testing.T) {
	h := newTestHandler()
	ctx := context.Background()

	obj := buildUnstructured(deploymentGVK, "default", "status-deploy",
		map[string]interface{}{"replicas": int64(1)})
	oldObj := buildUnstructured(deploymentGVK, "default", "status-deploy",
		map[string]interface{}{"replicas": int64(1)})

	req := buildAdmissionRequest(admissionv1.Update, obj, oldObj, "controller")
	req.SubResource = "status"

	resp := h.Handle(ctx, req)

	require.True(t, resp.Allowed)
	// Status updates don't set audit annotations
	assert.Empty(t, resp.AuditAnnotations)
}

func TestAuditAnnotations_FreezeDeniesMutation(t *testing.T) {
	// Parent with freeze annotation
	parent := buildUnstructured(deploymentGVK, "default", "frozen-deploy",
		map[string]interface{}{"replicas": int64(1)},
		withUID("frozen-uid-1"),
		withGeneration(1),
		withAnnotations(map[string]string{
			controller.PhaseAnnotation: controller.PhaseValueInitialized,
			"kausality.io/freeze":      `{"user":"admin","message":"emergency"}`,
		}),
		withStatus(map[string]interface{}{
			"observedGeneration": int64(1),
		}),
	)

	h := newTestHandler(parent)
	ctx := context.Background()

	child := buildUnstructured(replicaSetGVK, "default", "frozen-rs",
		map[string]interface{}{"replicas": int64(3)},
		withOwnerRef(deploymentGVK, "frozen-deploy", "frozen-uid-1"),
	)
	oldChild := buildUnstructured(replicaSetGVK, "default", "frozen-rs",
		map[string]interface{}{"replicas": int64(1)},
		withOwnerRef(deploymentGVK, "frozen-deploy", "frozen-uid-1"),
	)

	req := buildAdmissionRequest(admissionv1.Update, child, oldChild, "someone")
	resp := h.Handle(ctx, req)

	require.False(t, resp.Allowed, "frozen parent denies all mutations")
	audit := resp.AuditAnnotations
	assert.Equal(t, "denied", audit[auditKeyDecision])
	assert.NotEmpty(t, audit[auditKeyDrift]) // drift detection ran before freeze check

	// Mode is NOT set (resolved after freeze check)
	assert.Empty(t, audit[auditKeyMode])
}

func TestAuditDecision(t *testing.T) {
	assert.Equal(t, "allowed", auditDecision(nil))
	assert.Equal(t, "allowed", auditDecision([]string{}))
	assert.Equal(t, "allowed-with-warning", auditDecision([]string{"drift detected"}))
}

func TestWithAuditAnnotations(t *testing.T) {
	resp := admission.Allowed("ok")

	// nil audit → no change
	result := withAuditAnnotations(resp, nil)
	assert.Nil(t, result.AuditAnnotations)

	// empty audit → no change
	result = withAuditAnnotations(resp, map[string]string{})
	assert.Nil(t, result.AuditAnnotations)

	// non-empty audit → set
	audit := map[string]string{"key": "value"}
	result = withAuditAnnotations(resp, audit)
	assert.Equal(t, audit, result.AuditAnnotations)
}
