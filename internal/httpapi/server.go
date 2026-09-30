// Package httpapi serves the authenticated administration API and static panel.
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/moodiness/ingest/internal/backups"
	"github.com/moodiness/ingest/internal/connectors"
	"github.com/moodiness/ingest/internal/health"
	"github.com/moodiness/ingest/internal/jobs"
	"github.com/moodiness/ingest/internal/model"
	"github.com/moodiness/ingest/internal/providers"
	"github.com/moodiness/ingest/internal/store"
	"github.com/moodiness/ingest/internal/vault"
)

type Options struct {
	Store         *store.Store
	Providers     *providers.Registry
	Jobs          *jobs.Manager
	Vault         *vault.Vault
	Backups       *backups.Service
	Health        *health.Service
	AdminPassword string
	PublicURL     string
	Frontend      fs.FS
	Version       string
}

type server struct {
	options Options
	auth    *authentication
	streams chan struct{}
}

func New(options Options) (http.Handler, error) {
	if options.Store == nil || options.Providers == nil || options.Jobs == nil || options.Vault == nil || options.Frontend == nil {
		return nil, errors.New("API dependencies and compiled frontend are required")
	}
	auth, err := newAuthentication(options.AdminPassword, options.PublicURL, options.Store, options.Vault)
	if err != nil {
		return nil, err
	}
	if _, err := fs.Stat(options.Frontend, "index.html"); err != nil {
		return nil, errors.New("compiled frontend index.html is missing; build web before starting the server")
	}
	s := &server{options: options, auth: auth, streams: make(chan struct{}, 64)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/session", auth.sessionHandler)
	mux.HandleFunc("POST /api/login", auth.loginHandler)
	mux.HandleFunc("POST /api/logout", auth.protect(auth.logoutHandler))
	mux.HandleFunc("GET /api/overview", auth.protect(s.overview))
	mux.HandleFunc("GET /api/health", auth.protect(s.health))
	mux.HandleFunc("GET /api/providers", auth.protect(s.listProviders))
	mux.HandleFunc("GET /api/schedules", auth.protect(s.listSchedules))
	mux.HandleFunc("GET /api/provider-schema", auth.protect(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, providers.JSONSchema())
	}))
	mux.HandleFunc("GET /api/providers/{id}", auth.protect(s.getProvider))
	mux.HandleFunc("PUT /api/providers/{id}", auth.protect(s.saveProvider))
	mux.HandleFunc("DELETE /api/providers/{id}", auth.protect(s.deleteProvider))
	mux.HandleFunc("POST /api/providers/validate", auth.protect(s.validateProvider))
	mux.HandleFunc("GET /api/adapters", auth.protect(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"items": connectors.Adapters()})
	}))
	mux.HandleFunc("GET /api/secrets", auth.protect(s.listSecrets))
	mux.HandleFunc("PUT /api/secrets/{name}", auth.protect(s.putSecret))
	mux.HandleFunc("DELETE /api/secrets/{name}", auth.protect(s.deleteSecret))
	mux.HandleFunc("GET /api/runs", auth.protect(s.listRuns))
	mux.HandleFunc("POST /api/runs", auth.protect(s.startRun))
	mux.HandleFunc("GET /api/runs/{id}", auth.protect(s.getRun))
	mux.HandleFunc("POST /api/runs/{id}/pause", auth.protect(s.pauseRun))
	mux.HandleFunc("POST /api/runs/{id}/cancel", auth.protect(s.cancelRun))
	mux.HandleFunc("POST /api/runs/{id}/resume", auth.protect(s.resumeRun))
	mux.HandleFunc("GET /api/runs/{id}/events", auth.protect(s.runEvents))
	mux.HandleFunc("GET /api/events", auth.protect(s.events))
	mux.HandleFunc("GET /api/raw", auth.protect(s.listRaw))
	mux.HandleFunc("GET /api/raw/{id}", auth.protect(s.rawDetail))
	mux.HandleFunc("GET /api/raw/{id}/download", auth.protect(s.rawDownload))
	mux.HandleFunc("GET /api/pages/{id}/download", auth.protect(s.pageDownload))
	mux.HandleFunc("GET /api/torrents", auth.protect(s.torrents))
	s.registerActivityRoutes(mux)
	s.registerSharingRoutes(mux)
	s.registerRemoteRoutes(mux)
	s.registerSecurityRoutes(mux)
	s.registerSearchRoutes(mux)
	s.registerHistoryRoutes(mux)
	s.registerSystemHealthRoutes(mux)
	s.registerBackupRoutes(mux)
	s.registerCollectionSettingsRoutes(mux)
	mux.HandleFunc("/api/", auth.protect(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "API route not found")
	}))
	mux.Handle("/", staticFrontend(options.Frontend))
	return securityHeaders(recoverRequests(mux)), nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not encode the response")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n'))
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "A JSON request is required")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid or oversized JSON body")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "Only one JSON document is accepted")
		return false
	}
	return true
}

