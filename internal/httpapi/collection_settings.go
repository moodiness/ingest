package httpapi

import (
	"net/http"

	"github.com/moodiness/ingest/internal/model"
)

func (s *server) registerCollectionSettingsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings/collections", s.auth.protect(s.collectionSettings))
	mux.HandleFunc("PUT /api/settings/collections", s.auth.protect(s.updateCollectionSettings))
}

func (s *server) collectionSettings(w http.ResponseWriter, r *http.Request) {
	overview, err := s.options.Store.CollectionOverview(r.Context())
	if err != nil {
		respondError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, overview)
}

func (s *server) updateCollectionSettings(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Workers                      *int    `json:"workers"`
		MaxQuotaRetries              *int    `json:"max_quota_retries"`
		MaxQuotaWaitSeconds          *int    `json:"max_quota_wait_seconds"`
		AutoResumeInterrupted        *bool   `json:"auto_resume_interrupted"`
		NoProgressRequests           *int    `json:"no_progress_requests"`
		NoProgressAction             *string `json:"no_progress_action"`
		DefaultRequestTimeoutSeconds *int    `json:"default_request_timeout_seconds"`
		DefaultPreviewPages          *int    `json:"default_preview_pages"`
		DefaultMaxPages              *int    `json:"default_max_pages"`
		DefaultMaxDurationSeconds    *int    `json:"default_max_duration_seconds"`
		Revision                     *int64  `json:"revision"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Workers == nil || input.MaxQuotaRetries == nil || input.MaxQuotaWaitSeconds == nil ||
		input.AutoResumeInterrupted == nil || input.NoProgressRequests == nil || input.NoProgressAction == nil ||
		input.DefaultRequestTimeoutSeconds == nil || input.DefaultPreviewPages == nil ||
		input.DefaultMaxPages == nil || input.DefaultMaxDurationSeconds == nil || input.Revision == nil {
		writeError(w, http.StatusBadRequest, "a complete collection settings object and revision are required")
		return
	}
	settings := model.CollectionSettings{
		Workers: *input.Workers, MaxQuotaRetries: *input.MaxQuotaRetries,
		MaxQuotaWaitSeconds: *input.MaxQuotaWaitSeconds, AutoResumeInterrupted: *input.AutoResumeInterrupted,
		NoProgressRequests: *input.NoProgressRequests, NoProgressAction: *input.NoProgressAction,
		DefaultRequestTimeoutSeconds: *input.DefaultRequestTimeoutSeconds,
		DefaultPreviewPages:          *input.DefaultPreviewPages, DefaultMaxPages: *input.DefaultMaxPages,
		DefaultMaxDurationSeconds: *input.DefaultMaxDurationSeconds, Revision: *input.Revision,
	}
	if !settings.Valid() {
		writeError(w, http.StatusBadRequest, "collection settings contain an invalid value or revision")
		return
	}
	if err := s.options.Store.UpdateCollectionSettings(r.Context(), settings); err != nil {
		respondError(w, err)
		return
	}
	s.collectionSettings(w, r)
}
