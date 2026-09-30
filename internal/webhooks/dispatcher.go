package webhooks

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/moodiness/ingest/internal/model"
	"github.com/moodiness/ingest/internal/store"
)

type Dispatcher struct {
	db       *store.Store
	resolver model.SecretResolver
	client   *http.Client
	mu       sync.Mutex
	started  bool
	cancel   context.CancelFunc
	done     chan struct{}
	err      error
}

func New(db *store.Store, resolver model.SecretResolver) *Dispatcher {
	return &Dispatcher{db: db, resolver: resolver, client: webhookClient(), done: make(chan struct{})}
}

func (d *Dispatcher) Start(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.started {
		return errors.New("webhook dispatcher already started")
	}
	if d.db == nil || d.resolver == nil {
		return errors.New("webhook store and secret resolver are required")
	}
	workerCtx, cancel := context.WithCancel(ctx)
	changes, unsubscribe, err := d.db.SubscribeChanges(workerCtx)
	if err != nil {
		cancel()
		return err
	}
	d.started = true
	d.cancel = cancel
	go func() {
		defer close(d.done)
		defer unsubscribe()
		defer d.client.CloseIdleConnections()
		err := d.run(workerCtx, changes)
		if workerCtx.Err() != nil {
			err = nil
		}
		d.mu.Lock()
		d.err = err
		d.mu.Unlock()
	}()
	return nil
}

func (d *Dispatcher) Done() <-chan struct{} { return d.done }
func (d *Dispatcher) Err() error            { d.mu.Lock(); defer d.mu.Unlock(); return d.err }

func (d *Dispatcher) Close(ctx context.Context) error {
	d.mu.Lock()
	if !d.started {
		d.mu.Unlock()
		return nil
	}
	d.cancel()
	d.mu.Unlock()
	select {
	case <-d.done:
		return d.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *Dispatcher) run(ctx context.Context, changes <-chan struct{}) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		claim, err := d.db.ClaimWebhookDelivery(ctx)
		if err != nil {
			return err
		}
		if claim != nil {
			if err := d.attempt(ctx, changes, claim); err != nil {
				return err
			}
			continue
		}
		next, err := d.db.NextWebhookAttempt(ctx)
		if err != nil {
			return err
		}
		var timer *time.Timer
		var due <-chan time.Time
		if next != nil {
			delay := time.Until(*next)
			if delay < time.Millisecond {
				delay = time.Millisecond
			}
			timer = time.NewTimer(delay)
			due = timer.C
		}
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return nil
		case _, ok := <-changes:
			if timer != nil {
				timer.Stop()
			}
			if !ok {
				return errors.New("webhook change subscription disconnected")
			}
		case <-due:
		}
	}
}

func (d *Dispatcher) attempt(ctx context.Context, changes <-chan struct{}, claim *store.WebhookClaim) error {
	defer claim.Release()
	attemptCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// A disable committed immediately after claiming must be observed before
	// resolving any secret or initiating the outbound HTTP request.
	enabled, err := d.db.WebhookClaimEnabled(attemptCtx, claim)
	if err != nil {
		return err
	}
	if !enabled {
		return d.finish(claim, attemptResult{outcome: "cancelled"})
	}
	result := make(chan attemptResult, 1)
	go func() { result <- deliver(attemptCtx, d.client, d.resolver, claim) }()
	var disabled bool
	var subscriptionError error
	ctxDone := ctx.Done()
	for {
		select {
		case <-ctxDone:
			ctxDone = nil
			cancel()
		case _, ok := <-changes:
			if !ok {
				subscriptionError = errors.New("webhook change subscription disconnected")
				changes = nil
				cancel()
				continue
			}
			enabled, err := d.db.WebhookClaimEnabled(attemptCtx, claim)
			if err != nil && attemptCtx.Err() == nil {
				subscriptionError = err
				cancel()
			}
			if err == nil && !enabled {
				disabled = true
				cancel()
			}
		case outcome := <-result:
			// A receiver may already have accepted a request when cancellation
			// races its response. At-least-once semantics require deduplication.
			if outcome.outcome != "delivered" {
				if disabled {
					outcome = attemptResult{outcome: "cancelled"}
				} else if ctx.Err() != nil || subscriptionError != nil {
					outcome = attemptResult{outcome: "interrupted", retry: true}
				}
			}
			if err := d.finish(claim, outcome); err != nil {
				return err
			}
			return subscriptionError
		}
	}
}

func (d *Dispatcher) finish(claim *store.WebhookClaim, result attemptResult) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var next *time.Time
	if result.retry {
		value := time.Now().Add(retryDelay(claim.Delivery.Attempts))
		next = &value
	}
	return d.db.FinishWebhookDelivery(ctx, claim, result.status, result.outcome, next)
}
