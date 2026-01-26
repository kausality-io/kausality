package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:validation:Enum=log;enforce
type Mode string

const (
	// ModeLog logs drift but does not block requests.
	ModeLog Mode = "log"

	// ModeEnforce blocks requests that would cause drift.
	ModeEnforce Mode = "enforce"
)

// ResourceRule defines which resources to track within specific API groups.
//
// +kubebuilder:validation:XValidation:rule="self.apiGroups.all(g, g != '*')",message="apiGroups cannot contain '*', use explicit group names"
// +kubebuilder:validation:XValidation:rule="!has(self.excluded) || size(self.excluded) == 0 || self.resources.exists(r, r == '*')",message="excluded can only be used when resources contains '*'"
type ResourceRule struct {
	// APIGroups is the list of API groups. Required, no "*" allowed.
	// Use "" for the core API group.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=10
	APIGroups []string `json:"apiGroups"`

	// Resources is the list of resources. Use "*" to match all resources in the group.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=50
	Resources []string `json:"resources"`

	// Excluded subtracts resources from a wildcard resources list.
	// Only applies when Resources contains "*".
	// +optional
	// +kubebuilder:validation:MaxItems=50
	Excluded []string `json:"excluded,omitempty"`
}

// NamespaceSelector defines which namespaces to track.
//
// +kubebuilder:validation:XValidation:rule="!(size(self.names) > 0 && has(self.selector))",message="names and selector are mutually exclusive"
type NamespaceSelector struct {
	// Names is an explicit list of namespace names to include.
	// +optional
	// +kubebuilder:validation:MaxItems=100
	Names []string `json:"names,omitempty"`

	// Selector matches namespaces by labels.
	// +optional
	Selector *metav1.LabelSelector `json:"selector,omitempty"`

	// Excluded namespaces are always skipped, even if they match names or selector.
	// +optional
	// +kubebuilder:validation:MaxItems=100
	Excluded []string `json:"excluded,omitempty"`
}

// ModeOverride allows fine-grained mode configuration for specific resources or namespaces.
// Overrides are evaluated in order; first match wins.
//
// +kubebuilder:validation:XValidation:rule="size(self.apiGroups) > 0 || size(self.resources) > 0 || size(self.namespaces) > 0",message="override must have at least one filter (apiGroups, resources, or namespaces)"
type ModeOverride struct {
	// APIGroups limits this override to specific API groups.
	// +optional
	// +kubebuilder:validation:MaxItems=10
	APIGroups []string `json:"apiGroups,omitempty"`

	// Resources limits this override to specific resources.
	// +optional
	// +kubebuilder:validation:MaxItems=50
	Resources []string `json:"resources,omitempty"`

	// Namespaces limits this override to specific namespaces.
	// +optional
	// +kubebuilder:validation:MaxItems=100
	Namespaces []string `json:"namespaces,omitempty"`

	// Mode is the drift detection mode for matching resources.
	Mode Mode `json:"mode"`
}

// KausalitySpec defines the desired state of a Kausality policy.
type KausalitySpec struct {
	// Resources defines which resources to track.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=20
	Resources []ResourceRule `json:"resources"`

	// Namespaces defines which namespaces to track.
	// If omitted, all namespaces are tracked (except system namespaces).
	// +optional
	Namespaces *NamespaceSelector `json:"namespaces,omitempty"`

	// ObjectSelector filters objects by labels.
	// Only objects matching this selector are tracked.
	// +optional
	ObjectSelector *metav1.LabelSelector `json:"objectSelector,omitempty"`

	// Mode is the default drift detection mode for resources matched by this policy.
	Mode Mode `json:"mode"`

	// Overrides allows fine-grained mode configuration by namespace or resource.
	// Overrides are evaluated in order; first match wins.
	// +optional
	// +kubebuilder:validation:MaxItems=50
	Overrides []ModeOverride `json:"overrides,omitempty"`
}

// KausalityStatus defines the observed state of a Kausality policy.
type KausalityStatus struct {
	// Conditions represent the current state of the policy.
	// Known condition types: Ready, WebhookConfigured.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Kausality configures drift detection for a set of Kubernetes resources.
//
// Multiple Kausality instances can coexist. When multiple policies match
// the same resource, specificity-based precedence resolves conflicts:
// more specific namespace selectors and resource lists win over broader ones.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=`.spec.mode`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type Kausality struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   KausalitySpec   `json:"spec,omitempty"`
	Status KausalityStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// KausalityList contains a list of Kausality resources.
type KausalityList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Kausality `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Kausality{}, &KausalityList{})
}