func respondError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, model.ErrNotFound):
		writeError(w, http.StatusNotFound, "Item not found")
	case errors.Is(err, model.ErrBusy):
		writeError(w, http.StatusConflict, "A run is already active for this provider")
	case errors.Is(err, model.ErrConflict):
		writeError(w, http.StatusConflict, "The state has changed. Reload before trying again.")
	case errors.Is(err, model.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, "Invalid configuration or operation")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusServiceUnavailable, "Operation interrupted or timed out")
	default:
		// Do not expose database connection strings, source URLs or payloads.
		fmt.Fprintf(os.Stderr, "administration operation failed (%T)\n", err)
		writeError(w, http.StatusInternalServerError, "The operation failed. Check the service status.")
	}
}

func requestQuery(w http.ResponseWriter, r *http.Request) (url.Values, bool) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid query parameters")
		return nil, false
	}
	return query, true
}

func listOptions(w http.ResponseWriter, r *http.Request) (model.ListOptions, bool) {
	query, ok := requestQuery(w, r)
	if !ok {
		return model.ListOptions{}, false
	}
	options := model.ListOptions{ProviderID: query.Get("provider"), RunID: query.Get("run"), Status: query.Get("status"), Query: query.Get("q"), Limit: 50}
	if len(options.ProviderID) > 128 || len(options.RunID) > 128 || len(options.Query) > 500 || len(options.Status) > 32 {
		writeError(w, http.StatusBadRequest, "Filter is too long")
		return options, false
	}
	if raw := query.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 200 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 200")
			return options, false
		}
		options.Limit = n
	}
	if raw := query.Get("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "offset must be a nonnegative integer")
			return options, false
		}
		options.Offset = n
	}
	return options, true
}

func normalizedList[T any](value model.List[T]) model.List[T] {
	if value.Items == nil {
		value.Items = []T{}
	}
	return value
}

func (s *server) overview(w http.ResponseWriter, r *http.Request) {
	result, err := s.options.Store.Overview(r.Context())
	if err != nil {
		respondError(w, err)
		return
	}
	definitions, err := s.options.Providers.List()
	if err != nil {
		respondError(w, err)
		return
	}
	result.Providers = len(definitions)
	if result.RecentRuns == nil {
		result.RecentRuns = []model.Run{}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) health(w http.ResponseWriter, r *http.Request) {
	if _, err := s.options.Store.Overview(r.Context()); err != nil {
		respondError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"database": "ok", "version": s.options.Version})
}

