package drift

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestFindControllerOwnerRef(t *testing.T) {
	trueVal := true
	falseVal := false

	tests := []struct {
		name     string
		refs     []metav1.OwnerReference
		wantName string
		wantNil  bool
	}{
		{
			name:    "no owner refs",
			refs:    nil,
			wantNil: true,
		},
		{
			name: "owner ref without controller",
			refs: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "test",
				},
			},
			wantNil: true,
		},
		{
			name: "owner ref with controller=false",
			refs: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "test",
					Controller: &falseVal,
				},
			},
			wantNil: true,
		},
		{
			name: "owner ref with controller=true",
			refs: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "controller-owner",
					Controller: &trueVal,
				},
			},
			wantName: "controller-owner",
		},
		{
			name: "multiple refs - picks controller",
			refs: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "ReplicaSet",
					Name:       "non-controller",
					Controller: &falseVal,
				},
				{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "controller-owner",
					Controller: &trueVal,
				},
			},
			wantName: "controller-owner",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findControllerOwnerRef(tt.refs)
			if tt.wantNil {
				if got != nil {
					t.Errorf("findControllerOwnerRef() = %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("findControllerOwnerRef() = nil, want non-nil")
			}
			if got.Name != tt.wantName {
				t.Errorf("findControllerOwnerRef().Name = %q, want %q", got.Name, tt.wantName)
			}
		})
	}
}

func TestExtractParentState(t *testing.T) {
	trueVal := true
	ownerRef := metav1.OwnerReference{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       "parent-deploy",
		Controller: &trueVal,
	}

	tests := []struct {
		name      string
		parent    *unstructured.Unstructured
		wantGen   int64
		wantObsG  int64
		wantHasOG bool
		wantDel   bool
		wantInit  bool
		wantConds int
	}{
		{
			name: "minimal parent",
			parent: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "apps/v1",
					"kind":       "Deployment",
					"metadata": map[string]interface{}{
						"name":       "parent-deploy",
						"namespace":  "default",
						"generation": int64(5),
					},
				},
			},
			wantGen:   5,
			wantHasOG: false,
		},
		{
			name: "parent with observedGeneration",
			parent: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "apps/v1",
					"kind":       "Deployment",
					"metadata": map[string]interface{}{
						"name":       "parent-deploy",
						"namespace":  "default",
						"generation": int64(10),
					},
					"status": map[string]interface{}{
						"observedGeneration": int64(9),
					},
				},
			},
			wantGen:   10,
			wantObsG:  9,
			wantHasOG: true,
		},
		{
			name: "parent with conditions",
			parent: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "apps/v1",
					"kind":       "Deployment",
					"metadata": map[string]interface{}{
						"name":       "parent-deploy",
						"namespace":  "default",
						"generation": int64(3),
					},
					"status": map[string]interface{}{
						"observedGeneration": int64(3),
						"conditions": []interface{}{
							map[string]interface{}{
								"type":   "Ready",
								"status": "True",
							},
							map[string]interface{}{
								"type":   "Progressing",
								"status": "True",
							},
						},
					},
				},
			},
			wantGen:   3,
			wantObsG:  3,
			wantHasOG: true,
			wantConds: 2,
		},
		{
			name: "parent with initialized annotation",
			parent: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "apps/v1",
					"kind":       "Deployment",
					"metadata": map[string]interface{}{
						"name":       "parent-deploy",
						"namespace":  "default",
						"generation": int64(1),
						"annotations": map[string]interface{}{
							"kausality.io/initialized": "true",
						},
					},
				},
			},
			wantGen:  1,
			wantInit: true,
		},
		{
			name: "parent being deleted",
			parent: func() *unstructured.Unstructured {
				u := &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "apps/v1",
						"kind":       "Deployment",
						"metadata": map[string]interface{}{
							"name":              "parent-deploy",
							"namespace":         "default",
							"generation":        int64(5),
							"deletionTimestamp": time.Now().Format(time.RFC3339),
						},
					},
				}
				return u
			}(),
			wantGen: 5,
			wantDel: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := extractParentState(tt.parent, ownerRef)

			if state.Generation != tt.wantGen {
				t.Errorf("Generation = %d, want %d", state.Generation, tt.wantGen)
			}
			if state.ObservedGeneration != tt.wantObsG {
				t.Errorf("ObservedGeneration = %d, want %d", state.ObservedGeneration, tt.wantObsG)
			}
			if state.HasObservedGeneration != tt.wantHasOG {
				t.Errorf("HasObservedGeneration = %v, want %v", state.HasObservedGeneration, tt.wantHasOG)
			}
			if (state.DeletionTimestamp != nil) != tt.wantDel {
				t.Errorf("DeletionTimestamp set = %v, want %v", state.DeletionTimestamp != nil, tt.wantDel)
			}
			if state.IsInitialized != tt.wantInit {
				t.Errorf("IsInitialized = %v, want %v", state.IsInitialized, tt.wantInit)
			}
			if len(state.Conditions) != tt.wantConds {
				t.Errorf("len(Conditions) = %d, want %d", len(state.Conditions), tt.wantConds)
			}
		})
	}
}

