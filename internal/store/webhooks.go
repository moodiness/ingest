package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/moodiness/ingest/internal/model"
)

const webhookColumns = "id,name,enabled,url_secret_ref,signing_secret_ref,events,revision,created_at,updated_at,deleted_at"
const deliveryColumns = "id,webhook_id,event_id,event_type,status,attempts,next_attempt_at,last_status,last_error,created_at,delivered_at"

func scanWebhook(row scanner) (model.Webhook, error) {
	var item model.Webhook
	err := row.Scan(&item.ID, &item.Name, &item.Enabled, &item.URLSecretRef, &item.SigningSecretRef, &item.Events, &item.Revision, &item.CreatedAt, &item.UpdatedAt, &item.DeletedAt)
	return item, err
}

func scanDelivery(row scanner) (model.WebhookDelivery, error) {
	var item model.WebhookDelivery
	err := row.Scan(&item.ID, &item.WebhookID, &item.EventID, &item.EventType, &item.Status, &item.Attempts, &item.NextAttemptAt, &item.LastStatus, &item.LastError, &item.CreatedAt, &item.DeliveredAt)
	return item, err
}

func ValidateWebhook(input model.WebhookInput) error {
	if len(input.Name) == 0 || len(input.Name) > 120 || strings.TrimSpace(input.Name) != input.Name || !utf8.ValidString(input.Name) || !activityIdentity.MatchString(input.URLSecretRef) || strings.Contains(input.URLSecretRef, ".") || (input.SigningSecretRef != "" && (!activityIdentity.MatchString(input.SigningSecretRef) || strings.Contains(input.SigningSecretRef, "."))) || len(input.Events) < 1 {
		return model.ErrInvalid
	}
	for _, r := range input.Name {
		if unicode.IsControl(r) {
			return model.ErrInvalid
		}
	}
	seen := map[string]bool{}
	for _, event := range input.Events {
		if !model.IsWebhookEvent(event) || seen[event] {
			return model.ErrInvalid
		}
		seen[event] = true
	}
	return nil
}

func (s *Store) Webhooks(ctx context.Context, includeDeleted bool) ([]model.Webhook, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+webhookColumns+" FROM ingest.webhooks WHERE $1 OR deleted_at IS NULL ORDER BY created_at,id", includeDeleted)
	if err != nil {
		return nil, databaseError("list webhooks", err)
	}
	defer rows.Close()
	items := []model.Webhook{}
	for rows.Next() {
		item, err := scanWebhook(rows)
		if err != nil {
			return nil, databaseError("read webhook", err)
		}
		items = append(items, item)
	}
	return items, databaseError("list webhooks", rows.Err())
}

func (s *Store) SaveWebhook(ctx context.Context, id string, input model.WebhookInput) (model.Webhook, error) {
	if err := ValidateWebhook(input); err != nil {
		return model.Webhook{}, err
	}
	creating := id == ""
	if (creating && input.Revision != 0) || (!creating && input.Revision < 1) {
		return model.Webhook{}, model.ErrInvalid
	}
	var err error
	if creating {
		id, err = newRunID()
		if err != nil {
			return model.Webhook{}, err
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return model.Webhook{}, databaseError("begin webhook mutation", err)
	}
	defer rollback(tx)
	// Resolve references under the same locks used by secret deletion. The
	// database triggers provide the same protection for non-API callers.
	for _, ref := range []string{input.URLSecretRef, input.SigningSecretRef} {
		if ref == "" {
			continue
		}
		var name string
		if err := tx.QueryRow(ctx, "SELECT name FROM ingest.secrets WHERE name=$1 FOR KEY SHARE", ref).Scan(&name); err != nil {
			return model.Webhook{}, databaseError("check webhook secret", err)
		}
	}
	var item model.Webhook
	if creating {
		item, err = scanWebhook(tx.QueryRow(ctx, `INSERT INTO ingest.webhooks(id,name,enabled,url_secret_ref,signing_secret_ref,events) VALUES($1,$2,$3,$4,$5,$6) RETURNING `+webhookColumns, id, input.Name, input.Enabled, input.URLSecretRef, input.SigningSecretRef, input.Events))
	} else {
		var revision int64
		if err := tx.QueryRow(ctx, "SELECT revision FROM ingest.webhooks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE", id).Scan(&revision); err != nil {
			return item, databaseError("lock webhook", err)
		}
		if revision != input.Revision {
			return item, model.ErrConflict
		}
		item, err = scanWebhook(tx.QueryRow(ctx, `UPDATE ingest.webhooks SET name=$2,enabled=$3,url_secret_ref=$4,signing_secret_ref=$5,events=$6,revision=revision+1,updated_at=NOW() WHERE id=$1 RETURNING `+webhookColumns, id, input.Name, input.Enabled, input.URLSecretRef, input.SigningSecretRef, input.Events))
	}
	if err != nil {
		return item, databaseError("save webhook", err)
	}
	if !input.Enabled {
		if _, err := tx.Exec(ctx, `UPDATE ingest.webhook_deliveries SET status='cancelled',next_attempt_at=NULL,last_error='Webhook disabled' WHERE webhook_id=$1 AND status='pending'`, id); err != nil {
			return item, databaseError("cancel disabled webhook queue", err)
		}
	}
	if err := notify(ctx, tx); err != nil {
		return item, databaseError("notify webhook mutation", err)
	}
	return item, databaseError("commit webhook mutation", tx.Commit(ctx))
}

func (s *Store) DeleteWebhook(ctx context.Context, id string, revision int64) error {
	if revision < 1 {
		return model.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return databaseError("begin webhook deletion", err)
	}
	defer rollback(tx)
	var current int64
	if err := tx.QueryRow(ctx, `SELECT revision FROM ingest.webhooks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&current); err != nil {
		return databaseError("lock webhook", err)
	}
	if current != revision {
		return model.ErrConflict
	}
	if _, err := tx.Exec(ctx, `UPDATE ingest.webhooks SET enabled=FALSE,deleted_at=NOW(),updated_at=NOW(),revision=revision+1 WHERE id=$1`, id); err != nil {
		return databaseError("delete webhook", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE ingest.webhook_deliveries SET status='cancelled',next_attempt_at=NULL,last_error='Webhook deleted' WHERE webhook_id=$1 AND status='pending'`, id); err != nil {
		return databaseError("cancel deleted webhook queue", err)
	}
	if err := notify(ctx, tx); err != nil {
		return databaseError("notify webhook deletion", err)
	}
	return databaseError("commit webhook deletion", tx.Commit(ctx))
}

func (s *Store) WebhookDeliveries(ctx context.Context, id string, options model.ListOptions) (model.List[model.WebhookDelivery], error) {
	result := model.List[model.WebhookDelivery]{Items: []model.WebhookDelivery{}, Limit: options.Limit, Offset: options.Offset}
	if err := validateActivityPage(options); err != nil {
		return result, err
	}
	switch options.Status {
	case "", "pending", "delivering", "delivered", "failed", "cancelled":
	default:
		return result, model.ErrInvalid
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, databaseError("begin webhook history", err)
	}
	defer rollback(tx)
	// Deleted hooks retain inspectable history when their ID is known.
	var exists bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM ingest.webhooks WHERE id=$1)", id).Scan(&exists); err != nil {
		return result, databaseError("read webhook history", err)
	}
	if !exists {
		return result, model.ErrNotFound
	}
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM ingest.webhook_deliveries WHERE webhook_id=$1 AND ($2='' OR status=$2)", id, options.Status).Scan(&result.Total); err != nil {
		return result, databaseError("count webhook deliveries", err)
	}
	rows, err := tx.Query(ctx, "SELECT "+deliveryColumns+" FROM ingest.webhook_deliveries WHERE webhook_id=$1 AND ($2='' OR status=$2) ORDER BY created_at DESC,id DESC LIMIT $3 OFFSET $4", id, options.Status, options.Limit, options.Offset)
	if err != nil {
		return result, databaseError("list webhook deliveries", err)
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanDelivery(rows)
		if err != nil {
			return result, databaseError("read webhook delivery", err)
		}
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return result, databaseError("list webhook deliveries", err)
	}
	return result, databaseError("finish webhook history", tx.Commit(ctx))
}

