package admission

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func TestHasSpecChanged(t *testing.T) {
	h := &Handler{}

	tests := []struct {
		name        string
		oldObj      map[string]interface{}
		newObj      map[string]interface{}
		wantChanged bool
	}{
		{
			name: "spec unchanged",
			oldObj: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata":   map[string]interface{}{"name": "test"},
				"spec":       map[string]interface{}{"replicas": 3},
			},
			newObj: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata":   map[string]interface{}{"name": "test", "labels": map[string]interface{}{"foo": "bar"}},
				"spec":       map[string]interface{}{"replicas": 3},
			},
			wantChanged: false,
		},
		{
			name: "spec changed",
			oldObj: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata":   map[string]interface{}{"name": "test"},
				"spec":       map[string]interface{}{"replicas": 3},
			},
			newObj: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata":   map[string]interface{}{"name": "test"},
				"spec":       map[string]interface{}{"replicas": 5},
			},
			wantChanged: true,
		},
		{
			name: "status only changed",
			oldObj: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata":   map[string]interface{}{"name": "test"},
				"spec":       map[string]interface{}{"replicas": 3},
				"status":     map[string]interface{}{"ready": false},
			},
			newObj: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata":   map[string]interface{}{"name": "test"},
				"spec":       map[string]interface{}{"replicas": 3},
				"status":     map[string]interface{}{"ready": true},
			},
			wantChanged: false,
		},
		{
			name: "no spec in either",
			oldObj: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata":   map[string]interface{}{"name": "test"},
			},
			newObj: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata":   map[string]interface{}{"name": "test"},
			},
			wantChanged: false,
		},
		{
			name: "spec added",
			oldObj: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata":   map[string]interface{}{"name": "test"},
			},
			newObj: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata":   map[string]interface{}{"name": "test"},
				"spec":       map[string]interface{}{"replicas": 3},
			},
			wantChanged: true,
		},
		{
			name: "spec removed",
			oldObj: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata":   map[string]interface{}{"name": "test"},
				"spec":       map[string]interface{}{"replicas": 3},
			},
			newObj: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata":   map[string]interface{}{"name": "test"},
			},
			wantChanged: true,
		},
		{
			name: "nested spec change",
			oldObj: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"spec": map[string]interface{}{
					"template": map[string]interface{}{
						"containers": []interface{}{
							map[string]interface{}{"image": "nginx:1.0"},
						},
					},
				},
			},
			newObj: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"spec": map[string]interface{}{
					"template": map[string]interface{}{
						"containers": []interface{}{
							map[string]interface{}{"image": "nginx:2.0"},
						},
					},
				},
			},
			wantChanged: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldRaw, _ := json.Marshal(tt.oldObj)
			newRaw, _ := json.Marshal(tt.newObj)

			req := admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					OldObject: runtime.RawExtension{Raw: oldRaw},
					Object:    runtime.RawExtension{Raw: newRaw},
				},
			}

			changed, err := h.hasSpecChanged(req)
			require.NoError(t, err)
			assert.Equal(t, tt.wantChanged, changed)
		})
	}
}
