package model

import "time"

// HealthSettings controls diagnostics only. No health operation removes stored data.
type HealthSettings struct {
	StaleHours               int   `json:"stale_hours"`
	StuckMinutes             int   `json:"stuck_minutes"`
	MinFreeBytes             int64 `json:"min_free_bytes"`
	MinFreePercent           int   `json:"min_free_percent"`
	JournalGrowthBytesPerDay int64 `json:"journal_growth_bytes_per_day"`
}

func DefaultHealthSettings() HealthSettings {
	return HealthSettings{StaleHours: 24, StuckMinutes: 30, MinFreeBytes: 1073741824, MinFreePercent: 10, JournalGrowthBytesPerDay: 1073741824}
}

func (s HealthSettings) Valid() bool {
	return s.StaleHours >= 1 && s.StaleHours <= 2160 && s.StuckMinutes >= 5 && s.StuckMinutes <= 10080 && s.MinFreeBytes >= 0 && s.MinFreeBytes <= 1125899906842624 && s.MinFreePercent >= 0 && s.MinFreePercent <= 95 && s.JournalGrowthBytesPerDay >= 0 && s.JournalGrowthBytesPerDay <= 1125899906842624
}

type HealthStorageCategory struct {
	Key        string `json:"key"`
	Bytes      int64  `json:"bytes"`
	TableBytes int64  `json:"table_bytes"`
	IndexBytes int64  `json:"index_bytes"`
}

type HealthDatabase struct {
	Available          bool                    `json:"available"`
	MeasuredAt         *time.Time              `json:"measured_at,omitempty"`
	TotalBytes         int64                   `json:"total_bytes"`
	RelationBytes      int64                   `json:"relation_bytes"`
	OtherDatabaseBytes int64                   `json:"other_database_bytes"`
	Categories         []HealthStorageCategory `json:"categories"`
}

type HealthDisk struct {
	Available      bool      `json:"available"`
	Scope          string    `json:"scope"`
	MeasuredAt     time.Time `json:"measured_at"`
	TotalBytes     uint64    `json:"total_bytes"`
	FreeBytes      uint64    `json:"free_bytes"`
	AvailableBytes uint64    `json:"available_bytes"`
}

type HealthSample struct {
	SampledAt     time.Time `json:"sampled_at"`
	DatabaseBytes int64     `json:"database_bytes"`
	LiveBytes     int64     `json:"live_bytes"`
	RawBytes      int64     `json:"raw_bytes"`
	JournalBytes  int64     `json:"journal_bytes"`
	OtherBytes    int64     `json:"other_bytes"`
}

type HealthGrowth struct {
	From                time.Time `json:"from"`
	To                  time.Time `json:"to"`
	ElapsedDays         float64   `json:"elapsed_days"`
	DatabaseBytes       int64     `json:"database_bytes"`
	JournalBytes        int64     `json:"journal_bytes"`
	DatabaseBytesPerDay float64   `json:"database_bytes_per_day"`
	JournalBytesPerDay  float64   `json:"journal_bytes_per_day"`
}

type HealthDiagnostic struct {
	Code             string     `json:"code"`
	ProviderID       string     `json:"provider_id,omitempty"`
	RunID            string     `json:"run_id,omitempty"`
	FailureCode      string     `json:"failure_code,omitempty"`
	Reason           string     `json:"reason"`
	Since            time.Time  `json:"since"`
	ObservedAt       time.Time  `json:"observed_at"`
	LastSuccessAt    *time.Time `json:"last_success_at,omitempty"`
	LastProgressAt   *time.Time `json:"last_progress_at,omitempty"`
	MeasuredBytes    *int64     `json:"measured_bytes,omitempty"`
	ThresholdBytes   *int64     `json:"threshold_bytes,omitempty"`
	ThresholdSeconds int64      `json:"threshold_seconds,omitempty"`
}

type SystemHealth struct {
	CheckedAt        time.Time          `json:"checked_at"`
	Status           string             `json:"status"`
	Database         HealthDatabase     `json:"database"`
	Disk             HealthDisk         `json:"disk"`
	Settings         HealthSettings     `json:"settings"`
	Samples          []HealthSample     `json:"samples"`
	Growth           *HealthGrowth      `json:"growth,omitempty"`
	Diagnostics      []HealthDiagnostic `json:"diagnostics"`
	SourcesChecked   int                `json:"sources_checked"`
	SourcesAvailable bool               `json:"sources_available"`
	NextCheckAt      time.Time          `json:"next_check_at"`
}
