package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/moodiness/ingest/internal/model"
	"github.com/moodiness/ingest/internal/store"
	"github.com/moodiness/ingest/internal/webhooks"
)

func (s *server) registerActivityRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/logs", s.auth.protect(s.activityLogs))
	mux.HandleFunc("GET /api/notifications", s.auth.protect(s.activityNotifications))
	mux.HandleFunc("POST /api/notifications/{id}/read", s.auth.protect(s.readNotification))
	mux.HandleFunc("POST /api/notifications/read-all", s.auth.protect(s.readAllNotifications))
	mux.HandleFunc("GET /api/webhooks", s.auth.protect(s.listWebhooks))
	mux.HandleFunc("POST /api/webhooks", s.auth.protect(s.createWebhook))
	mux.HandleFunc("PUT /api/webhooks/{id}", s.auth.protect(s.updateWebhook))
	mux.HandleFunc("DELETE /api/webhooks/{id}", s.auth.protect(s.deleteWebhook))
	mux.HandleFunc("GET /api/webhooks/{id}/deliveries", s.auth.protect(s.webhookDeliveries))
	mux.HandleFunc("POST /api/webhooks/{id}/test", s.auth.protect(s.testWebhook))
	mux.HandleFunc("POST /api/webhooks/{id}/deliveries/{delivery_id}/retry", s.auth.protect(s.retryWebhook))
}

func activityOptions(w http.ResponseWriter, r *http.Request, defaultLimit int) (model.ListOptions, bool) {
	options, ok := listOptions(w, r)
	if !ok {
		return options, false
	}
	if r.URL.Query().Get("limit") == "" {
		options.Limit = defaultLimit
	}
	if options.Offset > 1000000 {
		writeError(w, http.StatusBadRequest, "offset must not exceed 1000000")
		return options, false
	}
	return options, true
}

func (s *server) activityLogs(w http.ResponseWriter, r *http.Request) {
	page, ok := activityOptions(w, r, 50)
	if !ok {
		return
	}
	options := model.LogOptions{ListOptions: page}
	if raw := r.URL.Query().Get("level"); raw != "" {
		if len(raw) > 32 {
			writeError(w, http.StatusBadRequest, "Invalid log level filter")
			return
		}
		options.Levels = strings.Split(raw, ",")
		for _, level := range options.Levels {
			if !model.IsActivityLevel(level) {
				writeError(w, http.StatusBadRequest, "Log levels must be debug, info, warn or error")
				return
			}
		}
	}
	for key, target := range map[string]**time.Time{"from": &options.From, "to": &options.To} {
		if raw := r.URL.Query().Get(key); raw != "" {
			value, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				writeError(w, http.StatusBadRequest, "Time filters must be RFC3339 timestamps")
				return
			}
			*target = &value
		}
	}
	if options.From != nil && options.To != nil && options.From.After(*options.To) {
		writeError(w, http.StatusBadRequest, "from must not be later than to")
		return
	}
	result, err := s.options.Store.Logs(r.Context(), options)
	if err != nil {
		respondError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) activityNotifications(w http.ResponseWriter, r *http.Request) {
	options, ok := activityOptions(w, r, 20)
	if !ok {
		return
	}
	unread := false
	if raw := r.URL.Query().Get("unread"); raw != "" {
		if raw != "true" && raw != "false" {
			writeError(w, http.StatusBadRequest, "unread must be true or false")
			return
		}
		unread = raw == "true"
	}
	result, err := s.options.Store.Notifications(r.Context(), unread, options)
	if err != nil {
		respondError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func emptyActivityRequest(w http.ResponseWriter, r *http.Request) bool {
	var input struct{}
	return decodeJSON(w, r, &input)
}

func (s *server) readNotification(w http.ResponseWriter, r *http.Request) {
	id, ok := positiveID(w, r)
	if !ok || !emptyActivityRequest(w, r) {
		return
	}
	if err := s.options.Store.ReadNotification(r.Context(), id); err != nil {
		respondError(w, err)
		return
	}
	s.options.Jobs.Notify("notification", strconv.FormatInt(id, 10))
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) readAllNotifications(w http.ResponseWriter, r *http.Request) {
	if !emptyActivityRequest(w, r) {
		return
	}
	if err := s.options.Store.ReadAllNotifications(r.Context()); err != nil {
		respondError(w, err)
		return
	}
	s.options.Jobs.Notify("notification", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) listWebhooks(w http.ResponseWriter, r *http.Request) {
	query, ok := requestQuery(w, r)
	if !ok {
		return
	}
	raw := query.Get("include_deleted")
	if raw != "" && raw != "true" && raw != "false" {
		writeError(w, http.StatusBadRequest, "include_deleted must be true or false")
		return
	}
	items, err := s.options.Store.Webhooks(r.Context(), raw == "true")
	if err != nil {
		respondError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *server) validateWebhookSecrets(w http.ResponseWriter, r *http.Request, input model.WebhookInput) bool {
	if err := store.ValidateWebhook(input); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "A webhook needs a name, valid secret references and unique supported events")
		return false
	}
	// Disabling must remain possible after a bad secret rotation. The store
	// still checks reference existence and the optimistic revision atomically.
	if !input.Enabled {
		return true
	}
	destination, err := s.options.Vault.Resolve(r.Context(), input.URLSecretRef)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "Destination secret is unavailable")
		return false
	}
	if webhooks.ValidateURL(destination) != nil {
		writeError(w, http.StatusUnprocessableEntity, "Destination secret must contain a valid HTTP or HTTPS URL without embedded credentials or a fragment")
		return false
	}
	if input.SigningSecretRef != "" {
		if _, err := s.options.Vault.Resolve(r.Context(), input.SigningSecretRef); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "Signing secret is unavailable")
			return false
		}
	}
	return true
}

