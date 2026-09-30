import { useRef, useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Link, useNavigate } from 'react-router-dom'
import { ArrowClockwiseIcon, GlobeIcon, PlusIcon } from '@phosphor-icons/react'
import { api, ApiError, queryClient, refreshData } from '@/lib/api'
import type { RemoteCatalog } from '@/lib/catalog-types'
import type { Items, ProviderDocument, Run, SecretInfo, Validation } from '@/lib/types'
import { mergeRemoteDraft, remoteDraft, remoteJSON, validateRemoteURL } from '@/lib/remote-config'
import type { RemoteDraft, RemoteTiming } from '@/lib/remote-config'
import { useDate } from '@/lib/format'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Badge } from '@/components/ui/badge'
import { Switch } from '@/components/ui/switch'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'
import {
  ConfirmAction,
  Empty,
  ErrorNotice,
  Loading,
  PageHeader,
  RunStatusBadge,
} from '@/components/common'

function scheduleLabel(remote: RemoteCatalog) {
  const schedule = remote.schedule
  if (!schedule?.every && !schedule?.cron) return 'Manual only'
  const timing = schedule.cron
    ? `${schedule.cron} (${schedule.timezone || 'UTC'})`
    : `Every ${schedule.every}`
  return `${timing} · ${schedule.mode || 'incremental'}${schedule.enabled === false ? ' · schedule disabled' : ''}`
}

function safeConfigurationError(error: unknown) {
  if (!(error instanceof ApiError))
    return new Error(
      'The remote configuration could not be saved. Your draft is preserved; check the settings and try again.',
    )
  if (error.status === 409)
    return new ApiError(409, 'The source changed on the server. Your draft has been preserved.')
  if (error.status === 422 || error.status === 400)
    return new ApiError(
      error.status,
      'The remote definition was rejected. Check its HTTPS URL, vault reference and schedule settings.',
    )
  if (error.status === 401 || error.status === 403)
    return new ApiError(error.status, 'Your security session needs to be refreshed before saving.')
  return new ApiError(
    error.status,
    'The server could not confirm the save. Your draft is preserved. Reload the current source before retrying.',
  )
}

