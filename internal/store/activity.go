package store

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/moodiness/ingest/internal/model"
)

var activityIdentity = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

func validateActivityPage(options model.ListOptions) error {
	if options.Limit < 1 || options.Limit > 200 || options.Offset < 0 || options.Offset > 1000000 || len(options.Query) > 500 || len(options.ProviderID) > 128 || len(options.RunID) > 128 {
		return model.ErrInvalid
	}
	return nil
}

func (s *Store) Logs(ctx context.Context, options model.LogOptions) (model.List[model.LogEntry], error) {
	result := model.List[model.LogEntry]{Items: []model.LogEntry{}, Limit: options.Limit, Offset: options.Offset}
	if err := validateActivityPage(options.ListOptions); err != nil {
		return result, err
	}
	for _, level := range options.Levels {
		if !model.IsActivityLevel(level) {
			return result, model.ErrInvalid
		}
	}
	if options.From != nil && options.To != nil && options.From.After(*options.To) {
		return result, model.ErrInvalid
	}
	levels := options.Levels
	if len(levels) == 0 {
		levels = []string{"debug", "info", "warn", "error"}
	}
	args := []any{levels, options.ProviderID, options.RunID, options.Query, options.From, options.To}
	where := ` WHERE level=ANY($1::text[]) AND ($2='' OR provider_id=$2) AND ($3='' OR run_id=$3)
 AND ($4='' OR strpos(lower(message||' '||kind||' '||provider_id||' '||run_id),lower($4))>0)
 AND ($5::timestamptz IS NULL OR created_at >= $5) AND ($6::timestamptz IS NULL OR created_at <= $6)`
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, databaseError("begin activity query", err)
	}
	defer rollback(tx)
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM ingest.activity_logs"+where, args...).Scan(&result.Total); err != nil {
		return result, databaseError("count activity logs", err)
	}
	args = append(args, options.Limit, options.Offset)
	rows, err := tx.Query(ctx, "SELECT id,level,kind,message,provider_id,run_id,created_at,data FROM ingest.activity_logs"+where+" ORDER BY created_at DESC,id DESC LIMIT $7 OFFSET $8", args...)
	if err != nil {
		return result, databaseError("list activity logs", err)
	}
	defer rows.Close()
	for rows.Next() {
		var entry model.LogEntry
		var data []byte
		if err := rows.Scan(&entry.ID, &entry.Level, &entry.Kind, &entry.Message, &entry.ProviderID, &entry.RunID, &entry.CreatedAt, &data); err != nil {
			return result, databaseError("read activity log", err)
		}
		entry.Data, err = decodeObject(data)
		if err != nil {
			return result, databaseError("decode activity metadata", err)
		}
		result.Items = append(result.Items, entry)
	}
	if err := rows.Err(); err != nil {
		return result, databaseError("list activity logs", err)
	}
	return result, databaseError("finish activity query", tx.Commit(ctx))
}

