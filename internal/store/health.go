package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/moodiness/ingest/internal/model"
)

// The two-int namespace is separate from provider leases; keys 1–3 are reserved
// for migrations, legacy import and catalogue publication.
const healthLockNamespace int32 = 1768843109
const healthLockID int32 = 4

func (s *Store) UpdateHealthSettings(ctx context.Context, value model.HealthSettings) error {
	if !value.Valid() {
		return model.ErrInvalid
	}
	_, err := s.pool.Exec(ctx, `UPDATE ingest.health_settings SET stale_hours=$1,stuck_minutes=$2,min_free_bytes=$3,min_free_percent=$4,journal_growth_bytes_per_day=$5 WHERE singleton`, value.StaleHours, value.StuckMinutes, value.MinFreeBytes, value.MinFreePercent, value.JournalGrowthBytesPerDay)
	return databaseError("update health thresholds", err)
}

// MeasureHealth serializes measurement and alert transitions in one transaction.
// Reading measurements under the lock prevents a slower replica from publishing
// an older observation after another replica has already reported recovery.
func (s *Store) MeasureHealth(ctx context.Context, sources []model.ProviderSummary, sourcesAvailable bool, disk model.HealthDisk, diskScope string) (*model.SystemHealth, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, databaseError("begin health measurement", err)
	}
	defer rollback(tx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1,$2)`, healthLockNamespace, healthLockID); err != nil {
		return nil, databaseError("lock health measurement", err)
	}
	report := &model.SystemHealth{Status: "healthy", Disk: disk, SourcesAvailable: sourcesAvailable, Samples: []model.HealthSample{}, Diagnostics: []model.HealthDiagnostic{}}
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp(),stale_hours,stuck_minutes,min_free_bytes,min_free_percent,journal_growth_bytes_per_day FROM ingest.health_settings WHERE singleton`).Scan(&report.CheckedAt, &report.Settings.StaleHours, &report.Settings.StuckMinutes, &report.Settings.MinFreeBytes, &report.Settings.MinFreePercent, &report.Settings.JournalGrowthBytesPerDay); err != nil {
		return nil, databaseError("read health thresholds", err)
	}
	report.NextCheckAt = report.CheckedAt.Add(time.Minute)
	report.Database.Categories = []model.HealthStorageCategory{{Key: "live"}, {Key: "raw"}, {Key: "journal"}, {Key: "other"}}
	if err := tx.QueryRow(ctx, `SELECT pg_database_size(current_database())`).Scan(&report.Database.TotalBytes); err != nil {
		return nil, databaseError("measure database storage", err)
	}
	rows, err := tx.Query(ctx, `SELECT CASE WHEN n.nspname='ingest' AND c.relname IN ('torrents','staged_torrents') THEN 'live'
WHEN n.nspname='ingest' AND c.relname IN ('pages','raw_records') THEN 'raw'
WHEN n.nspname='ingest' AND c.relname IN ('catalog_journal','torrent_history','torrent_history_baselines') THEN 'journal'
ELSE 'other' END AS category,COALESCE(SUM(pg_total_relation_size(c.oid)),0)::bigint,COALESCE(SUM(pg_table_size(c.oid)),0)::bigint,COALESCE(SUM(pg_indexes_size(c.oid)),0)::bigint
FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
WHERE c.relkind IN ('r','m') AND n.nspname !~ '^pg_' AND n.nspname<>'information_schema' GROUP BY category`)
	if err != nil {
		return nil, databaseError("measure database relations", err)
	}
	for rows.Next() {
		var category model.HealthStorageCategory
		if err := rows.Scan(&category.Key, &category.Bytes, &category.TableBytes, &category.IndexBytes); err != nil {
			rows.Close()
			return nil, databaseError("read relation measurement", err)
		}
		for i := range report.Database.Categories {
			if report.Database.Categories[i].Key == category.Key {
				report.Database.Categories[i] = category
			}
		}
		report.Database.RelationBytes += category.Bytes
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, databaseError("read relation measurements", err)
	}
	report.Database.OtherDatabaseBytes = report.Database.TotalBytes - report.Database.RelationBytes
	// Concurrent writes can grow relations between independent size calls. Keep
	// the raw measurements; a negative remainder is explicitly visible, not faked.
	report.Database.Available = true
	report.Database.MeasuredAt = &report.CheckedAt
	current := model.HealthSample{SampledAt: report.CheckedAt, DatabaseBytes: report.Database.TotalBytes, LiveBytes: report.Database.Categories[0].Bytes, RawBytes: report.Database.Categories[1].Bytes, JournalBytes: report.Database.Categories[2].Bytes, OtherBytes: report.Database.Categories[3].Bytes}
	if _, err := tx.Exec(ctx, `INSERT INTO ingest.health_daily_samples(day,sampled_at,database_bytes,live_bytes,raw_bytes,journal_bytes,other_bytes) VALUES(($1::timestamptz AT TIME ZONE 'UTC')::date,$1,$2,$3,$4,$5,$6) ON CONFLICT(day) DO NOTHING`, current.SampledAt, current.DatabaseBytes, current.LiveBytes, current.RawBytes, current.JournalBytes, current.OtherBytes); err != nil {
		return nil, databaseError("persist daily storage sample", err)
	}
	rows, err = tx.Query(ctx, `SELECT sampled_at,database_bytes,live_bytes,raw_bytes,journal_bytes,other_bytes FROM (SELECT * FROM ingest.health_daily_samples ORDER BY day DESC LIMIT 90) samples ORDER BY day`)
	if err != nil {
		return nil, databaseError("read storage history", err)
	}
	for rows.Next() {
		var sample model.HealthSample
		if err := rows.Scan(&sample.SampledAt, &sample.DatabaseBytes, &sample.LiveBytes, &sample.RawBytes, &sample.JournalBytes, &sample.OtherBytes); err != nil {
			rows.Close()
			return nil, databaseError("read daily storage sample", err)
		}
		report.Samples = append(report.Samples, sample)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, databaseError("read daily storage samples", err)
	}
	for i := len(report.Samples) - 1; i >= 0; i-- {
		previous := report.Samples[i]
		elapsed := current.SampledAt.Sub(previous.SampledAt).Hours() / 24
		if elapsed < 1 {
			continue
		}
		report.Growth = &model.HealthGrowth{From: previous.SampledAt, To: current.SampledAt, ElapsedDays: elapsed, DatabaseBytes: current.DatabaseBytes - previous.DatabaseBytes, JournalBytes: current.JournalBytes - previous.JournalBytes}
		report.Growth.DatabaseBytesPerDay = float64(report.Growth.DatabaseBytes) / elapsed
		report.Growth.JournalBytesPerDay = float64(report.Growth.JournalBytes) / elapsed
		break
	}
	if report.Growth != nil && report.Settings.JournalGrowthBytesPerDay > 0 && report.Growth.JournalBytesPerDay > float64(report.Settings.JournalGrowthBytesPerDay) {
		bytes := int64(report.Growth.JournalBytesPerDay)
		report.Diagnostics = append(report.Diagnostics, model.HealthDiagnostic{Code: "journal_growth", Reason: "Journal and publication history growth exceeds the daily threshold. No history is removed automatically.", Since: report.Growth.From, ObservedAt: report.CheckedAt, MeasuredBytes: &bytes, ThresholdBytes: &report.Settings.JournalGrowthBytesPerDay})
	}
	if sourcesAvailable {
		if err := healthSources(ctx, tx, report, sources); err != nil {
			return nil, err
		}
	}
	if err := healthRuns(ctx, tx, report); err != nil {
		return nil, err
	}
	if disk.Available && disk.TotalBytes > 0 && (disk.AvailableBytes < uint64(report.Settings.MinFreeBytes) || float64(disk.AvailableBytes)/float64(disk.TotalBytes)*100 < float64(report.Settings.MinFreePercent)) {
		available := int64(disk.AvailableBytes)
		report.Diagnostics = append(report.Diagnostics, model.HealthDiagnostic{Code: "storage_low", Reason: "Available space on this application state-directory filesystem is below the configured threshold. This is not a measurement of the database server volume.", Since: report.CheckedAt, ObservedAt: report.CheckedAt, MeasuredBytes: &available, ThresholdBytes: &report.Settings.MinFreeBytes})
	}
	if err := reconcileHealth(ctx, tx, report, diskScope); err != nil {
		return nil, err
	}
	if len(report.Diagnostics) > 0 || !sourcesAvailable || !disk.Available {
		report.Status = "warning"
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, databaseError("commit health measurement", err)
	}
	return report, nil
}

