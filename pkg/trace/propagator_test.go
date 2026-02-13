package trace

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kausality-io/kausality/pkg/controller"
	"github.com/kausality-io/kausality/pkg/drift"
)

func TestPropagator_isOrigin(t *testing.T) {
	controllerUser := "system:serviceaccount:kube-system:deployment-controller"
	otherUser := "admin@example.com"
	controllerHash := controller.HashUsername(controllerUser)

	p := &Propagator{} // client not needed for isOrigin

	tests := []struct {
		name          string
		parentState   *drift.ParentState
		username      string
		childUpdaters []string
		wantOrigin    bool
	}{
		{
			name:        "nil parent - origin",
			parentState: nil,
			username:    controllerUser,
			wantOrigin:  true,
		},

		// === Has observedGeneration ===
		{
			name: "has obsGen + stable + is controller - origin",
			parentState: &drift.ParentState{
				HasObservedGeneration: true,
				Generation:            5,
				ObservedGeneration:    5,
				Controllers:           []string{controllerHash},
			},
			username:      controllerUser,
			childUpdaters: []string{controllerHash},
			wantOrigin:    true,
		},
		{
			name: "has obsGen + reconciling + is controller - extend",
			parentState: &drift.ParentState{
				HasObservedGeneration: true,
				Generation:            6,
				ObservedGeneration:    5,
				Controllers:           []string{controllerHash},
			},
			username:      controllerUser,
			childUpdaters: []string{controllerHash},
			wantOrigin:    false,
		},
		{
			name: "has obsGen + reconciling + different actor with parent controllers - origin",
			parentState: &drift.ParentState{
				HasObservedGeneration: true,
				Generation:            6,
				ObservedGeneration:    5,
				Controllers:           []string{controllerHash},
			},
			username:      otherUser,
			childUpdaters: []string{controllerHash, controller.HashUsername(otherUser)},
			wantOrigin:    true,
		},
		{
			name: "has obsGen + reconciling + can't determine controller - extend (lenient)",
			parentState: &drift.ParentState{
				HasObservedGeneration: true,
				Generation:            6,
				ObservedGeneration:    5,
				Controllers:           nil,
			},
			username:      otherUser,
			childUpdaters: []string{controllerHash, controller.HashUsername(otherUser)},
			wantOrigin:    false,
		},

		// === No observedGeneration ===
		{
			name: "no obsGen + user is NOT controller (cross-validated) - origin",
			parentState: &drift.ParentState{
				Generation:  1,
				Controllers: []string{controllerHash},
			},
			username:      otherUser,
			childUpdaters: []string{controller.HashUsername(otherUser)},
			wantOrigin:    true,
		},
		{
			name: "no obsGen + user IS controller (confirmed) - extend",
			parentState: &drift.ParentState{
				Generation:  1,
				Controllers: []string{controllerHash},
			},
			username:      controllerUser,
			childUpdaters: []string{controllerHash},
			wantOrigin:    false,
		},
		{
			name: "no obsGen + can't determine controller - origin (safer default)",
			parentState: &drift.ParentState{
				Generation:  1,
				Controllers: nil,
			},
			username:      otherUser,
			childUpdaters: []string{controllerHash, controller.HashUsername(otherUser)},
			wantOrigin:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.isOrigin(tt.parentState, tt.username, tt.childUpdaters)
			assert.Equal(t, tt.wantOrigin, got)
		})
	}
}
