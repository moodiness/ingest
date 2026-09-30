package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/moodiness/ingest/internal/model"
)

func TestBackupScheduleUsesUTCAndQueuesEachDueTimeOnce(t *testing.T) {
	ctx, _, db := activityDatabase(t)
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	settings, err := db.BackupSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.SetBackupRecipient(ctx, identity.Recipient().String(), settings.Revision); err != nil {
		t.Fatal(err)
	}
	settings, err = db.BackupSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var now time.Time
	if err = db.pool.QueryRow(ctx, `SELECT now()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	expected := now.UTC().Truncate(time.Minute).Add(time.Hour)
	if err = db.ConfigureBackups(ctx, true, expected.Format("15:04"), settings.Revision); err != nil {
		t.Fatal(err)
	}
	settings, err = db.BackupSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !settings.Enabled || settings.NextRunAt == nil || !settings.NextRunAt.Equal(expected) {
		t.Fatalf("daily backup did not retain its UTC clock: %+v; want %s", settings, expected)
	}
	if err = db.ScheduleBackup(ctx, strings.Repeat("a", 32)); err != nil {
		t.Fatal(err)
	}
	_, total, err := db.BackupJobs(ctx, 10, 0)
	if err != nil || total != 0 {
		t.Fatalf("future backup queued early: %d %v", total, err)
	}
	if _, err = db.pool.Exec(ctx, `UPDATE ingest.backup_settings SET next_run_at=now()-interval '1 second' WHERE singleton`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{strings.Repeat("b", 32), strings.Repeat("c", 32)} {
		if err = db.ScheduleBackup(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	jobs, total, err := db.BackupJobs(ctx, 10, 0)
	if err != nil || total != 1 || jobs[0].Trigger != "scheduled" {
		t.Fatalf("due time did not queue exactly once: %+v %d %v", jobs, total, err)
	}
	if err = db.ConfigureBackups(ctx, false, expected.Format("15:04"), settings.Revision); err != nil {
		t.Fatal(err)
	}
	settings, err = db.BackupSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Enabled || settings.NextRunAt != nil {
		t.Fatalf("disabled backups retain a next run: %+v", settings)
	}
}

func TestBackupRecoveryPreservesReportNumbers(t *testing.T) {
	ctx, _, db := activityDatabase(t)
	const id = "precise-report"
	if _, err := db.pool.Exec(ctx, `INSERT INTO ingest.backup_jobs(id,kind,trigger,status,phase,cleanup_pending,report)
VALUES($1,'backup','manual','failed','recovering',TRUE,'{"records":9007199254740993,"nested":{"bytes":9223372036854775807}}')`, id); err != nil {
		t.Fatal(err)
	}
	recovered, err := db.ReconcileBackupArtifact(ctx, func(_ context.Context, job model.BackupJob) (bool, error) {
		return true, nil
	})
	if err != nil || !recovered {
		t.Fatalf("recover backup: recovered=%v error=%v", recovered, err)
	}
	job, err := db.BackupJob(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(job.Report)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != "succeeded" || !strings.Contains(string(encoded), `"records":9007199254740993`) || !strings.Contains(string(encoded), `"bytes":9223372036854775807`) {
		t.Fatalf("backup recovery changed exact report values: status=%s report=%s", job.Status, encoded)
	}
}
