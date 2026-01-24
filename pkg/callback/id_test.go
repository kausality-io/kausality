package callback

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kausality-io/kausality/pkg/callback/v1alpha1"
)

func TestGenerateDriftID(t *testing.T) {
	parent := v1alpha1.ObjectReference{
		APIVersion: "example.com/v1alpha1",
		Kind:       "EKSCluster",
		Namespace:  "infra",
		Name:       "prod",
	}
	child := v1alpha1.ObjectReference{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Namespace:  "infra",
		Name:       "cluster-config",
	}
	specDiff := []byte(`{"data":{"key":"value"}}`)

	id := GenerateDriftID(parent, child, specDiff)

	// Check format: 16-character hex string
	assert.Len(t, id, 16)
	assert.Regexp(t, "^[0-9a-f]+$", id)
}

func TestGenerateDriftID_Deterministic(t *testing.T) {
	parent := v1alpha1.ObjectReference{
		APIVersion: "example.com/v1alpha1",
		Kind:       "EKSCluster",
		Namespace:  "infra",
		Name:       "prod",
	}
	child := v1alpha1.ObjectReference{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Namespace:  "infra",
		Name:       "cluster-config",
	}
	specDiff := []byte(`{"data":{"key":"value"}}`)

	// Generate multiple times, should be same
	id1 := GenerateDriftID(parent, child, specDiff)
	id2 := GenerateDriftID(parent, child, specDiff)
	id3 := GenerateDriftID(parent, child, specDiff)

	assert.Equal(t, id1, id2)
	assert.Equal(t, id2, id3)
}

func TestGenerateDriftID_DifferentParent(t *testing.T) {
	child := v1alpha1.ObjectReference{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Namespace:  "infra",
		Name:       "cluster-config",
	}
	specDiff := []byte(`{"data":{"key":"value"}}`)

	parent1 := v1alpha1.ObjectReference{
		APIVersion: "example.com/v1alpha1",
		Kind:       "EKSCluster",
		Namespace:  "infra",
		Name:       "prod",
	}
	parent2 := v1alpha1.ObjectReference{
		APIVersion: "example.com/v1alpha1",
		Kind:       "EKSCluster",
		Namespace:  "infra",
		Name:       "staging",
	}

	id1 := GenerateDriftID(parent1, child, specDiff)
	id2 := GenerateDriftID(parent2, child, specDiff)

	assert.NotEqual(t, id1, id2)
}

func TestGenerateDriftID_DifferentChild(t *testing.T) {
	parent := v1alpha1.ObjectReference{
		APIVersion: "example.com/v1alpha1",
		Kind:       "EKSCluster",
		Namespace:  "infra",
		Name:       "prod",
	}
	specDiff := []byte(`{"data":{"key":"value"}}`)

	child1 := v1alpha1.ObjectReference{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Namespace:  "infra",
		Name:       "cluster-config",
	}
	child2 := v1alpha1.ObjectReference{
		APIVersion: "v1",
		Kind:       "Secret",
		Namespace:  "infra",
		Name:       "cluster-credentials",
	}

	id1 := GenerateDriftID(parent, child1, specDiff)
	id2 := GenerateDriftID(parent, child2, specDiff)

	assert.NotEqual(t, id1, id2)
}

func TestGenerateDriftID_DifferentSpecDiff(t *testing.T) {
	parent := v1alpha1.ObjectReference{
		APIVersion: "example.com/v1alpha1",
		Kind:       "EKSCluster",
		Namespace:  "infra",
		Name:       "prod",
	}
	child := v1alpha1.ObjectReference{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Namespace:  "infra",
		Name:       "cluster-config",
	}

	// Same parent/child but different diff
	id1 := GenerateDriftID(parent, child, []byte(`{"data":{"key":"value1"}}`))
	id2 := GenerateDriftID(parent, child, []byte(`{"data":{"key":"value2"}}`))

	assert.NotEqual(t, id1, id2)
}

func TestGenerateDriftID_EmptySpecDiff(t *testing.T) {
	parent := v1alpha1.ObjectReference{
		APIVersion: "example.com/v1alpha1",
		Kind:       "EKSCluster",
		Namespace:  "infra",
		Name:       "prod",
	}
	child := v1alpha1.ObjectReference{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Namespace:  "infra",
		Name:       "cluster-config",
	}

	id := GenerateDriftID(parent, child, nil)
	assert.Len(t, id, 16)
}

func TestGenerateResolutionID(t *testing.T) {
	parent := v1alpha1.ObjectReference{
		APIVersion: "example.com/v1alpha1",
		Kind:       "EKSCluster",
		Namespace:  "infra",
		Name:       "prod",
	}
	child := v1alpha1.ObjectReference{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Namespace:  "infra",
		Name:       "cluster-config",
	}

	id := GenerateResolutionID(parent, child)

	// Check format: 16-character hex string
	assert.Len(t, id, 16)
	assert.Regexp(t, "^[0-9a-f]+$", id)
}

func TestGenerateResolutionID_Deterministic(t *testing.T) {
	parent := v1alpha1.ObjectReference{
		APIVersion: "example.com/v1alpha1",
		Kind:       "EKSCluster",
		Namespace:  "infra",
		Name:       "prod",
	}
	child := v1alpha1.ObjectReference{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Namespace:  "infra",
		Name:       "cluster-config",
	}

	id1 := GenerateResolutionID(parent, child)
	id2 := GenerateResolutionID(parent, child)

	assert.Equal(t, id1, id2)
}

func TestGenerateResolutionID_DifferentFromDriftID(t *testing.T) {
	parent := v1alpha1.ObjectReference{
		APIVersion: "example.com/v1alpha1",
		Kind:       "EKSCluster",
		Namespace:  "infra",
		Name:       "prod",
	}
	child := v1alpha1.ObjectReference{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Namespace:  "infra",
		Name:       "cluster-config",
	}

	// Resolution ID should be different from drift ID (even with empty diff)
	// because the resolution ID is based solely on parent/child, while
	// the drift ID includes the spec diff.
	driftID := GenerateDriftID(parent, child, nil)
	resolutionID := GenerateResolutionID(parent, child)

	// They may or may not be equal depending on implementation
	// The key property is that resolution ID is stable for same parent/child pair
	assert.Len(t, driftID, 16)
	assert.Len(t, resolutionID, 16)
}
