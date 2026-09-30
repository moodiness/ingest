package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/moodiness/ingest/internal/model"
	"github.com/moodiness/ingest/internal/scheduling"
)

func updatePolicySettings(t *testing.T, ctx context.Context, db *Store, change func(*model.CollectionSettings)) model.CollectionSettings {
	t.Helper()
	settings, err := db.CollectionSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	change(&settings)
	if err := db.UpdateCollectionSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	return settings
}

func TestCollectionPolicySnapshotsSurviveSettingsChangesAndResume(t *testing.T) {
	ctx, _, db := activityDatabase(t)
	original := updatePolicySettings(t, ctx, db, func(s *model.CollectionSettings) {
		s.MaxQuotaRetries, s.DefaultRequestTimeoutSeconds, s.DefaultMaxPages = 0, 71, 9
		s.DefaultMaxDurationSeconds = 123
	})
	run := mirrorClaim(t, ctx, db, model.Provider{ID: "snapshot", Adapter: "http_json"}, model.ModeFull)
	run, err := db.SavePage(ctx, run, model.Page{Body: []byte("response"), Next: json.RawMessage(`{"page":2}`)}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.FinishRun(ctx, run.ID, model.StatusPaused, "", model.PauseBudget); err != nil {
		t.Fatal(err)
	}
	updatePolicySettings(t, ctx, db, func(s *model.CollectionSettings) { s.MaxQuotaRetries, s.DefaultRequestTimeoutSeconds = 8, 22 })
	if err := db.UpdateCollectionSettings(ctx, original); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("stale revision overwrote settings: %v", err)
	}
	resumed, err := db.ResumeRun(ctx, run.ID)
	if err != nil || resumed.EffectivePolicy().MaxQuotaRetries != 0 || resumed.Config.RequestTimeout != "71s" || resumed.EffectivePolicy().MaxDurationSeconds != 123 || !bytes.Equal(run.Cursor, resumed.Cursor) {
		t.Fatalf("resume mutated the immutable snapshot: %+v %v", resumed, err)
	}
	policy := model.CollectionPolicy{MaxQuotaRetries: 1, NoProgressAction: "warn", MaxDurationSeconds: 9}
	direct, err := db.CreateRun(ctx, model.Run{ProviderID: "explicit", Config: model.Provider{ID: "explicit", RequestTimeout: "17s"}, Mode: model.ModePreview, Policy: &policy})
	if err != nil || direct.Policy == nil || *direct.Policy != policy || direct.Config.RequestTimeout != "17s" {
		t.Fatalf("explicit direct snapshot changed: %+v %v", direct, err)
	}
	source := scheduledSource(t)
	source.Provider.Schedule.MaxPages = 0
	start := time.Now().UTC()
	if _, err := db.ReconcileSchedules(ctx, []scheduling.Source{source}, start); err != nil {
		t.Fatal(err)
	}
	_, scheduled, err := db.AttemptSchedule(ctx, source, start.Add(time.Minute), nil)
	if err != nil || scheduled == nil || scheduled.MaxPages != 0 || scheduled.EffectivePolicy().MaxQuotaRetries != 8 || scheduled.Config.RequestTimeout != "22s" {
		t.Fatalf("scheduled explicit unlimited budget or new defaults lost: %+v %v", scheduled, err)
	}
	// A legacy NULL policy retains old execution semantics, not today's defaults.
	if _, err := db.pool.Exec(ctx, "UPDATE ingest.runs SET policy=NULL WHERE id=$1", direct.ID); err != nil {
		t.Fatal(err)
	}
	legacy, err := db.GetRun(ctx, direct.ID)
	if err != nil || legacy.Policy != nil || legacy.EffectivePolicy().MaxQuotaRetries != 3 || legacy.EffectivePolicy().NoProgressRequests != 0 || legacy.EffectivePolicy().MaxDurationSeconds != 0 {
		t.Fatalf("legacy policy changed: %+v %v", legacy, err)
	}
}

