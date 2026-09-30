package backups

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"filippo.io/age"
	"github.com/moodiness/ingest/internal/model"
	"github.com/moodiness/ingest/internal/providers"
	"github.com/moodiness/ingest/internal/store"
)

type Options struct {
	Store       *store.Store
	Registry    *providers.Registry
	DatabaseURL string
	StateDir    string
	MasterKey   []byte
	PGDump      string
	PGRestore   string
}
type Service struct {
	options    Options
	tools      tools
	root       *os.Root
	directory  string
	masterKey  []byte
	owner      string
	mu         sync.Mutex
	started    bool
	closed     bool
	cancel     context.CancelFunc
	done       chan struct{}
	wake       chan struct{}
	err        error
	identities map[string]string
}
type Capability struct {
	Available       bool   `json:"available"`
	Message         string `json:"message"`
	PostgreSQLMajor int    `json:"postgresql_major,omitempty"`
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("secure randomness unavailable")
	}
	return hex.EncodeToString(b[:])
}
func New(o Options) (*Service, error) {
	if o.Store == nil || o.Registry == nil || o.DatabaseURL == "" || o.StateDir == "" || len(o.MasterKey) != 32 {
		return nil, errors.New("backup store, registry, database, private state and 32-byte master key are required")
	}
	if _, err := postgresEnv(o.DatabaseURL); err != nil {
		return nil, err
	}
	if err := privateDirectory(o.StateDir); err != nil {
		return nil, err
	}
	directory := filepath.Join(o.StateDir, "backups")
	if err := os.Mkdir(directory, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, errors.New("cannot create private backup directory")
	}
	if err := privateDirectory(directory); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, errors.New("cannot open private backup directory")
	}
	key := append([]byte(nil), o.MasterKey...)
	o.MasterKey = nil
	return &Service{options: o, tools: configuredTools(o.PGDump, o.PGRestore), root: root, directory: directory, masterKey: key, owner: newID(), done: make(chan struct{}), wake: make(chan struct{}, 1), identities: map[string]string{}}, nil
}
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.closed {
		return errors.New("backup service is already started or closed")
	}
	worker, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.started = true
	go func() { defer close(s.done); s.run(worker) }()
	return nil
}
func (s *Service) Done() <-chan struct{} { return s.done }
func (s *Service) Err() error            { s.mu.Lock(); defer s.mu.Unlock(); return s.err }
func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	started := s.started
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	if started {
		select {
		case <-s.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	clear(s.masterKey)
	clear(s.identities)
	return s.root.Close()
}
func (s *Service) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *Service) Capability(ctx context.Context) Capability {
	conn, err := connect(ctx, s.options.DatabaseURL)
	if err != nil {
		return Capability{Message: err.Error()}
	}
	defer closeConn(conn)
	v, err := checkVersion(ctx, s.tools, conn)
	if err != nil {
		return Capability{Message: err.Error()}
	}
	var canCreate bool
	if err = conn.QueryRow(ctx, `SELECT rolcreatedb OR rolsuper FROM pg_roles WHERE rolname=current_user`).Scan(&canCreate); err != nil {
		return Capability{Message: "Database role capabilities are unavailable"}
	}
	msg := "PostgreSQL tools are ready. Verification creates and destroys an isolated database."
	if !canCreate {
		msg = "Backup tools are ready. Restore verification requires CREATEDB permission on this database role."
	}
	return Capability{Available: true, Message: msg, PostgreSQLMajor: v}
}
func (s *Service) GenerateRecovery(ctx context.Context, revision int64) (string, string, error) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		return "", "", errors.New("cannot generate recovery identity")
	}
	recipient := identity.Recipient().String()
	if err = s.options.Store.SetBackupRecipient(ctx, recipient, revision); err != nil {
		return "", "", err
	}
	return identity.String(), recipient, nil
}
func (s *Service) Queue(ctx context.Context) (model.BackupJob, error) {
	j, err := s.options.Store.QueueBackup(ctx, newID(), "backup", "", "")
	if err == nil {
		s.signal()
	}
	return j, err
}
func (s *Service) Verify(ctx context.Context, backupID, identity string) (model.BackupJob, error) {
	if !identifier.MatchString(backupID) {
		return model.BackupJob{}, model.ErrNotFound
	}
	if _, err := age.ParseX25519Identity(identity); err != nil {
		return model.BackupJob{}, model.ErrInvalid
	}
	backup, err := s.options.Store.BackupJob(ctx, backupID)
	if err != nil {
		return model.BackupJob{}, err
	}
	if backup.Kind != "backup" || backup.Status != "succeeded" || len(backup.SHA256) != 64 {
		return model.BackupJob{}, model.ErrInvalid
	}
	id := newID()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started || s.closed {
		return model.BackupJob{}, errors.New("backup service is unavailable")
	}
	j, err := s.options.Store.QueueBackup(ctx, id, "verify", backupID, s.owner)
	if err != nil {
		return j, err
	}
	s.identities[id] = identity
	s.signal()
	return j, nil
}
func (s *Service) Download(ctx context.Context, id string) (*os.File, model.BackupJob, error) {
	var j model.BackupJob
	if !identifier.MatchString(id) {
		return nil, j, model.ErrNotFound
	}
	j, err := s.options.Store.BackupJob(ctx, id)
	if err != nil {
		return nil, j, err
	}
	if j.Kind != "backup" || j.Status != "succeeded" {
		return nil, j, model.ErrNotFound
	}
	info, err := s.root.Lstat(id + ".age")
	if err != nil || !privateArchiveFile(info) {
		return nil, j, errors.New("encrypted backup file is unavailable")
	}
	file, err := s.root.OpenFile(id+".age", os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, j, errors.New("encrypted backup file is unavailable")
	}
	opened, err := file.Stat()
	if err != nil || !privateArchiveFile(opened) || !os.SameFile(info, opened) || opened.Size() != j.Bytes {
		file.Close()
		return nil, j, errors.New("encrypted backup file integrity failed")
	}
	return file, j, nil
}
func privateArchiveFile(info os.FileInfo) bool {
	if info == nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink == 1
}
func (s *Service) phase(ctx context.Context, id, phase string) error {
	return s.options.Store.HeartbeatBackup(ctx, id, s.owner, phase)
}
func (s *Service) run(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		err := s.options.Store.RecoverBackups(ctx)
		if err == nil {
			err = s.recoverArtifacts(ctx)
		}
		if err == nil {
			err = s.options.Store.ScheduleBackup(ctx, newID())
		}
		if err == nil {
			j, e := s.options.Store.ClaimBackup(ctx, s.owner)
			if e == nil {
				s.execute(ctx, j)
				continue
			}
			if !errors.Is(e, model.ErrNotFound) {
				err = e
			}
		}
		s.mu.Lock()
		if err != nil {
			s.err = errors.New("backup scheduler requires database access or isolated-target cleanup")
		} else {
			s.err = nil
		}
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-ticker.C:
		}
	}
}
func (s *Service) execute(parent context.Context, j model.BackupJob) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Hour)
	defer cancel()
	beatDone := make(chan struct{})
	go func() {
		defer close(beatDone)
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if s.phase(ctx, j.ID, "") != nil {
					cancel()
					return
				}
			}
		}
	}()
	var bytes int64
	var checksum string
	var report map[string]any
	var err error
	if j.Kind == "backup" {
		bytes, checksum, report, err = s.createArchive(ctx, j)
	} else {
		s.mu.Lock()
		identity := s.identities[j.ID]
		delete(s.identities, j.ID)
		s.mu.Unlock()
		if identity == "" {
			err = errors.New("verification recovery identity is no longer available")
		} else {
			report, err = s.verifyArchive(ctx, j, identity)
		}
	}
	cancel()
	<-beatDone
	status, code := "succeeded", ""
	if err != nil {
		status, code = "failed", "unknown"
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			code = "timeout"
		}
		report = map[string]any{"reason": safeFailure(err)}
	}
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer finishCancel()
	if finishErr := s.options.Store.FinishBackup(finishCtx, j.ID, s.owner, status, code, bytes, checksum, report); finishErr != nil {
		s.mu.Lock()
		s.err = errors.New("backup result could not be persisted; lease recovery will report interruption")
		s.mu.Unlock()
	}
}
func safeFailure(err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "The operation was interrupted or exceeded its two-hour deadline."
	}
	// All archive/restore errors are deliberately static, but registry/store
	// errors may wrap filesystem or database values. Do not expose those here.
	return "The operation failed. Check PostgreSQL tool compatibility, permissions, disk space and the recovery identity. No live data was restored or replaced."
}
func (s *Service) verifyArchive(ctx context.Context, j model.BackupJob, identity string) (result map[string]any, resultErr error) {
	file, backup, err := s.Download(ctx, j.BackupID)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if err = s.phase(ctx, j.ID, "checking_integrity"); err != nil {
		return nil, err
	}
	sum := sha256.New()
	if _, err = io.Copy(sum, &contextReader{ctx: ctx, reader: file}); err != nil {
		return nil, errors.New("cannot verify backup checksum")
	}
	if hex.EncodeToString(sum.Sum(nil)) != backup.SHA256 {
		return nil, errors.New("encrypted backup checksum does not match durable metadata")
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return nil, errors.New("cannot read encrypted backup")
	}
	name := ".verify-" + j.ID
	if err = s.root.Mkdir(name, 0700); err != nil {
		return nil, errors.New("cannot create isolated verification workspace")
	}
	workspace := filepath.Join(s.directory, name)
	defer func() {
		if e := s.root.RemoveAll(name); e != nil {
			resultErr = errors.New("cannot remove private verification workspace")
		}
	}()
	if err = s.phase(ctx, j.ID, "restoring_isolated_database"); err != nil {
		return nil, err
	}
	report, err := restoreInto(ctx, RestoreOptions{Identity: identity, DatabaseURL: s.options.DatabaseURL, TargetDatabase: "scraper_verify_" + j.ID, PGDump: s.tools.dump, PGRestore: s.tools.restore}, file, workspace, true)
	if err != nil {
		return nil, err
	}
	return map[string]any{"verified": report.Verified, "tables": report.Tables, "source_files": report.SourceFiles, "secrets": report.Secrets, "postgresql_major": report.PostgreSQLMajor, "cleanup": "Isolated database and plaintext workspace removed."}, nil
}
