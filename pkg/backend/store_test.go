package backend

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kausality-io/kausality/pkg/callback/v1alpha1"
)

func TestStore_Add(t *testing.T) {
	store := NewStore()

	report := &v1alpha1.DriftReport{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "kausality.io/v1alpha1",
			Kind:       "DriftReport",
		},
		Spec: v1alpha1.DriftReportSpec{
			ID:    "test-drift-001",
			Phase: v1alpha1.DriftReportPhaseDetected,
			Parent: v1alpha1.ObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Namespace:  "default",
				Name:       "my-app",
			},
			Child: v1alpha1.ObjectReference{
				APIVersion: "v1",
				Kind:       "ConfigMap",
				Namespace:  "default",
				Name:       "my-app-config",
			},
			Request: v1alpha1.RequestContext{
				User:      "system:serviceaccount:default:my-controller",
				Operation: "UPDATE",
			},
		},
	}

	store.Add(report)

	assert.Equal(t, 1, store.Count())

	stored, ok := store.Get("test-drift-001")
	require.True(t, ok)
	assert.Equal(t, "test-drift-001", stored.Report.Spec.ID)
	assert.Equal(t, v1alpha1.DriftReportPhaseDetected, stored.Report.Spec.Phase)
	assert.Equal(t, "Deployment", stored.Report.Spec.Parent.Kind)
	assert.Equal(t, "ConfigMap", stored.Report.Spec.Child.Kind)
}

func TestStore_Add_Resolved_Removes(t *testing.T) {
	store := NewStore()

	// Add a detected drift
	detected := &v1alpha1.DriftReport{
		Spec: v1alpha1.DriftReportSpec{
			ID:    "drift-to-resolve",
			Phase: v1alpha1.DriftReportPhaseDetected,
		},
	}
	store.Add(detected)
	assert.Equal(t, 1, store.Count())

	// Send resolved - should remove
	resolved := &v1alpha1.DriftReport{
		Spec: v1alpha1.DriftReportSpec{
			ID:    "drift-to-resolve",
			Phase: v1alpha1.DriftReportPhaseResolved,
		},
	}
	store.Add(resolved)
	assert.Equal(t, 0, store.Count())

	_, ok := store.Get("drift-to-resolve")
	assert.False(t, ok)
}

func TestStore_List(t *testing.T) {
	store := NewStore()

	reports := []*v1alpha1.DriftReport{
		{
			Spec: v1alpha1.DriftReportSpec{
				ID:    "drift-1",
				Phase: v1alpha1.DriftReportPhaseDetected,
				Parent: v1alpha1.ObjectReference{
					Kind: "Deployment",
					Name: "app-1",
				},
			},
		},
		{
			Spec: v1alpha1.DriftReportSpec{
				ID:    "drift-2",
				Phase: v1alpha1.DriftReportPhaseDetected,
				Parent: v1alpha1.ObjectReference{
					Kind: "StatefulSet",
					Name: "db-1",
				},
			},
		},
		{
			Spec: v1alpha1.DriftReportSpec{
				ID:    "drift-3",
				Phase: v1alpha1.DriftReportPhaseDetected,
				Parent: v1alpha1.ObjectReference{
					Kind: "Deployment",
					Name: "app-2",
				},
			},
		},
	}

	for _, r := range reports {
		store.Add(r)
	}

	assert.Equal(t, 3, store.Count())

	listed := store.List()
	assert.Len(t, listed, 3)

	// Verify all IDs are present
	ids := make(map[string]bool)
	for _, item := range listed {
		ids[item.Report.Spec.ID] = true
	}
	assert.True(t, ids["drift-1"])
	assert.True(t, ids["drift-2"])
	assert.True(t, ids["drift-3"])
}

func TestStore_Remove(t *testing.T) {
	store := NewStore()

	store.Add(&v1alpha1.DriftReport{
		Spec: v1alpha1.DriftReportSpec{
			ID:    "to-remove",
			Phase: v1alpha1.DriftReportPhaseDetected,
		},
	})
	assert.Equal(t, 1, store.Count())

	store.Remove("to-remove")
	assert.Equal(t, 0, store.Count())

	// Remove non-existent - should not panic
	store.Remove("non-existent")
	assert.Equal(t, 0, store.Count())
}

func TestStore_Get_NotFound(t *testing.T) {
	store := NewStore()

	_, ok := store.Get("non-existent")
	assert.False(t, ok)
}

func TestStore_Update_Existing(t *testing.T) {
	store := NewStore()

	// Add initial report
	store.Add(&v1alpha1.DriftReport{
		Spec: v1alpha1.DriftReportSpec{
			ID:    "update-test",
			Phase: v1alpha1.DriftReportPhaseDetected,
			Request: v1alpha1.RequestContext{
				User: "user-1",
			},
		},
	})

	// Update with same ID
	store.Add(&v1alpha1.DriftReport{
		Spec: v1alpha1.DriftReportSpec{
			ID:    "update-test",
			Phase: v1alpha1.DriftReportPhaseDetected,
			Request: v1alpha1.RequestContext{
				User: "user-2",
			},
		},
	})

	assert.Equal(t, 1, store.Count())

	stored, ok := store.Get("update-test")
	require.True(t, ok)
	assert.Equal(t, "user-2", stored.Report.Spec.Request.User)
}
