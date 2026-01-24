// Package v1alpha1 contains API types for drift notification callbacks.
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

const (
	// GroupName is the API group name.
	GroupName = "kausality.io"
	// Version is the API version.
	Version = "v1alpha1"
)

// DriftReportPhase indicates the phase of a drift report.
type DriftReportPhase string

const (
	// DriftReportPhaseDetected indicates drift was detected.
	DriftReportPhaseDetected DriftReportPhase = "Detected"
	// DriftReportPhaseResolved indicates drift was resolved.
	DriftReportPhaseResolved DriftReportPhase = "Resolved"
)

// DriftReport is sent to webhook endpoints when drift is detected.
type DriftReport struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec contains the drift report details.
	Spec DriftReportSpec `json:"spec"`
}

// DriftReportSpec contains the details of a drift report.
type DriftReportSpec struct {
	// ID uniquely identifies this drift occurrence.
	// Format: sha256(parent-ref + child-ref + spec-diff-hash)[:16]
	ID string `json:"id"`

	// Phase indicates whether this is detection or resolution.
	Phase DriftReportPhase `json:"phase"`

	// Parent is the parent object reference.
	Parent ObjectReference `json:"parent"`

	// Child is the child object that drifted.
	Child ObjectReference `json:"child"`

	// OldObject is the previous state (UPDATE only).
	OldObject runtime.RawExtension `json:"oldObject,omitempty"`

	// NewObject is the new state.
	NewObject runtime.RawExtension `json:"newObject,omitempty"`

	// Request contains admission request context.
	Request RequestContext `json:"request"`

	// Detection contains drift detection details.
	Detection DetectionContext `json:"detection"`
}

// ObjectReference identifies a Kubernetes object.
type ObjectReference struct {
	// APIVersion is the API version of the object (e.g., "v1", "apps/v1").
	APIVersion string `json:"apiVersion"`
	// Kind is the kind of the object (e.g., "ConfigMap", "Deployment").
	Kind string `json:"kind"`
	// Namespace is the namespace of the object (empty for cluster-scoped).
	Namespace string `json:"namespace,omitempty"`
	// Name is the name of the object.
	Name string `json:"name"`
	// UID is the unique identifier of the object.
	UID types.UID `json:"uid,omitempty"`
	// Generation is the generation of the object.
	Generation int64 `json:"generation,omitempty"`
}

// RequestContext contains information about the admission request.
type RequestContext struct {
	// User is the username of the requestor.
	User string `json:"user"`
	// Groups are the groups the user belongs to.
	Groups []string `json:"groups,omitempty"`
	// UID is the unique identifier of the request.
	UID string `json:"uid"`
	// FieldManager is the field manager for the request.
	FieldManager string `json:"fieldManager,omitempty"`
	// Operation is the type of operation (CREATE, UPDATE, DELETE).
	Operation string `json:"operation"`
}

// DetectionContext contains details about the drift detection.
type DetectionContext struct {
	// ParentGeneration is the generation of the parent object.
	ParentGeneration int64 `json:"parentGeneration"`
	// ParentObservedGeneration is the observedGeneration from the parent's status.
	ParentObservedGeneration int64 `json:"parentObservedGeneration"`
	// ControllerManager is the manager that owns status.observedGeneration.
	ControllerManager string `json:"controllerManager"`
	// LifecyclePhase is the lifecycle phase of the parent (Initializing, Ready, Deleting).
	LifecyclePhase string `json:"lifecyclePhase"`
}

// DriftReportResponse is the response from a drift report webhook.
type DriftReportResponse struct {
	metav1.TypeMeta `json:",inline"`

	// Acknowledged indicates the webhook received the report.
	Acknowledged bool `json:"acknowledged"`
	// Error is set if the webhook had a problem processing the report.
	Error string `json:"error,omitempty"`
}
