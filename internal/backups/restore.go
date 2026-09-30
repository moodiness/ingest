package backups

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5"
	"github.com/moodiness/ingest/internal/store"
	"github.com/moodiness/ingest/internal/vault"
)

type RestoreOptions struct {
	ArchivePath    string
	Identity       string
	DatabaseURL    string // Maintenance connection on the destination server; never a subprocess argument.
	TargetDatabase string
	StateDir       string // Must not exist. Only created/owned paths are ever removed on failure.
	PGDump         string
	PGRestore      string
}
type RestoreReport struct {
	Tables          int  `json:"tables"`
	SourceFiles     int  `json:"source_files"`
	Secrets         int  `json:"secrets"`
	PostgreSQLMajor int  `json:"postgresql_major"`
	Verified        bool `json:"verified"`
}

func containsBackslash(s string) bool { return strings.ContainsAny(s, "\\\x00") }

// Historical source files are accepted only for exact-byte quarantine, never
// as active configuration. Current snapshots contain JSON definitions.
func hasSourceArchiveSuffix(s string) bool {
	s = strings.ToLower(s)
	return strings.HasSuffix(s, ".json") || strings.HasSuffix(s, ".yaml") || strings.HasSuffix(s, ".yml")
}

func privateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return errors.New("backup workspace must be a private real directory")
	}
	return nil
}
func extractArchive(ctx context.Context, archive io.Reader, identity, workspace string) (manifest, error) {
	var m manifest
	key, err := age.ParseX25519Identity(strings.TrimSpace(identity))
	if err != nil {
		return m, errors.New("invalid age recovery identity")
	}
	decrypted, err := age.Decrypt(archive, key)
	if err != nil {
		return m, errors.New("recovery identity cannot decrypt this backup")
	}
	zipPath := filepath.Join(workspace, "archive.zip")
	file, err := os.OpenFile(zipPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return m, errors.New("cannot create private recovery workspace")
	}
	n, copyErr := io.Copy(file, io.LimitReader(&contextReader{ctx: ctx, reader: decrypted}, maxArchiveBytes+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || n > maxArchiveBytes {
		return m, errors.New("encrypted archive is damaged or exceeds recovery limits")
	}
	z, err := zip.OpenReader(zipPath)
	if err != nil {
		return m, errors.New("invalid recovery archive")
	}
	defer z.Close()
	if len(z.File) > 10003 {
		return m, errors.New("recovery archive contains too many files")
	}
	entries := make(map[string]*zip.File, len(z.File))
	var total uint64
	for _, f := range z.File {
		if !safeEntry(f.Name) || !f.Mode().IsRegular() || entries[f.Name] != nil {
			return m, errors.New("recovery archive contains unsafe or duplicate paths")
		}
		total += f.UncompressedSize64
		if total > uint64(maxArchiveBytes) || f.UncompressedSize64 > uint64(maxArchiveBytes) {
			return m, errors.New("recovery archive exceeds extraction limits")
		}
		if f.Name != "database.dump" && f.UncompressedSize64 > 8<<20 {
			return m, errors.New("recovery metadata exceeds extraction limits")
		}
		entries[f.Name] = f
	}
	mf := entries["manifest.json"]
	if mf == nil || mf.UncompressedSize64 > maxManifestBytes {
		return m, errors.New("recovery manifest is missing or oversized")
	}
	mr, err := mf.Open()
	if err != nil {
		return m, errors.New("cannot read recovery manifest")
	}
	data, err := io.ReadAll(io.LimitReader(mr, maxManifestBytes+1))
	mr.Close()
	if err != nil {
		return m, errors.New("cannot read recovery manifest")
	}
	decoder := json.NewDecoder(bytesReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&m); err != nil || m.Version != 1 || len(m.Entries)+1 != len(entries) || m.PostgreSQLMajor < 18 {
		return m, errors.New("unsupported or incomplete recovery manifest")
	}
	var trailing any
	if err = decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return m, errors.New("invalid recovery manifest")
	}
	if m.Entries["database.dump"].Bytes <= 0 || m.Entries["master.key"].Bytes != 32 || len(m.Migrations) < 12 {
		return m, errors.New("recovery archive is missing required database or key metadata")
	}
	// Version 12 introduced recovery support; later schema migrations do not
	// change this archive format. Require the complete baseline, not a frozen
	// latest version, and compare the restored database's exact sequence below.
	for i, version := range m.Migrations {
		if version != i+1 {
			return m, errors.New("recovery archive has an incomplete migration history")
		}
	}
	if m.Migrations[len(m.Migrations)-1] > store.LatestSchemaVersion() {
		return m, errors.New("recovery archive requires a newer application schema")
	}
	if err = os.Mkdir(filepath.Join(workspace, "providers"), 0700); err != nil {
		return m, errors.New("cannot create recovery source directory")
	}
	for _, name := range sortedEntryNames(m.Entries) {
		f := entries[name]
		expected := m.Entries[name]
		if name == "manifest.json" || f == nil || expected.Bytes < 0 || uint64(expected.Bytes) != f.UncompressedSize64 || len(expected.SHA256) != 64 {
			return m, errors.New("recovery manifest does not match archive")
		}
		reader, e := f.Open()
		if e != nil {
			return m, errors.New("cannot extract recovery entry")
		}
		dest, e := os.OpenFile(filepath.Join(workspace, filepath.FromSlash(name)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			reader.Close()
			return m, errors.New("cannot create private recovery entry")
		}
		sum := sha256.New()
		written, e := io.Copy(io.MultiWriter(dest, sum), &contextReader{ctx: ctx, reader: io.LimitReader(reader, expected.Bytes+1)})
		reader.Close()
		syncErr := dest.Sync()
		closeErr := dest.Close()
		if e != nil || syncErr != nil || closeErr != nil || written != expected.Bytes || hex.EncodeToString(sum.Sum(nil)) != expected.SHA256 {
			return m, errors.New("recovery archive integrity check failed")
		}
	}
	return m, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// Restore trusts only operator-selected archives. PostgreSQL archives contain
// executable SQL. Never expose this operation to browser uploads.
func Restore(ctx context.Context, o RestoreOptions) (report RestoreReport, resultErr error) {
	if !targetName.MatchString(o.TargetDatabase) || o.TargetDatabase == "postgres" || strings.HasPrefix(o.TargetDatabase, "template") || o.StateDir == "" {
		return report, errors.New("restore requires a new explicit database name and state directory")
	}
	// Refuse an existing state path, including an empty one or a symbolic link.
	if err := os.Mkdir(o.StateDir, 0700); err != nil {
		return report, errors.New("recovery state directory must not already exist and its parent must exist")
	}
	keep := false
	defer func() {
		if !keep {
			if e := os.RemoveAll(o.StateDir); e != nil {
				resultErr = errors.New("recovery cleanup failed; the newly created state directory requires operator cleanup")
			}
		}
	}()
	if err := privateDirectory(o.StateDir); err != nil {
		return report, err
	}
	workspace, err := os.MkdirTemp(o.StateDir, ".recovery-")
	if err != nil {
		return report, errors.New("cannot create recovery workspace")
	}
	defer func() {
		if e := os.RemoveAll(workspace); e != nil {
			resultErr = errors.New("recovery plaintext cleanup failed; the private workspace requires operator cleanup")
		}
	}()
	archive, err := os.Open(o.ArchivePath)
	if err != nil {
		return report, errors.New("cannot open encrypted recovery archive")
	}
	defer archive.Close()
	report, err = restoreInto(ctx, o, archive, workspace, false)
	if err == nil {
		keep = true
	}
	return report, err
}
func restoreInto(ctx context.Context, o RestoreOptions, archive io.Reader, workspace string, verifyOnly bool) (report RestoreReport, resultErr error) {
	m, err := extractArchive(ctx, archive, o.Identity, workspace)
	if err != nil {
		return report, err
	}
	admin, err := connect(ctx, o.DatabaseURL)
	if err != nil {
		return report, err
	}
	defer closeConn(admin)
	major, err := checkVersion(ctx, configuredTools(o.PGDump, o.PGRestore), admin)
	if err != nil {
		return report, err
	}
	if major != m.PostgreSQLMajor {
		return report, errors.New("recovery destination PostgreSQL major must match the backup")
	}
	var exists bool
	if err = admin.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", o.TargetDatabase).Scan(&exists); err != nil {
		return report, errors.New("cannot inspect recovery destination")
	}
	if exists {
		return report, errors.New("recovery database already exists; it will never be overwritten")
	}
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{o.TargetDatabase}.Sanitize()+" TEMPLATE template0"); err != nil {
		return report, errors.New("cannot create isolated recovery database; CREATEDB permission is required")
	}
	keep := false
	defer func() {
		if keep {
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cleanupConn, e := connect(cleanup, o.DatabaseURL)
		if e != nil {
			resultErr = errors.New("recovery cleanup failed; an isolated recovery database requires operator cleanup")
			return
		}
		defer closeConn(cleanupConn)
		if _, e = cleanupConn.Exec(cleanup, "DROP DATABASE "+pgx.Identifier{o.TargetDatabase}.Sanitize()+" WITH (FORCE)"); e != nil {
			resultErr = errors.New("recovery cleanup failed; an isolated recovery database requires operator cleanup")
		}
	}()
	if verifyOnly {
		marker := "scraper.backup.verify:" + o.TargetDatabase
		if _, err = admin.Exec(ctx, "COMMENT ON DATABASE "+pgx.Identifier{o.TargetDatabase}.Sanitize()+" IS '"+marker+"'"); err != nil {
			return report, errors.New("cannot record isolated verification ownership")
		}
	}
	targetURL, err := databaseURLFor(o.DatabaseURL, o.TargetDatabase)
	if err != nil {
		return report, err
	}
	t := configuredTools(o.PGDump, o.PGRestore)
	if err = runPG(ctx, t.restore, targetURL, io.Discard, "--exit-on-error", "--no-owner", "--no-privileges", "--dbname="+o.TargetDatabase, filepath.Join(workspace, "database.dump")); err != nil {
		return report, err
	}
	restored, err := connect(ctx, targetURL)
	if err != nil {
		return report, err
	}
	defer closeConn(restored)
	tx, err := restored.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return report, errors.New("cannot inspect restored database")
	}
	counts, e := tableCounts(ctx, tx)
	versions, ve := migrationVersions(ctx, tx)
	_ = tx.Rollback(ctx)
	if e != nil || ve != nil || !reflect.DeepEqual(counts, m.Tables) || !reflect.DeepEqual(versions, m.Migrations) {
		return report, errors.New("restored schema or record counts do not match the backup snapshot")
	}
	key, err := os.ReadFile(filepath.Join(workspace, "master.key"))
	if err != nil || len(key) != 32 {
		return report, errors.New("recovery master key is invalid")
	}
	defer clear(key)
	db, err := store.Open(ctx, targetURL)
	if err != nil {
		return report, errors.New("cannot inspect restored vault")
	}
	defer db.Close()
	v, err := vault.New(ctx, db, key)
	if err != nil {
		return report, errors.New("restored vault key authentication failed")
	}
	secrets, err := v.List(ctx)
	if err != nil {
		return report, errors.New("cannot inspect restored vault")
	}
	for _, secret := range secrets {
		if _, err = v.Resolve(ctx, secret.Name); err != nil {
			return report, errors.New("restored vault ciphertext cannot be decrypted")
		}
	}
	var active, pending []byte
	if err = restored.QueryRow(ctx, `SELECT active_secret,pending_secret FROM ingest.admin_security WHERE singleton`).Scan(&active, &pending); err != nil {
		return report, errors.New("cannot inspect restored administrator security")
	}
	for _, sealed := range []struct {
		purpose string
		data    []byte
	}{{"admin-mfa-active-v1", active}, {"admin-mfa-pending-v1", pending}} {
		if len(sealed.data) > 0 {
			plain, e := v.Open(sealed.purpose, sealed.data)
			if e != nil {
				return report, errors.New("restored administrator ciphertext cannot be decrypted")
			}
			clear(plain)
		}
	}
	sourceCount := 0
	for name := range m.Entries {
		if strings.HasPrefix(name, "providers/") {
			sourceCount++
		}
	}
	report = RestoreReport{Tables: len(counts), SourceFiles: sourceCount, Secrets: len(secrets), PostgreSQLMajor: major, Verified: true}
	if verifyOnly {
		return report, nil
	}
	// Only this freshly created destination is modified. Original source data is
	// quarantined, not rewritten: no source can run until explicitly reviewed.
	_, err = restored.Exec(ctx, `BEGIN;
 DELETE FROM ingest.admin_sessions;
 UPDATE ingest.admin_security SET password_hash=NULL,pending_secret=NULL,pending_expires_at=NULL,pending_session_id=NULL;
 UPDATE ingest.runs SET status='paused',cancel_requested=FALSE,error='Restored; review before resuming' WHERE status IN ('queued','running');
 DO $safety$ BEGIN
  IF EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='ingest' AND table_name='runs' AND column_name='pause_reason') THEN
   UPDATE ingest.runs SET pause_reason='manual',pause_requested=TRUE,cancel_requested=FALSE WHERE status='paused';
  END IF;
  IF EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='ingest' AND table_name='collection_settings' AND column_name='auto_resume_interrupted') THEN
   UPDATE ingest.collection_settings SET auto_resume_interrupted=FALSE,revision=revision+1;
  END IF;
 END $safety$;
 UPDATE ingest.schedule_clocks SET active=FALSE,next_run_at=NULL;
 UPDATE ingest.webhooks SET enabled=FALSE;
 UPDATE ingest.webhook_deliveries SET status='cancelled',next_attempt_at=NULL,lease_until=NULL,lease_token=NULL WHERE status IN ('pending','delivering');
 UPDATE ingest.shares SET enabled=FALSE;
 UPDATE ingest.backup_settings SET enabled=FALSE,next_run_at=NULL,revision=revision+1;
 UPDATE ingest.backup_jobs SET status='failed',phase='failed',failure_code='stalled',lease_until=NULL,finished_at=now() WHERE status IN ('queued','running');
 UPDATE ingest.backup_jobs SET cleanup_pending=FALSE;
 COMMIT;`)
	if err != nil {
		return report, errors.New("cannot disable restored external automation")
	}
	if err = os.Mkdir(filepath.Join(o.StateDir, "providers"), 0700); err != nil {
		return report, errors.New("cannot initialize disabled source registry")
	}
	if err = os.Rename(filepath.Join(workspace, "providers"), filepath.Join(o.StateDir, "recovered-sources")); err != nil {
		return report, errors.New("cannot preserve restored source definitions")
	}
	encoded := []byte(base64.StdEncoding.EncodeToString(key) + "\n")
	defer clear(encoded)
	keyFile, err := os.OpenFile(filepath.Join(o.StateDir, "vault.key"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return report, errors.New("cannot persist restored vault key")
	}
	_, writeErr := keyFile.Write(encoded)
	syncErr := keyFile.Sync()
	closeErr := keyFile.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return report, errors.New("cannot persist restored vault key")
	}
	for _, directory := range []string{filepath.Join(o.StateDir, "recovered-sources"), filepath.Join(o.StateDir, "providers"), o.StateDir, filepath.Dir(filepath.Clean(o.StateDir))} {
		dir, e := os.Open(directory)
		if e != nil {
			return report, errors.New("cannot sync restored state")
		}
		e = dir.Sync()
		dir.Close()
		if e != nil {
			return report, errors.New("cannot sync restored state")
		}
	}
	keep = true
	return report, nil
}
