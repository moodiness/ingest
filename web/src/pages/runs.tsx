import { useRef } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Link, useParams, useSearchParams } from 'react-router-dom'
import {
  ArrowLeftIcon,
  ArrowRightIcon,
  CaretDownIcon,
  PauseIcon,
  PlayIcon,
  StopIcon,
} from '@phosphor-icons/react'
import { api, params, queryClient, refreshData } from '@/lib/api'
import type { Items, Page, ProviderSummary, Run, RunEvent } from '@/lib/types'
import { useDate, modeLabels, number, statusLabels } from '@/lib/format'
import { normalizePageOffset, usePageOffset, usePageSize } from '@/lib/display-preferences'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Label } from '@/components/ui/label'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import {
  Table,
  TableHeader,
  TableRow,
  TableHead,
  TableBody,
  TableCell,
} from '@/components/ui/table'
import { CollectDialog } from '@/components/collect-dialog'
import {
  ConfirmAction,
  Empty,
  ErrorNotice,
  Loading,
  PageHeader,
  Pager,
  RunStatusBadge,
} from '@/components/common'
import { RawTableView } from '@/pages/library'
import type { RunChanges } from '@/lib/history-types'
import { decodeSearchJSON } from '@/lib/search-json'
import { RunChangesList, RunPublicationCounts } from '@/components/torrent-history'
import { RunFailureDiagnostic } from '@/components/run-diagnostics'