func healthSources(ctx context.Context, tx pgx.Tx, report *model.SystemHealth, sources []model.ProviderSummary) error {
	ids := make([]string, 0, len(sources))
	for _, source := range sources {
		if source.Enabled {
			ids = append(ids, source.ID)
		}
	}
	report.SourcesChecked = len(ids)
	if _, err := tx.Exec(ctx, `INSERT INTO ingest.health_source_observations(provider_id) SELECT unnest($1::text[]) ON CONFLICT DO NOTHING`, ids); err != nil {
		return databaseError("observe configured sources", err)
	}
	rows, err := tx.Query(ctx, `SELECT o.provider_id,o.first_observed_at,success.finished_at,COALESCE(recent.id,''),COALESCE(failure.run_id,''),failure.created_at,COALESCE(failure.code,'')
FROM ingest.health_source_observations o
LEFT JOIN LATERAL (SELECT finished_at FROM ingest.runs WHERE provider_id=o.provider_id AND status='succeeded' AND mode<>'preview' ORDER BY finished_at DESC LIMIT 1) success ON TRUE
LEFT JOIN LATERAL (SELECT finished_at FROM ingest.runs WHERE provider_id=o.provider_id AND status='succeeded' ORDER BY finished_at DESC LIMIT 1) verified ON TRUE
LEFT JOIN LATERAL (SELECT id FROM ingest.runs WHERE provider_id=o.provider_id ORDER BY created_at DESC,id DESC LIMIT 1) recent ON TRUE
LEFT JOIN LATERAL (SELECT code,run_id,created_at FROM (
SELECT e.data->>'failure_code' AS code,e.run_id,e.created_at,e.id FROM ingest.events e JOIN ingest.runs r ON r.id=e.run_id WHERE r.provider_id=o.provider_id AND e.data ? 'failure_code' AND e.data->>'failure_code' IN ('authentication','certificate') AND e.created_at>COALESCE(verified.finished_at,'-infinity'::timestamptz)
UNION ALL SELECT metadata->>'failure_code',run_id,created_at,id FROM ingest.pages WHERE provider_id=o.provider_id AND metadata->>'failure_code' IN ('authentication','certificate') AND created_at>COALESCE(verified.finished_at,'-infinity'::timestamptz)
) failures ORDER BY created_at DESC,id DESC LIMIT 1) failure ON NOT EXISTS (
SELECT 1 FROM ingest.runs recovered
CROSS JOIN LATERAL (
SELECT created_at FROM ingest.pages WHERE run_id=recovered.id AND error='' ORDER BY id DESC LIMIT 1
) progress
WHERE recovered.provider_id=o.provider_id
AND (recovered.finished_at IS NULL OR recovered.finished_at>failure.created_at)
AND progress.created_at>failure.created_at
)
WHERE o.provider_id=ANY($1::text[])`, ids)
	if err != nil {
		return databaseError("inspect source health", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, runID, failureRunID, code string
		var first time.Time
		var success, finished *time.Time
		if err := rows.Scan(&id, &first, &success, &runID, &failureRunID, &finished, &code); err != nil {
			return databaseError("read source health", err)
		}
		threshold := time.Duration(report.Settings.StaleHours) * time.Hour
		if success == nil || report.CheckedAt.Sub(*success) > threshold {
			since, reason := first, "Source has never completed a successful non-preview run."
			if success != nil {
				since = *success
				reason = "Source has not completed a successful non-preview run within the freshness threshold."
			}
			report.Diagnostics = append(report.Diagnostics, model.HealthDiagnostic{Code: "source_stale", Reason: reason, ProviderID: id, RunID: runID, Since: since, ObservedAt: report.CheckedAt, LastSuccessAt: success, ThresholdSeconds: int64(threshold.Seconds())})
		}
		if code == "authentication" || code == "certificate" {
			since := first
			if finished != nil {
				since = *finished
			}
			reason := "A collection attempt encountered an authentication rejection without a successful page or run since. Check the source credentials and permissions."
			if code == "certificate" {
				reason = "A collection attempt failed certificate verification without a successful page or run since. Check the certificate chain and hostname; TLS verification remains enabled."
			}
			report.Diagnostics = append(report.Diagnostics, model.HealthDiagnostic{Code: code, FailureCode: code, Reason: reason, ProviderID: id, RunID: failureRunID, Since: since, ObservedAt: report.CheckedAt})
		}
	}
	return databaseError("read source diagnostics", rows.Err())
}

func healthRuns(ctx context.Context, tx pgx.Tx, report *model.SystemHealth) error {
	rows, err := tx.Query(ctx, `SELECT r.id,r.provider_id,GREATEST(COALESCE(r.started_at,r.created_at),COALESCE((SELECT MAX(created_at) FROM ingest.pages WHERE run_id=r.id AND error=''),r.created_at),COALESCE((SELECT MAX(created_at) FROM ingest.events WHERE run_id=r.id AND kind IN ('queued','resumed','started')),r.created_at)) AS progress FROM ingest.runs r WHERE status IN ('queued','running')`)
	if err != nil {
		return databaseError("inspect active run progress", err)
	}
	defer rows.Close()
	threshold := time.Duration(report.Settings.StuckMinutes) * time.Minute
	for rows.Next() {
		var runID, providerID string
		var progress time.Time
		if err := rows.Scan(&runID, &providerID, &progress); err != nil {
			return databaseError("read active run progress", err)
		}
		if report.CheckedAt.Sub(progress) > threshold {
			report.Diagnostics = append(report.Diagnostics, model.HealthDiagnostic{Code: "run_stuck", Reason: "Active run has no committed page progress within the configured interval. A slow request or intentional rate limit can also cause this warning.", RunID: runID, ProviderID: providerID, Since: progress, ObservedAt: report.CheckedAt, LastProgressAt: &progress, ThresholdSeconds: int64(threshold.Seconds())})
		}
	}
	return databaseError("read run diagnostics", rows.Err())
}

func reconcileHealth(ctx context.Context, tx pgx.Tx, report *model.SystemHealth, diskScope string) error {
	active := make(map[string]bool, len(report.Diagnostics))
	for i := range report.Diagnostics {
		diagnostic := &report.Diagnostics[i]
		scope, subject := "database", diagnostic.ProviderID
		if diagnostic.Code == "storage_low" {
			scope = diskScope
		}
		if diagnostic.Code == "run_stuck" {
			subject = diagnostic.RunID
		}
		active[scope+"/"+diagnostic.Code+"/"+subject] = true
		var previousActive bool
		var previousSince time.Time
		err := tx.QueryRow(ctx, `SELECT active,since FROM ingest.health_alert_states WHERE scope=$1 AND health_code=$2 AND subject=$3`, scope, diagnostic.Code, subject).Scan(&previousActive, &previousSince)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return databaseError("read health transition", err)
		}
		if previousActive {
			diagnostic.Since = previousSince
		}
		if _, err := tx.Exec(ctx, `INSERT INTO ingest.health_alert_states(scope,health_code,subject,active,since,observed_at,provider_id,run_id) VALUES($1,$2,$3,TRUE,$4,$5,$6,$7) ON CONFLICT(scope,health_code,subject) DO UPDATE SET active=TRUE,since=EXCLUDED.since,observed_at=EXCLUDED.observed_at,provider_id=EXCLUDED.provider_id,run_id=EXCLUDED.run_id`, scope, diagnostic.Code, subject, diagnostic.Since, report.CheckedAt, diagnostic.ProviderID, diagnostic.RunID); err != nil {
			return databaseError("persist health transition", err)
		}
		if !previousActive {
			data := map[string]any{"health_code": diagnostic.Code}
			if diagnostic.FailureCode != "" {
				data["failure_code"] = diagnostic.FailureCode
			}
			if diagnostic.MeasuredBytes != nil {
				data["bytes"] = *diagnostic.MeasuredBytes
			}
			if err := recordActivityTx(ctx, tx, "warn", "health.warning", "", diagnostic.ProviderID, diagnostic.RunID, data); err != nil {
				return err
			}
		}
	}
	rows, err := tx.Query(ctx, `SELECT scope,health_code,subject,provider_id,run_id FROM ingest.health_alert_states WHERE active AND scope IN ('database',$1)`, diskScope)
	if err != nil {
		return databaseError("read active health warnings", err)
	}
	type recovery struct{ scope, code, subject, provider, run string }
	recovered := []recovery{}
	for rows.Next() {
		var item recovery
		if err := rows.Scan(&item.scope, &item.code, &item.subject, &item.provider, &item.run); err != nil {
			rows.Close()
			return databaseError("read health recovery", err)
		}
		if active[item.scope+"/"+item.code+"/"+item.subject] {
			continue
		}
		if item.code == "storage_low" && !report.Disk.Available {
			continue
		}
		if !report.SourcesAvailable && (item.code == "source_stale" || item.code == "authentication" || item.code == "certificate") {
			continue
		}
		recovered = append(recovered, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return databaseError("read health recoveries", err)
	}
	for _, item := range recovered {
		if _, err := tx.Exec(ctx, `UPDATE ingest.health_alert_states SET active=FALSE,observed_at=$4 WHERE scope=$1 AND health_code=$2 AND subject=$3`, item.scope, item.code, item.subject, report.CheckedAt); err != nil {
			return databaseError("persist health recovery", err)
		}
		if err := recordActivityTx(ctx, tx, "info", "health.recovered", "", item.provider, item.run, map[string]any{"health_code": item.code}); err != nil {
			return err
		}
	}
	return nil
}

// HealthMeasurementUnavailable is best-effort: a disconnected database cannot
// durably record its own outage. It never replaces a failed measurement with zero.
func (s *Store) HealthMeasurementUnavailable(ctx context.Context, at time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return databaseError("begin unavailable health transition", err)
	}
	defer rollback(tx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1,$2)`, healthLockNamespace, healthLockID); err != nil {
		return databaseError("lock unavailable health transition", err)
	}
	var inserted bool
	err = tx.QueryRow(ctx, `INSERT INTO ingest.health_alert_states(scope,health_code,subject,active,since,observed_at) VALUES('database','database_unavailable','',TRUE,$1,$1) ON CONFLICT(scope,health_code,subject) DO UPDATE SET active=TRUE,since=EXCLUDED.since,observed_at=EXCLUDED.observed_at WHERE NOT ingest.health_alert_states.active RETURNING TRUE`, at).Scan(&inserted)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return databaseError("record unavailable health transition", err)
	}
	if inserted {
		if err := recordActivityTx(ctx, tx, "warn", "health.warning", "", "", "", map[string]any{"health_code": "database_unavailable"}); err != nil {
			return err
		}
	}
	return databaseError("commit unavailable health transition", tx.Commit(ctx))
}
