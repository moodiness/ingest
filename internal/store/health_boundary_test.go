package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/moodiness/ingest/internal/model"
)

func TestHealthQueuedResumeProgressWindow(t *testing.T) {
	ctx, _, db := scheduleDatabase(t)
	run := mirrorClaim(t, ctx, db, model.Provider{ID: "resumed-source", Name: "Resumed source"}, model.ModeIncremental)
	if _, err := db.SavePage(ctx, run, model.Page{Body: []byte("retained page")}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := db.FinishRun(ctx, run.ID, model.StatusPaused, "", model.PauseBudget); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)
	for _, statement := range []string{
		`UPDATE ingest.runs SET created_at=$2,started_at=$2 WHERE id=$1`,
		`UPDATE ingest.pages SET created_at=$2 WHERE run_id=$1`,
		`UPDATE ingest.events SET created_at=$2 WHERE run_id=$1`,
	} {
		if _, err := db.pool.Exec(ctx, statement, run.ID, old); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ResumeRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	var resumedAt time.Time
	if err := db.pool.QueryRow(ctx, `SELECT MAX(created_at) FROM ingest.events WHERE run_id=$1 AND kind='resumed'`, run.ID).Scan(&resumedAt); err != nil {
		t.Fatal(err)
	}
	threshold := time.Duration(model.DefaultHealthSettings().StuckMinutes) * time.Minute
	for _, elapsed := range []time.Duration{0, threshold} {
		report := healthRunsAt(t, ctx, db, resumedAt.Add(elapsed))
		if len(report.Diagnostics) != 0 {
			t.Fatalf("freshly resumed queue inherited obsolete progress: %+v", report.Diagnostics)
		}
	}
	report := healthRunsAt(t, ctx, db, resumedAt.Add(threshold+time.Microsecond))
	assertStuckRunProgress(t, report, run.ID, resumedAt)
}

func TestHealthRunningProgressIgnoresFailedAttempts(t *testing.T) {
	ctx, _, db := scheduleDatabase(t)
	run := mirrorClaim(t, ctx, db, model.Provider{ID: "running-source", Name: "Running source"}, model.ModeIncremental)
	old := time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)
	if _, err := db.pool.Exec(ctx, `UPDATE ingest.runs SET created_at=$2,started_at=$2 WHERE id=$1`, run.ID, old); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE ingest.events SET created_at=$2 WHERE run_id=$1`, run.ID, old); err != nil {
		t.Fatal(err)
	}
	started, err := db.AddEvent(ctx, run.ID, "started", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	threshold := time.Duration(model.DefaultHealthSettings().StuckMinutes) * time.Minute
	if report := healthRunsAt(t, ctx, db, started.CreatedAt.Add(threshold)); len(report.Diagnostics) != 0 {
		t.Fatalf("new running attempt inherited obsolete start time: %+v", report.Diagnostics)
	}
	run, err = db.SavePage(ctx, run, model.Page{Body: []byte("successful page")}, "")
	if err != nil {
		t.Fatal(err)
	}
	pageAt := started.CreatedAt.Add(time.Hour)
	if _, err := db.pool.Exec(ctx, `UPDATE ingest.pages SET created_at=$2 WHERE run_id=$1`, run.ID, pageAt); err != nil {
		t.Fatal(err)
	}
	if report := healthRunsAt(t, ctx, db, pageAt.Add(threshold)); len(report.Diagnostics) != 0 {
		t.Fatalf("successful page did not advance running progress: %+v", report.Diagnostics)
	}
	if _, err := db.SavePage(ctx, run, model.Page{Body: []byte("failed page"), Error: "source failed"}, ""); err != nil {
		t.Fatal(err)
	}
	failedAt := pageAt.Add(time.Hour)
	if _, err := db.pool.Exec(ctx, `UPDATE ingest.pages SET created_at=$2 WHERE run_id=$1 AND error<>''`, run.ID, failedAt); err != nil {
		t.Fatal(err)
	}
	assertStuckRunProgress(t, healthRunsAt(t, ctx, db, failedAt), run.ID, pageAt)
}

func TestHealthMeasurementDoesNotWaitForProviderNamedSystemHealth(t *testing.T) {
	ctx, _, db := scheduleDatabase(t)
	mirrorClaim(t, ctx, db, model.Provider{ID: "system-health", Name: "System health source"}, model.ModeIncremental)
	// Keep the real provider lease held through both health transition paths.
	measureCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	report, err := db.MeasureHealth(measureCtx, nil, true, model.HealthDisk{Available: true}, "test-state")
	if err != nil {
		t.Fatal("provider lease blocked health measurement:", err)
	}
	if !report.Database.Available || report.Status != "healthy" {
		t.Fatalf("active provider produced a false health outage: %+v", report)
	}
	unavailableCtx, stop := context.WithTimeout(ctx, 2*time.Second)
	defer stop()
	if err := db.HealthMeasurementUnavailable(unavailableCtx, report.CheckedAt); err != nil {
		t.Fatal("provider lease blocked unavailable-health transition:", err)
	}
}

func TestHealthTransitionsSerializeAcrossReplicas(t *testing.T) {
	ctx, url, owner := scheduleDatabase(t)
	peer, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(peer.Close)
	tx, err := owner.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(1768843109,4)`); err != nil {
		t.Fatal(err)
	}
	paths := []struct {
		name string
		run  func(context.Context) error
	}{
		{"measurement", func(ctx context.Context) error {
			_, err := peer.MeasureHealth(ctx, nil, true, model.HealthDisk{Available: true}, "test-state")
			return err
		}},
		{"unavailable", func(ctx context.Context) error {
			return peer.HealthMeasurementUnavailable(ctx, time.Now())
		}},
	}
	for _, path := range paths {
		t.Run(path.name, func(t *testing.T) {
			blocked, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
			defer cancel()
			if err := path.run(blocked); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("health transition bypassed the replica serialization lock: %v", err)
			}
		})
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if err := path.run(ctx); err != nil {
			t.Fatalf("%s did not recover after the peer released its lock: %v", path.name, err)
		}
	}
}

