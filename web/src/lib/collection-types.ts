import type { Provider } from './types'

export function supportsMetadata(provider: Provider | undefined): boolean {
  return Boolean(
    provider?.adapter === 'http_json' &&
    !provider.http.catalog &&
    provider.traversal?.id_recovery &&
    provider.traversal.enrich_fields?.length,
  )
}

export interface CollectionSettings {
  workers: number
  max_quota_retries: number
  max_quota_wait_seconds: number
  auto_resume_interrupted: boolean
  no_progress_requests: number
  no_progress_action: 'warn' | 'pause'
  default_request_timeout_seconds: number
  default_preview_pages: number
  default_max_pages: number
  default_max_duration_seconds: number
  revision: number
}

export interface CollectionOverview {
  settings: CollectionSettings
  running: number
  queued: number
  capacity: number
}
