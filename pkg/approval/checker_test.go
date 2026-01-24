package approval

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestChecker_Check(t *testing.T) {
	checker := NewChecker()
	child := ChildRef{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Name:       "test-cm",
	}

	tests := []struct {
		name             string
		annotations      map[string]string
		parentGeneration int64
		wantApproved     bool
		wantRejected     bool
	}{
		{
			name:             "no annotations",
			annotations:      nil,
			parentGeneration: 1,
			wantApproved:     false,
			wantRejected:     false,
		},
		{
			name:             "empty annotations",
			annotations:      map[string]string{},
			parentGeneration: 1,
			wantApproved:     false,
			wantRejected:     false,
		},
		{
			name: "matching approval - mode always",
			annotations: map[string]string{
				ApprovalsAnnotation: `[{"apiVersion":"v1","kind":"ConfigMap","name":"test-cm","mode":"always"}]`,
			},
			parentGeneration: 99,
			wantApproved:     true,
			wantRejected:     false,
		},
		{
			name: "matching approval - mode once valid",
			annotations: map[string]string{
				ApprovalsAnnotation: `[{"apiVersion":"v1","kind":"ConfigMap","name":"test-cm","generation":5,"mode":"once"}]`,
			},
			parentGeneration: 5,
			wantApproved:     true,
			wantRejected:     false,
		},
		{
			name: "matching approval - mode once stale",
			annotations: map[string]string{
				ApprovalsAnnotation: `[{"apiVersion":"v1","kind":"ConfigMap","name":"test-cm","generation":5,"mode":"once"}]`,
			},
			parentGeneration: 6,
			wantApproved:     false,
			wantRejected:     false,
		},
		{
			name: "matching approval - mode generation valid",
			annotations: map[string]string{
				ApprovalsAnnotation: `[{"apiVersion":"v1","kind":"ConfigMap","name":"test-cm","generation":10,"mode":"generation"}]`,
			},
			parentGeneration: 10,
			wantApproved:     true,
			wantRejected:     false,
		},
		{
			name: "no matching approval",
			annotations: map[string]string{
				ApprovalsAnnotation: `[{"apiVersion":"v1","kind":"Secret","name":"other","mode":"always"}]`,
			},
			parentGeneration: 1,
			wantApproved:     false,
			wantRejected:     false,
		},
		{
			name: "matching rejection",
			annotations: map[string]string{
				RejectionsAnnotation: `[{"apiVersion":"v1","kind":"ConfigMap","name":"test-cm","reason":"dangerous"}]`,
			},
			parentGeneration: 1,
			wantApproved:     false,
			wantRejected:     true,
		},
		{
			name: "rejection wins over approval",
			annotations: map[string]string{
				ApprovalsAnnotation:  `[{"apiVersion":"v1","kind":"ConfigMap","name":"test-cm","mode":"always"}]`,
				RejectionsAnnotation: `[{"apiVersion":"v1","kind":"ConfigMap","name":"test-cm","reason":"nope"}]`,
			},
			parentGeneration: 1,
			wantApproved:     false,
			wantRejected:     true,
		},
		{
			name: "rejection with generation - matching",
			annotations: map[string]string{
				RejectionsAnnotation: `[{"apiVersion":"v1","kind":"ConfigMap","name":"test-cm","generation":5,"reason":"bad"}]`,
			},
			parentGeneration: 5,
			wantApproved:     false,
			wantRejected:     true,
		},
		{
			name: "rejection with generation - not matching",
			annotations: map[string]string{
				RejectionsAnnotation: `[{"apiVersion":"v1","kind":"ConfigMap","name":"test-cm","generation":5,"reason":"bad"}]`,
			},
			parentGeneration: 6,
			wantApproved:     false,
			wantRejected:     false,
		},
		{
			name: "invalid approvals json",
			annotations: map[string]string{
				ApprovalsAnnotation: `not valid json`,
			},
			parentGeneration: 1,
			wantApproved:     false,
			wantRejected:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parent := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "apps/v1",
					"kind":       "Deployment",
					"metadata": map[string]interface{}{
						"name":        "parent",
						"namespace":   "default",
						"annotations": toInterfaceMap(tt.annotations),
					},
				},
			}

			result := checker.Check(parent, child, tt.parentGeneration)

			assert.Equal(t, tt.wantApproved, result.Approved, "Approved mismatch (reason: %s)", result.Reason)
			assert.Equal(t, tt.wantRejected, result.Rejected, "Rejected mismatch (reason: %s)", result.Reason)
		})
	}
}

func TestChecker_MatchedApproval(t *testing.T) {
	checker := NewChecker()
	child := ChildRef{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Name:       "test-cm",
	}

	parent := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]interface{}{
				"name":      "parent",
				"namespace": "default",
				"annotations": map[string]interface{}{
					ApprovalsAnnotation: `[{"apiVersion":"v1","kind":"ConfigMap","name":"test-cm","generation":5,"mode":"once"}]`,
				},
			},
		},
	}

	result := checker.Check(parent, child, 5)

	require.True(t, result.Approved, "expected approved")
	require.NotNil(t, result.MatchedApproval, "expected MatchedApproval to be set")
	assert.Equal(t, ModeOnce, result.MatchedApproval.Mode)
	assert.Equal(t, int64(5), result.MatchedApproval.Generation)
}

func TestChecker_MatchedRejection(t *testing.T) {
	checker := NewChecker()
	child := ChildRef{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Name:       "test-cm",
	}

	parent := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]interface{}{
				"name":      "parent",
				"namespace": "default",
				"annotations": map[string]interface{}{
					RejectionsAnnotation: `[{"apiVersion":"v1","kind":"ConfigMap","name":"test-cm","reason":"too risky"}]`,
				},
			},
		},
	}

	result := checker.Check(parent, child, 1)

	require.True(t, result.Rejected, "expected rejected")
	require.NotNil(t, result.MatchedRejection, "expected MatchedRejection to be set")
	assert.Equal(t, "too risky", result.MatchedRejection.Reason)
	assert.Equal(t, "too risky", result.Reason)
}

func TestCheckFromAnnotations(t *testing.T) {
	child := ChildRef{
		APIVersion: "v1",
		Kind:       "Secret",
		Name:       "creds",
	}

	tests := []struct {
		name         string
		approvals    string
		rejections   string
		parentGen    int64
		wantApproved bool
		wantRejected bool
	}{
		{
			name:         "approved",
			approvals:    `[{"apiVersion":"v1","kind":"Secret","name":"creds","mode":"always"}]`,
			rejections:   "",
			parentGen:    1,
			wantApproved: true,
		},
		{
			name:         "rejected",
			approvals:    "",
			rejections:   `[{"apiVersion":"v1","kind":"Secret","name":"creds","reason":"nope"}]`,
			parentGen:    1,
			wantRejected: true,
		},
		{
			name:         "neither",
			approvals:    "",
			rejections:   "",
			parentGen:    1,
			wantApproved: false,
			wantRejected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CheckFromAnnotations(tt.approvals, tt.rejections, child, tt.parentGen)
			assert.Equal(t, tt.wantApproved, result.Approved)
			assert.Equal(t, tt.wantRejected, result.Rejected)
		})
	}
}

// toInterfaceMap converts map[string]string to map[string]interface{} for unstructured.
func toInterfaceMap(m map[string]string) map[string]interface{} {
	if m == nil {
		return nil
	}
	result := make(map[string]interface{}, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result
}

// Ensure unstructured implements client.Object
var _ metav1.Object = &unstructured.Unstructured{}
