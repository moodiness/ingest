import type { Page } from '@/lib/types'

export type ActivityLevel = 'debug' | 'info' | 'warn' | 'error'
export const activityLevels: ActivityLevel[] = ['debug', 'info', 'warn', 'error']

export interface LogEntry {
  id: number
  level: ActivityLevel
  kind: string
  message: string
  provider_id?: string
  run_id?: string
  created_at: string
  data?: Record<string, unknown>
}

export interface Notification {
  id: number
  level: ActivityLevel
  kind: string
  title: string
  message: string
  provider_id?: string
  run_id?: string
  created_at: string
  read_at?: string
}

export interface NotificationPage extends Page<Notification> {
  unread_count: number
}

export const webhookEvents = [
  'run.succeeded',
  'run.failed',
  'run.paused',
  'run.cancelled',
  'run.no_progress',
  'schedule.failed',
  'backup.succeeded',
  'backup.failed',
  'backup.restore_succeeded',
  'backup.restore_failed',
  'health.warning',
  'health.recovered',
] as const
export type WebhookEvent = (typeof webhookEvents)[number]

export interface WebhookInput {
  name: string
  enabled: boolean
  url_secret_ref: string
  signing_secret_ref?: string
  events: WebhookEvent[]
}

export interface Webhook extends WebhookInput {
  id: string
  revision: number
  created_at: string
  updated_at: string
  deleted_at?: string
}

export type DeliveryStatus = 'pending' | 'delivering' | 'delivered' | 'failed' | 'cancelled'
export interface Delivery {
  id: string
  webhook_id: string
  event_id: string
  event_type: string
  status: DeliveryStatus
  attempts: number
  next_attempt_at?: string
  last_status?: number
  last_error?: string
  created_at: string
  delivered_at?: string
}
