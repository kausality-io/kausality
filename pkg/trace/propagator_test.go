package trace

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kausality-io/kausality/pkg/controller"
	"github.com/kausality-io/kausality/pkg/drift"
)

func TestPropagator_isOrigin(t *testing.T) {
	// Generate user hashes
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
		{
			name: "gen == obsGen (not reconciling) - origin",
			parentState: &drift.ParentState{
				Generation:         5,
				ObservedGeneration: 5,
			},
			username:      controllerUser,
			childUpdaters: []string{controllerHash},
			wantOrigin:    true,
		},
		{
			name: "gen != obsGen, is controller - hop (extend trace)",
			parentState: &drift.ParentState{
				Generation:         6,
				ObservedGeneration: 5,
			},
			username:      controllerUser,
			childUpdaters: []string{controllerHash},
			wantOrigin:    false,
		},
		{
			name: "gen != obsGen, different actor - origin",
			parentState: &drift.ParentState{
				Generation:         6,
				ObservedGeneration: 5,
			},
			username:      otherUser,
			childUpdaters: []string{controllerHash},
			wantOrigin:    true,
		},
		{
			name: "gen != obsGen, can't determine controller - hop (lenient)",
			parentState: &drift.ParentState{
				Generation:         6,
				ObservedGeneration: 5,
				Controllers:        nil, // no controllers annotation
			},
			username:      otherUser,
			childUpdaters: []string{controllerHash, controller.HashUsername(otherUser)},
			wantOrigin:    false, // can't determine, assume hop
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.isOrigin(tt.parentState, tt.username, tt.childUpdaters)
			assert.Equal(t, tt.wantOrigin, got)
		})
	}
}
