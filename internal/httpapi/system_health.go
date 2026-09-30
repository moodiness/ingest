package httpapi

import (
	"net/http"

	"github.com/moodiness/ingest/internal/model"
)

func (s *server) registerSystemHealthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/system-health", s.auth.protect(s.systemHealth))
	mux.HandleFunc("PUT /api/system-health/settings", s.auth.protect(s.updateSystemHealthSettings))
}

func (s *server) systemHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.options.Health == nil {
		writeError(w, http.StatusServiceUnavailable, "System health monitoring is unavailable")
		return
	}
	report := s.options.Health.Report()
	if report == nil {
		writeError(w, http.StatusServiceUnavailable, "The first health measurement is still pending")
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *server) updateSystemHealthSettings(w http.ResponseWriter, r *http.Request) {
	var request struct {
		StaleHours               *int   `json:"stale_hours"`
		StuckMinutes             *int   `json:"stuck_minutes"`
		MinFreeBytes             *int64 `json:"min_free_bytes"`
		MinFreePercent           *int   `json:"min_free_percent"`
		JournalGrowthBytesPerDay *int64 `json:"journal_growth_bytes_per_day"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.StaleHours == nil || request.StuckMinutes == nil || request.MinFreeBytes == nil || request.MinFreePercent == nil || request.JournalGrowthBytesPerDay == nil {
		writeError(w, http.StatusBadRequest, "All diagnostic thresholds are required")
		return
	}
	input := model.HealthSettings{StaleHours: *request.StaleHours, StuckMinutes: *request.StuckMinutes, MinFreeBytes: *request.MinFreeBytes, MinFreePercent: *request.MinFreePercent, JournalGrowthBytesPerDay: *request.JournalGrowthBytesPerDay}
	if !input.Valid() {
		writeError(w, http.StatusBadRequest, "Thresholds are outside the supported ranges")
		return
	}
	if s.options.Health == nil {
		writeError(w, http.StatusServiceUnavailable, "System health monitoring is unavailable")
		return
	}
	if err := s.options.Health.UpdateSettings(r.Context(), input); err != nil {
		respondError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, input)
}
