package callback

import (
	"sync"
	"time"
)

// DefaultTTL is the default time-to-live for tracked IDs.
const DefaultTTL = 10 * time.Minute

// Tracker tracks drift IDs for deduplication.
// It maintains an in-memory map of recently sent IDs with TTL-based expiration.
type Tracker struct {
	mu      sync.RWMutex
	ids     map[string]time.Time // ID -> expiration time
	ttl     time.Duration
	nowFunc func() time.Time // for testing
}

// TrackerOption configures the Tracker.
type TrackerOption func(*Tracker)

// WithTTL sets the time-to-live for tracked IDs.
func WithTTL(ttl time.Duration) TrackerOption {
	return func(t *Tracker) {
		t.ttl = ttl
	}
}

// WithNowFunc sets the function to get the current time (for testing).
func WithNowFunc(fn func() time.Time) TrackerOption {
	return func(t *Tracker) {
		t.nowFunc = fn
	}
}

// NewTracker creates a new Tracker with optional configuration.
func NewTracker(opts ...TrackerOption) *Tracker {
	t := &Tracker{
		ids:     make(map[string]time.Time),
		ttl:     DefaultTTL,
		nowFunc: time.Now,
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// Track adds an ID to the tracker and returns true if the ID was new.
// Returns false if the ID was already tracked and not expired.
func (t *Tracker) Track(id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.nowFunc()

	// Check if already tracked and not expired
	if expiry, exists := t.ids[id]; exists {
		if now.Before(expiry) {
			// Already tracked and not expired
			return false
		}
	}

	// Add/update with new expiration
	t.ids[id] = now.Add(t.ttl)
	return true
}

// IsTracked returns true if the ID is currently tracked and not expired.
func (t *Tracker) IsTracked(id string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	now := t.nowFunc()
	if expiry, exists := t.ids[id]; exists {
		return now.Before(expiry)
	}
	return false
}

// Remove removes an ID from the tracker.
func (t *Tracker) Remove(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.ids, id)
}

// Cleanup removes expired entries from the tracker.
// This should be called periodically to prevent memory growth.
func (t *Tracker) Cleanup() int {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.nowFunc()
	count := 0
	for id, expiry := range t.ids {
		if now.After(expiry) || now.Equal(expiry) {
			delete(t.ids, id)
			count++
		}
	}
	return count
}

// Size returns the number of tracked IDs (including expired ones).
func (t *Tracker) Size() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.ids)
}

// StartCleanupLoop starts a background goroutine that periodically cleans up
// expired entries. Returns a stop function to cancel the loop.
func (t *Tracker) StartCleanupLoop(interval time.Duration) func() {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				t.Cleanup()
			case <-done:
				return
			}
		}
	}()
	return func() {
		close(done)
	}
}