func (s *server) listProviders(w http.ResponseWriter, r *http.Request) {
	items, err := s.options.Providers.List()
	if err != nil {
		respondError(w, err)
		return
	}
	if items == nil {
		items = []model.ProviderSummary{}
	}
	ids := make([]string, len(items))
	for index := range items {
		ids[index] = items[index].ID
	}
	latest, err := s.options.Store.LatestRuns(r.Context(), ids)
	if err != nil {
		respondError(w, err)
		return
	}
	for index := range items {
		if items[index].Issues == nil {
			items[index].Issues = []string{}
		}
		if last, ok := latest[items[index].ID]; ok {
			items[index].LastRun = &last
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *server) listSchedules(w http.ResponseWriter, r *http.Request) {
	items, err := s.options.Jobs.Schedules(r.Context())
	if err != nil {
		respondError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *server) getProvider(w http.ResponseWriter, r *http.Request) {
	document, err := s.options.Providers.Get(r.PathValue("id"))
	if err != nil {
		respondError(w, err)
		return
	}
	if document.Issues == nil {
		document.Issues = []string{}
	}
	writeJSON(w, http.StatusOK, document)
}

func (s *server) validateProvider(w http.ResponseWriter, r *http.Request) {
	var input struct {
		JSON string `json:"json"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	result := s.options.Providers.Validate(input.JSON)
	if result.Issues == nil {
		result.Issues = []string{}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) saveProvider(w http.ResponseWriter, r *http.Request) {
	var input struct {
		JSON     string `json:"json"`
		Revision string `json:"revision"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	validation := s.options.Providers.Validate(input.JSON)
	if !validation.Valid {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "Invalid JSON configuration", "details": validation.Issues})
		return
	}
	document, err := s.options.Providers.Save(r.PathValue("id"), input.JSON, input.Revision)
	if err != nil {
		respondError(w, err)
		return
	}
	s.options.Jobs.Notify("provider", document.Provider.ID)
	if document.Issues == nil {
		document.Issues = []string{}
	}
	writeJSON(w, http.StatusOK, document)
}

func (s *server) deleteProvider(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Revision string `json:"revision"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	err := s.options.Providers.Delete(id, input.Revision, func() error {
		active, err := s.options.Store.HasActiveRun(r.Context(), id)
		if err != nil {
			return err
		}
		if active {
			return model.ErrBusy
		}
		return nil
	})
	if err != nil {
		respondError(w, err)
		return
	}
	s.options.Jobs.Notify("provider", id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) listSecrets(w http.ResponseWriter, r *http.Request) {
	items, err := s.options.Vault.List(r.Context())
	if err != nil {
		respondError(w, err)
		return
	}
	if items == nil {
		items = []model.SecretInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *server) putSecret(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Value string `json:"value"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if len(input.Value) > 64<<10 {
		writeError(w, http.StatusBadRequest, "Secret is too large")
		return
	}
	if err := s.options.Vault.Put(r.Context(), r.PathValue("name"), input.Value); err != nil {
		respondError(w, err)
		return
	}
	s.options.Jobs.Notify("secret", r.PathValue("name"))
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) deleteSecret(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	// Hold the interprocess registry lock until the store has atomically checked
	// durable run/webhook references and deleted the encrypted value.
	err := s.options.Providers.WithDefinitions(func(definitions map[string]model.ProviderDocument) error {
		for _, document := range definitions {
			references, err := document.Provider.SecretReferences()
			if err != nil {
				return err
			}
			for _, reference := range references {
				if reference == name {
					return model.ErrConflict
				}
			}
		}
		return s.options.Vault.Delete(r.Context(), name)
	})
	if errors.Is(err, model.ErrConflict) {
		writeError(w, http.StatusConflict, "This secret is still referenced by a provider, active or paused run, or webhook")
		return
	}
	if err != nil {
		respondError(w, err)
		return
	}
	s.options.Jobs.Notify("secret", name)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) startRun(w http.ResponseWriter, r *http.Request) {
	var request model.StartRun
	if !decodeJSON(w, r, &request) {
		return
	}
	run, err := s.options.Jobs.Enqueue(r.Context(), request)
	if err != nil {
		respondError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

func (s *server) listRuns(w http.ResponseWriter, r *http.Request) {
	options, ok := listOptions(w, r)
	if !ok {
		return
	}
	result, err := s.options.Store.ListRuns(r.Context(), options)
	if err != nil {
		respondError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, normalizedList(result))
}

func (s *server) getRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.options.Store.GetRun(r.Context(), r.PathValue("id"))
	if err != nil {
		respondError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *server) pauseRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.options.Jobs.Pause(r.Context(), r.PathValue("id"))
	if err != nil {
		respondError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *server) cancelRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.options.Jobs.Cancel(r.Context(), r.PathValue("id"))
	if err != nil {
		respondError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *server) resumeRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.options.Jobs.Resume(r.Context(), r.PathValue("id"))
	if err != nil {
		respondError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

func (s *server) runEvents(w http.ResponseWriter, r *http.Request) {
	options, ok := listOptions(w, r)
	if !ok {
		return
	}
	result, err := s.options.Store.Events(r.Context(), r.PathValue("id"), options)
	if err != nil {
		respondError(w, err)
		return
	}
	for i := range result.Items {
		connectors.SanitizeFailureDiagnostics(result.Items[i].Data)
	}
	writeJSON(w, http.StatusOK, normalizedList(result))
}

func (s *server) events(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache, no-store")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		return
	}
	select {
	case s.streams <- struct{}{}:
		defer func() { <-s.streams }()
	default:
		writeError(w, http.StatusServiceUnavailable, "Too many live update streams")
		return
	}
	controller := http.NewResponseController(w)
	notices, unsubscribe := s.options.Jobs.Subscribe()
	defer unsubscribe()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	if err := controller.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return
	}
	if _, err := io.WriteString(w, "retry: 3000\n: connected\n\n"); err != nil {
		return
	}
	if err := controller.Flush(); err != nil {
		return
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		var message []byte
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, ok := s.auth.current(r); !ok {
				return
			}
			message = []byte(": heartbeat\n\n")
		case notice, ok := <-notices:
			if !ok {
				return
			}
			if _, ok := s.auth.current(r); !ok {
				return
			}
			data, err := json.Marshal(notice)
			if err != nil {
				return
			}
			message = append(append([]byte("event: update\ndata: "), data...), '\n', '\n')
		}
		_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, err := w.Write(message); err != nil {
			return
		}
		if err := controller.Flush(); err != nil {
			return
		}
	}
}

func (s *server) listRaw(w http.ResponseWriter, r *http.Request) {
	options, ok := listOptions(w, r)
	if !ok {
		return
	}
	result, err := s.options.Store.ListRaw(r.Context(), options)
	if err != nil {
		respondError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, normalizedList(result))
}

func positiveID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "Invalid identifier")
		return 0, false
	}
	return id, true
}

