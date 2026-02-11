package admission

import (
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// Audit annotation keys for admission response audit events.
// These appear in the Kubernetes audit log, not on the object.
const (
	auditKeyDecision        = "kausality.io/decision"
	auditKeyDrift           = "kausality.io/drift"
	auditKeyMode            = "kausality.io/mode"
	auditKeyLifecyclePhase  = "kausality.io/lifecycle-phase"
	auditKeyDriftResolution = "kausality.io/drift-resolution"
	auditKeyTrace           = "kausality.io/trace"
)

// withAuditAnnotations sets audit annotations on an admission response.
func withAuditAnnotations(resp admission.Response, audit map[string]string) admission.Response {
	if len(audit) > 0 {
		resp.AuditAnnotations = audit
	}
	return resp
}

// auditDecision returns "allowed-with-warning" if there are warnings, "allowed" otherwise.
func auditDecision(warnings []string) string {
	if len(warnings) > 0 {
		return "allowed-with-warning"
	}
	return "allowed"
}
