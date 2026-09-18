// fake-psp is a synthetic Statuspage v2 and API target for the local demo.
// Its POST-only controls keep declared and observed state independently testable.
package main

import (
	"encoding/json"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"sync"
)

type state struct {
	mu         sync.RWMutex
	incident   bool
	apiFailure bool
}

func newServer() *http.ServeMux {
	state := &state{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/summary.json", state.summary)
	mux.HandleFunc("GET /payment-api/health", state.health)
	mux.HandleFunc("GET /control", state.showControl)
	mux.HandleFunc("POST /control", state.updateControl)
	return mux
}

func (s *state) summary(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	incident := s.incident
	s.mu.RUnlock()
	indicator, componentStatus := "none", "operational"
	incidents := []any{}
	if incident {
		indicator, componentStatus = "major", "degraded_performance"
		incidents = append(incidents, map[string]string{
			"id": "demo-incident", "status": "investigating", "impact": "major",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": map[string]string{"indicator": indicator},
		"components": []map[string]string{{
			"id": "payment-api", "status": componentStatus,
		}},
		"incidents":              incidents,
		"scheduled_maintenances": []any{},
	})
}

func (s *state) health(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	failure := s.apiFailure
	s.mu.RUnlock()
	if failure {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "synthetic failure"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *state) showControl(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]bool{"incident": s.incident, "api_failure": s.apiFailure})
}

func (s *state) updateControl(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	incident, hasIncident, err := boolControl(query, "incident")
	if err != nil {
		http.Error(w, "incident must be true or false", http.StatusBadRequest)
		return
	}
	apiFailure, hasAPIFailure, err := boolControl(query, "api_failure")
	if err != nil {
		http.Error(w, "api_failure must be true or false", http.StatusBadRequest)
		return
	}
	if !hasIncident && !hasAPIFailure {
		http.Error(w, "provide incident and/or api_failure", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	if hasIncident {
		s.incident = incident
	}
	if hasAPIFailure {
		s.apiFailure = apiFailure
	}
	current := map[string]bool{"incident": s.incident, "api_failure": s.apiFailure}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, current)
}

func boolControl(query map[string][]string, key string) (bool, bool, error) {
	values, ok := query[key]
	if !ok {
		return false, false, nil
	}
	if len(values) != 1 {
		return false, true, strconv.ErrSyntax
	}
	value, err := strconv.ParseBool(values[0])
	return value, true, err
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func main() {
	address := flag.String("listen-address", ":8080", "HTTP listen address")
	flag.Parse()
	server := &http.Server{Addr: *address, Handler: newServer()}
	slog.New(slog.NewTextHandler(os.Stdout, nil)).Info("fake PSP listening", "address", *address)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("fake PSP stopped", "error", err)
		os.Exit(1)
	}
}