func TestExtractConditions(t *testing.T) {
	tests := []struct {
		name      string
		status    map[string]interface{}
		wantCount int
		wantTypes []string
	}{
		{
			name:      "no conditions",
			status:    map[string]interface{}{},
			wantCount: 0,
		},
		{
			name: "empty conditions",
			status: map[string]interface{}{
				"conditions": []interface{}{},
			},
			wantCount: 0,
		},
		{
			name: "single condition",
			status: map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{
						"type":    "Ready",
						"status":  "True",
						"reason":  "AllGood",
						"message": "Everything is ready",
					},
				},
			},
			wantCount: 1,
			wantTypes: []string{"Ready"},
		},
		{
			name: "multiple conditions",
			status: map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{
						"type":   "Ready",
						"status": "True",
					},
					map[string]interface{}{
						"type":   "Initialized",
						"status": "True",
					},
					map[string]interface{}{
						"type":   "Available",
						"status": "False",
					},
				},
			},
			wantCount: 3,
			wantTypes: []string{"Ready", "Initialized", "Available"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conditions := extractConditions(tt.status)
			if len(conditions) != tt.wantCount {
				t.Errorf("len(conditions) = %d, want %d", len(conditions), tt.wantCount)
			}
			for i, wantType := range tt.wantTypes {
				if i >= len(conditions) {
					break
				}
				if conditions[i].Type != wantType {
					t.Errorf("conditions[%d].Type = %q, want %q", i, conditions[i].Type, wantType)
				}
			}
		})
	}
}

func TestParentRefFromOwnerRef(t *testing.T) {
	trueVal := true
	ref := metav1.OwnerReference{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       "test-deploy",
		Controller: &trueVal,
	}

	parentRef := ParentRefFromOwnerRef(ref, "my-namespace")

	if parentRef.APIVersion != "apps/v1" {
		t.Errorf("APIVersion = %q, want %q", parentRef.APIVersion, "apps/v1")
	}
	if parentRef.Kind != "Deployment" {
		t.Errorf("Kind = %q, want %q", parentRef.Kind, "Deployment")
	}
	if parentRef.Name != "test-deploy" {
		t.Errorf("Name = %q, want %q", parentRef.Name, "test-deploy")
	}
	if parentRef.Namespace != "my-namespace" {
		t.Errorf("Namespace = %q, want %q", parentRef.Namespace, "my-namespace")
	}
}

func TestFindControllerManager(t *testing.T) {
	tests := []struct {
		name          string
		managedFields []metav1.ManagedFieldsEntry
		wantManager   string
	}{
		{
			name:          "no managed fields",
			managedFields: nil,
			wantManager:   "",
		},
		{
			name: "manager owns status.observedGeneration",
			managedFields: []metav1.ManagedFieldsEntry{
				{
					Manager:     "kube-controller-manager",
					Operation:   metav1.ManagedFieldsOperationUpdate,
					Subresource: "status",
					FieldsV1: &metav1.FieldsV1{
						Raw: []byte(`{"f:status":{"f:observedGeneration":{}}}`),
					},
				},
			},
			wantManager: "kube-controller-manager",
		},
		{
			name: "multiple managers - one owns observedGeneration",
			managedFields: []metav1.ManagedFieldsEntry{
				{
					Manager:   "kubectl",
					Operation: metav1.ManagedFieldsOperationApply,
					FieldsV1: &metav1.FieldsV1{
						Raw: []byte(`{"f:spec":{"f:replicas":{}}}`),
					},
				},
				{
					Manager:     "capi-controller",
					Operation:   metav1.ManagedFieldsOperationUpdate,
					Subresource: "status",
					FieldsV1: &metav1.FieldsV1{
						Raw: []byte(`{"f:status":{"f:observedGeneration":{},"f:conditions":{}}}`),
					},
				},
			},
			wantManager: "capi-controller",
		},
		{
			name: "manager owns status but not observedGeneration",
			managedFields: []metav1.ManagedFieldsEntry{
				{
					Manager:     "some-controller",
					Operation:   metav1.ManagedFieldsOperationUpdate,
					Subresource: "status",
					FieldsV1: &metav1.FieldsV1{
						Raw: []byte(`{"f:status":{"f:conditions":{}}}`),
					},
				},
			},
			wantManager: "",
		},
		{
			name: "empty fieldsV1",
			managedFields: []metav1.ManagedFieldsEntry{
				{
					Manager:     "controller",
					Operation:   metav1.ManagedFieldsOperationUpdate,
					Subresource: "status",
					FieldsV1:    nil,
				},
			},
			wantManager: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findControllerManager(tt.managedFields)
			if got != tt.wantManager {
				t.Errorf("findControllerManager() = %q, want %q", got, tt.wantManager)
			}
		})
	}
}
