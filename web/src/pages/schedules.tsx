import { useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { parseProviderJSON } from '@/lib/provider-json'
import { ArrowClockwiseIcon, FloppyDiskIcon, PlusIcon } from '@phosphor-icons/react'
import { api, ApiError, refreshData } from '@/lib/api'
import type { Items, ProviderDocument, Schedule, ScheduleSummary } from '@/lib/types'
import { supportsMetadata } from '@/lib/collection-types'
import { useDate, modeLabels, number } from '@/lib/format'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import {
  Dialog,
  DialogTrigger,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'
import {
  Table,
  TableHeader,
  TableRow,
  TableHead,
  TableBody,
  TableCell,
} from '@/components/ui/table'
import { Empty, ErrorNotice, Loading, PageHeader, RunStatusBadge } from '@/components/common'

const stateLabels: Record<ScheduleSummary['state'], string> = {
  manual: 'Manual only',
  disabled: 'Disabled',
  waiting: 'Waiting',
  running: 'Running',
  paused: 'Paused',
  blocked: 'Blocked',
  invalid: 'Invalid',
}

function scheduleJSON(document: ProviderDocument, settings: Schedule) {
  const json = parseProviderJSON(document.json)
  if (json.errors.length) throw new Error('Fix the source JSON before changing its schedule.')
  for (const [key, value] of Object.entries(settings)) {
    if (value === undefined) json.deleteIn(['schedule', key])
    else json.setIn(['schedule', key], value)
  }
  return json.toString()
}

function saveSchedule(id: string, document: ProviderDocument, settings: Schedule) {
  return api<ProviderDocument>(`/providers/${encodeURIComponent(id)}`, {
    method: 'PUT',
    body: JSON.stringify({ json: scheduleJSON(document, settings), revision: document.revision }),
  })
}

export default function SchedulesPage() {
  const date = useDate()
  const schedules = useQuery({
    queryKey: ['schedules'],
    queryFn: ({ signal }) => api<Items<ScheduleSummary>>('/schedules', { signal }),
  })
  const toggle = useMutation({
    mutationFn: async (source: ScheduleSummary) => {
      const document = await api<ProviderDocument>(
        `/providers/${encodeURIComponent(source.provider_id)}`,
      )
      if (document.revision !== source.revision)
        throw new ApiError(
          409,
          'This source was changed elsewhere. Reload schedules before changing its activation.',
        )
      return saveSchedule(source.provider_id, document, { enabled: !source.enabled })
    },
    onSuccess: refreshData,
  })
  return (
    <div className="page">
      <PageHeader
        title="Schedules"
        description="Configure recurring collection for each source. The server keeps time and creates durable runs, even when this console is closed."
        actions={
          <Button
            variant="outline"
            disabled={schedules.isFetching}
            onClick={() => void schedules.refetch()}
          >
            <ArrowClockwiseIcon />
            Refresh
          </Button>
        }
      />
      <ErrorNotice error={schedules.error} retry={() => void schedules.refetch()} />
      <ErrorNotice error={toggle.error} />
      {toggle.error instanceof ApiError && toggle.error.status === 409 && (
        <Button
          className="w-fit"
          variant="outline"
          disabled={schedules.isFetching}
          onClick={async () => {
            const result = await schedules.refetch()
            if (!result.isError) toggle.reset()
          }}
        >
          <ArrowClockwiseIcon />
          Reload schedules
        </Button>
      )}
      {schedules.isPending ? (
        <Loading label="Loading schedules" />
      ) : schedules.data?.items?.length ? (
        <div className="table-frame">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Source</TableHead>
                <TableHead>Schedule</TableHead>
                <TableHead>Frequency / timezone</TableHead>
                <TableHead>Mode / budget</TableHead>
                <TableHead>Next run</TableHead>
                <TableHead>Last run</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {schedules.data.items.map((source) => {
                const configured = Boolean(source.schedule.every || source.schedule.cron)
                return (
                  <TableRow key={source.provider_id}>
                    <TableCell className="align-top">
                      <Link
                        className="font-medium hover:text-primary"
                        to={`/providers/${encodeURIComponent(source.provider_id)}`}
                      >
                        {source.provider_name || source.provider_id}
                      </Link>
                      <p className="mono mt-1 text-muted-foreground">{source.provider_id}</p>
                      {!source.provider_enabled && (
                        <p className="mt-2 text-xs text-muted-foreground">Source disabled</p>
                      )}
                      {!source.valid && (
                        <p className="mt-2 text-xs text-destructive">Source needs fixing</p>
                      )}
                    </TableCell>
                    <TableCell className="max-w-xs whitespace-normal align-top">
                      <Badge
                        variant="outline"
                        className={
                          source.state === 'blocked' || source.state === 'invalid'
                            ? 'text-destructive border-destructive/30'
                            : 'text-muted-foreground'
                        }
                      >
                        {stateLabels[source.state]}
                      </Badge>
                      <div className="mt-3 flex items-center gap-2">
                        <Switch
                          aria-label={`${source.enabled ? 'Disable' : 'Enable'} schedule for ${source.provider_name || source.provider_id}`}
                          checked={source.enabled}
                          disabled={!source.valid || !configured || toggle.isPending}
                          onCheckedChange={() => toggle.mutate(source)}
                        />
                        <span className="text-xs text-muted-foreground">
                          {source.enabled ? 'Enabled' : 'Disabled'}
                        </span>
                      </div>
                      {source.last_error && (
                        <p className="mt-3 break-words text-xs leading-5 text-destructive">
                          {source.last_error}
                        </p>
                      )}
                      {source.state === 'paused' && (
                        <p className="mt-3 text-xs leading-5 text-muted-foreground">
                          Budget reached, not complete. Continues at the next tick while the
                          revision matches, including Full reconciliation.
                        </p>
                      )}
                      {source.state === 'blocked' && (
                        <p className="mt-3 text-xs leading-5 text-muted-foreground">
                          Review the existing run and resume or cancel it manually before scheduling
                          can continue.{' '}
                          <Link
                            className="data-link"
                            to={`/runs?provider=${encodeURIComponent(source.provider_id)}`}
                          >
                            Review runs
                          </Link>
                        </p>
                      )}
                    </TableCell>
                    <TableCell className="align-top text-xs">
                      <p className="font-mono">
                        {source.schedule.cron ||
                          (source.schedule.every
                            ? `Every ${source.schedule.every}`
                            : 'Not configured')}
                      </p>
                      <p className="mt-2 text-muted-foreground">
                        {source.schedule.timezone || 'UTC'}
                      </p>
                      {source.schedule.full_every && (
                        <p className="mt-2 text-muted-foreground">
                          Full reconciliation every {source.schedule.full_every}
                        </p>
                      )}
                    </TableCell>
                    <TableCell className="align-top text-xs">
                      <p>{modeLabels[source.schedule.mode || 'incremental']}</p>
                      <p className="mt-2 text-muted-foreground">
                        {source.schedule.max_pages
                          ? `${number(source.schedule.max_pages)} pages / tick`
                          : 'Unlimited pages'}
                      </p>
                      {(source.schedule.mode || 'incremental') === 'incremental' && (
                        <p className="mt-1 text-muted-foreground">
                          Known-page boundary:{' '}
                          {source.schedule.known_pages
                            ? number(source.schedule.known_pages)
                            : 'Off'}
                        </p>
                      )}
                    </TableCell>
                    <TableCell className="align-top text-xs text-muted-foreground">
                      {source.next_run_at ? (
                        <time dateTime={source.next_run_at}>{date(source.next_run_at)}</time>
                      ) : (
                        'Not scheduled'
                      )}
                      {source.next_full_at && (
                        <p className="mt-2 max-w-56 whitespace-normal">
                          Full due{' '}
                          <time dateTime={source.next_full_at}>{date(source.next_full_at)}</time>.{' '}
                          Runs on the first eligible base tick after this deadline.
                        </p>
                      )}
                    </TableCell>
                    <TableCell className="align-top text-xs">
                      {source.last_run ? (
                        <Link
                          className="inline-flex flex-col items-start gap-2"
                          to={`/runs/${encodeURIComponent(source.last_run.id)}`}
                        >
                          <RunStatusBadge run={source.last_run} />
                          <span className="text-muted-foreground">
                            {date(source.last_run.created_at)}
                          </span>
                          <span className="text-muted-foreground">
                            {source.last_run.trigger === 'scheduled' ? 'Scheduled' : 'Manual'} ·{' '}
                            {modeLabels[source.last_run.mode]}
                          </span>
                        </Link>
                      ) : (
                        <span className="text-muted-foreground">No runs yet</span>
                      )}
                    </TableCell>
                    <TableCell className="align-top">
                      <div className="flex flex-col items-end gap-2">
                        <ScheduleEditor source={source} />
                        <Button asChild size="sm" variant="ghost">
                          <Link to={`/runs?provider=${encodeURIComponent(source.provider_id)}`}>
                            Run history
                          </Link>
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </div>
      ) : (
        !schedules.isError && (
          <Empty
            title="No sources to schedule"
            description="Add a source first, then choose when its collection should run. Sources without a schedule stay available for manual collection."
            action={
              <Button asChild>
                <Link to="/providers/new">
                  <PlusIcon />
                  Add source
                </Link>
              </Button>
            }
          />
        )
      )}
      <div className="space-y-2 text-xs leading-5 text-muted-foreground">
        <p>
          Schedule activation is independent of source activation. Both must be enabled. Timestamps
          use your browser timezone; cron expressions use the configured timezone.
        </p>
        <p>
          A page budget pauses a run without completing it. Scheduled runs continue with the same
          immutable source snapshot on a later tick. Full runs replace published torrents only after
          the entire crawl completes.
        </p>
      </div>
    </div>
  )
}

function ScheduleEditor({ source }: { source: ScheduleSummary }) {
  const [open, setOpen] = useState(false)
  const [saving, setSaving] = useState(false)
  return (
    <Dialog
      open={open}
      onOpenChange={(value) => {
        if (!saving) setOpen(value)
      }}
    >
      <DialogTrigger asChild>
        <Button
          size="sm"
          variant="outline"
          aria-label={`Configure schedule for ${source.provider_name || source.provider_id}`}
        >
          {source.schedule.every || source.schedule.cron ? 'Edit schedule' : 'Configure'}
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-xl" showCloseButton={!saving}>
        <DialogHeader>
          <DialogTitle>Schedule · {source.provider_name || source.provider_id}</DialogTitle>
          <DialogDescription>
            Changes are saved in the source JSON. Existing runs keep their original configuration.
          </DialogDescription>
        </DialogHeader>
        {open && (
          <ScheduleDocument source={source} onSaved={() => setOpen(false)} onSaving={setSaving} />
        )}
      </DialogContent>
    </Dialog>
  )
}

function ScheduleDocument({
  source,
  onSaved,
  onSaving,
}: {
  source: ScheduleSummary
  onSaved: () => void
  onSaving: (value: boolean) => void
}) {
  const document = useQuery({
    queryKey: ['schedule-document', source.provider_id],
    queryFn: ({ signal }) =>
      api<ProviderDocument>(`/providers/${encodeURIComponent(source.provider_id)}`, { signal }),
    staleTime: 0,
    gcTime: 0,
    refetchOnWindowFocus: false,
  })
  if (document.isPending) return <Loading label="Loading source definition" />
  if (!document.data)
    return <ErrorNotice error={document.error} retry={() => void document.refetch()} />
  return (
    <ScheduleForm source={source} document={document.data} onSaved={onSaved} onSaving={onSaving} />
  )
}

function ScheduleForm({
  source,
  document,
  onSaved,
  onSaving,
}: {
  source: ScheduleSummary
  document: ProviderDocument
  onSaved: () => void
  onSaving: (value: boolean) => void
}) {
  const [original, setOriginal] = useState(document)
  const initial = document.provider.schedule || {}
  const [timing, setTiming] = useState<'manual' | 'every' | 'cron'>(
    initial.cron ? 'cron' : initial.every ? 'every' : 'manual',
  )
  const [every, setEvery] = useState(initial.every || '')
  const [fullEvery, setFullEvery] = useState(initial.full_every || '')
  const [cron, setCron] = useState(initial.cron || '')
  const [timezone, setTimezone] = useState(initial.timezone || 'UTC')
  const [mode, setMode] = useState<NonNullable<Schedule['mode']>>(initial.mode || 'incremental')
  const [maxPages, setMaxPages] = useState(String(initial.max_pages || 0))
  const [knownPages, setKnownPages] = useState(String(initial.known_pages || 0))
  const [enabled, setEnabled] = useState(initial.enabled ?? Boolean(initial.every || initial.cron))
  const [reloaded, setReloaded] = useState(false)
  const metadataSupported = supportsMetadata(original.provider)
  const modeEligible = mode !== 'metadata' || metadataSupported
  const routineEligible = !fullEvery.trim() || (mode === 'incremental' && timing !== 'manual')
  const save = useMutation({
    mutationFn: () => {
      const settings: Schedule = {
        enabled,
        every: timing === 'every' ? every.trim() : undefined,
        full_every: fullEvery.trim() || undefined,
        cron: timing === 'cron' ? cron.trim() : undefined,
        timezone: timezone.trim() || 'UTC',
        mode,
        max_pages: Number(maxPages),
        known_pages: Number(knownPages),
      }
      const previous: Schedule = {
        enabled: initial.enabled ?? Boolean(initial.every || initial.cron),
        every: initial.every || undefined,
        full_every: initial.full_every || undefined,
        cron: initial.cron || undefined,
        timezone: initial.timezone || 'UTC',
        mode: initial.mode || 'incremental',
        max_pages: initial.max_pages ?? 0,
        known_pages: initial.known_pages ?? 0,
      }
      const edits = Object.fromEntries(
        Object.entries(settings).filter(
          ([key, value]) => value !== previous[key as keyof Schedule],
        ),
      )
      return saveSchedule(source.provider_id, original, edits)
    },
    onMutate: () => onSaving(true),
    onSuccess: async () => {
      await refreshData()
      onSaved()
    },
    onSettled: () => onSaving(false),
  })
  const reload = useMutation({
    mutationFn: () => api<ProviderDocument>(`/providers/${encodeURIComponent(source.provider_id)}`),
    onSuccess: (latest) => {
      setOriginal(latest)
      setReloaded(true)
      save.reset()
    },
  })
  const conflict = save.error instanceof ApiError && save.error.status === 409
  const busy = save.isPending || reload.isPending
  return (
    <form
      className="space-y-5"
      onSubmit={(event) => {
        event.preventDefault()
        if (!conflict && !busy && modeEligible && routineEligible) save.mutate()
      }}
    >
      <fieldset className="space-y-5" disabled={busy}>
        <div className="flex items-center justify-between gap-4 rounded-md border border-border p-3">
          <div>
            <Label htmlFor="schedule-enabled">Schedule enabled</Label>
            <p className="help mt-1">
              Disabling keeps these settings and does not stop an existing run.
            </p>
          </div>
          <Switch id="schedule-enabled" checked={enabled} onCheckedChange={setEnabled} />
        </div>
        {!original.provider.enabled && (
          <p className="notice">
            This source is disabled. Enable it in the source definition before scheduled runs can
            start.
          </p>
        )}
        <div className="field">
          <Label htmlFor="schedule-timing">Timing</Label>
          <select
            id="schedule-timing"
            className="native-select w-full"
            value={timing}
            onChange={(event) => setTiming(event.target.value as typeof timing)}
          >
            <option value="manual">Manual only</option>
            <option value="every">Fixed interval</option>
            <option value="cron">Cron expression</option>
          </select>
          {timing === 'manual' && (
            <p className="help">
              No automatic runs. Saving removes the interval or cron expression.
            </p>
          )}
        </div>
        {timing === 'every' && (
          <div className="field">
            <Label htmlFor="schedule-every">Interval</Label>
            <Input
              id="schedule-every"
              required
              value={every}
              onChange={(event) => setEvery(event.target.value)}
              placeholder="30m"
              aria-describedby="schedule-every-help"
            />
            <p className="help" id="schedule-every-help">
              Go duration of at least 1 minute, such as 30m, 2h or 1h30m. The first run waits one
              interval.
            </p>
          </div>
        )}
        {timing === 'cron' && (
          <div className="field">
            <Label htmlFor="schedule-cron">Cron expression</Label>
            <Input
              id="schedule-cron"
              required
              pattern="\s*\S+\s+\S+\s+\S+\s+\S+\s+\S+\s*"
              title="Enter five fields: minute hour day-of-month month day-of-week"
              value={cron}
              onChange={(event) => setCron(event.target.value)}
              placeholder="0 */6 * * *"
              aria-describedby="schedule-cron-help"
            />
            <p className="help" id="schedule-cron-help">
              Five fields: minute, hour, day of month, month, day of week. No seconds. The first run
              uses the next matching time.
            </p>
          </div>
        )}
        <div className="field">
          <Label htmlFor="schedule-full-every">Full reconciliation interval</Label>
          <Input
            id="schedule-full-every"
            value={fullEvery}
            onChange={(event) => setFullEvery(event.target.value)}
            placeholder="168h"
            aria-describedby="schedule-full-every-help"
            aria-invalid={!routineEligible}
          />
          <p className="help" id="schedule-full-every-help">
            Optional, Incremental schedules only. Leave empty to disable. Use a Go duration of at
            least 1m, such as 168h for a week. Full runs on the first eligible base tick after its
            deadline, with the same page budget. Activation or source edits start a fresh interval.
          </p>
          {!routineEligible && (
            <p role="alert" className="text-xs text-destructive">
              Choose an Incremental interval or cron schedule, or clear the Full interval.
            </p>
          )}
        </div>
        <div className="grid gap-5 sm:grid-cols-2">
          <div className="field">
            <Label htmlFor="schedule-timezone">Timezone</Label>
            <Input
              id="schedule-timezone"
              required
              value={timezone}
              onChange={(event) => setTimezone(event.target.value)}
              placeholder="UTC"
              aria-describedby="schedule-timezone-help"
            />
            <p className="help" id="schedule-timezone-help">
              IANA name, such as UTC or Europe/Paris. Cron follows local daylight-saving time.
            </p>
          </div>
          <div className="field">
            <Label htmlFor="schedule-mode">Collection mode</Label>
            <select
              id="schedule-mode"
              className="native-select w-full"
              value={mode}
              onChange={(event) => setMode(event.target.value as NonNullable<Schedule['mode']>)}
            >
              <option value="incremental">Incremental</option>
              <option value="full">Full</option>
              <option value="metadata" disabled={!metadataSupported}>
                Metadata
              </option>
            </select>
            <p className="help">
              {mode === 'full'
                ? 'Discovers the whole selected catalogue and replaces the published set atomically after completion.'
                : mode === 'metadata'
                  ? 'Fills missing configured fields throughout the existing native catalogue. Never discovers or removes torrents.'
                  : 'Discovers newest records, stopping per scope after the configured number of entirely known pages.'}
            </p>
            {!metadataSupported && (
              <p className="help">
                Metadata requires native HTTP/JSON with ID recovery and configured metadata fields.
                It is unavailable for this source.
              </p>
            )}
          </div>
          <div className="field">
            <Label htmlFor="schedule-budget">Page budget per tick</Label>
            <Input
              id="schedule-budget"
              type="number"
              min={0}
              max={10000}
              step={1}
              required
              value={maxPages}
              onChange={(event) => setMaxPages(event.target.value)}
            />
            <p className="help">
              0 = unlimited. A budget-limited run pauses and continues on the next tick.
            </p>
          </div>
          <div className="field">
            <Label htmlFor="schedule-known">Known-page boundary</Label>
            <Input
              id="schedule-known"
              type="number"
              min={0}
              max={10000}
              step={1}
              required
              disabled={mode !== 'incremental'}
              value={knownPages}
              onChange={(event) => setKnownPages(event.target.value)}
            />
            <p className="help">
              Incremental only: consecutive pages whose identities were all known before the run,
              separately per scope. 0 = off. This setting is kept but not used in other modes.
            </p>
          </div>
        </div>
      </fieldset>
      <p className="help">
        Changing a source with a paused scheduled run can block automatic continuation. Resume or
        cancel the old snapshot manually in run history. Missed ticks do not produce catch-up
        bursts.
      </p>
      <ErrorNotice error={save.error} />
      <ErrorNotice error={reload.error} />
      {conflict && (
        <div className="space-y-3">
          <p className="help">
            Your edits are still here. Reload the latest source to preserve its other changes, then
            review and save your schedule again. Only the schedule fields you edited will replace
            the current settings.
          </p>
          <Button type="button" variant="outline" disabled={busy} onClick={() => reload.mutate()}>
            <ArrowClockwiseIcon />
            {reload.isPending ? 'Reloading…' : 'Reload source, keep edits'}
          </Button>
        </div>
      )}
      {reloaded && !conflict && (
        <p role="status" className="notice">
          Latest source loaded. Your unsaved schedule edits are preserved. Review them before
          saving.
        </p>
      )}
      <DialogFooter>
        <Button type="button" variant="outline" asChild>
          <Link
            to={`/providers/${encodeURIComponent(source.provider_id)}`}
            aria-disabled={busy}
            tabIndex={busy ? -1 : undefined}
            onClick={(event) => {
              if (busy) event.preventDefault()
            }}
          >
            Source definition
          </Link>
        </Button>
        <Button type="submit" disabled={busy || conflict || !modeEligible || !routineEligible}>
          <FloppyDiskIcon />
          {save.isPending ? 'Saving…' : 'Save schedule'}
        </Button>
      </DialogFooter>
    </form>
  )
}
