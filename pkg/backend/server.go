package backend

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/kausality-io/kausality/pkg/callback/v1alpha1"
)

// Server handles DriftReport webhooks and serves the API
type Server struct {
	store *Store
}

// NewServer creates a new backend server
func NewServer() *Server {
	return &Server{
		store: NewStore(),
	}
}

// Store returns the underlying store
func (s *Server) Store() *Store {
	return s.store
}

// Handler returns the HTTP handler for the server
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Webhook endpoint - receives DriftReports
	mux.HandleFunc("POST /webhook", s.handleWebhook)

	// API endpoints
	mux.HandleFunc("GET /api/v1/drifts", s.handleListDrifts)
	mux.HandleFunc("GET /api/v1/drifts/{id}", s.handleGetDrift)
	mux.HandleFunc("DELETE /api/v1/drifts/{id}", s.handleDeleteDrift)

	// Health endpoint
	mux.HandleFunc("GET /healthz", s.handleHealth)

	return mux
}

// handleWebhook receives DriftReports
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	var report v1alpha1.DriftReport
	if err := json.Unmarshal(body, &report); err != nil {
		http.Error(w, "invalid DriftReport", http.StatusBadRequest)
		return
	}

	// Store the report
	s.store.Add(&report)

	// Send acknowledgement
	response := v1alpha1.DriftReportResponse{Acknowledged: true}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// handleListDrifts returns all stored drift reports
func (s *Server) handleListDrifts(w http.ResponseWriter, r *http.Request) {
	reports := s.store.List()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"items": reports,
		"count": len(reports),
	})
}

// handleGetDrift returns a single drift report by ID
func (s *Server) handleGetDrift(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}

	report, ok := s.store.Get(id)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(report)
}

// handleDeleteDrift removes a drift report
func (s *Server) handleDeleteDrift(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}

	s.store.Remove(id)
	w.WriteHeader(http.StatusNoContent)
}

// handleHealth returns health status
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":     "ok",
		"driftCount": s.store.Count(),
		"time":       time.Now().Format(time.RFC3339),
	})
}