export default function RemotesPage() {
  const date = useDate()
  const navigate = useNavigate()
  const returnFocus = useRef<HTMLElement | null>(null)
  const [editing, setEditing] = useState<RemoteCatalog | 'new' | null>(null)
  const [saving, setSaving] = useState(false)
  const [notice, setNotice] = useState('')
  const remotes = useQuery({
    queryKey: ['remotes'],
    queryFn: ({ signal }) => api<Items<RemoteCatalog>>('/remotes', { signal }),
  })
  function openEditor(remote: RemoteCatalog | 'new') {
    returnFocus.current =
      document.activeElement instanceof HTMLElement ? document.activeElement : null
    setEditing(remote)
  }
  async function sync(remote: RemoteCatalog, mode: 'incremental' | 'full') {
    const run = await api<Run>('/runs', {
      method: 'POST',
      body: JSON.stringify({ provider_id: remote.id, mode }),
    })
    await refreshData()
    navigate(`/runs/${encodeURIComponent(run.id)}`)
  }
  return (
    <div className="page">
      <PageHeader
        title="Remote catalogs"
        description="Scheduled, read-only copies from other instances. Published data and the last successful checkpoint change only after a complete sync."
        actions={
          <Button onClick={() => openEditor('new')}>
            <PlusIcon />
            Add remote catalog
          </Button>
        }
      />
      <div className="notice flex items-start gap-3">
        <GlobeIcon className="mt-1 shrink-0 text-primary" />
        <p>
          Remote catalogs use the generic HTTP/JSON adapter. HTTPS certificates are verified,
          redirects are not followed, and passwords stay in the encrypted vault. The owner controls
          which providers and fields you receive; imported values and original provenance are not
          editable.
        </p>
      </div>
      {notice && (
        <p role="status" className="notice text-primary">
          {notice}
        </p>
      )}
      <ErrorNotice error={remotes.error} retry={() => void remotes.refetch()} />
      {remotes.isPending ? (
        <Loading />
      ) : remotes.data?.items?.length ? (
        <div className="space-y-4">
          {remotes.data.items.map((remote) => {
            const active =
              remote.last_run?.status === 'queued' ||
              remote.last_run?.status === 'running' ||
              remote.last_run?.status === 'paused'
            return (
              <section
                key={remote.id}
                className="space-y-4 rounded-md border border-border p-5"
                aria-label={`Remote catalog ${remote.name || remote.id}`}
              >
                <div className="flex flex-wrap items-start justify-between gap-3">
                  <div className="min-w-0">
                    <h2 className="section-title break-words">{remote.name || remote.id}</h2>
                    <p className="mono mt-1 break-all text-muted-foreground">{remote.url}</p>
                  </div>
                  <div className="flex gap-2">
                    <Badge variant="outline">
                      {remote.enabled ? 'Enabled' : 'Source disabled'}
                    </Badge>
                    {!remote.valid && <Badge variant="destructive">Invalid configuration</Badge>}
                  </div>
                </div>
                <dl className="grid gap-4 text-sm md:grid-cols-3">
                  <div>
                    <dt className="text-muted-foreground">Schedule</dt>
                    <dd className="mt-1 break-words">{scheduleLabel(remote)}</dd>
                    {!remote.enabled && (
                      <p className="help mt-1">Source activation is independent of its schedule.</p>
                    )}
                  </div>
                  <div>
                    <dt className="text-muted-foreground">Last successful sync</dt>
                    <dd className="mt-1">
                      {remote.last_synced_at ? date(remote.last_synced_at) : 'Never completed'}
                    </dd>
                    <p className="help mt-1">
                      {remote.has_checkpoint
                        ? 'Durable checkpoint saved. Next sync requests changes.'
                        : 'No checkpoint for this URL. Next sync starts a full snapshot.'}
                    </p>
                  </div>
                  <div>
                    <dt className="text-muted-foreground">Latest run</dt>
                    <dd className="mt-1">
                      {remote.last_run ? (
                        <Link
                          to={`/runs/${encodeURIComponent(remote.last_run.id)}`}
                          className="inline-flex items-center gap-2"
                        >
                          <RunStatusBadge run={remote.last_run} />
                          <span className="data-link">View run</span>
                        </Link>
                      ) : (
                        'No runs yet'
                      )}
                    </dd>
                    {remote.last_run && (
                      <p className="help mt-1">{date(remote.last_run.created_at)}</p>
                    )}
                  </div>
                </dl>
                {remote.last_run?.error && (
                  <p role="status" className="notice break-words text-destructive">
                    Latest run error: {remote.last_run.error}
                  </p>
                )}
                {remote.last_run?.status === 'paused' && (
                  <p className="help">
                    A paused run has unpublished progress. Resume or cancel it from run details
                    before starting another sync.
                  </p>
                )}
                <div className="toolbar">
                  <Button size="sm" variant="outline" onClick={() => openEditor(remote)}>
                    Edit remote
                  </Button>
                  <ConfirmAction
                    title={`Sync “${remote.name || remote.id}” now?`}
                    description="Connect to this HTTPS catalog using its vault credential. The first sync imports a full snapshot; later syncs request only changes. The current published copy remains unchanged until the run completes."
                    label="Start sync"
                    trigger={
                      <Button size="sm" disabled={!remote.valid || !remote.enabled || active}>
                        <ArrowClockwiseIcon />
                        Sync now
                      </Button>
                    }
                    action={() => sync(remote, 'incremental')}
                  />
                  <ConfirmAction
                    title={`Fully resync “${remote.name || remote.id}”?`}
                    description="Download a fresh authoritative snapshot instead of using the checkpoint. Only this remote’s local copy is replaced, atomically after success. Local sources are not modified."
                    label="Start full resync"
                    trigger={
                      <Button
                        size="sm"
                        variant="outline"
                        disabled={!remote.valid || !remote.enabled || active}
                      >
                        Full resync
                      </Button>
                    }
                    action={() => sync(remote, 'full')}
                  />
                  <Button size="sm" variant="ghost" asChild>
                    <Link to={`/runs?provider=${encodeURIComponent(remote.id)}`}>History</Link>
                  </Button>
                  <Button size="sm" variant="ghost" asChild>
                    <Link to={`/logs?provider=${encodeURIComponent(remote.id)}`}>Logs</Link>
                  </Button>
                  <Button size="sm" variant="ghost" asChild>
                    <Link to={`/torrents?provider=${encodeURIComponent(remote.id)}`}>
                      View imported data
                    </Link>
                  </Button>
                </div>
              </section>
            )
          })}
        </div>
      ) : (
        !remotes.isError && (
          <Empty
            title="No remote catalogs"
            description="Enter a shared HTTPS catalog URL and its password or an existing vault reference. New imports default to daily incremental synchronization."
            action={
              <Button onClick={() => openEditor('new')}>
                <PlusIcon />
                Add remote catalog
              </Button>
            }
          />
        )
      )}
      <Dialog
        open={editing !== null}
        onOpenChange={(open) => {
          if (!open && !saving) setEditing(null)
        }}
      >
        <DialogContent
          className="sm:max-w-2xl"
          showCloseButton={!saving}
          onCloseAutoFocus={(event) => {
            event.preventDefault()
            returnFocus.current?.focus()
          }}
        >
          <DialogHeader>
            <DialogTitle>
              {editing === 'new' ? 'Add remote catalog' : 'Edit remote catalog'}
            </DialogTitle>
            <DialogDescription>
              Change local connection and scheduling metadata, not imported torrent values.
              Unrelated source settings and exact JSON values are preserved.
            </DialogDescription>
          </DialogHeader>
          {editing && (
            <RemoteDocument
              key={editing === 'new' ? 'new' : editing.id}
              remote={editing === 'new' ? undefined : editing}
              onSaving={setSaving}
              onSaved={() => {
                setEditing(null)
                setNotice(
                  'Remote settings saved. No manual sync was started; enabled schedules run at their next due time.',
                )
              }}
            />
          )}
        </DialogContent>
      </Dialog>
    </div>
  )
}

