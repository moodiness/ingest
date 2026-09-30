package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moodiness/ingest/internal/model"
)

func (s *server) registerBackupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/backups", s.auth.protect(s.backupOverview))
	mux.HandleFunc("PUT /api/backups/settings", s.auth.protect(s.backupSettings))
	mux.HandleFunc("POST /api/backups/recovery", s.auth.protect(s.backupRecovery))
	mux.HandleFunc("POST /api/backups", s.auth.protect(s.backupQueue))
	mux.HandleFunc("GET /api/backups/{id}", s.auth.protect(s.backupDetail))
	mux.HandleFunc("GET /api/backups/{id}/download", s.auth.protect(s.backupDownload))
	mux.HandleFunc("POST /api/backups/{id}/verify", s.auth.protect(s.backupVerify))
}
func (s *server) backupAvailable(w http.ResponseWriter) bool {
	w.Header().Set("Cache-Control", "no-store")
	if s.options.Backups == nil {
		writeError(w, http.StatusServiceUnavailable, "Backup service is unavailable")
		return false
	}
	return true
}
func backupError(w http.ResponseWriter, err error) {
	if errors.Is(err, model.ErrBusy) {
		writeError(w, http.StatusConflict, "A backup or restore verification is already queued or running")
		return
	}
	respondError(w, err)
}
func (s *server) backupOverview(w http.ResponseWriter, r *http.Request) {
	if !s.backupAvailable(w) {
		return
	}
	query, ok := requestQuery(w, r)
	if !ok {
		return
	}
	limit, offset := 25, 0
	if raw := query.Get("limit"); raw != "" {
		v, e := strconv.Atoi(raw)
		if e != nil || v < 1 || v > 100 {
			writeError(w, 422, "Limit must be between 1 and 100")
			return
		}
		limit = v
	}
	if raw := query.Get("offset"); raw != "" {
		v, e := strconv.Atoi(raw)
		if e != nil || v < 0 {
			writeError(w, 422, "Offset must be nonnegative")
			return
		}
		offset = v
	}
	settings, err := s.options.Store.BackupSettings(r.Context())
	if err != nil {
		backupError(w, err)
		return
	}
	jobs, total, err := s.options.Store.BackupJobs(r.Context(), limit, offset)
	if err != nil {
		backupError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"settings": settings, "items": jobs, "total": total, "limit": limit, "offset": offset, "capability": s.options.Backups.Capability(r.Context())})
}
func (s *server) backupSettings(w http.ResponseWriter, r *http.Request) {
	if !s.backupAvailable(w) {
		return
	}
	var body struct {
		Enabled  bool   `json:"enabled"`
		TimeUTC  string `json:"time_utc"`
		Revision int64  `json:"revision"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := s.options.Store.ConfigureBackups(r.Context(), body.Enabled, body.TimeUTC, body.Revision); err != nil {
		backupError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *server) backupRecovery(w http.ResponseWriter, r *http.Request) {
	if !s.backupAvailable(w) {
		return
	}
	var body struct {
		Revision int64 `json:"revision"`
		Confirm  bool  `json:"confirm"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !body.Confirm {
		writeError(w, 422, "Confirm you will save the recovery identity outside this server")
		return
	}
	identity, recipient, err := s.options.Backups.GenerateRecovery(r.Context(), body.Revision)
	if err != nil {
		backupError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"identity": identity, "recipient": recipient})
}
func (s *server) backupQueue(w http.ResponseWriter, r *http.Request) {
	if !s.backupAvailable(w) {
		return
	}
	job, err := s.options.Backups.Queue(r.Context())
	if err != nil {
		backupError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}
func (s *server) backupDetail(w http.ResponseWriter, r *http.Request) {
	if !s.backupAvailable(w) {
		return
	}
	job, err := s.options.Store.BackupJob(r.Context(), r.PathValue("id"))
	if err != nil {
		backupError(w, err)
		return
	}
	writeJSON(w, 200, job)
}
func (s *server) backupDownload(w http.ResponseWriter, r *http.Request) {
	if !s.backupAvailable(w) {
		return
	}
	file, job, err := s.options.Backups.Download(r.Context(), r.PathValue("id"))
	if err != nil {
		backupError(w, err)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="backup-`+job.ID+`.age"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	serveBackupContent(w, r, file, "backup-"+job.ID+".age", job.CreatedAt, s.options.Backups.Done())
}

const backupDownloadIdleTimeout = 30 * time.Second
const backupDownloadChunkSize = 32 << 10

// Keep ServeContent's conditional/HEAD/Range support while replacing the
// server-wide absolute write deadline with a bounded, sliding progress deadline.
func serveBackupContent(w http.ResponseWriter, r *http.Request, file *os.File, name string, modified time.Time, shutdown <-chan struct{}) {
	stream := &backupStreamWriter{ResponseWriter: w, controller: http.NewResponseController(w)}
	if err := stream.renew(); err != nil {
		writeError(w, http.StatusServiceUnavailable, "Backup streaming deadlines are unavailable")
		return
	}
	finished, watcherDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-r.Context().Done():
		case <-shutdown:
		case <-finished:
			return
		}
		stream.cancel()
		// The private immutable regular file belongs only to this request.
		// Closing it also interrupts any pending filesystem read.
		_ = file.Close()
	}()
	defer func() {
		close(finished)
		<-watcherDone
	}()
	http.ServeContent(stream, r, name, modified, file)
	if stream.failed() {
		// Never turn a failed partial response into an apparently complete
		// download or let HTTP/2 finish it with a clean END_STREAM.
		panic(http.ErrAbortHandler)
	}
}

type backupStreamWriter struct {
	http.ResponseWriter
	controller *http.ResponseController
	mu         sync.Mutex
	stopped    bool
	err        error
}

func (w *backupStreamWriter) renew() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return context.Canceled
	}
	if w.err == nil {
		w.err = w.controller.SetWriteDeadline(time.Now().Add(backupDownloadIdleTimeout))
	}
	return w.err
}

func (w *backupStreamWriter) cancel() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopped = true
	w.err = errors.Join(context.Canceled, w.controller.SetWriteDeadline(time.Now()))
}

func (w *backupStreamWriter) failed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err != nil
}

func (w *backupStreamWriter) Write(p []byte) (written int, err error) {
	// Deliberately do not expose ReaderFrom: sendfile would bypass renewal.
	for len(p) > 0 {
		if err = w.renew(); err != nil {
			return written, err
		}
		chunk := p[:min(len(p), backupDownloadChunkSize)]
		n, writeErr := w.ResponseWriter.Write(chunk)
		written += n
		if writeErr == nil && n != len(chunk) {
			writeErr = io.ErrShortWrite
		}
		if writeErr == nil {
			// Flush each bounded chunk so buffered writes cannot masquerade as
			// transport progress. The same idle deadline bounds Write + Flush.
			writeErr = w.controller.Flush()
		}
		if writeErr != nil {
			w.mu.Lock()
			w.err = writeErr
			w.mu.Unlock()
			return written, writeErr
		}
		p = p[n:]
	}
	return written, nil
}
func (s *server) backupVerify(w http.ResponseWriter, r *http.Request) {
	if !s.backupAvailable(w) {
		return
	}
	var body struct {
		Identity string `json:"identity"`
		Confirm  bool   `json:"confirm"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !body.Confirm || len(body.Identity) > 256 {
		writeError(w, 422, "Confirm isolated restore verification and supply the recovery identity")
		return
	}
	job, err := s.options.Backups.Verify(r.Context(), r.PathValue("id"), strings.TrimSpace(body.Identity))
	body.Identity = ""
	if err != nil {
		backupError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}
