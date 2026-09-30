package store

import (
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/moodiness/ingest/internal/model"
)

func TestWebhookRetryCapAndExplicitRecovery(t *testing.T) {
	ctx, _, db := activityDatabase(t)
	hook := activityHook(t, ctx, db)
	queued, err := db.QueueWebhookTest(ctx, hook.ID)
	if err != nil {
		t.Fatal(err)
	}
	for index := range WebhookMaxAttempts {
		claim, err := db.ClaimWebhookDelivery(ctx)
		if err != nil || claim == nil {
			t.Fatalf("attempt %d was not claimable: %v", index+1, err)
		}
		if claim.Delivery.Attempts != index+1 || claim.Delivery.ID != queued.ID || claim.Delivery.EventID != queued.EventID {
			claim.Release()
			t.Fatalf("attempt counter or immutable identity changed: %+v", claim.Delivery)
		}
		due := time.Now().Add(-time.Second)
		err = db.FinishWebhookDelivery(ctx, claim, 503, "http", &due)
		claim.Release()
		if err != nil {
			t.Fatal(err)
		}
	}
	unexpected, err := db.ClaimWebhookDelivery(ctx)
	if err != nil || unexpected != nil {
		if unexpected != nil {
			unexpected.Release()
		}
		t.Fatalf("attempt cap allowed another automatic attempt: %v", err)
	}
	failed, err := db.WebhookDeliveries(ctx, hook.ID, model.ListOptions{Limit: 20, Status: "failed"})
	if err != nil || failed.Total != 1 || failed.Items[0].Attempts != WebhookMaxAttempts || failed.Items[0].NextAttemptAt != nil {
		t.Fatalf("exhaustion not persisted: %+v %v", failed, err)
	}
	retried, err := db.RetryWebhookDelivery(ctx, hook.ID, queued.ID)
	if err != nil || retried.ID != queued.ID || retried.EventID != queued.EventID || retried.Status != "pending" || retried.Attempts != 0 {
		t.Fatalf("explicit retry lost identity or remained exhausted: %+v %v", retried, err)
	}
	if _, err := db.RetryWebhookDelivery(ctx, hook.ID, queued.ID); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("pending delivery accepted duplicate retry: %v", err)
	}
	updated, err := db.SaveWebhook(ctx, hook.ID, model.WebhookInput{Name: "Renamed", Enabled: true, URLSecretRef: hook.URLSecretRef, Events: hook.Events, Revision: hook.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteWebhook(ctx, hook.ID, hook.Revision); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("stale revision deleted updated hook: %v", err)
	}
	if err := db.DeleteWebhook(ctx, hook.ID, updated.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RetryWebhookDelivery(ctx, hook.ID, queued.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("deleted hook accepted retry: %v", err)
	}
}

func TestWebhookClaimFailureDoesNotLeakSessionLock(t *testing.T) {
	ctx, endpoint, db := activityDatabase(t)
	hook := activityHook(t, ctx, db)
	if _, err := db.QueueWebhookTest(ctx, hook.ID); err != nil {
		t.Fatal(err)
	}
	// Session locks survive transaction rollback, including an error raised
	// after acquisition but before the function returns its result.
	if _, err := db.pool.Exec(ctx, `CREATE SCHEMA webhook_lock_fault;
CREATE FUNCTION webhook_lock_fault.pg_try_advisory_lock(key bigint) RETURNS boolean LANGUAGE plpgsql AS $$
BEGIN
PERFORM pg_catalog.pg_advisory_lock(key);
RAISE EXCEPTION 'injected failure after session lock acquisition';
END;
$$;`); err != nil {
		t.Fatal(err)
	}
	address, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal("parse test database address")
	}
	query := address.Query()
	query.Set("search_path", "webhook_lock_fault,pg_catalog")
	address.RawQuery = query.Encode()
	faulty, err := Open(ctx, address.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(faulty.Close)
	if claim, err := faulty.ClaimWebhookDelivery(ctx); err == nil || claim != nil {
		if claim != nil {
			claim.Release()
		}
		t.Fatalf("injected claim failure was not observed: %v", err)
	}
	claim, err := db.ClaimWebhookDelivery(ctx)
	if err != nil || claim == nil {
		t.Fatalf("failed claim stranded the pending delivery behind a leaked session lock: %v", err)
	}
	claim.Release()
}