function RemoteDocument({
  remote,
  onSaving,
  onSaved,
}: {
  remote?: RemoteCatalog
  onSaving: (value: boolean) => void
  onSaved: () => void
}) {
  const document = useQuery({
    queryKey: ['remote-document', remote?.id],
    queryFn: ({ signal }) =>
      api<ProviderDocument>(`/providers/${encodeURIComponent(remote!.id)}`, { signal }),
    enabled: Boolean(remote),
    staleTime: 0,
    gcTime: 0,
    refetchOnWindowFocus: false,
  })
  if (remote && document.isPending) return <Loading label="Loading remote definition" />
  if (remote && !document.data)
    return <ErrorNotice error={document.error} retry={() => void document.refetch()} />
  return <RemoteForm document={document.data} onSaving={onSaving} onSaved={onSaved} />
}

function RemoteForm({
  document,
  onSaving,
  onSaved,
}: {
  document?: ProviderDocument
  onSaving: (value: boolean) => void
  onSaved: () => void
}) {
  const [original, setOriginal] = useState(document)
  const [draft, setDraft] = useState(() => remoteDraft(document))
  const [credentialMode, setCredentialMode] = useState<'password' | 'reference'>(
    document ? 'reference' : 'password',
  )
  const [password, setPassword] = useState('')
  const [reloaded, setReloaded] = useState(false)
  const [storedReference, setStoredReference] = useState('')
  const [localError, setLocalError] = useState<Error | null>(null)
  const secrets = useQuery({
    queryKey: ['secrets'],
    queryFn: ({ signal }) => api<Items<SecretInfo>>('/secrets', { signal }),
  })
  function change<K extends keyof RemoteDraft>(key: K, value: RemoteDraft[K]) {
    setDraft((current) => ({ ...current, [key]: value }))
  }
  const save = useMutation({
    gcTime: 0,
    mutationFn: async () => {
      let reference = draft.secretRef.trim()
      if (credentialMode === 'password') {
        reference = `remote-${Array.from(crypto.getRandomValues(new Uint8Array(32)), (byte) => byte.toString(16).padStart(2, '0')).join('')}`
      }
      const json = remoteJSON(original, draft, reference)
      let validation: Validation
      try {
        validation = await api<Validation>('/providers/validate', {
          method: 'POST',
          body: JSON.stringify({ json }),
        })
      } catch (error) {
        throw safeConfigurationError(error)
      }
      if (!validation.valid)
        throw new ApiError(
          422,
          'The remote definition is invalid. Review the settings before saving.',
          validation.issues ?? [],
        )
      if (credentialMode === 'password') {
        try {
          await api<void>(`/secrets/${encodeURIComponent(reference)}`, {
            method: 'PUT',
            body: JSON.stringify({ value: password }),
          })
        } catch {
          throw new Error(
            'The password could not be saved in the vault. Its value has not been included in this error. Try again or select an existing reference.',
          )
        }
        setPassword('')
        setStoredReference(reference)
        setCredentialMode('reference')
        change('secretRef', reference)
        void queryClient.invalidateQueries({ queryKey: ['secrets'] })
      }
      let result: ProviderDocument
      try {
        result = await api<ProviderDocument>(
          `/providers/${encodeURIComponent(original?.provider.id ?? draft.id.trim())}`,
          { method: 'PUT', body: JSON.stringify({ json, revision: original?.revision ?? '' }) },
        )
      } catch (error) {
        throw safeConfigurationError(error)
      }
      setOriginal(result)
      setStoredReference('')
      await refreshData()
      onSaved()
    },
    onMutate: () => onSaving(true),
    onSettled: () => onSaving(false),
  })
  const reload = useMutation({
    mutationFn: () =>
      api<ProviderDocument>(
        `/providers/${encodeURIComponent(original?.provider.id ?? draft.id.trim())}`,
      ),
    onSuccess: (latest) => {
      setDraft((current) => mergeRemoteDraft(current, remoteDraft(original), remoteDraft(latest)))
      setOriginal(latest)
      setReloaded(true)
      save.reset()
    },
  })
  const conflict = save.error instanceof ApiError && save.error.status === 409
  const busy = save.isPending || reload.isPending
  const knownReferences = secrets.data?.items ?? []
  return (
    <form
      className="space-y-5"
      autoComplete="off"
      onSubmit={(event) => {
        event.preventDefault()
        if (busy || conflict) return
        setLocalError(null)
        try {
          validateRemoteURL(draft.url.trim())
          if (credentialMode === 'password' ? !password : !draft.secretRef.trim())
            throw new Error('Enter a sharing password or select a vault reference.')
          save.mutate()
        } catch (error) {
          setLocalError(error instanceof Error ? error : new Error('Review the remote settings.'))
        }
      }}
    >
      <fieldset className="space-y-5" disabled={busy}>
        <div className="grid items-start gap-4 sm:grid-cols-2">
          <div className="field">
            <Label htmlFor="remote-id">Local source identifier</Label>
            <Input
              id="remote-id"
              required
              pattern="[A-Za-z0-9][A-Za-z0-9_-]*"
              maxLength={128}
              readOnly={Boolean(original)}
              value={draft.id}
              onChange={(event) => change('id', event.target.value)}
            />
            <p className="help">Unique locally. Imported data stays in this source’s namespace.</p>
          </div>
          <div className="field">
            <Label htmlFor="remote-name">Display name</Label>
            <Input
              id="remote-name"
              required
              maxLength={200}
              value={draft.name}
              onChange={(event) => change('name', event.target.value)}
            />
          </div>
        </div>
        <div className="field">
          <Label htmlFor="remote-url">HTTPS catalog URL</Label>
          <Input
            id="remote-url"
            type="url"
            required
            value={draft.url}
            onChange={(event) => change('url', event.target.value)}
            placeholder="https://catalog.example/api/catalogs/share-id"
            autoComplete="off"
            spellCheck={false}
            aria-describedby="remote-url-help"
          />
          <p id="remote-url-help" className="help">
            Use the owner’s catalog URL without query parameters, fragments or credentials.
            Certificates must be trusted; there is no insecure TLS option. Private LAN HTTPS is
            supported. No remote request is made while editing or validating.
          </p>
          {original && draft.url.trim() !== original.provider.url && (
            <p className="help">
              Changing the URL starts a fresh snapshot on the next sync. The current copy remains
              until that sync succeeds.
            </p>
          )}
        </div>
        <fieldset className="space-y-3">
          <legend className="mb-2 text-sm font-medium">Sharing credential</legend>
          <div className="flex flex-wrap gap-4">
            <label className="flex items-center gap-2">
              <input
                type="radio"
                name="remote-credential-mode"
                checked={credentialMode === 'password'}
                onChange={() => setCredentialMode('password')}
                className="accent-primary"
              />
              New password
            </label>
            <label className="flex items-center gap-2">
              <input
                type="radio"
                name="remote-credential-mode"
                checked={credentialMode === 'reference'}
                onChange={() => {
                  setCredentialMode('reference')
                  setPassword('')
                }}
                className="accent-primary"
              />
              Existing vault reference
            </label>
          </div>
          {credentialMode === 'password' ? (
            <div className="field">
              <Label htmlFor="remote-password">Sharing password</Label>
              <Input
                id="remote-password"
                type="password"
                required
                autoComplete="new-password"
                spellCheck={false}
                value={password}
                onChange={(event) => setPassword(event.target.value)}
              />
              <p className="help">
                Stored in a new, uniquely named encrypted vault entry. Existing shared secrets are
                never overwritten. Only its reference enters the source JSON.
              </p>
            </div>
          ) : (
            <div className="field">
              <Label htmlFor="remote-secret">Vault reference</Label>
              <select
                id="remote-secret"
                required
                className="native-select w-full"
                value={draft.secretRef}
                onChange={(event) => change('secretRef', event.target.value)}
              >
                <option value="">Choose a reference</option>
                {draft.secretRef &&
                  !knownReferences.some((secret) => secret.name === draft.secretRef) && (
                    <option value={draft.secretRef}>{draft.secretRef}</option>
                  )}
                {knownReferences.map((secret) => (
                  <option key={secret.name} value={secret.name}>
                    {secret.name}
                  </option>
                ))}
              </select>
              <p className="help">
                Uses the existing encrypted value without changing it. The server never returns that
                value.
              </p>
              <ErrorNotice error={secrets.error} retry={() => void secrets.refetch()} />
            </div>
          )}
        </fieldset>
        <div className="flex items-start justify-between gap-4 rounded-md border border-border p-3">
          <div>
            <Label htmlFor="remote-enabled">Remote source enabled</Label>
            <p className="help mt-1">
              Controls source activation independently of the schedule. Disabling does not cancel an
              existing run.
            </p>
          </div>
          <Switch
            id="remote-enabled"
            checked={draft.enabled}
            onCheckedChange={(value) => change('enabled', value)}
          />
        </div>
        <div className="field">
          <Label htmlFor="remote-timing">Synchronization frequency</Label>
          <select
            id="remote-timing"
            className="native-select w-full"
            value={draft.timing}
            onChange={(event) => change('timing', event.target.value as RemoteTiming)}
          >
            <option value="daily">Daily (every 24 hours)</option>
            <option value="hourly">Hourly</option>
            <option value="manual">Manual only</option>
            <option value="every">Custom interval</option>
            <option value="cron">Custom cron expression</option>
          </select>
        </div>
        {draft.timing === 'every' && (
          <div className="field">
            <Label htmlFor="remote-every">Interval</Label>
            <Input
              id="remote-every"
              required
              value={draft.every}
              onChange={(event) => change('every', event.target.value)}
              placeholder="6h"
            />
            <p className="help">Go duration of at least 1 minute, such as 30m, 6h or 1h30m.</p>
          </div>
        )}
        {draft.timing === 'cron' && (
          <div className="grid items-start gap-4 sm:grid-cols-2">
            <div className="field">
              <Label htmlFor="remote-cron">Cron expression</Label>
              <Input
                id="remote-cron"
                required
                pattern="\s*\S+\s+\S+\s+\S+\s+\S+\s+\S+\s*"
                title="Enter five fields: minute hour day-of-month month day-of-week"
                value={draft.cron}
                onChange={(event) => change('cron', event.target.value)}
                placeholder="0 3 * * *"
              />
              <p className="help">Five fields, no seconds. Uses the next matching time.</p>
            </div>
            <div className="field">
              <Label htmlFor="remote-timezone">Timezone</Label>
              <Input
                id="remote-timezone"
                required
                value={draft.timezone}
                onChange={(event) => change('timezone', event.target.value)}
                placeholder="UTC"
              />
              <p className="help">
                IANA timezone, such as Europe/Paris. Follows daylight-saving changes.
              </p>
            </div>
          </div>
        )}
        {draft.timing !== 'manual' && (
          <>
            <div className="flex items-start justify-between gap-4 rounded-md border border-border p-3">
              <div>
                <Label htmlFor="remote-schedule-enabled">Schedule enabled</Label>
                <p className="help mt-1">
                  Pause automatic sync without disabling the source or removing its frequency.
                </p>
              </div>
              <Switch
                id="remote-schedule-enabled"
                checked={draft.scheduleEnabled}
                onCheckedChange={(value) => change('scheduleEnabled', value)}
              />
            </div>
            <div className="grid items-start gap-4 sm:grid-cols-2">
              <div className="field">
                <Label htmlFor="remote-mode">Scheduled sync mode</Label>
                <select
                  id="remote-mode"
                  className="native-select w-full"
                  value={draft.mode}
                  onChange={(event) => change('mode', event.target.value as RemoteDraft['mode'])}
                >
                  <option value="incremental">Incremental (recommended)</option>
                  <option value="full">Full snapshot</option>
                </select>
              </div>
              <div className="field">
                <Label htmlFor="remote-budget">Page budget per tick</Label>
                <Input
                  id="remote-budget"
                  type="number"
                  min={0}
                  max={10000}
                  step={1}
                  required
                  value={draft.maxPages}
                  onChange={(event) => change('maxPages', event.target.value)}
                />
                <p className="help">0 = unlimited. Paused progress resumes on the next tick.</p>
              </div>
            </div>
          </>
        )}
      </fieldset>
      <p className="notice">
        {draft.timing === 'manual' || !draft.scheduleEnabled || !draft.enabled
          ? 'No automatic sync will start with these settings. Use Sync now when the source is enabled.'
          : `Saving authorizes automatic ${draft.mode} synchronization ${draft.timing === 'cron' ? 'at the next matching cron time' : `after ${draft.timing === 'daily' ? '24h' : draft.timing === 'hourly' ? '1h' : draft.every || 'the configured interval'}`}. No immediate manual run is started.`}{' '}
        Failed or incomplete runs leave the published copy and checkpoint unchanged.
      </p>
      {original && (
        <p className="help">
          Editing a source with a paused scheduled run can block automatic continuation. Resume or
          cancel the old run from history. Imported fields cannot be changed in this form.
        </p>
      )}
      <ErrorNotice error={localError} />
      <ErrorNotice error={save.error} />
      <ErrorNotice error={reload.error} />
      {storedReference && save.isError && (
        <p className="notice">
          The password was stored as <code className="mono break-all">{storedReference}</code>. This
          draft now uses that reference so retrying will not rotate any secret. If you abandon the
          draft, remove this entry in Secrets only after confirming it is unused.
        </p>
      )}
      {conflict && (
        <div className="notice space-y-3">
          <p>
            Your draft is preserved. Reload the current source to retain its comments and unrelated
            settings. Only your changed form values will be reapplied; review them before saving.
          </p>
          <Button type="button" variant="outline" disabled={busy} onClick={() => reload.mutate()}>
            Reload source, keep edits
          </Button>
        </div>
      )}
      {reloaded && !conflict && (
        <p role="status" className="notice">
          Latest source loaded. Your changed fields are preserved; untouched fields reflect the
          server. Review before saving again.
        </p>
      )}
      <DialogFooter>
        <Button type="submit" disabled={busy || conflict || !draft.name.trim() || !draft.id.trim()}>
          {save.isPending ? 'Saving…' : original ? 'Save remote' : 'Create remote catalog'}
        </Button>
      </DialogFooter>
    </form>
  )
}
