export interface BackupSettings {
  recipient: string
  enabled: boolean
  time_utc: string
  revision: number
  next_run_at: string | null
  last_success_at: string | null
  last_failure_at: string | null
}
export interface BackupJob {
  id: string
  kind: 'backup' | 'verify'
  backup_id?: string
  trigger: 'manual' | 'scheduled'
  status: 'queued' | 'running' | 'succeeded' | 'failed'
  cleanup_pending: boolean
  phase: string
  failure_code?: string
  bytes: number
  sha256?: string
  recipient?: string
  created_at: string
  started_at: string | null
  finished_at: string | null
  report?: {
    reason?: string
    verified?: boolean
    tables?: number
    source_files?: number
    secrets?: number
    postgresql_major?: number
    cleanup?: string
  }
}
export interface BackupOverview {
  settings: BackupSettings
  items: BackupJob[]
  total: number
  limit: number
  offset: number
  capability: { available: boolean; message: string; postgresql_major?: number }
}
