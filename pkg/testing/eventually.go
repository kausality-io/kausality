/*
Copyright 2026 The Kausality Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Adapted from github.com/kcp-dev/kcp/staging/src/github.com/kcp-dev/sdk/testing/helpers/eventually.go
*/

package testing

import (
	"fmt"
	"time"

	"github.com/stretchr/testify/require"

	"sigs.k8s.io/yaml"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	// Timeout is the default timeout for Eventually assertions in tests.
	// Use for most test conditions that should resolve quickly.
	Timeout = 10 * time.Second

	// LongTimeout is for conditions that may take longer (e.g., controller reconciliation).
	LongTimeout = 30 * time.Second

	// PollInterval is the default polling interval for Eventually assertions.
	PollInterval = 100 * time.Millisecond
)

// TestingT is the subset of testing.T used by these helpers.
type TestingT interface {
	Helper()
	Logf(format string, args ...interface{})
}

// Eventually asserts that given condition will be met in waitFor time, periodically checking target function
// each tick. In addition to require.Eventually, this function t.Logs the reason string value returned by the condition
// function (eventually after 20% of the wait time) to aid in debugging.
//
// The condition function should return (success bool, reason string). The reason is logged when waiting,
// helping debug slow or flaky tests.
func Eventually(t TestingT, condition func() (success bool, reason string), waitFor time.Duration, tick time.Duration, msgAndArgs ...interface{}) {
	t.Helper()

	var last string
	start := time.Now()
	require.Eventually(t.(require.TestingT), func() bool {
		t.Helper()

		ok, msg := condition()
		if time.Since(start) > waitFor/5 {
			if !ok && msg != "" && msg != last {
				last = msg
				t.Logf("Waiting for condition, but got: %s", msg)
			} else if ok && msg != "" && last != "" {
				t.Logf("Condition became true: %s", msg)
			}
		}
		return ok
	}, waitFor, tick, msgAndArgs...)
}

// EventuallyObject asserts that the object returned by getter() eventually satisfies the check function.
// This helper provides verbose logging including YAML representation of the object when conditions are not met.
func EventuallyObject[T runtime.Object](t TestingT, getter func() (T, error), check func(T) (bool, string), waitFor time.Duration, tick time.Duration, msgAndArgs ...interface{}) {
	t.Helper()

	Eventually(t, func() (bool, string) {
		obj, err := getter()
		if err != nil {
			return false, fmt.Sprintf("error fetching object: %v", err)
		}

		ok, reason := check(obj)
		if !ok {
			yamlBytes, _ := yaml.Marshal(obj)
			return false, fmt.Sprintf("%s\n\nCurrent object state:\n%s", reason, string(yamlBytes))
		}
		return true, reason
	}, waitFor, tick, msgAndArgs...)
}

// EventuallyUnstructured asserts that an unstructured object eventually satisfies the check function.
// Provides verbose YAML output for debugging.
func EventuallyUnstructured(t TestingT, getter func() (*unstructured.Unstructured, error), check func(*unstructured.Unstructured) (bool, string), waitFor time.Duration, tick time.Duration, msgAndArgs ...interface{}) {
	t.Helper()

	Eventually(t, func() (bool, string) {
		obj, err := getter()
		if err != nil {
			return false, fmt.Sprintf("error fetching object: %v", err)
		}
		if obj == nil {
			return false, "object is nil"
		}

		ok, reason := check(obj)
		if !ok {
			yamlBytes, _ := yaml.Marshal(obj.Object)
			return false, fmt.Sprintf("%s\n\nCurrent object state:\n%s", reason, string(yamlBytes))
		}
		return true, reason
	}, waitFor, tick, msgAndArgs...)
}

// ToYAML converts an object to YAML string for verbose logging in tests.
func ToYAML(obj interface{}) string {
	yamlBytes, err := yaml.Marshal(obj)
	if err != nil {
		return fmt.Sprintf("(error marshaling to YAML: %v)", err)
	}
	return string(yamlBytes)
}

// HasCondition checks if an object has a condition with the specified type and status.
// Returns a check function suitable for EventuallyObject/EventuallyUnstructured.
func HasCondition(conditionType string, status metav1.ConditionStatus) func(*unstructured.Unstructured) (bool, string) {
	return func(obj *unstructured.Unstructured) (bool, string) {
		conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
		if err != nil {
			return false, fmt.Sprintf("error reading conditions: %v", err)
		}
		if !found {
			return false, fmt.Sprintf("no conditions found, waiting for %s=%s", conditionType, status)
		}

		for _, c := range conditions {
			cond, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			cType, _, _ := unstructured.NestedString(cond, "type")
			cStatus, _, _ := unstructured.NestedString(cond, "status")

			if cType == conditionType {
				if cStatus == string(status) {
					return true, fmt.Sprintf("condition %s=%s", conditionType, status)
				}
				reason, _, _ := unstructured.NestedString(cond, "reason")
				message, _, _ := unstructured.NestedString(cond, "message")
				return false, fmt.Sprintf("condition %s has status %s (want %s), reason: %s, message: %s",
					conditionType, cStatus, status, reason, message)
			}
		}

		return false, fmt.Sprintf("condition %s not found, waiting for %s=%s", conditionType, conditionType, status)
	}
}

// HasGeneration checks if an object has the expected generation.
func HasGeneration(expected int64) func(*unstructured.Unstructured) (bool, string) {
	return func(obj *unstructured.Unstructured) (bool, string) {
		gen := obj.GetGeneration()
		if gen == expected {
			return true, fmt.Sprintf("generation is %d", expected)
		}
		return false, fmt.Sprintf("generation is %d, waiting for %d", gen, expected)
	}
}

// HasObservedGeneration checks if status.observedGeneration equals the object's generation.
func HasObservedGeneration() func(*unstructured.Unstructured) (bool, string) {
	return func(obj *unstructured.Unstructured) (bool, string) {
		gen := obj.GetGeneration()
		obsGen, found, err := unstructured.NestedInt64(obj.Object, "status", "observedGeneration")
		if err != nil {
			return false, fmt.Sprintf("error reading observedGeneration: %v", err)
		}
		if !found {
			return false, fmt.Sprintf("observedGeneration not found, waiting for it to equal generation %d", gen)
		}
		if obsGen == gen {
			return true, fmt.Sprintf("observedGeneration=%d equals generation", obsGen)
		}
		return false, fmt.Sprintf("observedGeneration=%d, waiting for it to equal generation=%d", obsGen, gen)
	}
}

// HasAnnotation checks if an object has the specified annotation with the expected value.
func HasAnnotation(key, value string) func(*unstructured.Unstructured) (bool, string) {
	return func(obj *unstructured.Unstructured) (bool, string) {
		annotations := obj.GetAnnotations()
		if annotations == nil {
			return false, fmt.Sprintf("no annotations found, waiting for %s=%s", key, value)
		}
		if actual, ok := annotations[key]; ok {
			if actual == value {
				return true, fmt.Sprintf("annotation %s=%s", key, value)
			}
			return false, fmt.Sprintf("annotation %s=%s, waiting for %s", key, actual, value)
		}
		return false, fmt.Sprintf("annotation %s not found, waiting for %s=%s", key, key, value)
	}
}

// Not negates a check function.
func Not(check func(*unstructured.Unstructured) (bool, string)) func(*unstructured.Unstructured) (bool, string) {
	return func(obj *unstructured.Unstructured) (bool, string) {
		ok, reason := check(obj)
		if ok {
			return false, fmt.Sprintf("condition met but expected not: %s", reason)
		}
		return true, fmt.Sprintf("condition correctly not met: %s", reason)
	}
}