func TestUsefulProgressCountsNativeNoveltyNotCoverageRefresh(t *testing.T) {
	ctx, _, db := activityDatabase(t)
	updatePolicySettings(t, ctx, db, func(s *model.CollectionSettings) { s.NoProgressRequests = 2 })
	if err := db.PutSecret(ctx, "progress-hook", []byte("encrypted-fixture")); err != nil {
		t.Fatal(err)
	}
	hook, err := db.SaveWebhook(ctx, "", model.WebhookInput{Name: "Progress", Enabled: true, URLSecretRef: "progress-hook", Events: []string{"run.no_progress"}})
	if err != nil {
		t.Fatal(err)
	}
	run := mirrorClaim(t, ctx, db, model.Provider{ID: "progress", Adapter: "http_json"}, model.ModeFull)
	save := func(page model.Page) {
		t.Helper()
		var err error
		run, err = db.SavePage(ctx, run, page, "")
		if err != nil {
			t.Fatal(err)
		}
	}
	save(model.Page{Body: []byte("first"), Items: []model.Record{coverageRecord("one", "root")}})
	save(model.Page{Body: []byte("duplicate"), Items: []model.Record{coverageRecord("one", "root")}})
	// Same hash, different source identity is useful progress.
	save(model.Page{Body: []byte("same hash, new native ID"), Items: []model.Record{coverageRecord("two", "root")}})
	if run.RequestsWithoutNewIDs != 0 {
		t.Fatalf("native identity was confused with hash: %+v", run)
	}
	save(model.Page{RefreshScopes: []string{"root"}})
	save(model.Page{Body: []byte("quota"), Error: "rate limited"})
	if run.RequestsWithoutNewIDs != 0 {
		t.Fatalf("control or quota response consumed progress allowance: %+v", run)
	}
	save(model.Page{Body: []byte("refreshed old ID"), Items: []model.Record{coverageRecord("one", "root")}})
	save(model.Page{Body: []byte("refreshed other old ID"), Items: []model.Record{coverageRecord("two", "root")}})
	save(model.Page{Body: []byte("still repeated"), Items: []model.Record{coverageRecord("one", "root")}})
	if run.RequestsWithoutNewIDs != 3 || !run.ProgressWarningSent || run.PauseReason != "" {
		t.Fatalf("warning streak lost: %+v", run)
	}
	alerts, err := db.Notifications(ctx, false, model.ListOptions{Limit: 20})
	if err != nil || alerts.Total != 1 || alerts.Items[0].Kind != "run.no_progress" || alerts.Items[0].Level != "warn" {
		t.Fatalf("warning fanout missing or repeated: %+v %v", alerts, err)
	}
	logs, err := db.Logs(ctx, model.LogOptions{ListOptions: model.ListOptions{RunID: run.ID, Query: "run.no_progress", Limit: 20}, Levels: []string{"warn"}})
	if err != nil || logs.Total != 1 {
		t.Fatalf("durable warning log missing: %+v %v", logs, err)
	}
	deliveries, err := db.WebhookDeliveries(ctx, hook.ID, model.ListOptions{Limit: 20})
	if err != nil || deliveries.Total != 1 || deliveries.Items[0].EventType != "run.no_progress" {
		t.Fatalf("warning subscription missing or repeated: %+v %v", deliveries, err)
	}
	save(model.Page{Body: []byte("new generation"), ResetStaging: true, Items: []model.Record{coverageRecord("one", "root")}})
	if run.RequestsWithoutNewIDs != 0 || run.ProgressWarningSent {
		t.Fatalf("genuine reset did not start a new generation: %+v", run)
	}
	save(model.Page{Body: []byte("repeat")})
	save(model.Page{Body: []byte("repeat again")})
	alerts, err = db.Notifications(ctx, false, model.ListOptions{Limit: 20})
	if err != nil || alerts.Total != 2 {
		t.Fatalf("new warning streak not delivered: %+v %v", alerts, err)
	}
}