func (s *server) createWebhook(w http.ResponseWriter, r *http.Request) { s.saveWebhook(w, r, true) }
func (s *server) updateWebhook(w http.ResponseWriter, r *http.Request) { s.saveWebhook(w, r, false) }

func (s *server) saveWebhook(w http.ResponseWriter, r *http.Request, create bool) {
	var input model.WebhookInput
	if !decodeJSON(w, r, &input) || !s.validateWebhookSecrets(w, r, input) {
		return
	}
	id := ""
	status := http.StatusCreated
	if !create {
		id = r.PathValue("id")
		status = http.StatusOK
	}
	item, err := s.options.Store.SaveWebhook(r.Context(), id, input)
	if err != nil {
		respondError(w, err)
		return
	}
	s.options.Jobs.Notify("webhook", item.ID)
	writeJSON(w, status, item)
}

func (s *server) deleteWebhook(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Revision int64 `json:"revision"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := s.options.Store.DeleteWebhook(r.Context(), r.PathValue("id"), input.Revision); err != nil {
		respondError(w, err)
		return
	}
	s.options.Jobs.Notify("webhook", r.PathValue("id"))
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) webhookDeliveries(w http.ResponseWriter, r *http.Request) {
	options, ok := activityOptions(w, r, 50)
	if !ok {
		return
	}
	switch options.Status {
	case "", "pending", "delivering", "delivered", "failed", "cancelled":
	default:
		writeError(w, http.StatusBadRequest, "Invalid delivery status")
		return
	}
	result, err := s.options.Store.WebhookDeliveries(r.Context(), r.PathValue("id"), options)
	if err != nil {
		respondError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) testWebhook(w http.ResponseWriter, r *http.Request) {
	if !emptyActivityRequest(w, r) {
		return
	}
	item, err := s.options.Store.QueueWebhookTest(r.Context(), r.PathValue("id"))
	if err != nil {
		respondError(w, err)
		return
	}
	s.options.Jobs.Notify("webhook", r.PathValue("id"))
	writeJSON(w, http.StatusAccepted, item)
}

func (s *server) retryWebhook(w http.ResponseWriter, r *http.Request) {
	if !emptyActivityRequest(w, r) {
		return
	}
	item, err := s.options.Store.RetryWebhookDelivery(r.Context(), r.PathValue("id"), r.PathValue("delivery_id"))
	if err != nil {
		respondError(w, err)
		return
	}
	s.options.Jobs.Notify("webhook", r.PathValue("id"))
	writeJSON(w, http.StatusAccepted, item)
}
