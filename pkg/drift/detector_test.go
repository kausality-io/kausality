package drift

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDetectFromState(t *testing.T) {
	detector := &Detector{
		lifecycleDetector: NewLifecycleDetector(),
	}

	tests := []struct {
		name              string
		state             *ParentState
		expectAllowed     bool
		expectDrift       bool
		expectPhase       LifecyclePhase
		expectReasonMatch string
	}{
		{
			name:          "nil state - allowed",
			state:         nil,
			expectAllowed: true,
			expectDrift:   false,
		},
		{
			name: "deleting - allowed without drift check",
			state: &ParentState{
				Ref:                   ParentRef{Kind: "Deployment", Name: "test"},
				Generation:            5,
				ObservedGeneration:    5,
				HasObservedGeneration: true,
				DeletionTimestamp:     &metav1.Time{Time: time.Now()},
			},
			expectAllowed: true,
			expectDrift:   false,
			expectPhase:   PhaseDeleting,
		},
		{
			name: "initializing (no observedGeneration) - allowed",
			state: &ParentState{
				Ref:                   ParentRef{Kind: "Deployment", Name: "test"},
				Generation:            1,
				HasObservedGeneration: false,
			},
			expectAllowed: true,
			expectDrift:   false,
			expectPhase:   PhaseInitializing,
		},
		{
			name: "initializing (has Initialized condition) - allowed as ready",
			state: &ParentState{
				Ref:                   ParentRef{Kind: "Deployment", Name: "test"},
				Generation:            1,
				HasObservedGeneration: false,
				Conditions: []metav1.Condition{
					{Type: "Initialized", Status: metav1.ConditionTrue},
				},
			},
			expectAllowed: true,
			expectDrift:   false,
			expectPhase:   PhaseReady,
		},
		{
			name: "expected change - gen != obsGen",
			state: &ParentState{
				Ref:                   ParentRef{Kind: "Deployment", Name: "test"},
				Generation:            5,
				ObservedGeneration:    4,
				HasObservedGeneration: true,
			},
			expectAllowed:     true,
			expectDrift:       false,
			expectPhase:       PhaseReady,
			expectReasonMatch: "expected change",
		},
		{
			name: "drift detected - gen == obsGen",
			state: &ParentState{
				Ref:                   ParentRef{Kind: "Deployment", Name: "test"},
				Generation:            5,
				ObservedGeneration:    5,
				HasObservedGeneration: true,
			},
			expectAllowed:     true, // Phase 1: always allow
			expectDrift:       true,
			expectPhase:       PhaseReady,
			expectReasonMatch: "drift detected",
		},
		{
			name: "marked initialized via annotation - drift check applies",
			state: &ParentState{
				Ref:                   ParentRef{Kind: "Deployment", Name: "test"},
				Generation:            5,
				ObservedGeneration:    5,
				HasObservedGeneration: true,
				IsInitialized:         true,
			},
			expectAllowed: true,
			expectDrift:   true,
			expectPhase:   PhaseReady,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.DetectFromState(tt.state)

			if result.Allowed != tt.expectAllowed {
				t.Errorf("Allowed = %v, want %v", result.Allowed, tt.expectAllowed)
			}

			if result.DriftDetected != tt.expectDrift {
				t.Errorf("DriftDetected = %v, want %v", result.DriftDetected, tt.expectDrift)
			}

			if tt.state != nil && result.LifecyclePhase != tt.expectPhase {
				t.Errorf("LifecyclePhase = %v, want %v", result.LifecyclePhase, tt.expectPhase)
			}

			if tt.expectReasonMatch != "" && !containsString(result.Reason, tt.expectReasonMatch) {
				t.Errorf("Reason = %q, want to contain %q", result.Reason, tt.expectReasonMatch)
			}
		})
	}
}

