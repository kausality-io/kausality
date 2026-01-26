# Kausality CRD Design

This document describes the `Kausality` custom resource for configuring drift detection policies.

## Overview

The `Kausality` CRD provides dynamic control over which Kubernetes resources are tracked and how drift is handled. It replaces static configuration (ConfigMaps, Helm values) with a declarative, cluster-scoped resource.

## Key Features

- **Full dynamic control** — CRD controls both resource selection AND protection mode
- **Cluster-scoped** — Single source of truth per policy
- **Multiple instances** — Teams can own their own policies
- **Specificity-based precedence** — More specific policies win over general ones
- **Fine-grained modes** — Default mode with namespace/resource-specific overrides

## Example

```yaml
apiVersion: kausality.io/v1alpha1
kind: Kausality
metadata:
  name: apps-policy
spec:
  # What to track
  resources:
    - apiGroups: ["apps"]
      resources: ["*"]
      excluded: ["replicasets"]
    - apiGroups: ["batch"]
      resources: ["jobs", "cronjobs"]

  # Where (omit for all namespaces)
  namespaces:
    names: ["production", "staging"]
    excluded: ["kube-system"]

  # Optional object filter
  objectSelector:
    matchLabels:
      protected: "true"

  # Default mode
  mode: log

  # Fine-grained overrides
  overrides:
    - namespaces: ["production"]
      mode: enforce
    - apiGroups: ["apps"]
      resources: ["statefulsets"]
      mode: enforce
```

## Spec Fields

### resources (required)

Defines which resources to track. Each rule specifies:

| Field | Description |
|-------|-------------|
| `apiGroups` | API groups to match. Required, no `"*"` allowed. Use `""` for core. |
| `resources` | Resources to match. Use `"*"` for all resources in the group. |
| `excluded` | Resources to exclude from a wildcard match. |

```yaml
resources:
  - apiGroups: ["apps"]
    resources: ["*"]           # all apps resources
    excluded: ["replicasets"]  # except ReplicaSets
  - apiGroups: [""]
    resources: ["configmaps", "secrets"]
```

### namespaces (optional)

Defines which namespaces to track. If omitted, all namespaces are tracked.

| Field | Description |
|-------|-------------|
| `names` | Explicit list of namespace names. |
| `selector` | Label selector for namespaces. |
| `excluded` | Namespaces to always skip. |

```yaml
namespaces:
  selector:
    matchLabels:
      env: production
  excluded: ["kube-system", "kube-public"]
```

### objectSelector (optional)

Filters objects by labels. Only objects matching this selector are tracked.

```yaml
objectSelector:
  matchLabels:
    protected: "true"
```

### mode (required)

Default drift detection mode:

| Mode | Behavior |
|------|----------|
| `log` | Detect and log drift, but allow the request |
| `enforce` | Detect drift and reject the request |

### overrides (optional)

Fine-grained mode overrides. Evaluated in order; first match wins.

| Field | Description |
|-------|-------------|
| `apiGroups` | Limit to specific API groups |
| `resources` | Limit to specific resources |
| `namespaces` | Limit to specific namespaces |
| `mode` | Mode to apply when matched |

More specific overrides should be listed first:

```yaml
overrides:
  # Most specific: resource + namespace
  - apiGroups: ["apps"]
    resources: ["statefulsets"]
    namespaces: ["production"]
    mode: enforce

  # Less specific: namespace only
  - namespaces: ["production"]
    mode: enforce

  # Least specific: resource only
  - apiGroups: ["batch"]
    resources: ["jobs"]
    mode: log
```

## Precedence Rules

### Between Kausality Instances

When multiple `Kausality` instances match the same resource, the most specific wins:

| Dimension | More Specific | Less Specific |
|-----------|---------------|---------------|
| Namespace | `names: [x]` | `selector: {...}` | `(omitted = all)` |
| Resource | `resources: [x]` | `resources: ["*"]` |

Tie-breaker: alphabetical by name.

**Example:**

```yaml
# Broad baseline (less specific)
apiVersion: kausality.io/v1alpha1
kind: Kausality
metadata:
  name: platform-baseline
spec:
  resources:
    - apiGroups: ["apps"]
      resources: ["*"]
  mode: log
---
# Team override (more specific - wins)
apiVersion: kausality.io/v1alpha1
kind: Kausality
metadata:
  name: team-payments-prod
spec:
  resources:
    - apiGroups: ["apps"]
      resources: ["deployments"]  # specific resource
  namespaces:
    names: ["payments-prod"]       # specific namespace
  mode: enforce
```

Result: Deployments in `payments-prod` use `enforce` (team policy wins).

### Within a Kausality Instance

Overrides are evaluated in order; first match wins. Evaluation order:

1. Override matching both namespace + resource
2. Override matching namespace only
3. Override matching resource only
4. Default mode

## Status

The status reports the policy's current state:

```yaml
status:
  conditions:
    - type: Ready
      status: "True"
      reason: AllResourcesDiscovered
      message: "All 5 resources discovered and configured"
    - type: WebhookConfigured
      status: "True"
      reason: RulesApplied
      message: "Webhook rules updated"
```

### Condition Types

| Type | Description |
|------|-------------|
| `Ready` | Policy is fully operational |
| `WebhookConfigured` | Webhook configuration has been updated |

## Controller Behavior (Future)

The Kausality controller (not yet implemented) will:

1. Watch `Kausality` resources
2. Expand `resources: ["*"]` via discovery API
3. Reconcile `MutatingWebhookConfiguration` rules
4. Update status conditions

## Design Rationale

### No Wildcard API Groups

API groups must be explicit (`apiGroups: ["apps"]`, not `apiGroups: ["*"]`). This:

- Prevents accidental tracking of system resources
- Makes policies explicit and auditable
- Simplifies webhook configuration

### Cluster-Scoped

The CRD is cluster-scoped because:

- Webhook configuration is cluster-scoped
- Cross-namespace resources need consistent policies
- Platform teams need global visibility

### Multiple Instances

Multiple `Kausality` instances are supported to allow:

- Platform teams to set broad baselines
- Application teams to override for their namespaces
- Gradual rollout (start with `log`, promote to `enforce`)

### Specificity-Based Precedence

Rather than complex merge logic, specificity wins. This is:

- Predictable: you can determine the winner by inspection
- Debuggable: `kubectl get kausality` shows all policies
- Safe: specific policies (usually team-owned) override broad policies
