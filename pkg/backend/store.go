package backend

import (
	"sync"
	"time"

	"github.com/kausality-io/kausality/pkg/callback/v1alpha1"
)

// StoredReport wraps a DriftReport with metadata
type StoredReport struct {
	Report     *v1alpha1.DriftReport `json:"report"`
	ReceivedAt time.Time             `json:"receivedAt"`
}

// Store holds drift reports in memory
type Store struct {
	mu      sync.RWMutex
	reports map[string]*StoredReport // keyed by report ID
}

// NewStore creates a new in-memory store
func NewStore() *Store {
	return &Store{
		reports: make(map[string]*StoredReport),
	}
}

// Add adds or updates a drift report
func (s *Store) Add(report *v1alpha1.DriftReport) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := report.Spec.ID

	// If phase is Resolved, remove from store
	if report.Spec.Phase == v1alpha1.DriftReportPhaseResolved {
		delete(s.reports, id)
		return
	}

	s.reports[id] = &StoredReport{
		Report:     report,
		ReceivedAt: time.Now(),
	}
}

// Get retrieves a report by ID
func (s *Store) Get(id string) (*StoredReport, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	r, ok := s.reports[id]
	return r, ok
}

// List returns all stored reports
func (s *Store) List() []*StoredReport {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*StoredReport, 0, len(s.reports))
	for _, r := range s.reports {
		result = append(result, r)
	}
	return result
}

// Remove removes a report by ID
func (s *Store) Remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.reports, id)
}

// Count returns the number of stored reports
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.reports)
}