func TestDetectFromStateWithFieldManager(t *testing.T) {
	detector := &Detector{
		lifecycleDetector: NewLifecycleDetector(),
	}

	tests := []struct {
		name              string
		state             *ParentState
		fieldManager      string
		expectDrift       bool
		expectReasonMatch string
	}{
		{
			name: "controller request - gen != obsGen - no drift",
			state: &ParentState{
				Ref:                   ParentRef{Kind: "Deployment", Name: "test"},
				Generation:            5,
				ObservedGeneration:    4,
				HasObservedGeneration: true,
				ControllerManager:     "my-controller",
			},
			fieldManager:      "my-controller",
			expectDrift:       false,
			expectReasonMatch: "expected change",
		},
		{
			name: "controller request - gen == obsGen - drift",
			state: &ParentState{
				Ref:                   ParentRef{Kind: "Deployment", Name: "test"},
				Generation:            5,
				ObservedGeneration:    5,
				HasObservedGeneration: true,
				ControllerManager:     "my-controller",
			},
			fieldManager:      "my-controller",
			expectDrift:       true,
			expectReasonMatch: "drift detected: parent generation",
		},
		{
			name: "different actor - drift (regardless of gen/obsGen)",
			state: &ParentState{
				Ref:                   ParentRef{Kind: "Deployment", Name: "test"},
				Generation:            5,
				ObservedGeneration:    4, // Parent is reconciling
				HasObservedGeneration: true,
				ControllerManager:     "my-controller",
			},
			fieldManager:      "other-actor",
			expectDrift:       true,
			expectReasonMatch: "request from",
		},
		{
			name: "unknown controller - fallback assumes controller",
			state: &ParentState{
				Ref:                   ParentRef{Kind: "Deployment", Name: "test"},
				Generation:            5,
				ObservedGeneration:    4,
				HasObservedGeneration: true,
				ControllerManager:     "", // Unknown
			},
			fieldManager:      "any-manager",
			expectDrift:       false,
			expectReasonMatch: "expected change",
		},
		{
			name: "empty fieldManager - fallback assumes controller",
			state: &ParentState{
				Ref:                   ParentRef{Kind: "Deployment", Name: "test"},
				Generation:            5,
				ObservedGeneration:    4,
				HasObservedGeneration: true,
				ControllerManager:     "my-controller",
			},
			fieldManager:      "",
			expectDrift:       false,
			expectReasonMatch: "expected change",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.DetectFromStateWithFieldManager(tt.state, tt.fieldManager)

			if result.DriftDetected != tt.expectDrift {
				t.Errorf("DriftDetected = %v, want %v (reason: %s)", result.DriftDetected, tt.expectDrift, result.Reason)
			}

			if tt.expectReasonMatch != "" && !containsString(result.Reason, tt.expectReasonMatch) {
				t.Errorf("Reason = %q, want to contain %q", result.Reason, tt.expectReasonMatch)
			}
		})
	}
}

func TestLifecycleDetector_DetectPhase(t *testing.T) {
	detector := NewLifecycleDetector()

	tests := []struct {
		name   string
		state  *ParentState
		expect LifecyclePhase
	}{
		{
			name:   "nil state - ready",
			state:  nil,
			expect: PhaseReady,
		},
		{
			name: "deletionTimestamp set - deleting",
			state: &ParentState{
				DeletionTimestamp: &metav1.Time{Time: time.Now()},
			},
			expect: PhaseDeleting,
		},
		{
			name: "deletion takes precedence over initialized",
			state: &ParentState{
				DeletionTimestamp:     &metav1.Time{Time: time.Now()},
				HasObservedGeneration: true,
				IsInitialized:         true,
			},
			expect: PhaseDeleting,
		},
		{
			name: "annotation initialized - ready",
			state: &ParentState{
				IsInitialized: true,
			},
			expect: PhaseReady,
		},
		{
			name: "Initialized condition true - ready",
			state: &ParentState{
				Conditions: []metav1.Condition{
					{Type: "Initialized", Status: metav1.ConditionTrue},
				},
			},
			expect: PhaseReady,
		},
		{
			name: "Ready condition true - ready",
			state: &ParentState{
				Conditions: []metav1.Condition{
					{Type: "Ready", Status: metav1.ConditionTrue},
				},
			},
			expect: PhaseReady,
		},
		{
			name: "observedGeneration exists - ready",
			state: &ParentState{
				HasObservedGeneration: true,
				ObservedGeneration:    1,
			},
			expect: PhaseReady,
		},
		{
			name: "no initialization signals - initializing",
			state: &ParentState{
				Generation: 1,
			},
			expect: PhaseInitializing,
		},
		{
			name: "Ready=False does not count as initialized",
			state: &ParentState{
				Conditions: []metav1.Condition{
					{Type: "Ready", Status: metav1.ConditionFalse},
				},
			},
			expect: PhaseInitializing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			phase := detector.DetectPhase(tt.state)
			if phase != tt.expect {
				t.Errorf("DetectPhase() = %v, want %v", phase, tt.expect)
			}
		})
	}
}

func TestParentRef_String(t *testing.T) {
	tests := []struct {
		name   string
		ref    ParentRef
		expect string
	}{
		{
			name: "cluster-scoped",
			ref: ParentRef{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       "test",
			},
			expect: "apps/v1/Deployment:test",
		},
		{
			name: "namespaced",
			ref: ParentRef{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Namespace:  "default",
				Name:       "test",
			},
			expect: "apps/v1/Deployment:default/test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ref.String(); got != tt.expect {
				t.Errorf("String() = %q, want %q", got, tt.expect)
			}
		})
	}
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