func (s *server) rawDetail(w http.ResponseWriter, r *http.Request) {
	id, ok := positiveID(w, r)
	if !ok {
		return
	}
	record, err := s.options.Store.Raw(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}
	if !record.PayloadRetained {
		writeJSON(w, http.StatusOK, map[string]any{"record": record, "raw": nil, "byte_length": nil, "valid_utf8": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"record": record, "raw": string(record.Raw), "byte_length": len(record.Raw), "valid_utf8": utf8.Valid(record.Raw)})
}

func download(w http.ResponseWriter, r *http.Request, data []byte, filename string) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, filename, time.Time{}, bytes.NewReader(data))
}

func (s *server) rawDownload(w http.ResponseWriter, r *http.Request) {
	id, ok := positiveID(w, r)
	if !ok {
		return
	}
	record, err := s.options.Store.Raw(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}
	if !record.PayloadRetained {
		writeError(w, http.StatusNotFound, "Original record payload was not stored")
		return
	}
	download(w, r, record.Raw, fmt.Sprintf("record-%d.bin", id))
}

func (s *server) pageDownload(w http.ResponseWriter, r *http.Request) {
	id, ok := positiveID(w, r)
	if !ok {
		return
	}
	data, _, err := s.options.Store.RawPage(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}
	download(w, r, data, fmt.Sprintf("response-%d.bin", id))
}

func (s *server) torrents(w http.ResponseWriter, r *http.Request) {
	options, ok := torrentListOptions(w, r)
	if !ok {
		return
	}
	result, err := s.options.Store.ListTorrents(r.Context(), options)
	if err != nil {
		respondError(w, err)
		return
	}
	if err := s.projectTorrentFields(result.Items); err != nil {
		respondError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, normalizedList(result))
}

func staticFrontend(frontend fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" || name == "." {
			name = "index.html"
		}
		if !fs.ValidPath(name) || strings.Contains(name, "\\") {
			http.NotFound(w, r)
			return
		}
		file, err := frontend.Open(name)
		if err != nil {
			// Only client navigation routes get the SPA entrypoint, never missing assets.
			if path.Ext(name) != "" || strings.HasPrefix(name, "assets/") {
				http.NotFound(w, r)
				return
			}
			name = "index.html"
			file, err = frontend.Open(name)
		}
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}
		if name == "index.html" {
			w.Header().Set("Cache-Control", "no-cache")
		} else if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		if seeker, ok := file.(io.ReadSeeker); ok {
			http.ServeContent(w, r, name, info.ModTime(), seeker)
			return
		}
		// An arbitrary fs.FS need not provide Seek; production embed.FS does.
		data, err := io.ReadAll(file)
		if err != nil {
			http.Error(w, "Asset read failed", http.StatusInternalServerError)
			return
		}
		http.ServeContent(w, r, name, info.ModTime(), bytes.NewReader(data))
	})
}

func recoverRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if value := recover(); value != nil {
				if value == http.ErrAbortHandler {
					panic(value)
				}
				fmt.Fprintf(os.Stderr, "administration handler panic (%T)\n", value)
				writeError(w, http.StatusInternalServerError, "Internal service error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
