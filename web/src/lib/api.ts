import { QueryClient } from '@tanstack/react-query'

export class ApiError extends Error {
  status: number
  details: string[]
  constructor(status: number, message: string, details: string[] = []) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.details = details
  }
}

let csrfToken: string | undefined
export function setCSRF(token?: string) {
  csrfToken = token
}

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 15_000,
      refetchOnWindowFocus: true,
      retry: (count, error) => count < 1 && !(error instanceof ApiError && error.status < 500),
    },
    mutations: { retry: false },
  },
})

async function checkedResponse(path: string, init: RequestInit = {}) {
  const method = init.method ?? 'GET'
  const headers = new Headers(init.headers)
  if (init.body) headers.set('Content-Type', 'application/json')
  if (!['GET', 'HEAD'].includes(method) && csrfToken) headers.set('X-CSRF-Token', csrfToken)
  let response: Response
  try {
    response = await fetch(`/api${path}`, { ...init, credentials: 'same-origin', headers })
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error
    throw new ApiError(0, 'The server is unreachable. Check your connection and try again.')
  }
  if (!response.ok) {
    let payload: { error?: string; details?: string[] } = {}
    try {
      payload = await response.json()
    } catch {
      /* A proxy may return a non-JSON error. */
    }
    if (response.status === 401 && path !== '/login') {
      setCSRF()
      window.dispatchEvent(new Event('session-expired'))
    }
    if (response.status === 403) void queryClient.invalidateQueries({ queryKey: ['session'] })
    const fallback =
      response.status === 403
        ? 'The security session must be refreshed. Try again after refreshing.'
        : `The request failed (HTTP ${response.status}).`
    throw new ApiError(response.status, payload.error || fallback, payload.details ?? [])
  }
  return response
}

export async function api<T>(
  path: string,
  init?: RequestInit,
  decode?: (text: string) => T,
): Promise<T> {
  const response = await checkedResponse(path, init)
  if (response.status === 204) return undefined as T
  if (decode) return decode(await response.text())
  return response.json() as Promise<T>
}

export async function rawPage(id: string, signal?: AbortSignal) {
  const response = await checkedResponse(`/pages/${encodeURIComponent(id)}/download`, { signal })
  const bytes = await response.arrayBuffer()
  return {
    text: new TextDecoder().decode(bytes),
    byteLength: bytes.byteLength,
    contentType: response.headers.get('Content-Type') ?? '',
  }
}

export async function downloadBytes(path: string, filename: string) {
  const response = await checkedResponse(path)
  const url = URL.createObjectURL(await response.blob())
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  link.remove()
  window.setTimeout(() => URL.revokeObjectURL(url), 1000)
}

export function params(values: Record<string, string | number | undefined>) {
  const query = new URLSearchParams()
  for (const [key, value] of Object.entries(values))
    if (value !== undefined && value !== '') query.set(key, String(value))
  return query.toString()
}

export async function refreshData() {
  await Promise.all(
    [
      'providers',
      'provider',
      'schedules',
      'overview',
      'runs',
      'run',
      'events',
      'raw',
      'torrents',
      'secrets',
      'logs',
      'notifications',
      'webhooks',
      'shares',
      'remotes',
      'saved-views',
      'torrent-history',
      'torrent-related',
      'run-changes',
      'system-health',
      'health-settings',
      'backups',
      'backup-settings',
      'security',
      'sessions',
      'security-audit',
      'collection-settings',
    ].map((key) => queryClient.invalidateQueries({ queryKey: [key] })),
  )
}
