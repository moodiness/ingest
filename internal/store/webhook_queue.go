package store

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/ingest/internal/model"
)

const WebhookLeaseDuration = time.Minute
const WebhookMaxAttempts = 5

// WebhookClaim owns a session advisory lock as well as a durable lease. A worker
// whose process is suspended beyond its lease cannot overlap a recovery worker:
// only session loss permits recovery. HTTP attempts must have a shorter timeout
// than WebhookLeaseDuration. Always call Release, including after Finish.
type WebhookClaim struct {
	Delivery         model.WebhookDelivery
	URLSecretRef     string
	SigningSecretRef string
	Body             []byte
	Token            string
	conn             *pgxpool.Conn
	key              int64
	once             sync.Once
}

func (claim *WebhookClaim) Release() {
	claim.once.Do(func() { releaseConnection(claim.conn, claim.key) })
}

// ClaimWebhookDelivery claims at most one due row. SKIP LOCKED plus the guarded
// state transition supports multiple service instances without duplicate claims.
func (s *Store) ClaimWebhookDelivery(ctx context.Context) (*WebhookClaim, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, databaseError("acquire webhook worker", err)
	}
	retained := false
	var heldKey *int64
	defer func() {
		if !retained {
			if heldKey != nil {
				releaseConnection(conn, *heldKey)
			} else {
				conn.Release()
			}
		}
	}()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, databaseError("begin webhook claim", err)
	}
	defer rollback(tx)
	var id, webhookID string
	err = tx.QueryRow(ctx, `SELECT id,webhook_id FROM ingest.webhook_deliveries
 WHERE (status='pending' AND next_attempt_at<=NOW()) OR (status='delivering' AND lease_until<=NOW())
 ORDER BY COALESCE(next_attempt_at,lease_until),id LIMIT 1`).Scan(&id, &webhookID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, databaseError("find due webhook", err)
	}
	// Every mutation takes hook-before-delivery locks, including disable/delete.
	var enabled bool
	claim := &WebhookClaim{conn: conn, key: providerLockKey("webhook-delivery:" + id)}
	err = tx.QueryRow(ctx, `SELECT enabled AND deleted_at IS NULL,url_secret_ref,signing_secret_ref FROM ingest.webhooks WHERE id=$1 FOR UPDATE`, webhookID).Scan(&enabled, &claim.URLSecretRef, &claim.SigningSecretRef)
	if err != nil {
		return nil, databaseError("lock webhook destination", err)
	}
	claim.Delivery, err = scanDelivery(tx.QueryRow(ctx, `SELECT `+deliveryColumns+` FROM ingest.webhook_deliveries WHERE id=$1
 AND ((status='pending' AND next_attempt_at<=NOW()) OR (status='delivering' AND lease_until<=NOW())) FOR UPDATE SKIP LOCKED`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, databaseError("lock webhook delivery", err)
	}
	var locked bool
	if err := tx.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", claim.key).Scan(&locked); err != nil {
		// The server may have acquired the session lock before cancellation
		// interrupted its reply. Never pool a possibly locked connection.
		heldKey = &claim.key
		return nil, databaseError("claim webhook lease", err)
	}
	if !locked {
		// A suspended but still-connected process remains the owner. Moving the
		// recovery deadline avoids spinning on an expired-but-owned lease.
		if _, err := tx.Exec(ctx, `UPDATE ingest.webhook_deliveries SET lease_until=NOW()+interval '1 minute' WHERE id=$1`, id); err != nil {
			return nil, databaseError("defer owned webhook recovery", err)
		}
		return nil, databaseError("commit webhook recovery deadline", tx.Commit(ctx))
	}
	// Once the session owns a lock it must never return to the pool locked.
	retained = true
	success := false
	defer func() {
		if !success {
			rollback(tx)
			claim.Release()
		}
	}()
	if !enabled || claim.Delivery.Attempts >= WebhookMaxAttempts {
		status, message := "cancelled", "Webhook disabled or deleted"
		if enabled {
			status, message = "failed", "Delivery attempts exhausted after interrupted attempt"
		}
		if _, err := tx.Exec(ctx, `UPDATE ingest.webhook_deliveries SET status=$2,next_attempt_at=NULL,lease_until=NULL,lease_token=NULL,last_error=$3 WHERE id=$1`, id, status, message); err != nil {
			return nil, databaseError("finish unavailable webhook", err)
		}
		if err := notify(ctx, tx); err != nil {
			return nil, databaseError("notify webhook recovery", err)
		}
		return nil, databaseError("commit webhook recovery", tx.Commit(ctx))
	}
	claim.Token, err = newRunID()
	if err != nil {
		return nil, err
	}
	claim.Delivery, err = scanDelivery(tx.QueryRow(ctx, `UPDATE ingest.webhook_deliveries SET status='delivering',attempts=attempts+1,next_attempt_at=NULL,lease_until=NOW()+interval '1 minute',lease_token=$2 WHERE id=$1 RETURNING `+deliveryColumns, id, claim.Token))
	if err != nil {
		return nil, databaseError("claim webhook delivery", err)
	}
	if err := tx.QueryRow(ctx, "SELECT body FROM ingest.webhook_deliveries WHERE id=$1", id).Scan(&claim.Body); err != nil {
		return nil, databaseError("read webhook envelope", err)
	}
	if err := notify(ctx, tx); err != nil {
		return nil, databaseError("notify webhook claim", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, databaseError("commit webhook claim", err)
	}
	success = true
	return claim, nil
}

