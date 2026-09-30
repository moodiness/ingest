package backups

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/moodiness/ingest/internal/model"
)

// Recovery touches only artifacts named by durable failed jobs. Row locks bind
// one replica to each cleanup; database markers prove verification ownership.
func (s *Service) recoverArtifacts(ctx context.Context) error {
	for range 100 {
		found, err := s.options.Store.ReconcileBackupArtifact(ctx, func(ctx context.Context, j model.BackupJob) (bool, error) {
			if !identifier.MatchString(j.ID) {
				return false, errors.New("invalid backup recovery record")
			}
			if j.Kind == "verify" {
				cleanup, cancel := context.WithTimeout(ctx, 45*time.Second)
				defer cancel()
				return false, s.cleanVerificationTarget(cleanup, j)
			}
			if j.Kind != "backup" {
				return false, errors.New("invalid backup recovery kind")
			}
			recovery, cancel := context.WithTimeout(ctx, 2*time.Hour)
			defer cancel()
			return s.recoverArchive(recovery, j)
		})
		if err != nil || !found {
			return err
		}
	}
	return nil
}

func (s *Service) recoverArchive(ctx context.Context, j model.BackupJob) (bool, error) {
	partial, final := j.ID+".partial", j.ID+".age"
	if j.SHA256 == "" {
		info, err := s.root.Lstat(partial)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil || !privateArchiveFile(info) {
			return false, errors.New("interrupted archive ownership cannot be proven")
		}
		// No completion metadata was committed. Only the incomplete temporary
		// file is disposable; even an unexpected final archive remains untouched.
		return false, s.root.Remove(partial)
	}
	name := final
	info, err := s.root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		name = partial
		info, err = s.root.Lstat(name)
	}
	if err != nil || !privateArchiveFile(info) {
		return false, errors.New("completed encrypted archive is unavailable")
	}
	file, err := s.root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return false, errors.New("cannot open completed encrypted archive")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !privateArchiveFile(opened) || !os.SameFile(info, opened) || opened.Size() != j.Bytes {
		return false, errors.New("completed encrypted archive integrity failed")
	}
	sum := sha256.New()
	if _, err = io.Copy(sum, &contextReader{ctx: ctx, reader: file}); err != nil {
		return false, err
	}
	if hex.EncodeToString(sum.Sum(nil)) != j.SHA256 {
		return false, errors.New("completed encrypted archive checksum failed")
	}
	if name == partial {
		if err = s.root.Rename(partial, final); err != nil {
			return false, errors.New("cannot finish interrupted archive rename")
		}
	}
	dir, err := s.root.Open(".")
	if err != nil {
		return false, errors.New("cannot sync recovered backup directory")
	}
	defer dir.Close()
	if err = dir.Sync(); err != nil {
		return false, errors.New("cannot sync recovered backup directory")
	}
	return true, nil
}
func (s *Service) cleanVerificationTarget(ctx context.Context, j model.BackupJob) error {
	// Private plaintext gets removed even if the database is temporarily offline.
	if err := s.root.RemoveAll(".verify-" + j.ID); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("cannot remove interrupted private verification workspace")
	}
	conn, err := connect(ctx, s.options.DatabaseURL)
	if err != nil {
		return err
	}
	defer closeConn(conn)
	target := "scraper_verify_" + j.ID
	var marker string
	err = conn.QueryRow(ctx, `SELECT COALESCE(shobj_description(oid,'pg_database'),'') FROM pg_database WHERE datname=$1`, target).Scan(&marker)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return errors.New("cannot inspect interrupted verification database")
	}
	if marker != "scraper.backup.verify:"+target {
		return errors.New("verification database ownership cannot be proven; operator review is required")
	}
	if _, err = conn.Exec(ctx, "DROP DATABASE "+pgx.Identifier{target}.Sanitize()+" WITH (FORCE)"); err != nil {
		return errors.New("cannot remove interrupted isolated verification database")
	}
	return nil
}
