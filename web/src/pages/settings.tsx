import { useState } from 'react'
import type { FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowClockwiseIcon } from '@phosphor-icons/react'
import { api, ApiError } from '@/lib/api'
import type { CollectionOverview, CollectionSettings } from '@/lib/collection-types'
import { ErrorNotice, Loading, PageHeader } from '@/components/common'
import { DisplayPreferencesPanel } from '@/components/display-preferences'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

const numberFields = {
  workers: {
    label: 'Simultaneous collections',
    min: 1,
    max: 32,
    help: 'Shared maximum across this database. Per-source request pacing is unchanged.',
  },
  max_quota_retries: {
    label: 'Automatic retries after HTTP 429',
    min: 0,
    max: 10,
    help: '0 = no automatic retry. Server quota deadlines are always respected.',
  },
  max_quota_wait_seconds: {
    label: 'Maximum automatic quota wait (seconds)',
    min: 0,
    max: 86400,
    help: '0 = unlimited. A longer server-requested wait pauses the run after saving its status and deadline, not its response body. Explicit Resume accepts that wait without shortening the server deadline.',
  },
  no_progress_requests: {
    label: 'Responses without new source IDs',
    min: 0,
    max: 100000,
    help: '0 = disabled. Counts successful data responses without a previously unseen native ID, not quota errors or auxiliary/control responses.',
  },
  default_request_timeout_seconds: {
    label: 'Request timeout (seconds)',
    min: 1,
    max: 900,
    help: 'Used only when the source does not explicitly configure its own request timeout.',
  },
  default_preview_pages: {
    label: 'Preview page budget',
    min: 1,
    max: 10000,
    help: 'Default number of pages for a new Preview run. A per-run page override takes precedence.',
  },
  default_max_pages: {
    label: 'Full / Incremental / Metadata page budget per attempt',
    min: 0,
    max: 10000,
    help: '0 = unlimited. Manual runs inherit this budget; scheduled runs keep their explicit schedule page budget.',
  },
  default_max_duration_seconds: {
    label: 'Duration budget per attempt (seconds)',
    min: 0,
    max: 604800,
    help: '0 = unlimited. Pauses at safe response boundaries or during cooldown waits, without interrupting atomic Full publication. A per-run override takes precedence.',
  },
} as const

type NumberField = keyof typeof numberFields
type FormValues = Record<NumberField, string> & {
  auto_resume_interrupted: boolean
  no_progress_action: CollectionSettings['no_progress_action']
}
type Draft = { values: FormValues; base: CollectionSettings }
const numberKeys = Object.keys(numberFields) as NumberField[]

function formValues(settings: CollectionSettings): FormValues {
  return {
    workers: String(settings.workers),
    max_quota_retries: String(settings.max_quota_retries),
    max_quota_wait_seconds: String(settings.max_quota_wait_seconds),
    no_progress_requests: String(settings.no_progress_requests),
    default_request_timeout_seconds: String(settings.default_request_timeout_seconds),
    default_preview_pages: String(settings.default_preview_pages),
    default_max_pages: String(settings.default_max_pages),
    default_max_duration_seconds: String(settings.default_max_duration_seconds),
    auto_resume_interrupted: settings.auto_resume_interrupted,
    no_progress_action: settings.no_progress_action,
  }
}

