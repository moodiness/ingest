package model

import "time"

// BackupSettings contains only the public age recipient, never a recovery identity.
type BackupSettings struct {
	Recipient     string     `json:"recipient"`
	Enabled       bool       `json:"enabled"`
	TimeUTC       string     `json:"time_utc"`
	Revision      int64      `json:"revision"`
	NextRunAt     *time.Time `json:"next_run_at"`
	LastSuccessAt *time.Time `json:"last_success_at"`
	LastFailureAt *time.Time `json:"last_failure_at"`
}

type BackupJob struct {
	ID             string         `json:"id"`
	Kind           string         `json:"kind"`
	BackupID       string         `json:"backup_id,omitempty"`
	Trigger        string         `json:"trigger"`
	Status         string         `json:"status"`
	CleanupPending bool           `json:"cleanup_pending"`
	Phase          string         `json:"phase"`
	FailureCode    string         `json:"failure_code,omitempty"`
	Bytes          int64          `json:"bytes"`
	SHA256         string         `json:"sha256,omitempty"`
	Recipient      string         `json:"recipient,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	StartedAt      *time.Time     `json:"started_at"`
	FinishedAt     *time.Time     `json:"finished_at"`
	Report         map[string]any `json:"report,omitempty"`
}
