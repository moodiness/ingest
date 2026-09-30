import { useState, useSyncExternalStore } from 'react'

export interface DisplayPreferences {
  timezone: string
  pageSize: 25 | 50 | 100
}

const storageKey = 'torrent-scrapers.display-preferences'
const defaults: DisplayPreferences = { timezone: '', pageSize: 50 }
const listeners = new Set<() => void>()

export function validTimezone(value: string) {
  if (!value) return true
  // Intl also accepts numeric offsets in newer browsers; only named zones belong here.
  if (/^[+-]/.test(value)) return false
  try {
    new Intl.DateTimeFormat('en-GB', { timeZone: value })
    return true
  } catch {
    return false
  }
}

function readPreferences(): DisplayPreferences {
  try {
    const value: unknown = JSON.parse(window.localStorage.getItem(storageKey) ?? 'null')
    if (!value || typeof value !== 'object') return defaults
    const saved = value as Partial<DisplayPreferences>
    return {
      timezone:
        typeof saved.timezone === 'string' && validTimezone(saved.timezone) ? saved.timezone : '',
      pageSize: saved.pageSize === 25 || saved.pageSize === 100 ? saved.pageSize : 50,
    }
  } catch {
    return defaults
  }
}

let current = readPreferences()

function publish(next: DisplayPreferences) {
  if (next.timezone === current.timezone && next.pageSize === current.pageSize) return
  current = next
  listeners.forEach((listener) => listener())
}

function onStorage(event: StorageEvent) {
  if (event.key === storageKey || event.key === null) publish(readPreferences())
}

if (typeof window !== 'undefined') window.addEventListener('storage', onStorage)

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

export function useDisplayPreferences() {
  return useSyncExternalStore(
    subscribe,
    () => current,
    () => defaults,
  )
}

export function updateDisplayPreferences(patch: Partial<DisplayPreferences>) {
  const next = { ...current, ...patch }
  if (!validTimezone(next.timezone) || ![25, 50, 100].includes(next.pageSize)) return false
  let persisted = true
  try {
    window.localStorage.setItem(storageKey, JSON.stringify(next))
  } catch {
    persisted = false
  }
  publish(next)
  return persisted
}

export function usePageSize(override?: string | null) {
  const { pageSize } = useDisplayPreferences()
  const requested = Number(override)
  return requested === 25 || requested === 50 || requested === 100 ? requested : pageSize
}

export function normalizePageOffset(value: string | number | null, limit: number) {
  const offset = Number(value)
  return Number.isSafeInteger(offset) && offset > 0 ? Math.floor(offset / limit) * limit : 0
}

export function usePageOffset(limit: number) {
  const [page, setPage] = useState({ limit, offset: 0 })
  const offset = page.limit === limit ? page.offset : 0
  if (page.limit !== limit) setPage({ limit, offset: 0 })
  return [
    offset,
    (value: number) => setPage({ limit, offset: normalizePageOffset(value, limit) }),
  ] as const
}
