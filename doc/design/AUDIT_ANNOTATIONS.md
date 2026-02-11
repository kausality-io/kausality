# Audit Annotations

## Overview

Kausality returns metadata in `AdmissionResponse.AuditAnnotations` on every webhook response. These annotations appear in the Kubernetes audit log, not on the object. This provides an independent record of kausality's decisions that survives object deletion and doesn't add to object size.

## Annotations

| Key | Values | When Set |
|-----|--------|----------|
| `kausality.io/decision` | `allowed`, `denied`, `allowed-with-warning` | Always |
| `kausality.io/drift` | `true`, `false` | After drift detection runs |
| `kausality.io/mode` | `log`, `enforce` | After mode resolution |
| `kausality.io/lifecycle-phase` | `Initializing`, `Initialized`, `Deleting` | When lifecycle phase is determined |
| `kausality.io/drift-resolution` | `approved`, `rejected`, `unresolved` | When drift is detected |
| `kausality.io/trace` | JSON array of Hop objects | After trace propagation |

### Decision

The `decision` annotation captures the webhook's actual response:

- **`allowed`** — mutation permitted, no drift concerns
- **`denied`** — mutation blocked (enforce mode drift, freeze, or rejection)
- **`allowed-with-warning`** — drift detected in log mode; allowed with a warning header

### Drift

Simple boolean: was drift detected for this mutation? This is the primary signal for audit log queries.

### Mode

Which mode applied to this resource. Determined by precedence: object annotation > namespace annotation > CRD policy > config default.

### Lifecycle Phase

The parent resource's lifecycle phase at decision time:

- **`Initializing`** — resource is still setting up, all changes allowed
- **`Initialized`** — drift detection active
- **`Deleting`** — resource is being deleted, all changes allowed

Only set when the phase is determined (i.e., a parent exists).

### Drift Resolution

How detected drift was handled:

- **`approved`** — matched an approval on the parent
- **`rejected`** — matched a rejection on the parent
- **`unresolved`** — no matching approval or rejection found

Only set when `drift=true`.

### Trace

The full causal trace as a JSON array. Same format as the `kausality.io/trace` object annotation (see [TRACING.md](TRACING.md)).

The trace in audit annotations is especially valuable for:
- **DELETE operations** — the object is gone, but the audit log preserves the trace
- **Denied mutations** — the object wasn't modified, so there's no object annotation, but the audit log records what the trace would have been
- **Post-incident analysis** — reconstruct causal chains from audit events without needing object access

## Paths Without Audit Annotations

The following response paths do not include audit annotations because no meaningful kausality decision was made:

- Operations other than CREATE/UPDATE/DELETE
- Status subresource updates (controller identity recording)
- Updates with no spec change (annotation preservation only)
- Internal errors (parse failures, drift detection errors)

## Example Audit Event

```json
{
  "kind": "Event",
  "apiVersion": "audit.k8s.io/v1",
  "level": "Metadata",
  "verb": "update",
  "objectRef": {
    "resource": "replicasets",
    "namespace": "default",
    "name": "nginx-abc123",
    "apiGroup": "apps",
    "apiVersion": "v1"
  },
  "annotations": {
    "kausality.io/decision": "allowed-with-warning",
    "kausality.io/drift": "true",
    "kausality.io/mode": "log",
    "kausality.io/lifecycle-phase": "Initialized",
    "kausality.io/drift-resolution": "unresolved",
    "kausality.io/trace": "[{\"apiVersion\":\"apps/v1\",\"kind\":\"Deployment\",\"name\":\"nginx\",\"generation\":3,\"user\":\"admin\",\"timestamp\":\"2026-01-24T10:30:00Z\"},{\"apiVersion\":\"apps/v1\",\"kind\":\"ReplicaSet\",\"name\":\"nginx-abc123\",\"generation\":5,\"user\":\"system:serviceaccount:kube-system:deployment-controller\",\"timestamp\":\"2026-01-24T10:30:05Z\"}]"
  }
}
```
