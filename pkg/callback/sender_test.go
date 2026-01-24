package callback

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kausality-io/kausality/pkg/callback/v1alpha1"
)

func TestSender_Send(t *testing.T) {
	var receivedReport *v1alpha1.DriftReport

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		receivedReport = &v1alpha1.DriftReport{}
		err = json.Unmarshal(body, receivedReport)
		require.NoError(t, err)

		response := v1alpha1.DriftReportResponse{
			TypeMeta: metav1.TypeMeta{
				APIVersion: v1alpha1.GroupName + "/" + v1alpha1.Version,
				Kind:       "DriftReportResponse",
			},
			Acknowledged: true,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	sender, err := NewSender(SenderConfig{
		URL:     server.URL,
		Timeout: 5 * time.Second,
		Log:     logr.Discard(),
	})
	require.NoError(t, err)

	report := &v1alpha1.DriftReport{
		Spec: v1alpha1.DriftReportSpec{
			ID:    "test-id-123",
			Phase: v1alpha1.DriftReportPhaseDetected,
			Parent: v1alpha1.ObjectReference{
				APIVersion: "example.com/v1alpha1",
				Kind:       "EKSCluster",
				Namespace:  "infra",
				Name:       "prod",
			},
			Child: v1alpha1.ObjectReference{
				APIVersion: "v1",
				Kind:       "ConfigMap",
				Namespace:  "infra",
				Name:       "cluster-config",
			},
		},
	}

	err = sender.Send(context.Background(), report)
	require.NoError(t, err)

	// Verify received report
	require.NotNil(t, receivedReport)
	assert.Equal(t, "test-id-123", receivedReport.Spec.ID)
	assert.Equal(t, v1alpha1.DriftReportPhaseDetected, receivedReport.Spec.Phase)
	assert.Equal(t, "kausality.io/v1alpha1", receivedReport.APIVersion)
	assert.Equal(t, "DriftReport", receivedReport.Kind)
}

func TestSender_Deduplication(t *testing.T) {
	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		response := v1alpha1.DriftReportResponse{Acknowledged: true}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	sender, err := NewSender(SenderConfig{
		URL: server.URL,
		Log: logr.Discard(),
	})
	require.NoError(t, err)

	report := &v1alpha1.DriftReport{
		Spec: v1alpha1.DriftReportSpec{
			ID:    "duplicate-id",
			Phase: v1alpha1.DriftReportPhaseDetected,
		},
	}

	ctx := context.Background()

	// First send should go through
	err = sender.Send(ctx, report)
	require.NoError(t, err)
	assert.Equal(t, int32(1), callCount.Load())

	// Second send of same ID should be deduplicated
	err = sender.Send(ctx, report)
	require.NoError(t, err)
	assert.Equal(t, int32(1), callCount.Load()) // Still 1

	// Different ID should go through
	report2 := &v1alpha1.DriftReport{
		Spec: v1alpha1.DriftReportSpec{
			ID:    "different-id",
			Phase: v1alpha1.DriftReportPhaseDetected,
		},
	}
	err = sender.Send(ctx, report2)
	require.NoError(t, err)
	assert.Equal(t, int32(2), callCount.Load())
}

func TestSender_NoDeduplicationForResolved(t *testing.T) {
	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		response := v1alpha1.DriftReportResponse{Acknowledged: true}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	sender, err := NewSender(SenderConfig{
		URL: server.URL,
		Log: logr.Discard(),
	})
	require.NoError(t, err)

	report := &v1alpha1.DriftReport{
		Spec: v1alpha1.DriftReportSpec{
			ID:    "resolved-id",
			Phase: v1alpha1.DriftReportPhaseResolved,
		},
	}

	ctx := context.Background()

	// Resolved reports should not be deduplicated
	err = sender.Send(ctx, report)
	require.NoError(t, err)
	assert.Equal(t, int32(1), callCount.Load())

	err = sender.Send(ctx, report)
	require.NoError(t, err)
	assert.Equal(t, int32(2), callCount.Load())
}

func TestSender_Retry(t *testing.T) {
	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := callCount.Add(1)
		if count < 3 {
			// Fail first 2 attempts
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("server error"))
			return
		}
		// Succeed on 3rd attempt
		response := v1alpha1.DriftReportResponse{Acknowledged: true}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	sender, err := NewSender(SenderConfig{
		URL:           server.URL,
		RetryCount:    3,
		RetryInterval: 10 * time.Millisecond,
		Log:           logr.Discard(),
	})
	require.NoError(t, err)

	report := &v1alpha1.DriftReport{
		Spec: v1alpha1.DriftReportSpec{
			ID:    "retry-test",
			Phase: v1alpha1.DriftReportPhaseDetected,
		},
	}

	err = sender.Send(context.Background(), report)
	require.NoError(t, err)
	assert.Equal(t, int32(3), callCount.Load())
}

