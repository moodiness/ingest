package model

import "time"

type LogEntry struct {
	ID         int64          `json:"id"`
	Level      string         `json:"level"`
	Kind       string         `json:"kind"`
	Message    string         `json:"message"`
	ProviderID string         `json:"provider_id,omitempty"`
	RunID      string         `json:"run_id,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	Data       map[string]any `json:"data,omitempty"`
}

type LogOptions struct {
	ListOptions
	Levels []string
	From   *time.Time
	To     *time.Time
}

type Notification struct {
	ID         int64      `json:"id"`
	Level      string     `json:"level"`
	Kind       string     `json:"kind"`
	Title      string     `json:"title"`
	Message    string     `json:"message"`
	ProviderID string     `json:"provider_id,omitempty"`
	RunID      string     `json:"run_id,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	ReadAt     *time.Time `json:"read_at,omitempty"`
}

type NotificationList struct {
	List[Notification]
	UnreadCount int64 `json:"unread_count"`
}

type Webhook struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Enabled          bool       `json:"enabled"`
	URLSecretRef     string     `json:"url_secret_ref"`
	SigningSecretRef string     `json:"signing_secret_ref,omitempty"`
	Events           []string   `json:"events"`
	Revision         int64      `json:"revision"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	DeletedAt        *time.Time `json:"deleted_at,omitempty"`
}

type WebhookInput struct {
	Name             string   `json:"name"`
	Enabled          bool     `json:"enabled"`
	URLSecretRef     string   `json:"url_secret_ref"`
	SigningSecretRef string   `json:"signing_secret_ref,omitempty"`
	Events           []string `json:"events"`
	Revision         int64    `json:"revision,omitempty"`
}

type WebhookDelivery struct {
	ID            string     `json:"id"`
	WebhookID     string     `json:"webhook_id"`
	EventID       string     `json:"event_id"`
	EventType     string     `json:"event_type"`
	Status        string     `json:"status"`
	Attempts      int        `json:"attempts"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
	LastStatus    *int       `json:"last_status,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	DeliveredAt   *time.Time `json:"delivered_at,omitempty"`
}

type WebhookEvent struct {
	Version    int            `json:"version"`
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	OccurredAt time.Time      `json:"occurred_at"`
	ProviderID string         `json:"provider_id,omitempty"`
	RunID      string         `json:"run_id,omitempty"`
	Message    string         `json:"message"`
	Data       map[string]any `json:"data,omitempty"`
}

func IsActivityLevel(level string) bool {
	return level == "debug" || level == "info" || level == "warn" || level == "error"
}

func IsWebhookEvent(kind string) bool {
	switch kind {
	case "run.succeeded", "run.failed", "run.paused", "run.cancelled", "run.no_progress", "schedule.failed",
		"backup.succeeded", "backup.failed", "backup.restore_succeeded", "backup.restore_failed", "health.warning", "health.recovered":
		return true
	default:
		return false
	}
}