export function RunsTable({ runs }: { runs: Run[] }) {
  const date = useDate()
  return (
    <div className="table-frame">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Source / run</TableHead>
            <TableHead>Mode / trigger</TableHead>
            <TableHead>Status</TableHead>
            <TableHead className="text-right">Pages</TableHead>
            <TableHead className="text-right">Records</TableHead>
            <TableHead>Created</TableHead>
            <TableHead>
              <span className="sr-only">Open</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {runs.map((run) => (
            <TableRow key={run.id}>
              <TableCell>
                <Link
                  className="font-medium hover:text-primary"
                  to={`/runs/${encodeURIComponent(run.id)}`}
                >
                  {run.provider_name || run.provider_id}
                </Link>
                <p className="mono mt-1 text-muted-foreground">{run.id.slice(0, 8)}</p>
              </TableCell>
              <TableCell className="text-xs text-muted-foreground">
                {modeLabels[run.mode]}
                <p className="mt-1">{run.trigger === 'scheduled' ? 'Scheduled' : 'Manual'}</p>
              </TableCell>
              <TableCell>
                <RunStatusBadge run={run} />
              </TableCell>
              <TableCell className="text-right font-mono text-xs">{number(run.pages)}</TableCell>
              <TableCell className="text-right font-mono text-xs">
                {number(run.distinct_records)}
                {run.errors > 0 && (
                  <p className="mt-1 text-muted-foreground">
                    {number(run.errors)} historical error{run.errors > 1 ? 's' : ''}
                  </p>
                )}
              </TableCell>
              <TableCell className="text-xs text-muted-foreground">
                {date(run.created_at)}
              </TableCell>
              <TableCell>
                <Button variant="ghost" size="icon-sm" asChild>
                  <Link
                    to={`/runs/${encodeURIComponent(run.id)}`}
                    aria-label={`Open run ${run.id}`}
                  >
                    <ArrowRightIcon />
                  </Link>
                </Button>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

export function RunsPage() {
  const [search, setSearch] = useSearchParams()
  const provider = search.get('provider') ?? ''
  const status = search.get('status') ?? ''
  const limit = usePageSize()
  const offset = normalizePageOffset(search.get('offset'), limit)
  const runs = useQuery({
    queryKey: ['runs', provider, status, offset, limit],
    queryFn: ({ signal }) =>
      api<Page<Run>>(`/runs?${params({ provider, status, offset, limit })}`, { signal }),
  })
  const providers = useQuery({
    queryKey: ['providers'],
    queryFn: ({ signal }) => api<Items<ProviderSummary>>('/providers', { signal }),
  })
  function filter(key: string, value: string) {
    const next = new URLSearchParams(search)
    value ? next.set(key, value) : next.delete(key)
    next.delete('offset')
    setSearch(next)
  }
  return (
    <div className="page">
      <PageHeader
        title="Runs"
        description="Persistent run history. Pausing preserves the resume point; only a fully completed run publishes a complete replacement."
        actions={<CollectDialog />}
      />
      <div className="toolbar">
        <div className="field">
          <Label htmlFor="run-provider" className="sr-only">
            Filter by source
          </Label>
          <select
            id="run-provider"
            className="native-select min-w-48"
            value={provider}
            onChange={(event) => filter('provider', event.target.value)}
          >
            <option value="">All sources</option>
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
          <Label htmlFor="run-status" className="sr-only">
            Filter by status
          </Label>
          <select
            id="run-status"
            className="native-select"
            value={status}
            onChange={(event) => filter('status', event.target.value)}
          >
            <option value="">All statuses</option>
            {Object.entries(statusLabels).map(([value, label]) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
          </select>
        </div>
      </div>
      <ErrorNotice error={providers.error} retry={() => void providers.refetch()} />
      <ErrorNotice error={runs.error} retry={() => void runs.refetch()} />
      {runs.isPending ? (
        <Loading />
      ) : (
        runs.data && (
          <>
            {runs.data.items?.length ? (
              <RunsTable runs={runs.data.items} />
            ) : (
              <Empty
                title="No runs in this view"
                description={
                  provider || status
                    ? 'Change the filters to broaden the search.'
                    : 'Start a preview from an enabled source to inspect the first data without changing published torrents.'
                }
              />
            )}
            <Pager
              total={runs.data.total}
              limit={runs.data.limit}
              offset={runs.data.offset}
              busy={runs.isFetching}
              onChange={(value) => {
                const next = new URLSearchParams(search)
                next.set('offset', String(value))
                setSearch(next)
              }}
            />
          </>
        )
      )}
    </div>
  )
}

type RunAction = 'pause' | 'cancel' | 'resume'

function RunControls({
  run,
  pending,
  onAction,
}: {
  run: Run
  pending?: RunAction
  onAction: (action: RunAction) => Promise<Run>
}) {
  const active = run.status === 'running' || run.status === 'queued'
  const resumable = ['paused', 'failed', 'cancelled'].includes(run.status)
  const pausing = run.pause_requested || pending === 'pause'
  const cancelling = run.cancel_requested || pending === 'cancel'
  const canHold = run.status === 'paused' && run.trigger === 'scheduled' && !run.pause_requested
  return (
    <>
      {(active || canHold || (run.status === 'paused' && pending === 'pause')) && (
        <Button
          variant="warning"
          className="min-h-11 sm:min-h-9"
          disabled={pausing || cancelling || pending === 'resume'}
          title={
            canHold
              ? 'Prevent scheduled continuation until you explicitly resume this run.'
              : 'Finish and save the current page, then wait for you to resume.'
          }
          onClick={() => void onAction('pause').catch(() => undefined)}
        >
          <PauseIcon aria-hidden="true" />
          {pending === 'pause' && run.status === 'paused'
            ? 'Holding…'
            : pausing
              ? 'Pausing…'
              : canHold
                ? 'Keep paused'
                : 'Pause'}
        </Button>
      )}
      {(active || run.status === 'paused' || (run.status === 'failed' && run.pause_requested)) && (
        <ConfirmAction
          title={active ? 'Cancel this run?' : 'Cancel this held run?'}
          description={
            active
              ? 'The current request will be interrupted. Validated pages and the resume point will be preserved. Cancellation becomes final when the worker confirms it.'
              : 'The saved resume point and raw archives are preserved. Cancelling releases this run’s hold, allowing future scheduled runs to use the current source configuration.'
          }
          label={active ? 'Request cancellation' : 'Cancel run'}
          destructive
          trigger={
            <Button
              variant="destructive"
              className="min-h-11 sm:min-h-9"
              disabled={cancelling || pending === 'resume'}
            >
              <StopIcon aria-hidden="true" />
              {cancelling ? 'Cancellation requested' : 'Cancel'}
            </Button>
          }
          action={() => onAction('cancel')}
        />
      )}
      {resumable && (
        <ConfirmAction
          title="Resume this run?"
          description="Resuming clears any manual hold, starts from the last validated point and adds a new page budget. It uses the original source snapshot, not later JSON changes. The current source must still be valid and enabled."
          label="Resume run"
          trigger={
            <Button className="min-h-11 sm:min-h-9" disabled={Boolean(pending)}>
              <PlayIcon aria-hidden="true" />
              {pending === 'resume' ? 'Resuming…' : 'Resume'}
            </Button>
          }
          action={() => onAction('resume')}
        />
      )}
    </>
  )
}

function EventsView({ runId }: { runId: string }) {
  const date = useDate()
  const pageSize = usePageSize()
  const [offset, setOffset] = usePageOffset(pageSize)
  const page = offset / pageSize
  const section = useRef<HTMLElement>(null)
  const events = useQuery({
    queryKey: ['events', runId, 'page', offset, pageSize],
    queryFn: ({ signal }) =>
      api<Page<RunEvent>>(
        `/runs/${encodeURIComponent(runId)}/events?${params({ limit: pageSize, offset })}`,
        { signal },
      ),
  })
  const items = events.data?.items ?? []
  const pageCount = Math.max(1, Math.ceil((events.data?.total ?? 0) / pageSize))
  const hasNext = page + 1 < pageCount
  const busy = events.isFetching
  const move = (value: number) => {
    setOffset(value * pageSize)
    section.current?.scrollIntoView({ block: 'start' })
  }
  const pagination = (
    <div className="flex flex-wrap items-center justify-between gap-3">
      <p className="text-xs text-muted-foreground">
        Page {number(page + 1)}
        {events.data && ` of ${number(pageCount)} · ${number(events.data.total)} events`}
      </p>
      <div className="flex gap-1">
        <Button variant="ghost" size="sm" disabled={busy || page === 0} onClick={() => move(0)}>
          First
        </Button>
        <Button
          variant="outline"
          size="sm"
          disabled={busy || page === 0}
          onClick={() => move(page - 1)}
        >
          <ArrowLeftIcon />
          Previous
        </Button>
        <Button
          variant="outline"
          size="sm"
          disabled={busy || !hasNext}
          onClick={() => move(page + 1)}
        >
          Next
          <ArrowRightIcon />
        </Button>
        <Button
          variant="ghost"
          size="sm"
          disabled={busy || !hasNext}
          onClick={() => move(pageCount - 1)}
        >
          Latest
        </Button>
      </div>
    </div>
  )
  return (
    <section ref={section} className="scroll-mt-24 space-y-3" aria-label="Run events">
      <ErrorNotice error={events.error} retry={() => void events.refetch()} />
      <p className="sr-only" role="status">
        Events page {number(page + 1)}
        {events.data && ` of ${number(pageCount)}`}
      </p>
      <nav aria-label="Run events pagination (top)">{pagination}</nav>
      {events.isPending ? (
        <Loading />
      ) : items.length ? (
        <div className="table-frame">
          <Table className="table-fixed">
            <TableHeader>
              <TableRow>
                <TableHead className="w-32 sm:w-48">Time</TableHead>
                <TableHead className="hidden w-36 lg:table-cell">Type</TableHead>
                <TableHead>Event</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((event) => (
                <TableRow key={event.id}>
                  <TableCell className="align-top text-xs leading-5 whitespace-normal text-muted-foreground">
                    <time dateTime={event.created_at}>{date(event.created_at)}</time>
                    <p className="mono mt-1">#{event.id}</p>
                  </TableCell>
                  <TableCell className="hidden align-top lg:table-cell">
                    <Badge
                      variant="outline"
                      className="max-w-full font-mono break-all whitespace-normal"
                    >
                      {event.kind}
                    </Badge>
                  </TableCell>
                  <TableCell className="align-top whitespace-normal">
                    <Badge
                      variant="outline"
                      className="mb-1 max-w-full font-mono break-all whitespace-normal lg:hidden"
                    >
                      {event.kind}
                    </Badge>
                    <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1">
                      <p className="min-w-0 break-words leading-6">{event.message}</p>
                      {event.kind === 'metadata_queued' &&
                        typeof event.data?.metadata_run_id === 'string' &&
                        event.data.metadata_run_id !== '' && (
                          <Link
                            className="data-link inline-flex items-center gap-2 text-xs leading-6"
                            to={`/runs/${encodeURIComponent(event.data.metadata_run_id)}`}
                          >
                            Open Metadata follow-up · new torrents only
                            <ArrowRightIcon />
                          </Link>
                        )}
                      {typeof event.data?.page_id === 'number' &&
                        event.data.page_id > 0 &&
                        (event.data.payload_retained === false ? (
                          <span className="text-xs leading-6 text-muted-foreground">
                            Response body not stored
                          </span>
                        ) : (
                          <Link
                            className="data-link inline-flex items-center gap-2 text-xs leading-6"
                            to={`/pages/${event.data.page_id}`}
                          >
                            Open response
                            <ArrowRightIcon />
                          </Link>
                        ))}
                    </div>
                    <RunFailureDiagnostic data={event.data} />
                    {event.data && Object.keys(event.data).length > 0 && (
                      <details className="mt-1">
                        <summary className="cursor-pointer text-xs text-muted-foreground">
                          Event data
                        </summary>
                        <pre className="mt-2 overflow-x-auto whitespace-pre-wrap break-all rounded bg-background p-3 font-mono text-xs">
                          {JSON.stringify(event.data, null, 2)}
                        </pre>
                      </details>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      ) : (
        !events.isError && (
          <Empty title="No events yet" description="Events will appear as this run is processed." />
        )
      )}
      <nav aria-label="Run events pagination (bottom)">{pagination}</nav>
    </section>
  )
}

export function RunDetailPage() {
  const date = useDate()
  const limit = usePageSize()
  const { id = '' } = useParams()
  const [search, setSearch] = useSearchParams()
  const actionsInFlight = useRef(new Map<string, Promise<Run>>())
  const actionSequence = useRef(0)
  const actionMutation = useMutation({
    mutationFn: async ({ runId, action }: { runId: string; action: RunAction; sequence: number }) =>
      api<Run>(`/runs/${encodeURIComponent(runId)}/${action}`, {
        method: 'POST',
      }),
    onSuccess: async (updated, { runId, sequence }) => {
      if (sequence === actionSequence.current) {
        // Discard pre-action polling responses before publishing the acknowledged state.
        await queryClient.cancelQueries({ queryKey: ['run', runId], exact: true })
        if (sequence === actionSequence.current) {
          queryClient.setQueryData(['run', runId], updated)
        }
      }
      await refreshData()
    },
  })
  function runAction(action: RunAction) {
    const key = `${id}/${action}`
    const inFlight = actionsInFlight.current.get(key)
    if (inFlight) return inFlight
    const sequence = ++actionSequence.current
    const request = actionMutation
      .mutateAsync({ runId: id, action, sequence })
      .finally(() => actionsInFlight.current.delete(key))
    actionsInFlight.current.set(key, request)
    return request
  }
  const activeTab = ['events', 'raw', 'checkpoint', 'changes'].includes(search.get('tab') ?? '')
    ? search.get('tab')!
    : 'events'
  const runQuery = useQuery({
    queryKey: ['run', id],
    queryFn: ({ signal }) => api<Run>(`/runs/${encodeURIComponent(id)}`, { signal }),
  })
  const pendingAction =
    actionMutation.isPending && actionMutation.variables.runId === id
      ? actionMutation.variables.action
      : undefined
  const observedRun = runQuery.data
  // Keep request feedback independent of polling until the server acknowledges the action.
  const run =
    observedRun &&
    pendingAction === 'pause' &&
    !observedRun.cancel_requested &&
    ['queued', 'running', 'paused'].includes(observedRun.status)
      ? { ...observedRun, pause_requested: true }
      : observedRun
  const changesOffset = normalizePageOffset(search.get('changes_offset'), limit)
  const changes = useQuery({
    queryKey: ['run-changes', id, changesOffset, limit],
    queryFn: ({ signal }) =>
      api<RunChanges>(
        `/runs/${encodeURIComponent(id)}/changes?${params({ limit, offset: changesOffset })}`,
        { signal },
        decodeSearchJSON<RunChanges>,
      ),
    enabled: Boolean(run) && activeTab === 'changes',
    refetchInterval: run?.status === 'running' || run?.status === 'queued' ? 10000 : false,
  })
  if (runQuery.isPending)
    return (
      <div className="page max-w-6xl">
        <Loading />
      </div>
    )
  if (!run)
    return (
      <div className="page max-w-6xl">
        <PageHeader title="Run details" back={{ to: '/runs', label: 'All runs' }} />
        <ErrorNotice error={runQuery.error} retry={() => void runQuery.refetch()} />
      </div>
    )
  return (
    <div className="page max-w-6xl">
      <PageHeader
        title={run.provider_name || run.provider_id}
        description={`Run ${run.id}`}
        back={{ to: '/runs', label: 'All runs' }}
        actions={
          <>
            <RunStatusBadge run={run} />
            <RunControls run={run} pending={pendingAction} onAction={runAction} />
          </>
        }
      />
      <ErrorNotice error={runQuery.error} retry={() => void runQuery.refetch()} />
      <ErrorNotice error={actionMutation.variables?.runId === id ? actionMutation.error : null} />
      {run.error && run.status === 'failed' && <ErrorNotice error={new Error(run.error)} />}
      {(run.status === 'paused' || (run.status === 'failed' && run.pause_requested)) && (
        <div
          role="status"
          className="notice flex items-start gap-3 border-amber-300/30 bg-amber-300/5"
        >
          <PauseIcon aria-hidden="true" className="mt-1 shrink-0 text-amber-200" />
          <p>
            <span className="text-amber-200">
              {run.status === 'failed'
                ? 'This run failed while a manual hold was requested.'
                : 'This run is paused, not complete.'}
            </span>{' '}
            {run.mode === 'full'
              ? 'Torrents from this run do not replace the published dataset yet. '
              : ''}
            {pendingAction === 'pause' && !observedRun?.pause_requested ? (
              'Requesting a manual hold to prevent automatic continuation…'
            ) : run.pause_requested ? (
              'Manual hold: this run will not continue automatically. Choose Resume to continue.'
            ) : run.trigger === 'scheduled' ? (
              <>
                This scheduled run can continue at the next scheduled tick if the source revision
                {run.metadata_parent_run_id
                  ? ' and the originating Incremental schedule still match. '
                  : ' and mode still match. '}
                Choose Keep paused to require an explicit Resume instead. Otherwise, resolve the
                blocked run manually.{' '}
                <Link className="data-link" to="/schedules">
                  View schedules
                </Link>
              </>
            ) : (
              'Resume it to continue.'
            )}
            {run.status === 'paused' && !run.pause_requested && run.error && (
              <span className="mt-2 block text-muted-foreground">{run.error}</span>
            )}
          </p>
        </div>
      )}
      {run.pause_requested &&
        !run.cancel_requested &&
        (run.status === 'queued' || run.status === 'running') && (
          <p role="status" className="notice border-amber-300/30 bg-amber-300/5 text-amber-200">
            Pausing… The worker will finish and save the current page before stopping. This run will
            then wait for an explicit Resume; scheduled ticks will not continue it. You can still
            Cancel to interrupt the current request.
          </p>
        )}
      {run.cancel_requested && (run.status === 'queued' || run.status === 'running') && (
        <p className="notice">
          Cancellation requested. The worker still needs to confirm the stop; resuming is
          unavailable during this transition.
        </p>
      )}
      {run.mode === 'preview' && (
        <p className="notice text-muted-foreground">
          Preview mode: a parsed sample is saved without publishing torrents or storing raw bodies.
          Reaching the budget is normal in this mode.
        </p>
      )}
      {run.mode === 'metadata' && (
        <p className="notice text-muted-foreground">
          {run.metadata_parent_run_id ? (
            <>
              Automatic Metadata follow-up: fills configured missing fields only for native torrents
              newly inserted by{' '}
              <Link
                className="data-link"
                to={`/runs/${encodeURIComponent(run.metadata_parent_run_id)}`}
              >
                the parent Incremental run
              </Link>
              . Existing torrents encountered by that run are excluded. No torrents are discovered
              or removed.
            </>
          ) : (
            <>
              Metadata mode: fills missing configured fields throughout the native catalogue that
              existed before this run, including old records. No torrents are discovered or removed.
            </>
          )}
        </p>
      )}
      <section className="space-y-3" aria-label="Collection progress">
        <dl className="grid grid-cols-2 overflow-hidden rounded-md border border-border text-center sm:grid-cols-3">
          <div className="col-span-2 border-b border-border px-3 py-5 sm:col-span-1 sm:border-r sm:border-b-0">
            <dt className="text-xs text-muted-foreground">Records</dt>
            <dd className="mt-2 font-mono text-3xl text-primary tabular-nums">
              {number(run.distinct_records)}
            </dd>
          </div>
          <div className="border-r border-border px-3 py-5">
            <dt className="text-xs text-muted-foreground">Validated pages</dt>
            <dd className="mt-2 font-mono text-3xl tabular-nums">{number(run.pages)}</dd>
          </div>
          <div className="px-3 py-5">
            <dt className="text-xs text-muted-foreground">Historical errors</dt>
            <dd className="mt-2 font-mono text-3xl tabular-nums">{number(run.errors)}</dd>
          </div>
        </dl>
        <p className="help">
          Records count distinct source IDs.
          {run.mode === 'full' && ' Full collections publish only after successful completion.'}
          {run.errors > 0 &&
            (run.status === 'succeeded'
              ? ' This run succeeded. Retained errors describe earlier attempts or rejected records, not a current run failure; inspect Events and Observations for unresolved record omissions.'
              : ' Errors are cumulative across attempts and rejected records. Current run health is shown by its status, not this history count.')}
        </p>
      </section>
      <details className="group border-b border-border pb-5">
        <summary className="flex cursor-pointer list-none flex-wrap items-center justify-between gap-3 [&::-webkit-details-marker]:hidden">
          <span className="flex flex-wrap items-center gap-x-4 gap-y-1 text-sm">
            <span className="font-medium">{modeLabels[run.mode]}</span>
            <span className="text-muted-foreground">
              {run.trigger === 'scheduled' ? 'Scheduled trigger' : 'Manual trigger'}
            </span>
            <span className="text-muted-foreground">
              {run.started_at ? `Started ${date(run.started_at)}` : 'Not started'}
            </span>
          </span>
          <span className="inline-flex items-center gap-2 text-xs text-muted-foreground">
            Run details
            <CaretDownIcon aria-hidden="true" className="group-open:rotate-180" />
          </span>
        </summary>
        <dl className="mt-5 grid gap-x-8 gap-y-5 text-sm sm:grid-cols-2 xl:grid-cols-4">
          <div>
            <dt className="text-xs text-muted-foreground">Created</dt>
            <dd className="mt-1.5">{date(run.created_at)}</dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">Started</dt>
            <dd className="mt-1.5">{date(run.started_at)}</dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">Attempt finished</dt>
            <dd className="mt-1.5">
              {run.finished_at
                ? date(run.finished_at)
                : run.status === 'running'
                  ? 'In progress'
                  : 'Not recorded'}
            </dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">Budget per attempt</dt>
            <dd className="mt-1.5">
              {run.max_pages === 0 ? 'Unlimited' : `${number(run.max_pages)} pages`}
            </dd>
          </div>
        </dl>
      </details>
      <Tabs
        value={activeTab}
        onValueChange={(value) => {
          const next = new URLSearchParams(search)
          next.set('tab', value)
          setSearch(next, { replace: true })
        }}
      >
        <TabsList className="grid w-full grid-cols-2 group-data-[orientation=horizontal]/tabs:h-auto sm:flex sm:w-fit">
          <TabsTrigger value="events">Events</TabsTrigger>
          <TabsTrigger value="changes">Published changes</TabsTrigger>
          <TabsTrigger value="raw">Observations / archives</TabsTrigger>
          <TabsTrigger value="checkpoint">Resume point</TabsTrigger>
        </TabsList>
        <TabsContent value="events" className="mt-4">
          <EventsView key={run.id} runId={run.id} />
        </TabsContent>
        <TabsContent value="changes" className="mt-5 space-y-6">
          <ErrorNotice error={changes.error} retry={() => void changes.refetch()} />
          {changes.isPending ? (
            <Loading label="Loading published changes" />
          ) : (
            changes.data && (
              <>
                <RunPublicationCounts data={changes.data} />
                <RunChangesList
                  data={changes.data}
                  busy={changes.isFetching}
                  onPage={(offset) => {
                    const next = new URLSearchParams(search)
                    next.set('changes_offset', String(offset))
                    setSearch(next)
                  }}
                />
              </>
            )
          )}
        </TabsContent>
        <TabsContent value="raw" className="mt-5">
          <RawTableView runId={run.id} />
        </TabsContent>
        <TabsContent value="checkpoint" className="mt-5 space-y-4">
          <div className="notice">
            <p>
              The resume point is private and managed by the adapter. It advances only after the
              page is validated and is never exposed in the console.
            </p>
            <p className="mono mt-2 break-all">Source revision: {run.revision}</p>
          </div>
        </TabsContent>
      </Tabs>
    </div>
  )
}
