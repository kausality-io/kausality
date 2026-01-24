// Package admission provides admission handling for drift detection.
package admission

import (
	"context"
	"fmt"
	"net/http"

	"github.com/go-logr/logr"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/kausality-io/kausality/pkg/drift"
)

// Handler handles admission requests for drift detection.
type Handler struct {
	client   client.Client
	decoder  admission.Decoder
	detector *drift.Detector
	log      logr.Logger
}

// Config configures the admission handler.
type Config struct {
	Client client.Client
	Log    logr.Logger
}

// NewHandler creates a new admission Handler.
func NewHandler(cfg Config) *Handler {
	return &Handler{
		client:   cfg.Client,
		detector: drift.NewDetector(cfg.Client),
		log:      cfg.Log.WithName("drift-admission"),
	}
}

// Handle processes an admission request for drift detection.
func (h *Handler) Handle(ctx context.Context, req admission.Request) admission.Response {
	log := h.log.WithValues(
		"operation", req.Operation,
		"kind", req.Kind.String(),
		"namespace", req.Namespace,
		"name", req.Name,
	)

	// Only handle CREATE, UPDATE, DELETE
	if req.Operation != admissionv1.Create &&
		req.Operation != admissionv1.Update &&
		req.Operation != admissionv1.Delete {
		return admission.Allowed("operation not relevant for drift detection")
	}

	// Parse the object from the request
	obj, err := h.parseObject(req)
	if err != nil {
		log.Error(err, "failed to parse object from request")
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("failed to parse object: %w", err))
	}

	// Detect drift
	result, err := h.detector.Detect(ctx, obj)
	if err != nil {
		log.Error(err, "drift detection failed")
		return admission.Errored(http.StatusInternalServerError, fmt.Errorf("drift detection failed: %w", err))
	}

	// Log the result
	logFields := []interface{}{
		"allowed", result.Allowed,
		"reason", result.Reason,
		"driftDetected", result.DriftDetected,
		"lifecyclePhase", result.LifecyclePhase,
	}
	if result.ParentRef != nil {
		logFields = append(logFields,
			"parentKind", result.ParentRef.Kind,
			"parentName", result.ParentRef.Name,
		)
		if result.ParentRef.Namespace != "" {
			logFields = append(logFields, "parentNamespace", result.ParentRef.Namespace)
		}
	}

	if result.DriftDetected {
		log.Info("DRIFT DETECTED - would be blocked in enforcement mode", logFields...)
	} else {
		log.V(1).Info("drift check passed", logFields...)
	}

	// Phase 1: always allow, logging only
	return admission.Allowed(result.Reason)
}

// parseObject parses the object from the admission request.
func (h *Handler) parseObject(req admission.Request) (client.Object, error) {
	var rawObj []byte

	// For DELETE, use OldObject; for CREATE/UPDATE, use Object
	if req.Operation == admissionv1.Delete {
		rawObj = req.OldObject.Raw
	} else {
		rawObj = req.Object.Raw
	}

	if len(rawObj) == 0 {
		return nil, fmt.Errorf("no object data in request")
	}

	// Parse as unstructured
	obj := &unstructured.Unstructured{}
	if err := runtime.DecodeInto(unstructured.UnstructuredJSONScheme, rawObj, obj); err != nil {
		return nil, fmt.Errorf("failed to decode object: %w", err)
	}

	// Set GVK from request
	gvk := schema.GroupVersionKind{
		Group:   req.Kind.Group,
		Version: req.Kind.Version,
		Kind:    req.Kind.Kind,
	}
	obj.SetGroupVersionKind(gvk)

	// Set namespace if not set
	if obj.GetNamespace() == "" && req.Namespace != "" {
		obj.SetNamespace(req.Namespace)
	}

	return obj, nil
}

// InjectDecoder injects the decoder.
func (h *Handler) InjectDecoder(d admission.Decoder) error {
	h.decoder = d
	return nil
}

// ValidatingWebhookFor creates a ValidatingAdmissionResponse for the given result.
func ValidatingWebhookFor(result *drift.DriftResult) admission.Response {
	if result.Allowed {
		return admission.Allowed(result.Reason)
	}

	return admission.Response{
		AdmissionResponse: admissionv1.AdmissionResponse{
			Allowed: false,
			Result: &metav1.Status{
				Code:    http.StatusForbidden,
				Message: result.Reason,
				Reason:  metav1.StatusReasonForbidden,
			},
		},
	}
}