func TestSender_RetryExhausted(t *testing.T) {
	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("server error"))
	}))
	defer server.Close()

	sender, err := NewSender(SenderConfig{
		URL:           server.URL,
		RetryCount:    2,
		RetryInterval: 10 * time.Millisecond,
		Log:           logr.Discard(),
	})
	require.NoError(t, err)

	report := &v1alpha1.DriftReport{
		Spec: v1alpha1.DriftReportSpec{
			ID:    "retry-exhausted",
			Phase: v1alpha1.DriftReportPhaseDetected,
		},
	}

	err = sender.Send(context.Background(), report)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
	assert.Equal(t, int32(3), callCount.Load()) // 1 initial + 2 retries
}

func TestSender_NotAcknowledged(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := v1alpha1.DriftReportResponse{
			Acknowledged: false,
			Error:        "processing failed",
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	sender, err := NewSender(SenderConfig{
		URL:        server.URL,
		RetryCount: 0, // No retries
		Log:        logr.Discard(),
	})
	require.NoError(t, err)

	report := &v1alpha1.DriftReport{
		Spec: v1alpha1.DriftReportSpec{
			ID:    "not-ack",
			Phase: v1alpha1.DriftReportPhaseDetected,
		},
	}

	err = sender.Send(context.Background(), report)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not acknowledge")
	assert.Contains(t, err.Error(), "processing failed")
}

func TestSender_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1 * time.Second)
		response := v1alpha1.DriftReportResponse{Acknowledged: true}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	sender, err := NewSender(SenderConfig{
		URL:           server.URL,
		Timeout:       5 * time.Second,
		RetryCount:    5,
		RetryInterval: 100 * time.Millisecond,
		Log:           logr.Discard(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	report := &v1alpha1.DriftReport{
		Spec: v1alpha1.DriftReportSpec{
			ID:    "cancel-test",
			Phase: v1alpha1.DriftReportPhaseDetected,
		},
	}

	err = sender.Send(ctx, report)
	require.Error(t, err)
}

func TestSender_MarkResolved(t *testing.T) {
	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		response := v1alpha1.DriftReportResponse{Acknowledged: true}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	sender, err := NewSender(SenderConfig{
		URL: server.URL,
		Log: logr.Discard(),
	})
	require.NoError(t, err)

	report := &v1alpha1.DriftReport{
		Spec: v1alpha1.DriftReportSpec{
			ID:    "mark-resolved-test",
			Phase: v1alpha1.DriftReportPhaseDetected,
		},
	}

	ctx := context.Background()

	// First send
	err = sender.Send(ctx, report)
	require.NoError(t, err)
	assert.Equal(t, int32(1), callCount.Load())

	// Second send should be deduplicated
	err = sender.Send(ctx, report)
	require.NoError(t, err)
	assert.Equal(t, int32(1), callCount.Load())

	// Mark as resolved
	sender.MarkResolved("mark-resolved-test")

	// Now it can be sent again
	err = sender.Send(ctx, report)
	require.NoError(t, err)
	assert.Equal(t, int32(2), callCount.Load())
}

func TestSender_SendAsync(t *testing.T) {
	received := make(chan *v1alpha1.DriftReport, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		report := &v1alpha1.DriftReport{}
		_ = json.Unmarshal(body, report)
		received <- report

		response := v1alpha1.DriftReportResponse{Acknowledged: true}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	sender, err := NewSender(SenderConfig{
		URL: server.URL,
		Log: logr.Discard(),
	})
	require.NoError(t, err)

	report := &v1alpha1.DriftReport{
		Spec: v1alpha1.DriftReportSpec{
			ID:    "async-test",
			Phase: v1alpha1.DriftReportPhaseDetected,
		},
	}

	sender.SendAsync(context.Background(), report)

	select {
	case r := <-received:
		assert.Equal(t, "async-test", r.Spec.ID)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for async send")
	}
}

func TestSender_IsEnabled(t *testing.T) {
	sender, err := NewSender(SenderConfig{
		URL: "https://webhook.example.com",
	})
	require.NoError(t, err)
	assert.True(t, sender.IsEnabled())

	sender2, err := NewSender(SenderConfig{
		URL: "",
	})
	require.NoError(t, err)
	assert.False(t, sender2.IsEnabled())
}

func TestNewSender_InvalidCAFile(t *testing.T) {
	_, err := NewSender(SenderConfig{
		URL:    "https://webhook.example.com",
		CAFile: "/nonexistent/ca.crt",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read CA file")
}
