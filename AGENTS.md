# Kausality Development Guidelines

This document outlines the coding and testing conventions for the Kausality project.

## Test Conventions

### Libraries

Use the following testing libraries consistently across all tests:

- **github.com/stretchr/testify** - For assertions (`assert`, `require`)
- **github.com/google/go-cmp** - For comparing complex objects with readable diffs

### Assertions

Use `assert` for non-fatal assertions and `require` for fatal assertions:

```go
import (
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestExample(t *testing.T) {
    // Use require when the test cannot continue if the assertion fails
    result, err := doSomething()
    require.NoError(t, err)
    require.NotNil(t, result)

    // Use assert for non-fatal checks
    assert.Equal(t, expected, result.Value)
    assert.Len(t, result.Items, 3)
}
```

### Object Comparison

Use `cmp.Diff` from go-cmp for comparing complex objects:

```go
import "github.com/google/go-cmp/cmp"

func TestObjectComparison(t *testing.T) {
    want := SomeStruct{...}
    got := computeResult()

    if diff := cmp.Diff(want, got); diff != "" {
        t.Errorf("Result mismatch (-want +got):\n%s", diff)
    }
}
```

### Eventually Helpers

For tests that need to wait for conditions, use the helpers in `pkg/testing`:

```go
import ktesting "github.com/kausality-io/kausality/pkg/testing"

func TestEventualCondition(t *testing.T) {
    // Wait for an unstructured object to meet a condition
    ktesting.EventuallyUnstructured(t,
        func() (*unstructured.Unstructured, error) {
            return client.Get(ctx, name, namespace)
        },
        ktesting.HasCondition("Ready", metav1.ConditionTrue),
        30*time.Second,
        100*time.Millisecond,
        "waiting for object to become ready",
    )
}
```

The `Eventually` helpers log verbose context (including YAML representation of objects) when conditions are not met, making test failures easier to debug.

Available check functions:
- `HasCondition(type, status)` - Check status conditions
- `HasGeneration(gen)` - Check object generation
- `HasObservedGeneration()` - Check observedGeneration equals generation
- `HasAnnotation(key, value)` - Check annotation presence and value
- `Not(check)` - Negate any check function
- `ToYAML(obj)` - Convert object to YAML for logging

### Verbose Logging

When assertions fail in eventually loops, provide helpful context. The `pkg/testing` helpers automatically include YAML representation of objects:

```go
ktesting.EventuallyUnstructured(t, getter,
    func(obj *unstructured.Unstructured) (bool, string) {
        // Return a descriptive reason string
        if obj.GetGeneration() != expected {
            return false, fmt.Sprintf(
                "generation is %d, waiting for %d",
                obj.GetGeneration(), expected,
            )
        }
        return true, "generation matches"
    },
    timeout, tick,
)
```

### Table-Driven Tests

Use table-driven tests with descriptive test names:

```go
func TestFeature(t *testing.T) {
    tests := []struct {
        name    string
        input   Input
        want    Output
        wantErr bool
    }{
        {
            name:  "valid input produces expected output",
            input: Input{...},
            want:  Output{...},
        },
        {
            name:    "invalid input returns error",
            input:   Input{...},
            wantErr: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := Process(tt.input)
            if tt.wantErr {
                assert.Error(t, err)
                return
            }
            require.NoError(t, err)
            assert.Equal(t, tt.want, got)
        })
    }
}
```

## Commit Conventions

Follow the commit message format:

```
area/subarea: short description

Longer description if needed.

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>
```

Example areas:
- `admission`: Admission webhook handler
- `approval`: Approval/rejection system
- `config`: Configuration handling
- `drift`: Drift detection
- `trace`: Request trace propagation
- `webhook`: Webhook server
- `doc`: Documentation
- `test`: Test improvements

## Code Style

- Keep functions focused and small
- Prefer explicit over implicit
- Avoid over-engineering - implement what's needed now
- Add comments only where the logic isn't self-evident
- Preserve existing formatting unless changing semantics