func (s *Store) Notifications(ctx context.Context, unread bool, options model.ListOptions) (model.NotificationList, error) {
	result := model.NotificationList{List: model.List[model.Notification]{Items: []model.Notification{}, Limit: options.Limit, Offset: options.Offset}}
	if err := validateActivityPage(options); err != nil {
		return result, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, databaseError("begin notification query", err)
	}
	defer rollback(tx)
	if err := tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE NOT $1 OR read_at IS NULL),count(*) FILTER (WHERE read_at IS NULL) FROM ingest.notifications`, unread).Scan(&result.Total, &result.UnreadCount); err != nil {
		return result, databaseError("count notifications", err)
	}
	rows, err := tx.Query(ctx, `SELECT id,level,kind,title,message,provider_id,run_id,created_at,read_at FROM ingest.notifications WHERE NOT $1 OR read_at IS NULL ORDER BY created_at DESC,id DESC LIMIT $2 OFFSET $3`, unread, options.Limit, options.Offset)
	if err != nil {
		return result, databaseError("list notifications", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item model.Notification
		if err := rows.Scan(&item.ID, &item.Level, &item.Kind, &item.Title, &item.Message, &item.ProviderID, &item.RunID, &item.CreatedAt, &item.ReadAt); err != nil {
			return result, databaseError("read notification", err)
		}
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return result, databaseError("list notifications", err)
	}
	return result, databaseError("finish notification query", tx.Commit(ctx))
}

// ReadNotification is idempotent and preserves the first acknowledgement time.
func (s *Store) ReadNotification(ctx context.Context, id int64) error {
	if id < 1 {
		return model.ErrInvalid
	}
	return s.readNotifications(ctx, &id)
}

func (s *Store) ReadAllNotifications(ctx context.Context) error { return s.readNotifications(ctx, nil) }

func (s *Store) readNotifications(ctx context.Context, id *int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return databaseError("begin notification acknowledgement", err)
	}
	defer rollback(tx)
	result, err := tx.Exec(ctx, `UPDATE ingest.notifications SET read_at=COALESCE(read_at,NOW()) WHERE $1::bigint IS NULL OR id=$1`, id)
	if err != nil {
		return databaseError("acknowledge notifications", err)
	}
	if id != nil && result.RowsAffected() == 0 {
		return model.ErrNotFound
	}
	if err := notify(ctx, tx); err != nil {
		return databaseError("notify acknowledgement", err)
	}
	return databaseError("commit notification acknowledgement", tx.Commit(ctx))
}

// RecordActivity records independent service/scheduler events. Never pass secret
// values, URLs, request headers, source documents or payloads. Messages are
// canonicalized by kind rather than trusting arbitrary error strings; the message
// argument is intentionally not persisted. Metadata uses the same SQL typed
// allowlist as collection events; unknown keys and nonmatching types are dropped.
// Each call creates an event except consecutive schedule.failed for one provider,
// which is deduplicated until another schedule.* activity records recovery. Calls
// are serialized per provider so multiple schedulers cannot create alert storms.
func (s *Store) RecordActivity(ctx context.Context, level, kind, message, providerID, runID string, data map[string]any) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return databaseError("begin service activity", err)
	}
	defer rollback(tx)
	if err := recordActivityTx(ctx, tx, level, kind, message, providerID, runID, data); err != nil {
		return err
	}
	return databaseError("commit service activity", tx.Commit(ctx))
}

func recordActivityTx(ctx context.Context, tx pgx.Tx, level, kind, message, providerID, runID string, data map[string]any) error {
	if !model.IsActivityLevel(level) || (providerID != "" && !activityIdentity.MatchString(providerID)) || (runID != "" && !activityIdentity.MatchString(runID)) {
		return model.ErrInvalid
	}
	switch kind {
	case "service.started", "service.stopped", "service.failed", "scheduler.started", "scheduler.stopped", "scheduler.failed", "schedule.failed", "schedule.queued", "schedule.skipped",
		"backup.succeeded", "backup.failed", "backup.restore_succeeded", "backup.restore_failed", "health.warning", "health.recovered":
	default:
		return model.ErrInvalid
	}
	encoded, err := jsonObject(data)
	if err != nil {
		return err
	}
	id, err := newRunID()
	if err != nil {
		return err
	}
	if strings.HasPrefix(kind, "schedule.") {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", providerLockKey("activity:"+providerID)); err != nil {
			return databaseError("lock schedule activity", err)
		}
		if kind == "schedule.failed" {
			var lastKind string
			err := tx.QueryRow(ctx, `SELECT kind FROM ingest.activity_logs WHERE provider_id=$1 AND kind LIKE 'schedule.%' ORDER BY id DESC LIMIT 1`, providerID).Scan(&lastKind)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return databaseError("read previous schedule activity", err)
			}
			if lastKind == kind {
				return nil
			}
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO ingest.activity_logs(event_id,level,kind,message,provider_id,run_id,data) VALUES($1,$2,$3,ingest.activity_message($3),$4,$5,ingest.safe_activity_data($6))`, "activity-"+id, level, kind, providerID, runID, encoded)
	if err != nil {
		return databaseError("record service activity", err)
	}
	return nil
}
