package store

import (
	"context"

	"github.com/moodiness/ingest/internal/model"
)

const collectionSettingsSchema = `
CREATE TABLE ingest.collection_settings (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK(singleton),
    workers INTEGER CHECK(workers BETWEEN 1 AND 32),
    revision BIGINT NOT NULL DEFAULT 1 CHECK(revision > 0)
);
-- NULL preserves the first serve invocation's --workers bootstrap value.
INSERT INTO ingest.collection_settings(singleton) VALUES(TRUE);
`

// InitializeCollectionSettings seeds only an unset value, never a saved setting.
func (s *Store) InitializeCollectionSettings(ctx context.Context, workers int) error {
	if workers < 1 || workers > model.MaxCollectionWorkers {
		return model.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return databaseError("begin collection settings initialization", err)
	}
	defer rollback(tx)
	result, err := tx.Exec(ctx, "UPDATE ingest.collection_settings SET workers=$1 WHERE singleton AND workers IS NULL", workers)
	if err != nil {
		return databaseError("initialize collection settings", err)
	}
	if result.RowsAffected() != 0 {
		if err := notify(ctx, tx); err != nil {
			return databaseError("notify collection settings initialization", err)
		}
	}
	return databaseError("commit collection settings initialization", tx.Commit(ctx))
}

const collectionSettingsColumns = `COALESCE(workers,2),max_quota_retries,max_quota_wait_seconds,auto_resume_interrupted,no_progress_requests,no_progress_action,default_request_timeout_seconds,default_preview_pages,default_max_pages,default_max_duration_seconds,revision`

func scanCollectionSettings(row scanner) (model.CollectionSettings, error) {
	var value model.CollectionSettings
	err := row.Scan(&value.Workers, &value.MaxQuotaRetries, &value.MaxQuotaWaitSeconds, &value.AutoResumeInterrupted,
		&value.NoProgressRequests, &value.NoProgressAction, &value.DefaultRequestTimeoutSeconds,
		&value.DefaultPreviewPages, &value.DefaultMaxPages, &value.DefaultMaxDurationSeconds, &value.Revision)
	return value, databaseError("read collection settings", err)
}

func (s *Store) CollectionSettings(ctx context.Context) (model.CollectionSettings, error) {
	return scanCollectionSettings(s.pool.QueryRow(ctx, "SELECT "+collectionSettingsColumns+" FROM ingest.collection_settings WHERE singleton"))
}

func (s *Store) CollectionOverview(ctx context.Context) (model.CollectionOverview, error) {
	var result model.CollectionOverview
	var err error
	result.Settings, err = s.CollectionSettings(ctx)
	if err != nil {
		return result, err
	}
	err = s.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status='running'),
count(*) FILTER (WHERE status='queued') FROM ingest.runs WHERE status IN ('running','queued')`).Scan(&result.Running, &result.Queued)
	result.Capacity = min(cap(s.claimSlots), model.MaxCollectionWorkers)
	return result, databaseError("read collection capacity", err)
}

func (s *Store) UpdateCollectionSettings(ctx context.Context, input model.CollectionSettings) error {
	if !input.Valid() {
		return model.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return databaseError("begin collection settings update", err)
	}
	defer rollback(tx)
	result, err := tx.Exec(ctx, `UPDATE ingest.collection_settings SET workers=$1,max_quota_retries=$2,
max_quota_wait_seconds=$3,auto_resume_interrupted=$4,no_progress_requests=$5,no_progress_action=$6,
default_request_timeout_seconds=$7,default_preview_pages=$8,default_max_pages=$9,
default_max_duration_seconds=$10,revision=revision+1 WHERE singleton AND revision=$11`,
		input.Workers, input.MaxQuotaRetries, input.MaxQuotaWaitSeconds, input.AutoResumeInterrupted,
		input.NoProgressRequests, input.NoProgressAction, input.DefaultRequestTimeoutSeconds,
		input.DefaultPreviewPages, input.DefaultMaxPages, input.DefaultMaxDurationSeconds, input.Revision)
	if err != nil {
		return databaseError("update collection settings", err)
	}
	if result.RowsAffected() == 0 {
		return model.ErrConflict
	}
	if err := notify(ctx, tx); err != nil {
		return databaseError("notify collection settings update", err)
	}
	return databaseError("commit collection settings update", tx.Commit(ctx))
}
