package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/moodiness/ingest/internal/model"
	"github.com/moodiness/ingest/internal/testutil"
)

func activityDatabase(t *testing.T) (context.Context, string, *Store) {
	t.Helper()
	ctx, url := testutil.NewDatabase(t)
	db, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return ctx, url, db
}

func activityHook(t *testing.T, ctx context.Context, db *Store) model.Webhook {
	t.Helper()
	if err := db.PutSecret(ctx, "destination", []byte("encrypted-test-fixture")); err != nil {
		t.Fatal(err)
	}
	hook, err := db.SaveWebhook(ctx, "", model.WebhookInput{Name: "Test receiver", Enabled: true, URLSecretRef: "destination", Events: []string{"run.succeeded", "run.failed", "schedule.failed"}})
	if err != nil {
		t.Fatal(err)
	}
	return hook
}

func TestActivityAtomicFanoutFilteringAndAcknowledgements(t *testing.T) {
	ctx, _, db := activityDatabase(t)
	hook := activityHook(t, ctx, db)
	run, err := db.CreateRun(ctx, model.Run{ProviderID: "source", Config: model.Provider{ID: "source"}, Mode: model.ModePreview})
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now().Add(-time.Second)
	if _, err := db.AddEvent(ctx, run.ID, "page_saved", "https://private.example/?token=never-publish", map[string]any{"page": 2, "records": 3, "headers": map[string]any{"Authorization": "never-publish"}, "mode": "never-publish", "errors": "never-publish", "done": true, "failure_code": "never-publish", "health_code": "never-publish", "backup_id": "https://private.example/never-publish", "bytes": "never-publish"}); err != nil {
		t.Fatal(err)
	}
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := lifecycleEvent(ctx, tx, run.ID, "succeeded", "never-publish"); err != nil {
		t.Fatal(err)
	}
	var fanout int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM ingest.webhook_deliveries WHERE webhook_id=$1", hook.ID).Scan(&fanout); err != nil || fanout != 1 {
		t.Fatalf("fanout absent inside event transaction: %d %v", fanout, err)
	}
	rollback(tx)
	rolledBack, err := db.Notifications(ctx, false, model.ListOptions{Limit: 20})
	if err != nil || rolledBack.Total != 0 {
		t.Fatalf("rolled back event leaked notification: %+v %v", rolledBack, err)
	}
	if _, err := db.AddEvent(ctx, run.ID, "succeeded", "never-publish", map[string]any{"records": 3, "url": "never-publish"}); err != nil {
		t.Fatal(err)
	}
	debug, err := db.Logs(ctx, model.LogOptions{ListOptions: model.ListOptions{ProviderID: "source", RunID: run.ID, Limit: 1}, Levels: []string{"debug"}, From: &before})
	if err != nil || debug.Total != 1 || len(debug.Items) != 1 || debug.Items[0].Kind != "run.page_saved" {
		t.Fatalf("combined log filters: %+v %v", debug, err)
	}
	encoded, _ := json.Marshal(debug)
	if strings.Contains(string(encoded), "never-publish") || strings.Contains(string(encoded), "Authorization") {
		t.Fatalf("private event metadata escaped: %s", encoded)
	}
	if debug.Items[0].Data["records"] != json.Number("3") {
		t.Fatalf("safe numeric metadata lost: %+v", debug.Items[0].Data)
	}
	notifications, err := db.Notifications(ctx, true, model.ListOptions{Limit: 20})
	if err != nil || notifications.Total != 1 || notifications.UnreadCount != 1 {
		t.Fatalf("terminal notification: %+v %v", notifications, err)
	}
	id := notifications.Items[0].ID
	if err := db.ReadNotification(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := db.ReadNotification(ctx, id); err != nil {
		t.Fatal(err)
	}
	notifications, err = db.Notifications(ctx, true, model.ListOptions{Limit: 20})
	if err != nil || notifications.Total != 0 || notifications.UnreadCount != 0 {
		t.Fatalf("acknowledgement not durable: %+v %v", notifications, err)
	}
	if _, err := db.AddEvent(ctx, run.ID, "failed", "never-publish", nil); err != nil {
		t.Fatal(err)
	}
	if err := db.ReadAllNotifications(ctx); err != nil {
		t.Fatal(err)
	}
	notifications, err = db.Notifications(ctx, false, model.ListOptions{Limit: 1, Offset: 1})
	if err != nil || notifications.Total != 2 || notifications.UnreadCount != 0 || len(notifications.Items) != 1 || notifications.Items[0].ReadAt == nil {
		t.Fatalf("read-all/pagination mismatch: %+v %v", notifications, err)
	}
	deliveries, err := db.WebhookDeliveries(ctx, hook.ID, model.ListOptions{Limit: 20})
	if err != nil || deliveries.Total != 2 {
		t.Fatalf("committed event outbox: %+v %v", deliveries, err)
	}
}

