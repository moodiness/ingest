export type HealthSettings = {
  stale_hours: number
  stuck_minutes: number
  min_free_bytes: number
  min_free_percent: number
  journal_growth_bytes_per_day: number
}

export type HealthDiagnostic = {
  code:
    | 'storage_low'
    | 'source_stale'
    | 'authentication'
    | 'certificate'
    | 'run_stuck'
    | 'journal_growth'
    | 'database_unavailable'
  provider_id?: string
  run_id?: string
  failure_code?: string
  reason: string
  since: string
  observed_at: string
  last_success_at?: string
  last_progress_at?: string
  measured_bytes?: number
  threshold_bytes?: number
  threshold_seconds?: number
}

export type HealthSample = {
  sampled_at: string
  database_bytes: number
  live_bytes: number
  raw_bytes: number
  journal_bytes: number
  other_bytes: number
}

export type SystemHealth = {
  checked_at: string
  next_check_at: string
  status: 'healthy' | 'warning' | 'unavailable'
  database: {
    available: boolean
    measured_at?: string
    total_bytes: number
    relation_bytes: number
    other_database_bytes: number
    categories: Array<{
      key: 'live' | 'raw' | 'journal' | 'other'
      bytes: number
      table_bytes: number
      index_bytes: number
    }>
  }
  disk: {
    available: boolean
    scope: string
    measured_at: string
    total_bytes: number
    free_bytes: number
    available_bytes: number
  }
  settings: HealthSettings
  samples: HealthSample[]
  growth?: {
    from: string
    to: string
    elapsed_days: number
    database_bytes: number
    journal_bytes: number
    database_bytes_per_day: number
    journal_bytes_per_day: number
  }
  diagnostics: HealthDiagnostic[]
  sources_checked: number
  sources_available: boolean
}