// NextWebhookAttempt is the next durable retry or recovery deadline. A nil
// deadline means the dispatcher can sleep until SubscribeChanges wakes it.
func (s *Store) NextWebhookAttempt(ctx context.Context) (*time.Time, error) {
	var next *time.Time
	err := s.pool.QueryRow(ctx, `SELECT min(CASE WHEN status='pending' THEN next_attempt_at ELSE lease_until END) FROM ingest.webhook_deliveries WHERE status IN ('pending','delivering')`).Scan(&next)
	return next, databaseError("read webhook deadline", err)
}

func (s *Store) WebhookClaimEnabled(ctx context.Context, claim *WebhookClaim) (bool, error) {
	var enabled bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ingest.webhooks w JOIN ingest.webhook_deliveries d ON d.webhook_id=w.id WHERE d.id=$1 AND d.lease_token=$2 AND d.status='delivering' AND w.enabled AND w.deleted_at IS NULL)`, claim.Delivery.ID, claim.Token).Scan(&enabled)
	return enabled, databaseError("check webhook attempt", err)
}

// FinishWebhookDelivery persists only an enumerated safe outcome, never an HTTP
// error string, URL, header or response body. retryAt is ignored at the attempt cap.
func (s *Store) FinishWebhookDelivery(ctx context.Context, claim *WebhookClaim, statusCode int, outcome string, retryAt *time.Time) error {
	var status, message string
	switch outcome {
	case "delivered":
		status = "delivered"
	case "network":
		status, message = "failed", "Destination connection failed or timed out"
	case "http":
		status, message = "failed", "Destination returned an unsuccessful HTTP status"
	case "redirect":
		status, message = "failed", "Destination redirect refused"
	case "destination":
		status, message = "failed", "Destination secret is missing or contains an invalid URL"
	case "signing":
		status, message = "failed", "Signing secret is unavailable"
	case "cancelled":
		status, message = "cancelled", "Webhook disabled or deleted"
	case "interrupted":
		status, message = "failed", "Delivery interrupted by service shutdown"
	default:
		return model.ErrInvalid
	}
	if statusCode < 0 || statusCode > 999 {
		return model.ErrInvalid
	}
	if status == "failed" && retryAt != nil && claim.Delivery.Attempts < WebhookMaxAttempts {
		status = "pending"
	} else {
		retryAt = nil
	}
	tx, err := claim.conn.Begin(ctx)
	if err != nil {
		return databaseError("begin webhook outcome", err)
	}
	defer rollback(tx)
	var enabled bool
	if err := tx.QueryRow(ctx, `SELECT enabled AND deleted_at IS NULL FROM ingest.webhooks WHERE id=$1 FOR UPDATE`, claim.Delivery.WebhookID).Scan(&enabled); err != nil {
		return databaseError("lock webhook outcome", err)
	}
	if !enabled && status != "delivered" {
		status, message, retryAt = "cancelled", "Webhook disabled or deleted", nil
	}
	var code *int
	if statusCode != 0 {
		code = &statusCode
	}
	result, err := tx.Exec(ctx, `UPDATE ingest.webhook_deliveries SET status=$3,last_status=$4,last_error=$5,next_attempt_at=$6,lease_until=NULL,lease_token=NULL,delivered_at=CASE WHEN $3='delivered' THEN NOW() ELSE NULL END WHERE id=$1 AND lease_token=$2 AND status='delivering'`, claim.Delivery.ID, claim.Token, status, code, message, retryAt)
	if err != nil {
		return databaseError("save webhook outcome", err)
	}
	if result.RowsAffected() != 1 {
		return model.ErrConflict
	}
	if err := notify(ctx, tx); err != nil {
		return databaseError("notify webhook outcome", err)
	}
	return databaseError("commit webhook outcome", tx.Commit(ctx))
}
