package drift

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kausality-io/kausality/pkg/controller"
)

// Detector detects drift by comparing parent generation with observedGeneration.
type Detector struct {
	resolver          *ParentResolver
	lifecycleDetector *LifecycleDetector
}

// NewDetector creates a new Detector.
func NewDetector(c client.Client) *Detector {
	return &Detector{
		resolver:          NewParentResolver(c),
		lifecycleDetector: NewLifecycleDetector(),
	}
}

// DetectorOption configures a Detector.
type DetectorOption func(*Detector)

// WithLifecycleDetector configures a custom lifecycle detector.
func WithLifecycleDetector(ld *LifecycleDetector) DetectorOption {
	return func(d *Detector) {
		d.lifecycleDetector = ld
	}
}

// NewDetectorWithOptions creates a new Detector with options.
func NewDetectorWithOptions(c client.Client, opts ...DetectorOption) *Detector {
	d := NewDetector(c)
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// checkLifecycle handles lifecycle phase detection and early returns.
// Returns (result, done) where done=true means caller should return result immediately.
func (d *Detector) checkLifecycle(parentState *ParentState) (*DriftResult, bool) {
	phase := d.lifecycleDetector.DetectPhase(parentState)

	result := &DriftResult{
		ParentRef:      &parentState.Ref,
		ParentState:    parentState,
		LifecyclePhase: phase,
	}

	switch phase {
	case PhaseDeleting:
		result.Allowed = true
		result.Reason = "parent is being deleted (cleanup phase)"
		return result, true
	case PhaseInitializing:
		result.Allowed = true
		result.Reason = "parent is initializing"
		return result, true
	}

	return result, false
}

// checkGeneration checks generation vs observedGeneration for drift.
// Must be called when request is from the controller.
func checkGeneration(result *DriftResult, parentState *ParentState) *DriftResult {
	if parentState.Generation != parentState.ObservedGeneration {
		result.Allowed = true
		result.DriftDetected = false
		result.Reason = fmt.Sprintf("expected change: parent generation (%d) != observedGeneration (%d)",
			parentState.Generation, parentState.ObservedGeneration)
		return result
	}

	// Controller is updating but parent hasn't changed - drift
	result.Allowed = true // Phase 1: logging only
	result.DriftDetected = true
	result.Reason = fmt.Sprintf("drift detected: parent generation (%d) == observedGeneration (%d)",
		parentState.Generation, parentState.ObservedGeneration)
	return result
}

// Detect checks whether a mutation would be considered drift.
// It uses user hash tracking to identify if the request comes from the controller.
// childUpdaters contains the current updater hashes from the child's annotation (before this update).
func (d *Detector) Detect(ctx context.Context, obj client.Object, username string, childUpdaters []string) (*DriftResult, error) {
	parentState, err := d.resolver.ResolveParent(ctx, obj)
	if err != nil {
		return &DriftResult{Allowed: false, Reason: fmt.Sprintf("failed to resolve parent: %v", err)}, nil
	}
	if parentState == nil {
		return &DriftResult{Allowed: true, Reason: "no controller owner reference"}, nil
	}

	result, done := d.checkLifecycle(parentState)
	if done {
		return result, nil
	}

	isController, canDetermine := IsControllerByHash(parentState, username, childUpdaters)
	if !canDetermine {
		result.Allowed = true
		result.DriftDetected = false
		result.Reason = "cannot determine controller identity (multiple updaters, no parent controllers annotation)"
		return result, nil
	}
	if !isController {
		result.Allowed = true
		result.DriftDetected = false
		result.Reason = fmt.Sprintf("change by different actor (hash %s)", controller.HashUsername(username))
		return result, nil
	}

	return checkGeneration(result, parentState), nil
}

// IsControllerByHash checks if the request comes from the controller using user hash tracking.
// Returns (isController, canDetermine).
func IsControllerByHash(parentState *ParentState, username string, childUpdaters []string) (bool, bool) {
	userHash := controller.HashUsername(username)

	// When parent has controllers annotation, cross-validate
	if len(parentState.Controllers) > 0 {
		if len(childUpdaters) > 0 {
			intersection := controller.Intersect(childUpdaters, parentState.Controllers)
			if len(intersection) > 0 {
				return controller.ContainsHash(intersection, userHash), true
			}

			// No intersection: child updaters are all non-controllers
			return controller.ContainsHash(parentState.Controllers, userHash), true
		}

		// No child updaters (CREATE): check parent controllers directly
		return controller.ContainsHash(parentState.Controllers, userHash), true
	}

	// No parent controllers: fall back to child updaters heuristic
	if len(childUpdaters) == 1 {
		return userHash == childUpdaters[0], true
	}
	if len(childUpdaters) == 0 {
		return true, true
	}

	// Can't determine (multiple updaters, no parent controllers)
	return false, false
}

// ParseUpdaterHashes extracts updater hashes from the child object's annotation.
func ParseUpdaterHashes(obj client.Object) []string {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return nil
	}
	return controller.ParseHashes(annotations[controller.UpdatersAnnotation])
}
