# Example Generic Control Plane

This example demonstrates how to embed kausality in a generic Kubernetes-style API server using `k8s.io/apiserver` and `kcp-dev/embeddedetcd`.

## Key Concepts

### Static Policy Resolver

Instead of using the Kausality CRD for policy configuration, this example uses a `StaticResolver` that returns a fixed mode for all resources:

```go
policyResolver := policy.NewStaticResolver(kausalityv1alpha1.ModeEnforce)
```

The `StaticResolver` implements the `policy.Resolver` interface:

```go
type Resolver interface {
    ResolveMode(ctx ResourceContext, objectAnnotations, namespaceAnnotations map[string]string) Mode
    IsTracked(ctx ResourceContext) bool
}
```

### Embedded etcd

The example uses `kcp-dev/embeddedetcd` to run etcd in-process, eliminating the need for a separate etcd cluster during development or testing.

## Building

```bash
cd cmd/example-generic-control-plane
go build .
```

## Running

```bash
./example-generic-control-plane --data-dir=/tmp/example-cp
```

## Full Implementation

For a complete implementation that includes:
- Full apiserver setup with admission chain
- API extensions server for CRDs
- Aggregated API server

See:
- [kcp-dev/generic-controlplane](https://github.com/kcp-dev/generic-controlplane)
- [kubernetes/sample-apiserver](https://github.com/kubernetes/sample-apiserver)

## Integrating Kausality

To wire kausality into a real apiserver's admission chain:

```go
import (
    "github.com/kausality-io/kausality/pkg/admission"
    "github.com/kausality-io/kausality/pkg/policy"
)

// Create policy resolver (static or CRD-based)
policyResolver := policy.NewStaticResolver(kausalityv1alpha1.ModeEnforce)

// Create admission handler
admissionHandler := admission.NewHandler(admission.Config{
    Client:         client,
    Log:            log,
    PolicyResolver: policyResolver,
})

// Register with apiserver's admission chain
// (exact integration depends on apiserver framework)
```

## Sub-module

This example is a separate Go module to avoid pulling embeddedetcd dependencies into the main kausality module. The go.mod uses a replace directive to reference the local kausality code:

```go
replace github.com/kausality-io/kausality => ../..
```