func TestHealthTransportFailureRequiresSuccessfulPageRecovery(t *testing.T) {
	ctx, _, db := scheduleDatabase(t)
	source := model.Provider{ID: "recovering-source", Name: "Recovering source", Enabled: true}
	run := mirrorClaim(t, ctx, db, source, model.ModeIncremental)
	check := func(wantFailure string) {
		t.Helper()
		report, err := db.MeasureHealth(ctx, []model.ProviderSummary{{ID: source.ID, Enabled: true}}, true, model.HealthDisk{Available: true}, "test-state")
		if err != nil {
			t.Fatal(err)
		}
		failure, stale := "", false
		for _, diagnostic := range report.Diagnostics {
			if diagnostic.ProviderID != source.ID {
				continue
			}
			switch diagnostic.Code {
			case "authentication", "certificate":
				failure = diagnostic.Code
			case "source_stale":
				stale = true
			}
		}
		if failure != wantFailure || !stale {
			t.Fatalf("transport recovery must preserve publication freshness: failure=%q want=%q stale=%t", failure, wantFailure, stale)
		}
	}
	if _, err := db.AddEvent(ctx, run.ID, "source_error", "Source request failed", map[string]any{"failure_code": "certificate"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.FinishRun(ctx, run.ID, model.StatusFailed, "Source request failed", ""); err != nil {
		t.Fatal(err)
	}
	check("certificate")
	if _, err := db.ResumeRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	resumed, err := db.ClaimNext(ctx)
	if err != nil || resumed == nil || resumed.ID != run.ID {
		t.Fatalf("resume original run: %+v %v", resumed, err)
	}
	run = *resumed
	check("certificate")
	run, err = db.SavePage(ctx, run, model.Page{Error: "Retry failed"}, "")
	if err != nil {
		t.Fatal(err)
	}
	check("certificate")
	run, err = db.SavePage(ctx, run, model.Page{}, "")
	if err != nil {
		t.Fatal(err)
	}
	check("")
	if run.Status != model.StatusRunning || run.Errors != 1 {
		t.Fatalf("recovery must not complete the run or erase historical errors: status=%s errors=%d", run.Status, run.Errors)
	}
	if _, err := db.AddEvent(ctx, run.ID, "source_error", "Source request failed", map[string]any{"failure_code": "authentication"}); err != nil {
		t.Fatal(err)
	}
	check("authentication")
}

func healthRunsAt(t *testing.T, ctx context.Context, db *Store, at time.Time) *model.SystemHealth {
	t.Helper()
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	report := &model.SystemHealth{CheckedAt: at, Settings: model.DefaultHealthSettings()}
	if err := healthRuns(ctx, tx, report); err != nil {
		t.Fatal(err)
	}
	return report
}

func assertStuckRunProgress(t *testing.T, report *model.SystemHealth, runID string, progress time.Time) {
	t.Helper()
	if len(report.Diagnostics) != 1 {
		t.Fatalf("stale active run did not produce one warning: %+v", report.Diagnostics)
	}
	diagnostic := report.Diagnostics[0]
	if diagnostic.Code != "run_stuck" || diagnostic.RunID != runID || diagnostic.LastProgressAt == nil || !diagnostic.LastProgressAt.Equal(progress) {
		t.Fatalf("stuck warning used the wrong lifecycle or page progress: %+v", diagnostic)
	}
}
