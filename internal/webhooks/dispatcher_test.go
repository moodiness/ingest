package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moodiness/ingest/internal/model"
	"github.com/moodiness/ingest/internal/store"
	"github.com/moodiness/ingest/internal/testutil"
)

func TestWebhookSigningAndStableRetryIdentity(t *testing.T) {
	body := []byte(`{"version":1,"id":"event-41","type":"run.failed","message":"Collection failed"}`)
	var requests atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		actual, err := io.ReadAll(r.Body)
		if err != nil || !bytes.Equal(actual, body) {
			t.Errorf("signed body changed: %q %v", actual, err)
		}
		if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("X-Ingest-Event-ID") != "event-41" || r.Header.Get("X-Ingest-Delivery-ID") != "delivery-41" {
			t.Errorf("unstable delivery contract: %s %v", r.Method, r.Header)
		}
		stamp := r.Header.Get("X-Ingest-Timestamp")
		unix, err := strconv.ParseInt(stamp, 10, 64)
		if err != nil || time.Since(time.Unix(unix, 0)) > 5*time.Second {
			t.Errorf("invalid signing timestamp: %q", stamp)
		}
		mac := hmac.New(sha256.New, []byte("private-signing-key"))
		_, _ = mac.Write([]byte(stamp + "."))
		_, _ = mac.Write(actual)
		if r.Header.Get("X-Ingest-Signature") != "sha256="+hex.EncodeToString(mac.Sum(nil)) {
			t.Error("signature does not bind timestamp and exact body bytes")
		}
		if requests.Load() == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer receiver.Close()
	client := webhookClient()
	defer client.CloseIdleConnections()
	resolver := func(_ context.Context, name string) (string, error) {
		if name == "url" {
			return receiver.URL + "/?token=private-url-token", nil
		}
		return "private-signing-key", nil
	}
	claim := &store.WebhookClaim{Delivery: model.WebhookDelivery{ID: "delivery-41", EventID: "event-41"}, URLSecretRef: "url", SigningSecretRef: "key", Body: body}
	first := deliver(context.Background(), client, resolver, claim)
	second := deliver(context.Background(), client, resolver, claim)
	if first.outcome != "http" || !first.retry || first.status != 503 || second.outcome != "delivered" || second.retry || requests.Load() != 2 {
		t.Fatalf("transient/success transitions: first=%+v second=%+v", first, second)
	}
}

func TestWebhookRedirectRefusalAndHTTPRetryPolicy(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1); w.WriteHeader(204) }))
	defer target.Close()
	client := webhookClient()
	defer client.CloseIdleConnections()
	for _, test := range []struct {
		status  int
		retry   bool
		outcome string
	}{{302, false, "redirect"}, {400, false, "http"}, {401, false, "http"}, {408, true, "http"}, {425, true, "http"}, {429, true, "http"}, {500, true, "http"}} {
		t.Run(strconv.Itoa(test.status), func(t *testing.T) {
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", target.URL+"/?token=private")
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, "private-response-body")
			}))
			defer receiver.Close()
			resolver := func(context.Context, string) (string, error) { return receiver.URL, nil }
			result := deliver(context.Background(), client, resolver, &store.WebhookClaim{URLSecretRef: "url", Body: []byte("{}")})
			if result.status != test.status || result.retry != test.retry || result.outcome != test.outcome {
				t.Fatalf("unexpected retry policy: %+v", result)
			}
			if strings.Contains(fmt.Sprint(result), "private") {
				t.Fatal("receiver content escaped outcome")
			}
		})
	}
	if redirected.Load() != 0 {
		t.Fatal("followed a redirect and disclosed an event to another endpoint")
	}
}

func TestWebhookDestinationAndSecretFailuresAreRedacted(t *testing.T) {
	client := webhookClient()
	defer client.CloseIdleConnections()
	claim := &store.WebhookClaim{URLSecretRef: "url", Body: []byte("{}")}
	failed := deliver(context.Background(), client, func(context.Context, string) (string, error) { return "", errors.New("private-secret-value") }, claim)
	if failed.outcome != "destination" || strings.Contains(fmt.Sprint(failed), "private") {
		t.Fatalf("secret error escaped: %+v", failed)
	}
	for _, value := range []string{"file:///etc/passwd", "https://user:private@example.com/", "http://169.254.169.254/", "http://[::]/", "http://224.0.0.1/", "https://example.com:65536/", "https://example.com/#private"} {
		if ValidateURL(value) == nil {
			t.Errorf("unsafe destination accepted: %s", value)
		}
	}
	receiver := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endpoint := receiver.URL + "/?token=private-url-token"
	receiver.Close()
	failed = deliver(context.Background(), client, func(context.Context, string) (string, error) { return endpoint, nil }, claim)
	if failed.outcome != "network" || !failed.retry || strings.Contains(fmt.Sprint(failed), "private") {
		t.Fatalf("network URL error escaped or lost retry: %+v", failed)
	}
}

