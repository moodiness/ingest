import { useState, type FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { ArrowClockwiseIcon, CheckCircleIcon, WarningCircleIcon } from '@phosphor-icons/react'
import { api } from '@/lib/api'
import { bytes, useDate, number } from '@/lib/format'
import type { HealthDiagnostic, HealthSettings, SystemHealth } from '@/lib/health-types'
import { Empty, ErrorNotice, Loading, PageHeader } from '@/components/common'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'

const categoryLabels = {
  live: {
    title: 'Published and staging',
    detail:
      'Normalized occurrences and unpublished staging. Raw observations are counted separately.',
  },
  raw: {
    title: 'Raw archives',
    detail:
      'Compact observations and historical raw payloads, including their TOAST storage. New raw bodies are not stored.',
  },
  journal: {
    title: 'Journal and history',
    detail:
      'Catalogue journal, semantic publication history and historical baselines. Never pruned automatically.',
  },
  other: {
    title: 'Other relations',
    detail: 'Runs, events, security, settings and all remaining user relations.',
  },
}

const diagnosticLabels: Record<HealthDiagnostic['code'], string> = {
  storage_low: 'Local storage is low',
  source_stale: 'Source freshness',
  authentication: 'Authentication rejected',
  certificate: 'Certificate verification failed',
  run_stuck: 'Run has no recent progress',
  journal_growth: 'Journal growth threshold exceeded',
  database_unavailable: 'Database measurements unavailable',
}

function signedBytes(value: number) {
  return `${value > 0 ? '+' : value < 0 ? '−' : ''}${bytes(Math.abs(value))}`
}

export function HealthPage() {
  const date = useDate()
  const report = useQuery({
    queryKey: ['system-health'],
    queryFn: ({ signal }) => api<SystemHealth>('/system-health', { signal }),
    refetchInterval: 15_000,
    refetchIntervalInBackground: false,
  })
  const data = report.data
  const stale = data && Date.now() - new Date(data.checked_at).getTime() > 150_000
  return (
    <div className="page">
      <PageHeader
        title="System health"
        description="Measured storage, source freshness and run progress. Monitoring never deletes archives or publication history."
        actions={
          <Button
            variant="outline"
            disabled={report.isFetching}
            onClick={() => void report.refetch()}
          >
            <ArrowClockwiseIcon />
            Refresh
          </Button>
        }
      />
      <ErrorNotice error={report.error} retry={() => void report.refetch()} />
      {report.isPending ? (
        <Loading label="Loading system health measurements" />
      ) : (
        data && (
          <>
            <div className="flex flex-wrap items-start justify-between gap-4 rounded-md border border-border bg-muted/30 p-4">
              <div className="flex items-start gap-3">
                {data.status === 'healthy' && !stale ? (
                  <CheckCircleIcon className="mt-0.5 size-5 shrink-0 text-primary" />
                ) : (
                  <WarningCircleIcon className="mt-0.5 size-5 shrink-0 text-amber-600 dark:text-amber-400" />
                )}
                <div>
                  <p className="font-medium">
                    {stale
                      ? 'Monitor evidence is stale'
                      : data.status === 'healthy'
                        ? 'No active health warnings'
                        : data.status === 'unavailable'
                          ? 'Some measurements are unavailable'
                          : `${number(data.diagnostics.length)} active health warnings`}
                  </p>
                  <p className="mt-1 text-xs leading-5 text-muted-foreground">
                    Last check {date(data.checked_at)}. Measurements run every minute; this page
                    refreshes while visible.
                  </p>
                  <p className="text-xs leading-5 text-muted-foreground">
                    {data.sources_available
                      ? `${number(data.sources_checked)} enabled sources checked. Preview runs do not establish publication freshness.`
                      : 'Source health could not be refreshed. Existing diagnostics retain their last recorded evidence.'}
                  </p>
                </div>
              </div>
              <Button variant="ghost" size="sm" asChild>
                <Link to="/notifications">View notifications</Link>
              </Button>
            </div>

            <dl className="metric-strip">
              <div className="metric">
                <dt className="text-xs text-muted-foreground">PostgreSQL database</dt>
                <dd className="mt-3 font-mono text-2xl font-medium tracking-tight">
                  {data.database.measured_at ? bytes(data.database.total_bytes) : 'Unavailable'}
                </dd>
                <p className="mt-2 text-xs text-muted-foreground">
                  {data.database.available ? 'Live measurement' : 'Not current'} ·{' '}
                  {date(data.database.measured_at)}
                </p>
              </div>
              <div className="metric">
                <dt className="text-xs text-muted-foreground">Local available space</dt>
                <dd className="mt-3 font-mono text-2xl font-medium tracking-tight">
                  {data.disk.available ? bytes(data.disk.available_bytes) : 'Unavailable'}
                </dd>
                <p className="mt-2 text-xs text-muted-foreground">
                  {data.disk.available
                    ? `of ${bytes(data.disk.total_bytes)} total`
                    : 'Filesystem could not be measured'}
                </p>
              </div>
              <div className="metric">
                <dt className="text-xs text-muted-foreground">Database growth / day</dt>
                <dd className="mt-3 font-mono text-2xl font-medium tracking-tight">
                  {data.growth ? signedBytes(data.growth.database_bytes_per_day) : 'Not yet known'}
                </dd>
                <p className="mt-2 text-xs text-muted-foreground">
                  {data.growth
                    ? `${data.growth.elapsed_days.toFixed(2)} elapsed days measured`
                    : 'Requires measurements at least 24 hours apart'}
                </p>
              </div>
              <div className="metric">
                <dt className="text-xs text-muted-foreground">Journal growth / day</dt>
                <dd className="mt-3 font-mono text-2xl font-medium tracking-tight">
                  {data.growth ? signedBytes(data.growth.journal_bytes_per_day) : 'Not yet known'}
                </dd>
                <p className="mt-2 text-xs text-muted-foreground">
                  Includes catalogue and semantic history
                </p>
              </div>
            </dl>

            <section className="space-y-4" aria-labelledby="health-storage">
              <div>
                <h2 id="health-storage" className="section-title">
                  Where storage is used
                </h2>
                <p className="mt-1.5 text-sm text-muted-foreground">
                  Category totals include their indexes once. These are physical relation sizes, not
                  estimated record counts.
                </p>
              </div>
              {!data.database.measured_at ? (
                <Empty
                  title="Database storage is unavailable"
                  description="No successful measurement has been collected. The monitor will retry automatically."
                />
              ) : (
                <div className="overflow-hidden rounded-md border border-border">
                  {data.database.categories.map((category) => (
                    <div
                      key={category.key}
                      className="grid gap-3 border-b border-border p-4 last:border-b-0 md:grid-cols-[minmax(0,1fr)_auto] md:gap-6"
                    >
                      <div>
                        <h3 className="text-sm font-medium">
                          {categoryLabels[category.key].title}
                        </h3>
                        <p className="mt-1 text-xs leading-5 text-muted-foreground">
                          {categoryLabels[category.key].detail}
                        </p>
                      </div>
                      <div className="md:text-right">
                        <p className="font-mono text-lg">{bytes(category.bytes)}</p>
                        <p className="mt-1 text-xs text-muted-foreground">
                          Tables {bytes(category.table_bytes)} · Indexes{' '}
                          {bytes(category.index_bytes)}
                        </p>
                      </div>
                    </div>
                  ))}
                  <div className="bg-muted/20 p-4 text-xs leading-5 text-muted-foreground">
                    <span className="font-medium text-foreground">
                      Other database storage: {signedBytes(data.database.other_database_bytes)}.
                    </span>{' '}
                    Difference between total database size and measured user relations. Includes
                    system catalogues and other database files; concurrent writes can produce a
                    small negative difference. WAL and server-wide space are not included in
                    database size.
                  </div>
                </div>
              )}
              <div className="rounded-md border border-border p-4 text-sm leading-6">
                <h3 className="font-medium">Local filesystem scope</h3>
                <p className="mt-1 text-muted-foreground">
                  {data.disk.scope}. This must not be used as free space on a remote PostgreSQL
                  host.
                </p>
                <p className="mt-2 text-xs text-muted-foreground">
                  {data.disk.available
                    ? `Filesystem free: ${bytes(data.disk.free_bytes)}. Available to the application: ${bytes(data.disk.available_bytes)}. Total: ${bytes(data.disk.total_bytes)}.`
                    : 'Filesystem values are unavailable, not zero.'}{' '}
                  Measured {date(data.disk.measured_at)}.
                </p>
              </div>
            </section>

            <section
              className="space-y-4 border-t border-border pt-6"
              aria-labelledby="health-diagnostics"
            >
              <div>
                <h2 id="health-diagnostics" className="section-title">
                  Diagnostics
                </h2>
                <p className="mt-1.5 text-sm text-muted-foreground">
                  Warnings and recoveries enter the shared notification feed only when state
                  changes. No automatic cleanup or source mutation occurs.
                </p>
              </div>
              {data.diagnostics.length ? (
                <div className="space-y-3">
                  {data.diagnostics.map((item) => (
                    <Diagnostic
                      key={`${item.code}:${item.provider_id ?? ''}:${item.run_id ?? ''}`}
                      item={item}
                    />
                  ))}
                </div>
              ) : (
                <Empty
                  title="No active diagnostics"
                  description="Configured thresholds are currently satisfied. Missing measurements are called out separately above."
                />
              )}
            </section>

            <section
              className="space-y-4 border-t border-border pt-6"
              aria-labelledby="health-history"
            >
              <div>
                <h2 id="health-history" className="section-title">
                  Daily storage evidence
                </h2>
                <p className="mt-1.5 text-sm text-muted-foreground">
                  First successful sample per UTC day. Up to 90 recent days are shown; earlier
                  samples remain stored. Deltas use actual elapsed time, including missed days.
                </p>
              </div>
              {data.growth && (
                <p className="text-xs text-muted-foreground">
                  Current rate compares {date(data.growth.from)} to {date(data.growth.to)}:{' '}
                  {signedBytes(data.growth.database_bytes)} database,{' '}
                  {signedBytes(data.growth.journal_bytes)} journal over{' '}
                  {data.growth.elapsed_days.toFixed(2)} days.
                  {!data.database.available &&
                    ' Growth reflects the last successful measurement, not current usage.'}
                </p>
              )}
              {data.samples.length ? (
                <div
                  className="table-frame max-h-96 overflow-auto"
                  tabIndex={0}
                  role="region"
                  aria-label="Daily database storage samples"
                >
                  <table className="w-full min-w-[760px] text-left text-sm">
                    <thead>
                      <tr>
                        <th className="p-3 font-medium">Sample time</th>
                        <th className="p-3 font-medium">Database</th>
                        <th className="p-3 font-medium">Published / staging</th>
                        <th className="p-3 font-medium">Raw</th>
                        <th className="p-3 font-medium">Journal</th>
                        <th className="p-3 font-medium">Change / day</th>
                      </tr>
                    </thead>
                    <tbody>
                      {data.samples
                        .map((sample, index) => {
                          const previous = data.samples[index - 1]
                          const elapsed = previous
                            ? (Date.parse(sample.sampled_at) - Date.parse(previous.sampled_at)) /
                              86_400_000
                            : 0
                          return (
                            <tr key={sample.sampled_at} className="border-t border-border">
                              <td className="p-3 text-xs whitespace-nowrap">
                                {date(sample.sampled_at)}
                              </td>
                              <td className="p-3 font-mono text-xs">
                                {bytes(sample.database_bytes)}
                              </td>
                              <td className="p-3 font-mono text-xs">{bytes(sample.live_bytes)}</td>
                              <td className="p-3 font-mono text-xs">{bytes(sample.raw_bytes)}</td>
                              <td className="p-3 font-mono text-xs">
                                {bytes(sample.journal_bytes)}
                              </td>
                              <td className="p-3 font-mono text-xs">
                                {previous && elapsed > 0
                                  ? signedBytes(
                                      (sample.database_bytes - previous.database_bytes) / elapsed,
                                    )
                                  : 'Baseline'}
                              </td>
                            </tr>
                          )
                        })
                        .reverse()}
                    </tbody>
                  </table>
                </div>
              ) : (
                <Empty
                  title="No daily samples yet"
                  description="The first successful database measurement starts the durable history. Growth is never inferred from an absent baseline."
                />
              )}
            </section>
            {data.database.measured_at ? (
              <HealthThresholds key={JSON.stringify(data.settings)} settings={data.settings} />
            ) : (
              <p className="text-sm text-muted-foreground">
                Diagnostic settings become available after the first successful database
                measurement.
              </p>
            )}
          </>
        )
      )}
    </div>
  )
}

function Diagnostic({ item }: { item: HealthDiagnostic }) {
  const date = useDate()
  return (
    <article className="min-w-0 rounded-md border border-border p-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h3 className="text-sm font-medium">{diagnosticLabels[item.code]}</h3>
        <Badge variant="outline">{item.failure_code ?? item.code.replaceAll('_', ' ')}</Badge>
      </div>
      <p className="mt-2 text-sm leading-6 text-muted-foreground">{item.reason}</p>
      <dl className="mt-3 grid gap-x-6 gap-y-2 text-xs sm:grid-cols-2 lg:grid-cols-3">
        <div>
          <dt className="text-muted-foreground">Since</dt>
          <dd className="mt-1">{date(item.since)}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground">Observed</dt>
          <dd className="mt-1">{date(item.observed_at)}</dd>
        </div>
        {item.code === 'source_stale' && (
          <div>
            <dt className="text-muted-foreground">Last successful publication run</dt>
            <dd className="mt-1">
              {item.last_success_at ? date(item.last_success_at) : 'Never completed'}
            </dd>
          </div>
        )}
        {item.last_progress_at && (
          <div>
            <dt className="text-muted-foreground">Last progress</dt>
            <dd className="mt-1">{date(item.last_progress_at)}</dd>
          </div>
        )}
        {item.measured_bytes !== undefined && (
          <div>
            <dt className="text-muted-foreground">
              {item.code === 'journal_growth' ? 'Measured growth per day' : 'Available bytes'}
            </dt>
            <dd className="mt-1 font-mono">{bytes(item.measured_bytes)}</dd>
          </div>
        )}
        {item.threshold_bytes !== undefined && (
          <div>
            <dt className="text-muted-foreground">Byte threshold</dt>
            <dd className="mt-1 font-mono">
              {bytes(item.threshold_bytes)}
              {item.code === 'journal_growth' ? ' / day' : ' (or configured percentage)'}
            </dd>
          </div>
        )}
        {!!item.threshold_seconds && (
          <div>
            <dt className="text-muted-foreground">Time threshold</dt>
            <dd className="mt-1">{number(item.threshold_seconds / 60)} minutes</dd>
          </div>
        )}
      </dl>
      {(item.provider_id || item.run_id) && (
        <div className="mt-4 flex flex-wrap gap-x-5 gap-y-2 text-xs">
          {item.provider_id && (
            <Link
              className="min-w-0 break-all text-primary underline-offset-4 hover:underline"
              to={`/providers/${encodeURIComponent(item.provider_id)}`}
            >
              Source: {item.provider_id}
            </Link>
          )}
          {item.run_id && (
            <Link
              className="min-w-0 break-all text-primary underline-offset-4 hover:underline"
              to={`/runs/${encodeURIComponent(item.run_id)}`}
            >
              Run: {item.run_id}
            </Link>
          )}
        </div>
      )}
    </article>
  )
}

const thresholdFields: Array<{
  key: keyof HealthSettings
  label: string
  min: number
  max: number
  help: string
}> = [
  {
    key: 'stale_hours',
    label: 'Source freshness (hours)',
    min: 1,
    max: 2160,
    help: 'Warn after this interval without a successful non-preview run. Never-successful enabled sources are always listed.',
  },
  {
    key: 'stuck_minutes',
    label: 'No run progress (minutes)',
    min: 5,
    max: 10080,
    help: 'Warn on queued or running collections without a committed page for this long.',
  },
  {
    key: 'min_free_bytes',
    label: 'Minimum local available bytes',
    min: 0,
    max: 1125899906842624,
    help: 'Local state-directory filesystem only. Set to 0 to disable this byte threshold.',
  },
  {
    key: 'min_free_percent',
    label: 'Minimum local available percent',
    min: 0,
    max: 95,
    help: 'Warn if either the byte or percentage threshold is crossed. Set to 0 to disable this threshold.',
  },
  {
    key: 'journal_growth_bytes_per_day',
    label: 'Journal growth bytes per day',
    min: 0,
    max: 1125899906842624,
    help: 'Catalogue and semantic history combined, normalized by elapsed days. Set to 0 to disable growth warnings.',
  },
]

function HealthThresholds({ settings }: { settings: HealthSettings }) {
  const client = useQueryClient()
  const [values, setValues] = useState<Record<keyof HealthSettings, string>>({
    stale_hours: String(settings.stale_hours),
    stuck_minutes: String(settings.stuck_minutes),
    min_free_bytes: String(settings.min_free_bytes),
    min_free_percent: String(settings.min_free_percent),
    journal_growth_bytes_per_day: String(settings.journal_growth_bytes_per_day),
  })
  const [validation, setValidation] = useState<string>()
  const save = useMutation({
    mutationFn: (input: HealthSettings) =>
      api<HealthSettings>('/system-health/settings', {
        method: 'PUT',
        body: JSON.stringify(input),
      }),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['system-health'] })
      void client.invalidateQueries({ queryKey: ['health-settings'] })
    },
  })
  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const input = {} as HealthSettings
    for (const field of thresholdFields) {
      const raw = values[field.key]
      const value = Number(raw)
      if (
        !/^\d+$/.test(raw) ||
        !Number.isSafeInteger(value) ||
        value < field.min ||
        value > field.max
      ) {
        setValidation(
          `${field.label} must be a whole number between ${field.min} and ${field.max}.`,
        )
        return
      }
      input[field.key] = value
    }
    setValidation(undefined)
    save.mutate(input)
  }
  return (
    <section className="space-y-4 border-t border-border pt-6" aria-labelledby="health-thresholds">
      <div>
        <h2 id="health-thresholds" className="section-title">
          Diagnostic thresholds
        </h2>
        <p className="mt-1.5 text-sm text-muted-foreground">
          Shared across server replicas. Changes trigger a fresh measurement; they do not alter
          ingestion or retention.
        </p>
      </div>
      <form onSubmit={submit} className="space-y-5">
        <div className="grid gap-5 md:grid-cols-2">
          {thresholdFields.map((field) => (
            <div key={field.key} className="space-y-2">
              <label htmlFor={`health-${field.key}`} className="text-sm font-medium">
                {field.label}
              </label>
              <Input
                id={`health-${field.key}`}
                name={field.key}
                type="number"
                min={field.min}
                max={field.max}
                step={1}
                required
                value={values[field.key]}
                disabled={save.isPending}
                aria-describedby={`health-${field.key}-help`}
                onChange={(event) => {
                  setValues((current) => ({ ...current, [field.key]: event.target.value }))
                  setValidation(undefined)
                  save.reset()
                }}
              />
              <p
                id={`health-${field.key}-help`}
                className="text-xs leading-5 text-muted-foreground"
              >
                {field.help}
              </p>
            </div>
          ))}
        </div>
        {validation && (
          <p role="alert" className="text-sm text-destructive">
            {validation}
          </p>
        )}
        <ErrorNotice error={save.error} />
        <div className="flex flex-wrap items-center gap-4">
          <Button type="submit" disabled={save.isPending}>
            {save.isPending ? 'Saving…' : 'Save thresholds'}
          </Button>
          {save.isSuccess && (
            <p role="status" className="text-sm text-primary">
              Thresholds saved. Measurements are refreshing.
            </p>
          )}
        </div>
      </form>
    </section>
  )
}