func TestUsefulProgressPauseResumeRetainsIDsAndAllowsFinalResponse(t *testing.T) {
	ctx, _, db := activityDatabase(t)
	updatePolicySettings(t, ctx, db, func(s *model.CollectionSettings) { s.NoProgressRequests, s.NoProgressAction = 1, "pause" })
	run := mirrorClaim(t, ctx, db, model.Provider{ID: "progress-pause", Adapter: "http_json"}, model.ModeFull)
	var err error
	for _, control := range []model.Page{
		{Body: []byte("capabilities"), Items: []model.Record{{Raw: []byte("caps"), Auxiliary: true, Ignored: true}}},
		{Body: []byte("options"), Metadata: map[string]any{"auxiliary_response": true}},
	} {
		run, err = db.SavePage(ctx, run, control, "")
		if err != nil || run.RequestsWithoutNewIDs != 0 || run.ProgressWarningSent || run.PauseReason != "" {
			t.Fatalf("auxiliary response exhausted allowance before first data request: %+v %v", run, err)
		}
	}
	run, err = db.SavePage(ctx, run, model.Page{Body: []byte("first"), Items: []model.Record{coverageRecord("one")}}, "")
	if err != nil {
		t.Fatal(err)
	}
	run, err = db.SavePage(ctx, run, model.Page{Body: []byte{}}, "")
	if err != nil || run.PauseReason != model.PauseNoProgress {
		t.Fatalf("progress pause missing: %+v %v", run, err)
	}
	if _, err := db.FinishRun(ctx, run.ID, model.StatusPaused, "", model.PauseNoProgress); err != nil {
		t.Fatal(err)
	}
	resumed, err := db.ResumeRun(ctx, run.ID)
	if err != nil || resumed.RequestsWithoutNewIDs != 0 || resumed.ProgressWarningSent || resumed.PauseReason != "" {
		t.Fatalf("explicit resume did not grant allowance: %+v %v", resumed, err)
	}
	claimed, err := db.ClaimNext(ctx)
	if err != nil || claimed == nil {
		t.Fatalf("resume claim: %+v %v", claimed, err)
	}
	run, err = db.SavePage(ctx, *claimed, model.Page{Body: []byte("final duplicate"), Items: []model.Record{coverageRecord("one")}, Done: true}, "")
	if err != nil || run.RequestsWithoutNewIDs != 1 || !run.ProgressWarningSent || run.PauseReason != "" {
		t.Fatalf("resume forgot IDs or final response paused: %+v %v", run, err)
	}
	finished, err := db.FinishRun(ctx, run.ID, model.StatusSucceeded, "", "")
	if err != nil || finished.Status != model.StatusSucceeded {
		t.Fatalf("final traversal failed publication: %+v %v", finished, err)
	}
}

func TestAutomaticRecoveryRequiresTechnicalInterruptionAndFreeLease(t *testing.T) {
	for _, test := range []struct {
		name, failure  string
		manual, cancel bool
		want           model.RunStatus
		reason         model.PauseReason
	}{
		{name: "technical", want: model.StatusQueued},
		{name: "manual", manual: true, want: model.StatusPaused, reason: model.PauseManual},
		{name: "cancel", cancel: true, want: model.StatusCancelled},
		{name: "authentication", failure: "authentication", want: model.StatusFailed},
		{name: "certificate", failure: "certificate", want: model.StatusFailed},
		{name: "configuration", failure: "configuration", want: model.StatusFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, endpoint, owner := activityDatabase(t)
			updatePolicySettings(t, ctx, owner, func(s *model.CollectionSettings) { s.AutoResumeInterrupted = true })
			run := mirrorClaim(t, ctx, owner, model.Provider{ID: "recovery", Adapter: "http_json"}, model.ModeFull)
			run, err := owner.SavePage(ctx, run, model.Page{Body: []byte("checkpoint"), Next: json.RawMessage(`{"page":2}`)}, "")
			if err != nil {
				t.Fatal(err)
			}
			if test.failure != "" {
				if _, err := owner.AddEvent(ctx, run.ID, "source_error", "private detail", map[string]any{"failure_code": test.failure}); err != nil {
					t.Fatal(err)
				}
			}
			if test.manual {
				if _, err := owner.RequestPause(ctx, run.ID); err != nil {
					t.Fatal(err)
				}
			}
			if test.cancel {
				if _, err := owner.RequestCancel(ctx, run.ID); err != nil {
					t.Fatal(err)
				}
			}
			peer, err := Open(ctx, endpoint)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(peer.Close)
			if err := peer.RecoverInterrupted(ctx, map[string]bool{run.ProviderID: true}); err != nil {
				t.Fatal(err)
			}
			live, err := peer.GetRun(ctx, run.ID)
			if err != nil || live.Status != model.StatusRunning {
				t.Fatalf("live lease stolen: %+v %v", live, err)
			}
			owner.Close()
			if err := peer.RecoverInterrupted(ctx, map[string]bool{run.ProviderID: true}); err != nil {
				t.Fatal(err)
			}
			recovered, err := peer.GetRun(ctx, run.ID)
			if err != nil || recovered.Status != test.want || recovered.PauseReason != test.reason || !bytes.Equal(recovered.Cursor, run.Cursor) {
				t.Fatalf("unsafe recovery: %+v %v", recovered, err)
			}
		})
	}
}