func TestScheduleFailureDedupAndRecovery(t *testing.T) {
	ctx, _, db := activityDatabase(t)
	for range 2 {
		if err := db.RecordActivity(ctx, "error", "schedule.failed", "token=never-publish", "source", "", nil); err != nil {
			t.Fatal(err)
		}
	}
	first, err := db.Notifications(ctx, true, model.ListOptions{Limit: 20})
	if err != nil || first.Total != 1 {
		t.Fatalf("repeated failure alert storm: %+v %v", first, err)
	}
	if err := db.RecordActivity(ctx, "info", "schedule.queued", "", "source", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordActivity(ctx, "error", "schedule.failed", "", "source", "", nil); err != nil {
		t.Fatal(err)
	}
	after, err := db.Notifications(ctx, true, model.ListOptions{Limit: 20})
	if err != nil || after.Total != 2 {
		t.Fatalf("new failure after recovery lost: %+v %v", after, err)
	}
}

func TestWebhookClaimRecoveryAndDisable(t *testing.T) {
	ctx, url, db := activityDatabase(t)
	hook := activityHook(t, ctx, db)
	queued, err := db.QueueWebhookTest(ctx, hook.ID)
	if err != nil {
		t.Fatal(err)
	}
	other, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(other.Close)
	claim, err := db.ClaimWebhookDelivery(ctx)
	if err != nil || claim == nil {
		t.Fatalf("initial claim: %+v %v", claim, err)
	}
	defer claim.Release()
	if _, err := db.pool.Exec(ctx, "UPDATE ingest.webhook_deliveries SET lease_until=NOW()-interval '1 second' WHERE id=$1", queued.ID); err != nil {
		t.Fatal(err)
	}
	overlap, err := other.ClaimWebhookDelivery(ctx)
	if err != nil || overlap != nil {
		if overlap != nil {
			overlap.Release()
		}
		t.Fatalf("live lease owner overlapped: %v", err)
	}
	claim.Release()
	if _, err := db.pool.Exec(ctx, "UPDATE ingest.webhook_deliveries SET lease_until=NOW()-interval '1 second' WHERE id=$1", queued.ID); err != nil {
		t.Fatal(err)
	}
	recovered, err := other.ClaimWebhookDelivery(ctx)
	if err != nil || recovered == nil {
		t.Fatalf("expired disconnected lease not recovered: %v", err)
	}
	defer recovered.Release()
	if recovered.Delivery.ID != queued.ID || recovered.Delivery.EventID != queued.EventID || recovered.Delivery.Attempts != 2 || recovered.Token == claim.Token {
		t.Fatalf("recovery changed delivery identity or did not fence old owner: %+v", recovered.Delivery)
	}
	if err := other.FinishWebhookDelivery(ctx, recovered, 200, "delivered", nil); err != nil {
		t.Fatal(err)
	}
	recovered.Release()
	if err := db.DeleteSecret(ctx, "destination"); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("referenced secret deletion allowed: %v", err)
	}
	if _, err := db.QueueWebhookTest(ctx, hook.ID); err != nil {
		t.Fatal(err)
	}
	hook, err = db.SaveWebhook(ctx, hook.ID, model.WebhookInput{Name: hook.Name, Enabled: false, URLSecretRef: hook.URLSecretRef, Events: hook.Events, Revision: hook.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.QueueWebhookTest(ctx, hook.ID); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("disabled hook test accepted: %v", err)
	}
	cancelled, err := db.WebhookDeliveries(ctx, hook.ID, model.ListOptions{Limit: 20, Status: "cancelled"})
	if err != nil || cancelled.Total != 1 {
		t.Fatalf("disabled queue survived: %+v %v", cancelled, err)
	}
	if err := db.DeleteWebhook(ctx, hook.ID, hook.Revision); err != nil {
		t.Fatal(err)
	}
	history, err := db.WebhookDeliveries(ctx, hook.ID, model.ListOptions{Limit: 20})
	if err != nil || history.Total != 2 {
		t.Fatalf("soft deletion lost history: %+v %v", history, err)
	}
	active, err := db.Webhooks(ctx, false)
	if err != nil || len(active) != 0 {
		t.Fatalf("deleted hook remained active: %+v %v", active, err)
	}
	archived, err := db.Webhooks(ctx, true)
	if err != nil || len(archived) != 1 || archived[0].DeletedAt == nil {
		t.Fatalf("deleted history cannot be discovered: %+v %v", archived, err)
	}
	if err := db.DeleteSecret(ctx, "destination"); err != nil {
		t.Fatalf("deleted hook retained live secret reference: %v", err)
	}
}

func TestActivityMigrationBackfillsWithoutChangingEvents(t *testing.T) {
	ctx, url := testutil.NewDatabase(t)
	db, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := db.pool.Exec(ctx, "CREATE SCHEMA ingest;"+initialSchema+observationSchema+scheduleSchema+`
INSERT INTO ingest.runs(id,provider_id,provider_name,mode,status,revision,config)
VALUES('old-source-run','old-source','Old source','preview','queued','','{"id":"old-source"}');
INSERT INTO ingest.events(run_id,kind,message,data)
VALUES('old-source-run','queued','Collection queued','{}');`); err != nil {
		t.Fatal(err)
	}
	event, err := db.AddEvent(ctx, "old-source-run", "succeeded", "original private history", map[string]any{"url": "private"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, activitySchema); err != nil {
		t.Fatal(err)
	}
	events, err := db.Events(ctx, "old-source-run", model.ListOptions{Limit: 20})
	if err != nil || len(events.Items) != 2 || events.Items[1].Message != event.Message {
		t.Fatalf("migration mutated original history: %+v %v", events, err)
	}
	logs, err := db.Logs(ctx, model.LogOptions{ListOptions: model.ListOptions{Limit: 20}})
	if err != nil || logs.Total != 2 {
		t.Fatalf("history not backfilled: %+v %v", logs, err)
	}
	notifications, err := db.Notifications(ctx, true, model.ListOptions{Limit: 20})
	if err != nil || notifications.Total != 1 {
		t.Fatalf("notable history not backfilled: %+v %v", notifications, err)
	}
	var deliveries int
	if err := db.pool.QueryRow(ctx, "SELECT count(*) FROM ingest.webhook_deliveries").Scan(&deliveries); err != nil || deliveries != 0 {
		t.Fatalf("historical events queued deliveries: %d %v", deliveries, err)
	}
}
