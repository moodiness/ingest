package backups

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/jackc/pgx/v5"
	"github.com/moodiness/ingest/internal/model"
	"github.com/moodiness/ingest/internal/store"
	"github.com/moodiness/ingest/internal/testutil"
)

func encryptedRecoveryFixture(t *testing.T) ([]byte, *age.X25519Identity) {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	writer, err := age.Encrypt(&out, identity.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Write([]byte("completed encrypted recovery fixture")); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes(), identity
}

func TestInterruptedArchivesRecoverAcrossBothCommitBoundaries(t *testing.T) {
	ctx, databaseURL := testutil.NewDatabase(t)
	db, err := store.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	ciphertext, identity := encryptedRecoveryFixture(t)
	settings, err := db.BackupSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.SetBackupRecipient(ctx, identity.Recipient().String(), settings.Revision); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	s := &Service{root: root, options: Options{Store: db}}
	untouched := filepath.Join(dir, strings.Repeat("ef", 16)+".age")
	if err = os.WriteFile(untouched, ciphertext, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, id, suffix string
		complete         bool
	}{
		{"before rename", strings.Repeat("ab", 16), ".partial", true},
		{"after rename", strings.Repeat("ac", 16), ".age", true},
		{"incomplete stream", strings.Repeat("ad", 16), ".partial", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err = db.QueueBackup(ctx, tc.id, "backup", "", "crashed-worker"); err != nil {
				t.Fatal(err)
			}
			if _, err = db.ClaimBackup(ctx, "crashed-worker"); err != nil {
				t.Fatal(err)
			}
			data := ciphertext
			if !tc.complete {
				data = ciphertext[:20]
			}
			if err = os.WriteFile(filepath.Join(dir, tc.id+tc.suffix), data, 0600); err != nil {
				t.Fatal(err)
			}
			if tc.complete {
				sum := sha256.Sum256(data)
				if err = db.StageBackupArchive(ctx, tc.id, "crashed-worker", int64(len(data)), hex.EncodeToString(sum[:]), map[string]any{"tables": 1}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = conn.Exec(ctx, `UPDATE ingest.backup_jobs SET lease_until=now()-interval '1 second' WHERE id=$1`, tc.id); err != nil {
				t.Fatal(err)
			}
			if err = db.RecoverBackups(ctx); err != nil {
				t.Fatal(err)
			}
			if err = s.recoverArtifacts(ctx); err != nil {
				t.Fatal(err)
			}
			job, err := db.BackupJob(ctx, tc.id)
			if err != nil {
				t.Fatal(err)
			}
			if tc.complete {
				if job.Status != "succeeded" || job.CleanupPending {
					t.Fatalf("completed archive unavailable after recovery: %+v", job)
				}
				file, _, err := s.Download(ctx, tc.id)
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				plain, err := age.Decrypt(file, identity)
				if err != nil {
					t.Fatal(err)
				}
				actual, err := io.ReadAll(plain)
				if err != nil || string(actual) != "completed encrypted recovery fixture" {
					t.Fatalf("recovered ciphertext changed: %q %v", actual, err)
				}
			} else if job.Status != "failed" || job.CleanupPending {
				t.Fatalf("incomplete stream was not cleaned as a failed backup: %+v", job)
			}
			if _, err = os.Stat(filepath.Join(dir, tc.id+".partial")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("temporary archive remains: %v", err)
			}
			actual, err := os.ReadFile(untouched)
			if err != nil || !bytes.Equal(actual, ciphertext) {
				t.Fatalf("unrelated successful archive changed: %v", err)
			}
			var failed, succeeded int
			if err = conn.QueryRow(ctx, `SELECT count(*) FILTER(WHERE kind='backup.failed'),count(*) FILTER(WHERE kind='backup.succeeded') FROM ingest.activity_logs WHERE data->>'backup_id'=$1`, tc.id).Scan(&failed, &succeeded); err != nil {
				t.Fatal(err)
			}
			if tc.complete && (failed != 0 || succeeded != 1) {
				t.Fatalf("recoverable archive emitted a false failure or duplicate success: failed=%d succeeded=%d", failed, succeeded)
			}
			if !tc.complete && (failed != 1 || succeeded != 0) {
				t.Fatalf("incomplete archive notification mismatch: failed=%d succeeded=%d", failed, succeeded)
			}
		})
	}
}

func TestArchiveRecoveryPreservesUnverifiableAndFinalFiles(t *testing.T) {
	ciphertext, _ := encryptedRecoveryFixture(t)
	for _, tc := range []struct {
		name, checksum string
		corrupt        bool
	}{
		{"incomplete temporary beside final", "", false},
		{"corrupt completed temporary", strings.Repeat("0", 64), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			id := strings.Repeat("ba", 16)
			if err = os.WriteFile(filepath.Join(dir, id+".partial"), ciphertext, 0600); err != nil {
				t.Fatal(err)
			}
			if !tc.corrupt {
				if err = os.WriteFile(filepath.Join(dir, id+".age"), ciphertext, 0600); err != nil {
					t.Fatal(err)
				}
			}
			s := &Service{root: root}
			recovered, err := s.recoverArchive(context.Background(), model.BackupJob{ID: id, SHA256: tc.checksum, Bytes: int64(len(ciphertext))})
			if recovered || (err != nil) != tc.corrupt {
				t.Fatalf("unexpected recovery outcome: recovered=%v err=%v", recovered, err)
			}
			preserved := id + ".age"
			if tc.corrupt {
				preserved = id + ".partial"
			}
			actual, err := os.ReadFile(filepath.Join(dir, preserved))
			if err != nil || !bytes.Equal(actual, ciphertext) {
				t.Fatalf("unverified/final ciphertext was lost: %v", err)
			}
		})
	}
}

func TestRecoveryArchiveAcceptsLaterSchemaWithoutLosingIntegrity(t *testing.T) {
	for _, test := range []struct {
		name       string
		last       int
		gap        bool
		corrupt    bool
		wantReject bool
		sourceName string
	}{
		{name: "recovery baseline", last: 12},
		{name: "current schema", last: store.LatestSchemaVersion()},
		{name: "future schema", last: store.LatestSchemaVersion() + 1, wantReject: true},
		{name: "missing baseline", last: 11, wantReject: true},
		{name: "migration gap", last: store.LatestSchemaVersion(), gap: true, wantReject: true},
		{name: "modified payload", last: store.LatestSchemaVersion(), corrupt: true, wantReject: true},
		{name: "parent traversal", last: store.LatestSchemaVersion(), sourceName: "providers/../source.json", wantReject: true},
		{name: "nested source", last: store.LatestSchemaVersion(), sourceName: "providers/nested/source.json", wantReject: true},
		{name: "backslash source", last: store.LatestSchemaVersion(), sourceName: `providers/nested\source.json`, wantReject: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			identity, err := age.GenerateX25519Identity()
			if err != nil {
				t.Fatal(err)
			}
			var ciphertext bytes.Buffer
			encrypted, err := age.Encrypt(&ciphertext, identity.Recipient())
			if err != nil {
				t.Fatal(err)
			}
			archive := zip.NewWriter(encrypted)
			m := manifest{Version: 1, PostgreSQLMajor: 18, Entries: map[string]entryDigest{}}
			for version := 1; version <= test.last; version++ {
				if test.gap && version == 13 {
					continue
				}
				m.Migrations = append(m.Migrations, version)
			}
			sourceName := test.sourceName
			if sourceName == "" {
				sourceName = "providers/source.json"
			}
			files := map[string][]byte{
				"database.dump":         []byte("fixture database dump"),
				"master.key":            bytes.Repeat([]byte{7}, 32),
				sourceName:              []byte("{\n  \"version\": 1, \"id\": \"fixture\", \"name\": \"Fixture\", \"adapter\": \"http_json\",\n  \"url\": \"https://source.invalid/api\", \"enabled\": false,\n  \"http\": {\"query\": {\"exact\": 9007199254740993, \"exponent\": 1e2}}\n}\n"),
				"providers/legacy.yml":  []byte("# Exact quarantined historical source fixture\n"),
				"providers/legacy.yaml": []byte("version: 1\nid: historical\n"),
			}
			for name, data := range files {
				if err := writeEntry(archive, &m, name, bytes.NewReader(data)); err != nil {
					t.Fatal(err)
				}
			}
			if test.corrupt {
				digest := m.Entries["database.dump"]
				digest.SHA256 = strings.Repeat("0", 64)
				m.Entries["database.dump"] = digest
			}
			manifestWriter, err := archive.Create("manifest.json")
			if err != nil {
				t.Fatal(err)
			}
			if err := json.NewEncoder(manifestWriter).Encode(m); err != nil {
				t.Fatal(err)
			}
			if err := archive.Close(); err != nil {
				t.Fatal(err)
			}
			if err := encrypted.Close(); err != nil {
				t.Fatal(err)
			}
			workspace := t.TempDir()
			_, err = extractArchive(context.Background(), bytes.NewReader(ciphertext.Bytes()), identity.String(), workspace)
			if test.wantReject {
				if err == nil {
					t.Fatal("invalid recovery archive was accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for name, expected := range files {
				actual, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(name)))
				if err != nil || !bytes.Equal(actual, expected) {
					t.Fatalf("recovered entry %q differs from the archived bytes: %v", name, err)
				}
			}
		})
	}
}