func (s *Store) QueueWebhookTest(ctx context.Context, id string) (model.WebhookDelivery, error) {
	identity, err := newRunID()
	if err != nil {
		return model.WebhookDelivery{}, err
	}
	event := model.WebhookEvent{Version: 1, ID: "test-" + identity, Type: "webhook.test", OccurredAt: time.Now().UTC(), Message: "Webhook test delivery"}
	body, err := json.Marshal(event)
	if err != nil {
		return model.WebhookDelivery{}, model.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return model.WebhookDelivery{}, databaseError("begin webhook test", err)
	}
	defer rollback(tx)
	if err := enabledWebhook(ctx, tx, id); err != nil {
		return model.WebhookDelivery{}, err
	}
	item, err := scanDelivery(tx.QueryRow(ctx, `INSERT INTO ingest.webhook_deliveries(id,webhook_id,event_id,event_type,body) VALUES($1,$2,$3,$4,$5) RETURNING `+deliveryColumns, id+":"+event.ID, id, event.ID, event.Type, body))
	if err != nil {
		return item, databaseError("queue webhook test", err)
	}
	if err := notify(ctx, tx); err != nil {
		return item, databaseError("notify webhook test", err)
	}
	return item, databaseError("commit webhook test", tx.Commit(ctx))
}

func enabledWebhook(ctx context.Context, tx pgx.Tx, id string) error {
	var enabled bool
	if err := tx.QueryRow(ctx, "SELECT enabled FROM ingest.webhooks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE", id).Scan(&enabled); err != nil {
		return databaseError("read webhook", err)
	}
	if !enabled {
		return model.ErrConflict
	}
	return nil
}

func (s *Store) RetryWebhookDelivery(ctx context.Context, webhookID, id string) (model.WebhookDelivery, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return model.WebhookDelivery{}, databaseError("begin webhook retry", err)
	}
	defer rollback(tx)
	if err := enabledWebhook(ctx, tx, webhookID); err != nil {
		return model.WebhookDelivery{}, err
	}
	item, err := scanDelivery(tx.QueryRow(ctx, "SELECT "+deliveryColumns+" FROM ingest.webhook_deliveries WHERE id=$1 AND webhook_id=$2 FOR UPDATE", id, webhookID))
	if err != nil {
		return item, databaseError("read webhook retry", err)
	}
	if item.Status != "failed" {
		return item, model.ErrConflict
	}
	item, err = scanDelivery(tx.QueryRow(ctx, `UPDATE ingest.webhook_deliveries SET status='pending',attempts=0,next_attempt_at=NOW(),last_status=NULL,last_error='',lease_until=NULL,lease_token=NULL WHERE id=$1 RETURNING `+deliveryColumns, id))
	if err != nil {
		return item, databaseError("queue webhook retry", err)
	}
	if err := notify(ctx, tx); err != nil {
		return item, databaseError("notify webhook retry", err)
	}
	return item, databaseError("commit webhook retry", tx.Commit(ctx))
}
