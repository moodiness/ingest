import type { ProviderDocument, Schedule } from './types'
import { parseProviderJSON } from './provider-json'

export type RemoteTiming = 'manual' | 'daily' | 'hourly' | 'every' | 'cron'
export interface RemoteDraft {
  id: string
  name: string
  url: string
  enabled: boolean
  secretRef: string
  timing: RemoteTiming
  every: string
  cron: string
  timezone: string
  scheduleEnabled: boolean
  mode: NonNullable<Schedule['mode']>
  maxPages: string
}

export function remoteDraft(document?: ProviderDocument): RemoteDraft {
  const provider = document?.provider
  const schedule = provider?.schedule ?? {}
  return {
    id: provider?.id ?? '',
    name: provider?.name ?? '',
    url: provider?.url ?? '',
    enabled: provider?.enabled ?? true,
    secretRef: provider?.auth.secret_ref ?? '',
    timing: !provider
      ? 'daily'
      : schedule.cron
        ? 'cron'
        : schedule.every === '24h'
          ? 'daily'
          : schedule.every === '1h'
            ? 'hourly'
            : schedule.every
              ? 'every'
              : 'manual',
    every: schedule.every ?? '24h',
    cron: schedule.cron ?? '',
    timezone: schedule.timezone ?? 'UTC',
    scheduleEnabled: schedule.enabled ?? (!provider || Boolean(schedule.every || schedule.cron)),
    mode: schedule.mode ?? 'incremental',
    maxPages: String(schedule.max_pages ?? 0),
  }
}

export function validateRemoteURL(value: string) {
  let url: URL
  try {
    url = new URL(value)
  } catch {
    throw new Error('Enter a complete HTTPS catalog URL.')
  }
  if (
    url.protocol !== 'https:' ||
    !url.hostname ||
    url.username ||
    url.password ||
    url.search ||
    url.hash ||
    /[?#]/.test(value)
  ) {
    throw new Error(
      'Use an HTTPS catalog URL without a username, password, query string or fragment. Enter the sharing password separately.',
    )
  }
}

function draftSchedule(draft: RemoteDraft): Schedule {
  return {
    enabled: draft.timing !== 'manual' && draft.scheduleEnabled,
    every:
      draft.timing === 'daily'
        ? '24h'
        : draft.timing === 'hourly'
          ? '1h'
          : draft.timing === 'every'
            ? draft.every.trim()
            : undefined,
    cron: draft.timing === 'cron' ? draft.cron.trim() : undefined,
    timezone: draft.timezone.trim() || 'UTC',
    mode: draft.mode,
    max_pages: Number(draft.maxPages),
  }
}

export function remoteJSON(
  document: ProviderDocument | undefined,
  draft: RemoteDraft,
  secretRef: string,
) {
  validateRemoteURL(draft.url.trim())
  const json = parseProviderJSON(
    document?.json ??
      `{
  "version": 1,
  "adapter": "http_json",
  "request_interval": "1s",
  "rate_limit_reset": "epoch",
  "page_size": 100,
  "http": {
    "method": "GET",
    "items_path": "/items",
    "catalog": true
  },
  "pagination": {
    "type": "cursor",
    "in": "query",
    "cursor_param": "cursor",
    "size_param": "limit",
    "next_path": "/next_cursor",
    "start": 0
  },
  "mapping": {
    "id": "/id",
    "fields": {}
  },
  "output": {
    "fields": ["title", "size", "info_hash", "seeders", "leechers", "published_at", "category", "categories"]
  }
}
`,
  )
  if (json.errors.length)
    throw new Error('Fix the source JSON before changing its remote settings.')
  if (document && (document.provider.adapter !== 'http_json' || !document.provider.http.catalog)) {
    throw new Error(
      'This source is no longer a remote catalog. Reload its configuration in Sources.',
    )
  }
  const initial = remoteDraft(document)
  const update = (path: string[], value: unknown, previous: unknown) => {
    if (document && value === previous) return
    if (value === undefined) json.deleteIn(path)
    else json.setIn(path, value)
  }
  if (!document) json.set('id', draft.id.trim())
  update(['name'], draft.name.trim(), initial.name)
  update(['url'], draft.url.trim(), initial.url)
  update(['enabled'], draft.enabled, initial.enabled)
  if (!document || secretRef !== initial.secretRef) {
    json.setIn(['auth', 'type'], 'bearer')
    json.setIn(['auth', 'secret_ref'], secretRef)
    for (const key of ['username_ref', 'password_ref', 'in', 'name']) json.deleteIn(['auth', key])
  }
  const schedule = draftSchedule(draft)
  const previousSchedule = draftSchedule(initial)
  for (const [key, value] of Object.entries(schedule)) {
    update(['schedule', key], value, previousSchedule[key as keyof Schedule])
  }
  return json.toString()
}

export function mergeRemoteDraft(
  current: RemoteDraft,
  previous: RemoteDraft,
  latest: RemoteDraft,
): RemoteDraft {
  return {
    id: latest.id,
    name: current.name === previous.name ? latest.name : current.name,
    url: current.url === previous.url ? latest.url : current.url,
    enabled: current.enabled === previous.enabled ? latest.enabled : current.enabled,
    secretRef: current.secretRef === previous.secretRef ? latest.secretRef : current.secretRef,
    timing: current.timing === previous.timing ? latest.timing : current.timing,
    every: current.every === previous.every ? latest.every : current.every,
    cron: current.cron === previous.cron ? latest.cron : current.cron,
    timezone: current.timezone === previous.timezone ? latest.timezone : current.timezone,
    scheduleEnabled:
      current.scheduleEnabled === previous.scheduleEnabled
        ? latest.scheduleEnabled
        : current.scheduleEnabled,
    mode: current.mode === previous.mode ? latest.mode : current.mode,
    maxPages: current.maxPages === previous.maxPages ? latest.maxPages : current.maxPages,
  }
}
