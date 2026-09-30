package store

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/moodiness/ingest/internal/model"
)

const backupColumns = `id,kind,backup_id,trigger,status,cleanup_pending,phase,failure_code,bytes,sha256,recipient,created_at,started_at,finished_at,report`

var backupClock = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

func scanBackup(row pgx.Row) (model.BackupJob, error) {
	var j model.BackupJob
	var report []byte
	err := row.Scan(&j.ID, &j.Kind, &j.BackupID, &j.Trigger, &j.Status, &j.CleanupPending, &j.Phase, &j.FailureCode, &j.Bytes, &j.SHA256, &j.Recipient, &j.CreatedAt, &j.StartedAt, &j.FinishedAt, &report)
	if err != nil {
		return j, databaseError("read backup job", err)
	}
	j.Report, err = decodeObject(report)
	return j, err
}
func (s *Store) BackupSettings(ctx context.Context) (model.BackupSettings, error) {
	var v model.BackupSettings
	err := s.pool.QueryRow(ctx, `SELECT recipient,enabled,time_utc,revision,next_run_at,last_success_at,last_failure_at FROM ingest.backup_settings WHERE singleton`).Scan(&v.Recipient, &v.Enabled, &v.TimeUTC, &v.Revision, &v.NextRunAt, &v.LastSuccessAt, &v.LastFailureAt)
	return v, err
}
func (s *Store) ConfigureBackups(ctx context.Context, enabled bool, clock string, revision int64) error {
	if !backupClock.MatchString(clock) {
		return model.ErrInvalid
	}
	result, err := s.pool.Exec(ctx, `UPDATE ingest.backup_settings SET enabled=$1,time_utc=$2::text,revision=revision+1,next_run_at=CASE WHEN $1 THEN
 ((now() AT TIME ZONE 'UTC')::date + $2::text::time + CASE WHEN (now() AT TIME ZONE 'UTC')::time >= $2::text::time THEN interval '1 day' ELSE interval '0' END) AT TIME ZONE 'UTC' ELSE NULL END
 WHERE singleton AND revision=$3 AND (NOT $1 OR recipient<>'')`, enabled, clock, revision)
	if err == nil && result.RowsAffected() == 0 {
		return model.ErrConflict
	}
	return err
}
func (s *Store) SetBackupRecipient(ctx context.Context, recipient string, revision int64) error {
	result, err := s.pool.Exec(ctx, `UPDATE ingest.backup_settings SET recipient=$1,enabled=FALSE,next_run_at=NULL,revision=revision+1 WHERE singleton AND revision=$2`, recipient, revision)
	if err == nil && result.RowsAffected() == 0 {
		return model.ErrConflict
	}
	return err
}
func (s *Store) BackupJobs(ctx context.Context, limit, offset int) ([]model.BackupJob, int, error) {
	if limit < 1 || limit > 100 || offset < 0 {
		return nil, 0, model.ErrInvalid
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM ingest.backup_jobs`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+backupColumns+` FROM ingest.backup_jobs ORDER BY created_at DESC,id DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []model.BackupJob{}
	for rows.Next() {
		j, e := scanBackup(rows)
		if e != nil {
			return nil, 0, e
		}
		items = append(items, j)
	}
	return items, total, rows.Err()
}
func (s *Store) BackupJob(ctx context.Context, id string) (model.BackupJob, error) {
	return scanBackup(s.pool.QueryRow(ctx, `SELECT `+backupColumns+` FROM ingest.backup_jobs WHERE id=$1`, id))
}

// QueueBackup serializes manual and scheduled work through the singleton row.
// Verification keys stay in the initiating service; its job is owner-bound.
func (s *Store) QueueBackup(ctx context.Context, id, kind, backupID, owner string) (model.BackupJob, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return model.BackupJob{}, err
	}
	defer tx.Rollback(ctx)
	var recipient string
	if err = tx.QueryRow(ctx, `SELECT recipient FROM ingest.backup_settings WHERE singleton FOR UPDATE`).Scan(&recipient); err != nil {
		return model.BackupJob{}, err
	}
	if recipient == "" && kind == "backup" {
		return model.BackupJob{}, model.ErrInvalid
	}
	var busy bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ingest.backup_jobs WHERE status IN ('queued','running'))`).Scan(&busy); err != nil {
		return model.BackupJob{}, err
	}
	if busy {
		return model.BackupJob{}, model.ErrBusy
	}
	j, err := scanBackup(tx.QueryRow(ctx, `INSERT INTO ingest.backup_jobs(id,kind,backup_id,trigger,status,recipient,owner,lease_until) VALUES($1,$2,$3,'manual','queued',$4,$5,CASE WHEN $2='verify' THEN now()+interval '2 minutes' ELSE NULL END) RETURNING `+backupColumns, id, kind, backupID, recipient, owner))
	if err != nil {
		return j, err
	}
	return j, tx.Commit(ctx)
}
func (s *Store) ScheduleBackup(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var recipient string
	err = tx.QueryRow(ctx, `SELECT recipient FROM ingest.backup_settings WHERE singleton AND enabled AND next_run_at<=now() FOR UPDATE`).Scan(&recipient)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var busy bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ingest.backup_jobs WHERE status IN ('queued','running'))`).Scan(&busy); err != nil {
		return err
	}
	if busy {
		return nil
	}
	if _, err = tx.Exec(ctx, `INSERT INTO ingest.backup_jobs(id,kind,trigger,status,recipient) VALUES($1,'backup','scheduled','queued',$2)`, id, recipient); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE ingest.backup_settings SET next_run_at=(((now() AT TIME ZONE 'UTC')::date+time_utc::time+CASE WHEN (now() AT TIME ZONE 'UTC')::time>=time_utc::time THEN interval '1 day' ELSE interval '0' END) AT TIME ZONE 'UTC') WHERE singleton`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) ClaimBackup(ctx context.Context, owner string) (model.BackupJob, error) {
	return scanBackup(s.pool.QueryRow(ctx, `UPDATE ingest.backup_jobs SET status='running',phase='preparing',cleanup_pending=TRUE,owner=$1,started_at=now(),lease_until=now()+interval '2 minutes' WHERE id=(SELECT id FROM ingest.backup_jobs WHERE status='queued' AND (kind='backup' OR owner=$1) ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING `+backupColumns, owner))
}
func (s *Store) HeartbeatBackup(ctx context.Context, id, owner, phase string) error {
	r, err := s.pool.Exec(ctx, `UPDATE ingest.backup_jobs SET lease_until=now()+interval '2 minutes',phase=CASE WHEN $3='' THEN phase ELSE $3 END WHERE id=$1 AND owner=$2 AND status='running' AND lease_until>now()`, id, owner, phase)
	if err == nil && r.RowsAffected() != 1 {
		return model.ErrConflict
	}
	return err
}

// StageBackupArchive durably binds a fully written file to its job before rename.
// Recovery can then finish either side of the filesystem/database commit boundary.
func (s *Store) StageBackupArchive(ctx context.Context, id, owner string, bytes int64, checksum string, report map[string]any) error {
	raw, err := json.Marshal(report)
	if err != nil {
		return err
	}
	result, err := s.pool.Exec(ctx, `UPDATE ingest.backup_jobs SET phase='finalizing',bytes=$3,sha256=$4,report=$5,cleanup_pending=TRUE
 WHERE id=$1 AND owner=$2 AND kind='backup' AND status='running' AND lease_until>now()`, id, owner, bytes, checksum, raw)
	if err == nil && result.RowsAffected() != 1 {
		return model.ErrConflict
	}
	return err
}

func deferBackupRecovery(ctx context.Context, tx pgx.Tx, id string) error {
	_, err := tx.Exec(ctx, `UPDATE ingest.backup_jobs SET status='failed',phase='recovering',failure_code='',cleanup_pending=TRUE,lease_until=NULL WHERE id=$1`, id)
	return err
}
func finishBackupTx(ctx context.Context, tx pgx.Tx, j model.BackupJob, status, code string, bytes int64, checksum string, report map[string]any) error {
	raw, err := json.Marshal(report)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE ingest.backup_jobs SET status=$2,phase=$2,failure_code=$3,bytes=$4,sha256=$5,report=$6,finished_at=now(),lease_until=NULL,cleanup_pending=CASE WHEN $2='succeeded' THEN FALSE ELSE cleanup_pending END WHERE id=$1`, j.ID, status, code, bytes, checksum, raw); err != nil {
		return err
	}
	if j.Kind == "backup" {
		col := "last_success_at"
		if status == "failed" {
			col = "last_failure_at"
		}
		if _, err = tx.Exec(ctx, `UPDATE ingest.backup_settings SET `+col+`=now() WHERE singleton`); err != nil {
			return err
		}
	}
	kind := "backup."
	if j.Kind == "verify" {
		kind = "backup.restore_"
	}
	kind += status
	level := "info"
	if status == "failed" {
		level = "error"
	}
	data := map[string]any{"backup_id": j.ID, "bytes": bytes}
	if code != "" {
		data["failure_code"] = code
	}
	if j.StartedAt != nil {
		data["duration_ms"] = time.Since(*j.StartedAt).Milliseconds()
	}
	return recordActivityTx(ctx, tx, level, kind, "", "", "", data)
}
func (s *Store) FinishBackup(ctx context.Context, id, owner, status, code string, bytes int64, checksum string, report map[string]any) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	j, err := scanBackup(tx.QueryRow(ctx, `SELECT `+backupColumns+` FROM ingest.backup_jobs WHERE id=$1 AND owner=$2 AND status='running' AND lease_until>now() FOR UPDATE`, id, owner))
	if err != nil {
		return err
	}
	if status == "failed" && j.Kind == "backup" && j.SHA256 != "" {
		err = deferBackupRecovery(ctx, tx, j.ID)
	} else {
		err = finishBackupTx(ctx, tx, j, status, code, bytes, checksum, report)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) RecoverBackups(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT `+backupColumns+` FROM ingest.backup_jobs WHERE status IN ('queued','running') AND lease_until<=now() FOR UPDATE`)
	if err != nil {
		return err
	}
	jobs := []model.BackupJob{}
	for rows.Next() {
		j, e := scanBackup(rows)
		if e != nil {
			rows.Close()
			return e
		}
		jobs = append(jobs, j)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if j.Kind == "backup" && j.SHA256 != "" {
			err = deferBackupRecovery(ctx, tx, j.ID)
		} else {
			err = finishBackupTx(ctx, tx, j, "failed", "stalled", 0, "", map[string]any{"reason": "Job interrupted; queue a new attempt."})
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ReconcileBackupArtifact serializes filesystem recovery across replicas without
// occupying the application's connection pool during potentially large hashes.
func (s *Store) ReconcileBackupArtifact(ctx context.Context, reconcile func(context.Context, model.BackupJob) (bool, error)) (bool, error) {
	var pending bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ingest.backup_jobs WHERE status='failed' AND cleanup_pending)`).Scan(&pending); err != nil || !pending {
		return false, err
	}
	conn, err := pgx.ConnectConfig(ctx, s.pool.Config().ConnConfig.Copy())
	if err != nil {
		return false, err
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	j, err := scanBackup(tx.QueryRow(ctx, `SELECT `+backupColumns+` FROM ingest.backup_jobs WHERE status='failed' AND cleanup_pending ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1`))
	if errors.Is(err, model.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if j.Report == nil {
		j.Report = make(map[string]any)
	}
	recovered, recoveryErr := reconcile(ctx, j)
	if recoveryErr != nil {
		if ctx.Err() != nil || j.FailureCode != "" {
			return true, recoveryErr
		}
		j.Report["reason"] = "Interrupted archive recovery failed; encrypted files are preserved for operator review."
		if err = finishBackupTx(ctx, tx, j, "failed", "stalled", j.Bytes, j.SHA256, j.Report); err != nil {
			return true, err
		}
		if err = tx.Commit(ctx); err != nil {
			return true, err
		}
		return true, recoveryErr
	}
	if recovered {
		delete(j.Report, "reason")
		j.Report["recovered"] = true
		err = finishBackupTx(ctx, tx, j, "succeeded", "", j.Bytes, j.SHA256, j.Report)
	} else {
		message := "Incomplete temporary archive removed; finalized archives are never deleted."
		if j.Kind == "verify" {
			message = "Isolated database and plaintext workspace removed."
		}
		_, err = tx.Exec(ctx, `UPDATE ingest.backup_jobs SET cleanup_pending=FALSE,report=report || jsonb_build_object('cleanup',$2::text) WHERE id=$1`, j.ID, message)
	}
	if err != nil {
		return true, err
	}
	return true, tx.Commit(ctx)
}
