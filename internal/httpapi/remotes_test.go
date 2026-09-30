package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moodiness/ingest/internal/connectors"
	"github.com/moodiness/ingest/internal/model"
	"github.com/moodiness/ingest/internal/providers"
	"github.com/moodiness/ingest/internal/store"
	"github.com/moodiness/ingest/internal/testutil"
	"github.com/moodiness/ingest/internal/vault"
)

func TestRemoteSummaryUsesCurrentDefinitionsAndEndpointBoundCheckpoint(t *testing.T) {
	ctx, databaseURL := testutil.NewDatabase(t)
	db, err := store.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	secrets, err := vault.New(ctx, db, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	if err := secrets.Put(ctx, "remote-password", "synthetic-remote-password"); err != nil {
		t.Fatal(err)
	}
	registry, err := providers.New(t.TempDir(), connectors.Validate)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	definition := `{
  "version": 1,
  "id": "remote-copy",
  "name": "Original remote",
  "adapter": "http_json",
  "url": "https://catalogue.example/api/catalogs/friend",
  "enabled": true,
  "auth": {
    "type": "bearer",
    "secret_ref": "remote-password"
  },
  "http": {"catalog": true},
  "schedule": {"every": "24h", "mode": "incremental"}
}`
	document, err := registry.Save("remote-copy", definition, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Save("local-source", `{"version":1,"id":"local-source","name":"Local","adapter":"http_json","url":"https://local.example/","enabled":false}`, ""); err != nil {
		t.Fatal(err)
	}
	run, err := db.CreateRun(ctx, model.Run{ProviderID: document.Provider.ID, Config: document.Provider, Mode: model.ModeIncremental})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := db.ClaimNext(ctx)
	if err != nil || claimed == nil || claimed.ID != run.ID {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	cursor, _ := json.Marshal(model.CatalogCursor{Version: 1, InstanceID: "remote-owner", Mode: "full", Done: true, Checkpoint: "complete"})
	if _, err := db.SavePage(ctx, *claimed, model.Page{Next: cursor, Done: true}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := db.FinishRun(ctx, run.ID, model.StatusSucceeded, "", ""); err != nil {
		t.Fatal(err)
	}
	s := &server{options: Options{Store: db, Providers: registry}}
	list := func() []remoteSummary {
		t.Helper()
		response := httptest.NewRecorder()
		s.listRemotes(response, httptest.NewRequest(http.MethodGet, "/api/remotes", nil).WithContext(ctx))
		var body struct {
			Items []remoteSummary `json:"items"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil {
			t.Fatalf("remote summary failed: %d %s", response.Code, response.Body.String())
		}
		return body.Items
	}
	items := list()
	if len(items) != 1 || !items[0].HasCheckpoint || items[0].LastSyncedAt == nil || items[0].LastRun == nil || items[0].LastRun.ID != run.ID {
		t.Fatalf("summary omitted committed remote state or included local definitions: %+v", items)
	}
	updatedJSON := strings.ReplaceAll(strings.ReplaceAll(definition, "Original remote", "Edited remote"), "catalogue.example", "replacement.example")
	updated, err := registry.Save("remote-copy", updatedJSON, document.Revision)
	if err != nil {
		t.Fatal(err)
	}
	items = list()
	if len(items) != 1 || items[0].Name != "Edited remote" || items[0].URL != updated.Provider.URL || items[0].Revision != updated.Revision || items[0].HasCheckpoint || items[0].LastSyncedAt != nil || items[0].SecretRef != "remote-password" {
		t.Fatalf("summary used stale configuration or another endpoint's checkpoint: %+v", items)
	}
}
