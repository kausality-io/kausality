package policy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	kausalityv1alpha1 "github.com/kausality-io/kausality/api/v1alpha1"
)

func TestFilterExcluded(t *testing.T) {
	tests := []struct {
		name      string
		resources []string
		excluded  []string
		want      []string
	}{
		{
			name:      "no exclusions",
			resources: []string{"deployments", "replicasets", "statefulsets"},
			excluded:  nil,
			want:      []string{"deployments", "replicasets", "statefulsets"},
		},
		{
			name:      "empty exclusions",
			resources: []string{"deployments", "replicasets"},
			excluded:  []string{},
			want:      []string{"deployments", "replicasets"},
		},
		{
			name:      "exclude one",
			resources: []string{"deployments", "replicasets", "statefulsets"},
			excluded:  []string{"replicasets"},
			want:      []string{"deployments", "statefulsets"},
		},
		{
			name:      "exclude multiple",
			resources: []string{"deployments", "replicasets", "statefulsets", "daemonsets"},
			excluded:  []string{"replicasets", "daemonsets"},
			want:      []string{"deployments", "statefulsets"},
		},
		{
			name:      "exclude non-existent",
			resources: []string{"deployments"},
			excluded:  []string{"pods"},
			want:      []string{"deployments"},
		},
		{
			name:      "exclude all",
			resources: []string{"deployments"},
			excluded:  []string{"deployments"},
			want:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterExcluded(tt.resources, tt.excluded)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExpandResources_NoWildcard(t *testing.T) {
	c := &Controller{}

	rule := kausalityv1alpha1.ResourceRule{
		APIGroups: []string{"apps"},
		Resources: []string{"deployments", "statefulsets"},
		Excluded:  []string{},
	}

	got, err := c.expandResources(rule)
	require.NoError(t, err)
	assert.Equal(t, []string{"deployments", "statefulsets"}, got)
}

func TestExpandResources_NoWildcardWithExclusions(t *testing.T) {
	c := &Controller{}

	rule := kausalityv1alpha1.ResourceRule{
		APIGroups: []string{"apps"},
		Resources: []string{"deployments", "replicasets", "statefulsets"},
		Excluded:  []string{"replicasets"},
	}

	got, err := c.expandResources(rule)
	require.NoError(t, err)
	assert.Equal(t, []string{"deployments", "statefulsets"}, got)
}

func TestBuildNamespaceSelector(t *testing.T) {
	tests := []struct {
		name       string
		excluded   []string
		wantNil    bool
		wantValues []string
	}{
		{
			name:     "no exclusions",
			excluded: nil,
			wantNil:  true,
		},
		{
			name:     "empty exclusions",
			excluded: []string{},
			wantNil:  true,
		},
		{
			name:       "with exclusions",
			excluded:   []string{"kube-system", "kube-public"},
			wantNil:    false,
			wantValues: []string{"kube-system", "kube-public"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Controller{ExcludedNamespaces: tt.excluded}
			got := c.buildNamespaceSelector()

			if tt.wantNil {
				assert.Nil(t, got)
				return
			}

			require.NotNil(t, got)
			require.Len(t, got.MatchExpressions, 1)
			assert.Equal(t, "kubernetes.io/metadata.name", got.MatchExpressions[0].Key)
			assert.Equal(t, tt.wantValues, got.MatchExpressions[0].Values)
		})
	}
}