export function SettingsPage() {
  const client = useQueryClient()
  const overview = useQuery({
    queryKey: ['collection-settings'],
    queryFn: ({ signal }) => api<CollectionOverview>('/settings/collections', { signal }),
  })
  const [draft, setDraft] = useState<Draft | null>(null)
  const save = useMutation({
    mutationFn: (settings: CollectionSettings) =>
      api<CollectionOverview>('/settings/collections', {
        method: 'PUT',
        body: JSON.stringify(settings),
      }),
    onMutate: () => client.cancelQueries({ queryKey: ['collection-settings'] }),
    onSuccess: async (result) => {
      await client.cancelQueries({ queryKey: ['collection-settings'] })
      client.setQueryData<CollectionOverview>(['collection-settings'], (current) =>
        current && current.settings.revision > result.settings.revision ? current : result,
      )
      setDraft(null)
      void client.invalidateQueries({ queryKey: ['collection-settings'] })
    },
    onError: (error) => {
      if (error instanceof ApiError && error.status === 409) {
        void client.invalidateQueries({ queryKey: ['collection-settings'] })
      }
    },
  })
  const data = overview.data
  const values = draft?.values ?? (data ? formValues(data.settings) : undefined)
  const dirty =
    draft !== null &&
    (numberKeys.some((key) => draft.values[key] !== String(draft.base[key])) ||
      draft.values.auto_resume_interrupted !== draft.base.auto_resume_interrupted ||
      draft.values.no_progress_action !== draft.base.no_progress_action)
  const rejected = save.error instanceof ApiError && save.error.status === 409
  const conflict =
    draft !== null && (rejected || (data && data.settings.revision !== draft.base.revision))
  const latestAvailable =
    draft !== null && data !== undefined && data.settings.revision > draft.base.revision
  const errors: Partial<Record<NumberField, string>> = {}
  if (values) {
    for (const key of numberKeys) {
      const field = numberFields[key]
      const value = Number(values[key])
      if (
        !/^\d+$/.test(values[key]) ||
        !Number.isSafeInteger(value) ||
        value < field.min ||
        value > field.max
      ) {
        errors[key] = `Enter a whole number from ${field.min} to ${field.max}.`
      }
    }
  }
  const invalid = Object.keys(errors).length > 0

  function change(patch: Partial<FormValues>) {
    if (!data || !values) return
    setDraft({ values: { ...values, ...patch }, base: draft?.base ?? data.settings })
    if (!rejected) save.reset()
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!draft || !dirty || save.isPending || conflict || invalid) return
    const value = draft.values
    save.mutate({
      workers: Number(value.workers),
      max_quota_retries: Number(value.max_quota_retries),
      max_quota_wait_seconds: Number(value.max_quota_wait_seconds),
      auto_resume_interrupted: value.auto_resume_interrupted,
      no_progress_requests: Number(value.no_progress_requests),
      no_progress_action: value.no_progress_action,
      default_request_timeout_seconds: Number(value.default_request_timeout_seconds),
      default_preview_pages: Number(value.default_preview_pages),
      default_max_pages: Number(value.default_max_pages),
      default_max_duration_seconds: Number(value.default_max_duration_seconds),
      revision: draft.base.revision,
    })
  }

  function useSavedValues() {
    setDraft(null)
    save.reset()
  }

  function numberInput(key: NumberField) {
    const field = numberFields[key]
    const id = `collection-${key}`
    return (
      <div className="min-w-0 space-y-2" key={key}>
        <Label htmlFor={id}>{field.label}</Label>
        <Input
          id={id}
          name={key}
          type="number"
          min={field.min}
          max={field.max}
          step={1}
          required
          value={values?.[key] ?? ''}
          disabled={save.isPending}
          aria-invalid={Boolean(errors[key])}
          aria-describedby={`${id}-help${errors[key] ? ` ${id}-error` : ''}`}
          onChange={(event) => change({ [key]: event.target.value })}
        />
        <p id={`${id}-help`} className="text-xs leading-5 text-muted-foreground">
          {field.help} Range: {field.min}–{field.max}. Currently saved: {data?.settings[key]}.
        </p>
        {errors[key] && (
          <p id={`${id}-error`} role="alert" className="text-sm text-destructive">
            {errors[key]}
          </p>
        )}
      </div>
    )
  }

  return (
    <div className="page">
      <PageHeader
        title="Settings"
        description="Manage persistent collection settings shared by all instances using this database. Display preferences stay in this browser."
        actions={
          <Button
            variant="outline"
            onClick={() => void overview.refetch()}
            disabled={overview.isFetching || save.isPending}
          >
            <ArrowClockwiseIcon />
            Refresh
          </Button>
        }
      />
      <section
        className="space-y-5 rounded-md border border-border bg-card/30 p-5 sm:p-6"
        aria-labelledby="collection-settings-heading"
      >
        <div>
          <h2 id="collection-settings-heading" className="font-medium">
            Collection settings
          </h2>
          <p className="mt-1 max-w-2xl text-sm leading-6 text-muted-foreground">
            Concurrency applies immediately. Recovery is checked when interrupted work is recovered.
            All other defaults are captured for new collections only; existing configurations and
            checkpoints are unchanged.
          </p>
        </div>
        <ErrorNotice error={overview.error} retry={() => void overview.refetch()} />
        {overview.isPending ? (
          <Loading label="Loading collection settings" />
        ) : (
          data &&
          values && (
            <>
              <dl className="grid gap-4 sm:grid-cols-3">
                <div>
                  <dt className="text-xs text-muted-foreground">Running across database</dt>
                  <dd className="mt-1 text-xl font-medium tabular-nums">{data.running}</dd>
                </div>
                <div>
                  <dt className="text-xs text-muted-foreground">Queued across database</dt>
                  <dd className="mt-1 text-xl font-medium tabular-nums">{data.queued}</dd>
                </div>
                <div>
                  <dt className="text-xs text-muted-foreground">Local DB capacity</dt>
                  <dd className="mt-1 text-xl font-medium tabular-nums">{data.capacity}</dd>
                </div>
              </dl>
              <p className="text-sm leading-6 text-muted-foreground">
                This instance can claim at most {data.capacity} simultaneous collections through its
                database slots, subject to the shared limit. Other instances sharing this database
                may provide additional capacity.
              </p>
              {data.settings.workers > data.capacity && (
                <p className="notice" role="status">
                  The saved limit of {data.settings.workers} exceeds this instance’s capacity of{' '}
                  {data.capacity}. This instance cannot fill the shared limit on its own.
                </p>
              )}
              <form onSubmit={submit} className="space-y-6" noValidate>
                <fieldset className="min-w-0 space-y-4 border-t border-border pt-5">
                  <legend className="pr-3 text-sm font-medium">Concurrency · Immediate</legend>
                  <div className="max-w-sm">{numberInput('workers')}</div>
                  {dirty && Number(values.workers) > data.capacity && (
                    <p className="notice">
                      Your proposed limit exceeds the local capacity of {data.capacity}. Saving is
                      allowed, but this instance alone cannot use every configured slot.
                    </p>
                  )}
                  <p className="max-w-2xl text-sm leading-6 text-muted-foreground">
                    Increasing the limit lets queued collections start when capacity is available.
                    Decreasing it does not cancel active collections; new work waits until the
                    running count falls below the new limit.
                  </p>
                </fieldset>
                <fieldset className="min-w-0 space-y-3 border-t border-border pt-5">
                  <legend className="pr-3 text-sm font-medium">
                    Interrupted runs · On recovery
                  </legend>
                  <div className="flex items-start gap-3">
                    <input
                      id="collection-auto-resume"
                      name="auto_resume_interrupted"
                      type="checkbox"
                      className="mt-1 size-4 shrink-0 accent-primary"
                      checked={values.auto_resume_interrupted}
                      disabled={save.isPending}
                      aria-describedby="collection-auto-resume-help"
                      onChange={(event) =>
                        change({ auto_resume_interrupted: event.target.checked })
                      }
                    />
                    <Label htmlFor="collection-auto-resume" className="leading-6">
                      Automatically resume technically interrupted runs
                    </Label>
                  </div>
                  <p
                    id="collection-auto-resume-help"
                    className="max-w-2xl text-xs leading-5 text-muted-foreground"
                  >
                    Recover work interrupted by a process or connection failure from its saved
                    checkpoint. Manual, quota and no-progress pauses, cancellations, and
                    authentication, certificate or configuration failures are not resumed
                    automatically. Restoring a backup disables automatic recovery.
                  </p>
                </fieldset>
                <fieldset className="min-w-0 space-y-4 border-t border-border pt-5">
                  <legend className="pr-3 text-sm font-medium">
                    Quota handling · New collections
                  </legend>
                  <div className="grid items-start gap-5 md:grid-cols-2">
                    {numberInput('max_quota_retries')}
                    {numberInput('max_quota_wait_seconds')}
                  </div>
                </fieldset>
                <fieldset className="min-w-0 space-y-4 border-t border-border pt-5">
                  <legend className="pr-3 text-sm font-medium">
                    Useful progress · New collections
                  </legend>
                  <div className="grid items-start gap-5 md:grid-cols-2">
                    {numberInput('no_progress_requests')}
                    <div className="space-y-2">
                      <Label htmlFor="collection-no-progress-action">
                        At the no-progress threshold
                      </Label>
                      <select
                        id="collection-no-progress-action"
                        name="no_progress_action"
                        className="native-select w-full"
                        value={values.no_progress_action}
                        disabled={save.isPending}
                        aria-describedby="collection-no-progress-action-help"
                        onChange={(event) =>
                          change({
                            no_progress_action: event.target
                              .value as CollectionSettings['no_progress_action'],
                          })
                        }
                      >
                        <option value="warn">Warn and continue</option>
                        <option value="pause">Warn and pause</option>
                      </select>
                      <p
                        id="collection-no-progress-action-help"
                        className="text-xs leading-5 text-muted-foreground"
                      >
                        Send one warning per streak. New source IDs reset the streak. A pause
                        requires manual resume, which grants a fresh allowance without forgetting
                        seen IDs. A completed traversal is not paused. This action has no effect
                        when the threshold is 0.
                      </p>
                    </div>
                  </div>
                </fieldset>
                <fieldset className="min-w-0 space-y-4 border-t border-border pt-5">
                  <legend className="pr-3 text-sm font-medium">
                    Run defaults · New collections
                  </legend>
                  <div className="grid items-start gap-5 md:grid-cols-2">
                    {numberInput('default_request_timeout_seconds')}
                    {numberInput('default_preview_pages')}
                    {numberInput('default_max_pages')}
                    {numberInput('default_max_duration_seconds')}
                  </div>
                </fieldset>
                {conflict && (
                  <div className="notice space-y-3" role="alert">
                    <p>
                      These settings changed while you were editing. Your unsaved draft is
                      preserved.
                      {latestAvailable
                        ? ' Choose whether to use the saved settings or keep your entire draft before saving again. Keeping your draft will replace all saved collection settings on the next save.'
                        : ' Refresh the saved settings before choosing how to continue.'}
                    </p>
                    <div className="toolbar">
                      {latestAvailable ? (
                        <>
                          <Button
                            type="button"
                            variant="outline"
                            disabled={save.isPending}
                            onClick={useSavedValues}
                          >
                            Use saved values
                          </Button>
                          <Button
                            type="button"
                            variant="outline"
                            disabled={save.isPending}
                            onClick={() => {
                              setDraft({ values, base: data.settings })
                              save.reset()
                            }}
                          >
                            Keep my draft
                          </Button>
                        </>
                      ) : (
                        <Button
                          type="button"
                          variant="outline"
                          disabled={overview.isFetching}
                          onClick={() => void overview.refetch()}
                        >
                          {overview.isFetching ? 'Refreshing…' : 'Refresh saved settings'}
                        </Button>
                      )}
                    </div>
                  </div>
                )}
                {!rejected && <ErrorNotice error={save.error} />}
                <div className="flex flex-wrap items-center gap-4 border-t border-border pt-5">
                  <Button
                    type="submit"
                    disabled={!dirty || invalid || save.isPending || Boolean(conflict)}
                  >
                    {save.isPending ? 'Saving…' : 'Save'}
                  </Button>
                  {draft && !conflict && (
                    <Button
                      type="button"
                      variant="outline"
                      onClick={useSavedValues}
                      disabled={save.isPending}
                    >
                      Reset
                    </Button>
                  )}
                  <p role="status" className="text-sm text-muted-foreground">
                    {save.isPending
                      ? 'Saving collection settings…'
                      : save.isSuccess
                        ? 'Collection settings saved. Each setting applies at the scope shown above.'
                        : invalid
                          ? 'Correct the highlighted values before saving.'
                          : dirty
                            ? 'Unsaved changes'
                            : 'No unsaved changes'}
                  </p>
                </div>
              </form>
            </>
          )
        )}
      </section>
      <DisplayPreferencesPanel />
    </div>
  )
}
