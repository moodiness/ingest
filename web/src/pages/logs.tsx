import { useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useSearchParams } from 'react-router-dom'
import { ArrowClockwiseIcon, MagnifyingGlassIcon } from '@phosphor-icons/react'
import { api, params } from '@/lib/api'
import { activityLevels, type LogEntry } from '@/lib/activity-types'
import type { Items, Page, ProviderSummary } from '@/lib/types'
import { useDate } from '@/lib/format'
import { normalizePageOffset, usePageSize } from '@/lib/display-preferences'
import { ActivityLinks, LevelBadge } from '@/components/activity-common'
import { Empty, ErrorNotice, Loading, PageHeader, Pager } from '@/components/common'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from '@/components/ui/dialog'
import {
  Table,
  TableHeader,
  TableRow,
  TableHead,
  TableBody,
  TableCell,
} from '@/components/ui/table'

function localTime(value: string) {
  const parsed = new Date(value)
  if (!value || Number.isNaN(parsed.getTime())) return ''
  return new Date(parsed.getTime() - parsed.getTimezoneOffset() * 60_000).toISOString().slice(0, 16)
}

export function LogsPage() {
  const date = useDate()
  const limit = usePageSize()
  const [search, setSearch] = useSearchParams()
  const [inspecting, setInspecting] = useState<LogEntry | null>(null)
  const returnFocus = useRef<HTMLElement | null>(null)
  const [filterError, setFilterError] = useState<Error | null>(null)
  const level = search.get('level') ?? ''
  const provider = search.get('provider') ?? ''
  const run = search.get('run') ?? ''
  const q = search.get('q') ?? ''
  const from = search.get('from') ?? ''
  const to = search.get('to') ?? ''
  const offset = normalizePageOffset(search.get('offset'), limit)
  const query = params({ level, provider, run, q, from, to, limit, offset })
  const logs = useQuery({
    queryKey: ['logs', query],
    queryFn: ({ signal }) => api<Page<LogEntry>>(`/logs?${query}`, { signal }),
  })
  const providers = useQuery({
    queryKey: ['providers'],
    queryFn: ({ signal }) => api<Items<ProviderSummary>>('/providers', { signal }),
  })
  return (
    <div className="page">
      <PageHeader
        title="Logs"
        description="Durable collection, scheduler and service activity. Filter by severity, source, run or time to investigate an event."
        actions={
          <Button variant="outline" disabled={logs.isFetching} onClick={() => void logs.refetch()}>
            <ArrowClockwiseIcon />
            Refresh
          </Button>
        }
      />
      <form
        key={search.toString()}
        className="space-y-4 rounded-md border border-border p-4"
        onSubmit={(event) => {
          event.preventDefault()
          const form = new FormData(event.currentTarget)
          const next = new URLSearchParams()
          const selected = form.getAll('level').map(String)
          if (selected.length === 0) {
            setFilterError(new Error('Select at least one log level.'))
            return
          }
          if (selected.length !== activityLevels.length) next.set('level', selected.join(','))
          for (const key of ['provider', 'run', 'q']) {
            const value = String(form.get(key) ?? '').trim()
            if (value) next.set(key, value)
          }
          for (const key of ['from', 'to']) {
            const value = String(form.get(key) ?? '')
            if (value) next.set(key, new Date(value).toISOString())
          }
          if (next.has('from') && next.has('to') && next.get('from')! > next.get('to')!) {
            setFilterError(new Error('The start time must be before the end time.'))
            return
          }
          setFilterError(null)
          setSearch(next)
        }}
      >
        <fieldset className="flex flex-wrap gap-x-5 gap-y-2">
          <legend className="mb-2 text-sm font-medium">Log levels</legend>
          {activityLevels.map((item) => (
            <label key={item} className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                name="level"
                value={item}
                defaultChecked={!level || level.split(',').includes(item)}
                className="size-4 accent-primary"
              />
              <LevelBadge level={item} />
            </label>
          ))}
        </fieldset>
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          <div className="field">
            <Label htmlFor="log-provider">Source</Label>
            <select
              id="log-provider"
              name="provider"
              className="native-select"
              defaultValue={provider}
            >
              <option value="">All sources and service activity</option>
              {provider && !providers.data?.items?.some((item) => item.id === provider) && (
                <option value={provider}>{provider}</option>
              )}
              {providers.data?.items?.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.name || item.id}
                </option>
              ))}
            </select>
          </div>
          <div className="field">
            <Label htmlFor="log-run">Run ID</Label>
            <Input id="log-run" name="run" defaultValue={run} placeholder="Any run" />
          </div>
          <div className="field">
            <Label htmlFor="log-query">Search text</Label>
            <Input id="log-query" name="q" defaultValue={q} placeholder="Search event messages" />
          </div>
          <div className="field">
            <Label htmlFor="log-from">From (browser local time)</Label>
            <Input id="log-from" name="from" type="datetime-local" defaultValue={localTime(from)} />
          </div>
          <div className="field">
            <Label htmlFor="log-to">To (browser local time)</Label>
            <Input id="log-to" name="to" type="datetime-local" defaultValue={localTime(to)} />
          </div>
          <div className="flex flex-wrap items-end gap-2">
            <Button type="submit">
              <MagnifyingGlassIcon />
              Apply filters
            </Button>
            <Button
              type="button"
              variant="outline"
              onClick={() => {
                setFilterError(null)
                setSearch(new URLSearchParams())
              }}
            >
              Clear filters
            </Button>
          </div>
        </div>
        <p className="help">Date filters use browser local time, not the display timezone.</p>
        <ErrorNotice error={filterError} />
      </form>
      <ErrorNotice error={providers.error} retry={() => void providers.refetch()} />
      <ErrorNotice error={logs.error} retry={() => void logs.refetch()} />
      {logs.isPending ? (
        <Loading label="Loading logs" />
      ) : (
        logs.data && (
          <>
            {logs.data.items?.length ? (
              <div className="table-frame">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Time / level</TableHead>
                      <TableHead>Event / message</TableHead>
                      <TableHead>Context</TableHead>
                      <TableHead>
                        <span className="sr-only">Metadata</span>
                      </TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {logs.data.items.map((entry) => (
                      <TableRow key={entry.id}>
                        <TableCell className="align-top">
                          <time
                            className="mb-2 block text-xs text-muted-foreground"
                            dateTime={entry.created_at}
                            title={entry.created_at}
                          >
                            {date(entry.created_at)}
                          </time>
                          <LevelBadge level={entry.level} />
                        </TableCell>
                        <TableCell className="min-w-64 max-w-xl whitespace-normal align-top">
                          <p className="mono text-muted-foreground">{entry.kind}</p>
                          <p className="mt-1 break-words leading-6">{entry.message}</p>
                        </TableCell>
                        <TableCell className="align-top">
                          <ActivityLinks {...entry} />
                        </TableCell>
                        <TableCell className="align-top">
                          {entry.data && Object.keys(entry.data).length > 0 && (
                            <Button
                              variant="ghost"
                              size="sm"
                              onClick={(event) => {
                                returnFocus.current = event.currentTarget
                                setInspecting(entry)
                              }}
                            >
                              Metadata<span className="sr-only"> for log {entry.id}</span>
                            </Button>
                          )}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            ) : (
              <Empty
                title="No logs match these filters"
                description="Broaden the filters or wait for collection and service activity."
              />
            )}
            <Pager
              total={logs.data.total}
              limit={logs.data.limit}
              offset={logs.data.offset}
              busy={logs.isFetching}
              onChange={(value) => {
                const next = new URLSearchParams(search)
                next.set('offset', String(value))
                setSearch(next)
              }}
            />
          </>
        )
      )}
      <Dialog
        open={Boolean(inspecting)}
        onOpenChange={(open) => {
          if (!open) setInspecting(null)
        }}
      >
        <DialogContent
          onCloseAutoFocus={(event) => {
            event.preventDefault()
            if (returnFocus.current?.isConnected) returnFocus.current.focus()
            else document.getElementById('log-query')?.focus()
          }}
        >
          <DialogHeader>
            <DialogTitle>Event metadata</DialogTitle>
            <DialogDescription>
              {inspecting?.kind} · {date(inspecting?.created_at)}. Only server-approved activity
              metadata is included.
            </DialogDescription>
          </DialogHeader>
          <pre className="max-h-96 overflow-auto rounded-md border border-border bg-muted p-4 font-mono text-xs whitespace-pre-wrap break-words">
            {JSON.stringify(inspecting?.data, null, 2)}
          </pre>
        </DialogContent>
      </Dialog>
    </div>
  )
}
