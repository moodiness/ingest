package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/moodiness/ingest/internal/model"
)

func (s *Store) PutSecret(ctx context.Context, name string, ciphertext []byte) error {
	if name == "" || len(ciphertext) == 0 {
		return model.ErrInvalid
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO ingest.secrets(name,ciphertext) VALUES($1,$2)
ON CONFLICT(name) DO UPDATE SET ciphertext=EXCLUDED.ciphertext,updated_at=NOW()`, name, ciphertext)
	return databaseError("store encrypted secret", err)
}

func (s *Store) Secret(ctx context.Context, name string) ([]byte, error) {
	var ciphertext []byte
	err := s.pool.QueryRow(ctx, "SELECT ciphertext FROM ingest.secrets WHERE name=$1", name).Scan(&ciphertext)
	return ciphertext, databaseError("read encrypted secret", err)
}

func (s *Store) ListSecrets(ctx context.Context) ([]model.SecretInfo, error) {
	rows, err := s.pool.Query(ctx, "SELECT name,updated_at FROM ingest.secrets ORDER BY name")
	if err != nil {
		return nil, databaseError("list secret references", err)
	}
	defer rows.Close()
	secrets := []model.SecretInfo{}
	for rows.Next() {
		var secret model.SecretInfo
		if err := rows.Scan(&secret.Name, &secret.UpdatedAt); err != nil {
			return nil, databaseError("read secret reference", err)
		}
		secrets = append(secrets, secret)
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError("list secret references", err)
	}
	return secrets, nil
}

// HasEncryptedMaterial includes MFA factors, which use the same vault key as
// named secrets even when no provider or webhook credentials are configured.
func (s *Store) HasEncryptedMaterial(ctx context.Context) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ingest.secrets)
OR EXISTS(SELECT 1 FROM ingest.admin_security WHERE active_secret IS NOT NULL OR pending_secret IS NOT NULL)`).Scan(&exists)
	return exists, databaseError("inspect encrypted material", err)
}

func lockProviderSecrets(ctx context.Context, tx pgx.Tx, provider model.Provider) error {
	names, err := provider.SecretReferences()
	if err != nil {
		return err
	}
	for _, name := range names {
		var locked string
		err := tx.QueryRow(ctx, "SELECT name FROM ingest.secrets WHERE name=$1 FOR KEY SHARE", name).Scan(&locked)
		if errors.Is(err, pgx.ErrNoRows) {
			return model.ErrInvalid
		}
		if err != nil {
			return databaseError("lock collection secret", err)
		}
	}
	return nil
}

func (s *Store) DeleteSecret(ctx context.Context, name string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return databaseError("begin encrypted secret deletion", err)
	}
	defer rollback(tx)
	var locked string
	if err := tx.QueryRow(ctx, "SELECT name FROM ingest.secrets WHERE name=$1 FOR UPDATE", name).Scan(&locked); err != nil {
		return databaseError("lock encrypted secret", err)
	}
	// Admissions hold KEY SHARE until their immutable snapshot commits. This
	// separate statement sees admissions that committed while the lock waited.
	var referenced bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (
SELECT 1 FROM ingest.runs WHERE status IN ('queued','running','paused') AND (
$1 IN (config#>>'{auth,secret_ref}',config#>>'{auth,username_ref}',config#>>'{auth,password_ref}')
OR EXISTS (SELECT 1 FROM jsonb_each_text(CASE WHEN jsonb_typeof(config#>'{http,secret_headers}')='object'
THEN config#>'{http,secret_headers}' ELSE '{}'::jsonb END) AS headers WHERE headers.value=$1)))
OR EXISTS (SELECT 1 FROM ingest.webhooks WHERE deleted_at IS NULL AND (url_secret_ref=$1 OR signing_secret_ref=$1))`, name).Scan(&referenced); err != nil {
		return databaseError("check encrypted secret dependencies", err)
	}
	if referenced {
		return model.ErrConflict
	}
	if _, err := tx.Exec(ctx, "DELETE FROM ingest.secrets WHERE name=$1", name); err != nil {
		return databaseError("delete encrypted secret", err)
	}
	return databaseError("commit encrypted secret deletion", tx.Commit(ctx))
}

func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var value string
	err := s.pool.QueryRow(ctx, "SELECT value FROM ingest.settings WHERE key=$1", key).Scan(&value)
	return value, databaseError("read application setting", err)
}

func (s *Store) SetSettingOnce(ctx context.Context, key, value string) (string, error) {
	if key == "" {
		return "", model.ErrInvalid
	}
	var established string
	// The harmless conflict update returns the winning value even when another
	// transaction establishes it after this statement's initial snapshot.
	err := s.pool.QueryRow(ctx, `INSERT INTO ingest.settings(key,value) VALUES($1,$2)
ON CONFLICT(key) DO UPDATE SET value=ingest.settings.value RETURNING value`, key, value).Scan(&established)
	return established, databaseError("establish application setting", err)
}
