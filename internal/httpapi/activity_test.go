package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moodiness/ingest/internal/jobs"
	"github.com/moodiness/ingest/internal/model"
	"github.com/moodiness/ingest/internal/store"
	"github.com/moodiness/ingest/internal/testutil"
	"github.com/moodiness/ingest/internal/vault"
)

func TestWebhookCanBeDisabledAfterInvalidSecretRotation(t *testing.T) {
	ctx, url := testutil.NewDatabase(t)
	db, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	secrets, err := vault.New(ctx, db, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	if err := secrets.Put(ctx, "destination", "https://receiver.example.invalid/events"); err != nil {
		t.Fatal(err)
	}
	hook, err := db.SaveWebhook(ctx, "", model.WebhookInput{Name: "Receiver", Enabled: true, URLSecretRef: "destination", Events: []string{"run.failed"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.QueueWebhookTest(ctx, hook.ID); err != nil {
		t.Fatal(err)
	}
	if err := secrets.Put(ctx, "destination", "not-a-valid-url"); err != nil {
		t.Fatal(err)
	}
	s := &server{options: Options{Store: db, Vault: secrets, Jobs: jobs.New(db, nil, nil, 1)}}
	update := func(enabled bool, revision int64) *httptest.ResponseRecorder {
		t.Helper()
		body, err := json.Marshal(model.WebhookInput{Name: hook.Name, Enabled: enabled, URLSecretRef: hook.URLSecretRef, Events: hook.Events, Revision: revision})
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPut, "/api/webhooks/"+hook.ID, bytes.NewReader(body)).WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		r.SetPathValue("id", hook.ID)
		w := httptest.NewRecorder()
		s.updateWebhook(w, r)
		return w
	}
	disabled := update(false, hook.Revision)
	if disabled.Code != http.StatusOK {
		t.Fatalf("invalid destination prevented disabling: %d %s", disabled.Code, disabled.Body.String())
	}
	var saved model.Webhook
	if err := json.Unmarshal(disabled.Body.Bytes(), &saved); err != nil || saved.Enabled {
		t.Fatalf("webhook remained enabled: %+v %v", saved, err)
	}
	deliveries, err := db.WebhookDeliveries(ctx, hook.ID, model.ListOptions{Limit: 20})
	if err != nil || len(deliveries.Items) != 1 || deliveries.Items[0].Status != "cancelled" {
		t.Fatalf("disabling left a pending delivery: %+v %v", deliveries, err)
	}
	if enabled := update(true, saved.Revision); enabled.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid destination was re-enabled: %d %s", enabled.Code, enabled.Body.String())
	}
}
