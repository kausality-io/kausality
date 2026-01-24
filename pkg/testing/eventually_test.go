package testing

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestEventually(t *testing.T) {
	counter := 0
	Eventually(t, func() (bool, string) {
		counter++
		if counter >= 3 {
			return true, "reached count"
		}
		return false, "waiting for count"
	}, 5*time.Second, 100*time.Millisecond)

	assert.GreaterOrEqual(t, counter, 3)
}

func TestEventuallyUnstructured(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":      "test",
				"namespace": "default",
			},
		},
	}

	EventuallyUnstructured(t, func() (*unstructured.Unstructured, error) {
		return obj, nil
	}, func(u *unstructured.Unstructured) (bool, string) {
		return u.GetName() == "test", "name check"
	}, time.Second, 100*time.Millisecond)
}

func TestToYAML(t *testing.T) {
	obj := map[string]interface{}{
		"name":  "test",
		"value": 42,
	}

	yaml := ToYAML(obj)
	assert.Contains(t, yaml, "name: test")
	assert.Contains(t, yaml, "value: 42")
}

func TestHasCondition(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name": "test",
			},
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{
						"type":    "Ready",
						"status":  "True",
						"reason":  "AllGood",
						"message": "Everything is ready",
					},
				},
			},
		},
	}

	check := HasCondition("Ready", metav1.ConditionTrue)
	ok, _ := check(obj)
	assert.True(t, ok)

	check = HasCondition("Ready", metav1.ConditionFalse)
	ok, reason := check(obj)
	assert.False(t, ok)
	assert.Contains(t, reason, "has status True")
}

func TestHasGeneration(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":       "test",
				"generation": int64(5),
			},
		},
	}

	check := HasGeneration(5)
	ok, _ := check(obj)
	assert.True(t, ok)

	check = HasGeneration(6)
	ok, reason := check(obj)
	assert.False(t, ok)
	assert.Contains(t, reason, "generation is 5")
}

func TestHasObservedGeneration(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":       "test",
				"generation": int64(5),
			},
			"status": map[string]interface{}{
				"observedGeneration": int64(5),
			},
		},
	}

	check := HasObservedGeneration()
	ok, _ := check(obj)
	assert.True(t, ok)

	// Change generation to make it mismatched
	obj.Object["metadata"].(map[string]interface{})["generation"] = int64(6)
	ok, reason := check(obj)
	assert.False(t, ok)
	assert.Contains(t, reason, "observedGeneration=5")
}

func TestHasAnnotation(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name": "test",
				"annotations": map[string]interface{}{
					"foo": "bar",
				},
			},
		},
	}

	check := HasAnnotation("foo", "bar")
	ok, _ := check(obj)
	assert.True(t, ok)

	check = HasAnnotation("foo", "baz")
	ok, reason := check(obj)
	assert.False(t, ok)
	assert.Contains(t, reason, "foo=bar")
}

func TestNot(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":       "test",
				"generation": int64(5),
			},
		},
	}

	check := Not(HasGeneration(6))
	ok, _ := check(obj)
	assert.True(t, ok, "generation is 5, not 6, so Not(HasGeneration(6)) should be true")

	check = Not(HasGeneration(5))
	ok, _ = check(obj)
	assert.False(t, ok, "generation is 5, so Not(HasGeneration(5)) should be false")
}