func TestDispatcherPersistsRetryAndResolvesChangedEndpoint(t *testing.T) {
	ctx, url := testutil.NewDatabase(t)
	db, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.PutSecret(ctx, "url", []byte("encrypted-fixture")); err != nil {
		t.Fatal(err)
	}
	hook, err := db.SaveWebhook(ctx, "", model.WebhookInput{Name: "Receiver", Enabled: true, URLSecretRef: "url", Events: []string{"run.failed"}})
	if err != nil {
		t.Fatal(err)
	}
	type observed struct {
		event, delivery string
		body            []byte
	}
	first := make(chan observed, 1)
	second := make(chan observed, 1)
	failed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		first <- observed{r.Header.Get("X-Ingest-Event-ID"), r.Header.Get("X-Ingest-Delivery-ID"), body}
		w.WriteHeader(503)
	}))
	defer failed.Close()
	succeeded := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		second <- observed{r.Header.Get("X-Ingest-Event-ID"), r.Header.Get("X-Ingest-Delivery-ID"), body}
		w.WriteHeader(204)
	}))
	defer succeeded.Close()
	var endpoint atomic.Value
	endpoint.Store(failed.URL)
	dispatcher := New(db, func(context.Context, string) (string, error) { return endpoint.Load().(string), nil })
	if err := dispatcher.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := dispatcher.Close(shutdown); err != nil {
			t.Error(err)
		}
	}()
	queued, err := db.QueueWebhookTest(ctx, hook.ID)
	if err != nil {
		t.Fatal(err)
	}
	var initial observed
	select {
	case initial = <-first:
	case <-ctx.Done():
		t.Fatal("initial delivery was not sent")
	}
	endpoint.Store(succeeded.URL)
	var retried observed
	select {
	case retried = <-second:
	case <-time.After(12 * time.Second):
		t.Fatal("durable due-time retry was not sent")
	}
	if initial.event != queued.EventID || retried.event != initial.event || retried.delivery != initial.delivery || !bytes.Equal(retried.body, initial.body) {
		t.Fatal("retry changed immutable delivery identity or envelope")
	}
	var event model.WebhookEvent
	if err := json.Unmarshal(retried.body, &event); err != nil || event.Version != 1 || event.Type != "webhook.test" {
		t.Fatalf("invalid event envelope: %+v %v", event, err)
	}
	changes, stop, err := db.SubscribeChanges(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	for {
		deliveries, err := db.WebhookDeliveries(ctx, hook.ID, model.ListOptions{Limit: 20})
		if err != nil {
			t.Fatal(err)
		}
		if len(deliveries.Items) == 1 && deliveries.Items[0].Status == "delivered" {
			if deliveries.Items[0].Attempts != 2 || deliveries.Items[0].DeliveredAt == nil {
				t.Fatalf("retry was not durably finalized: %+v", deliveries.Items[0])
			}
			break
		}
		select {
		case <-changes:
		case <-ctx.Done():
			t.Fatal("delivery result not persisted")
		}
	}
}

func TestTransientSecretLookupKeepsDeliveryPending(t *testing.T) {
	for _, failedRef := range []string{"url", "signing"} {
		t.Run(failedRef, func(t *testing.T) {
			ctx, url := testutil.NewDatabase(t)
			db, err := store.Open(ctx, url)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err := db.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			for _, ref := range []string{"url", "signing"} {
				if err := db.PutSecret(ctx, ref, []byte("encrypted-fixture")); err != nil {
					t.Fatal(err)
				}
			}
			hook, err := db.SaveWebhook(ctx, "", model.WebhookInput{Name: "Receiver", Enabled: true, URLSecretRef: "url", SigningSecretRef: "signing", Events: []string{"run.failed"}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.QueueWebhookTest(ctx, hook.ID); err != nil {
				t.Fatal(err)
			}
			claim, err := db.ClaimWebhookDelivery(ctx)
			if err != nil || claim == nil {
				t.Fatalf("claim webhook: %+v %v", claim, err)
			}
			dispatcher := New(db, func(_ context.Context, ref string) (string, error) {
				if ref == failedRef {
					return "", errors.New("transient-private-storage-detail")
				}
				return "https://receiver.example.invalid/events", nil
			})
			defer dispatcher.client.CloseIdleConnections()
			if err := dispatcher.attempt(ctx, nil, claim); err != nil {
				t.Fatal(err)
			}
			deliveries, err := db.WebhookDeliveries(ctx, hook.ID, model.ListOptions{Limit: 20})
			if err != nil || len(deliveries.Items) != 1 {
				t.Fatalf("read delivery outcome: %+v %v", deliveries, err)
			}
			outcome := deliveries.Items[0]
			if outcome.Status != "pending" || outcome.NextAttemptAt == nil || outcome.Attempts != 1 {
				t.Fatalf("transient secret lookup lost its durable retry: %+v", outcome)
			}
			if strings.Contains(outcome.LastError, "private") {
				t.Fatalf("secret lookup detail escaped: %+v", outcome)
			}
		})
	}
}
