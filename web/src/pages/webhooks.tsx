import { useRef, useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import {
  ArrowClockwiseIcon,
  ClockCounterClockwiseIcon,
  PaperPlaneTiltIcon,
  PencilSimpleIcon,
  PlusIcon,
  TrashIcon,
} from '@phosphor-icons/react'
import { api, ApiError, params, queryClient } from '@/lib/api'
import { useDate } from '@/lib/format'
import { usePageOffset, usePageSize } from '@/lib/display-preferences'
import type { Items, Page, SecretInfo } from '@/lib/types'
import { webhookEvents } from '@/lib/activity-types'
import type { Delivery, DeliveryStatus, Webhook, WebhookInput } from '@/lib/activity-types'
import { ConfirmAction, Empty, ErrorNotice, Loading, PageHeader, Pager } from '@/components/common'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Dialog,
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

const hookPath = (id: string) => `/webhooks/${encodeURIComponent(id)}`
const refreshWebhooks = () => queryClient.invalidateQueries({ queryKey: ['webhooks'] })
const eventLabels: Record<(typeof webhookEvents)[number], string> = {
  'run.succeeded': 'Run succeeded',
  'run.failed': 'Run failed',
  'run.paused': 'Run paused',
  'run.cancelled': 'Run cancelled',
  'run.no_progress': 'Run without new source IDs',
  'schedule.failed': 'Schedule failed',
  'backup.succeeded': 'Backup completed',
  'backup.failed': 'Backup failed',
  'backup.restore_succeeded': 'Recovery verified',
  'backup.restore_failed': 'Recovery verification failed',
  'health.warning': 'System health warning',
  'health.recovered': 'System health recovered',
}

function WebhookEditor({
  initial,
  onSaved,
  onCancel,
  onBusyChange,
}: {
  initial: Webhook | null
  onSaved: () => void
  onCancel: () => void
  onBusyChange: (busy: boolean) => void
}) {
  const [draft, setDraft] = useState<WebhookInput>(() =>
    initial
      ? {
          name: initial.name,
          enabled: initial.enabled,
          url_secret_ref: initial.url_secret_ref,
          signing_secret_ref: initial.signing_secret_ref ?? '',
          events: [...initial.events],
        }
      : {
          name: '',
          enabled: false,
          url_secret_ref: '',
          signing_secret_ref: '',
          events: ['run.failed', 'schedule.failed'],
        },
  )
  const [revision, setRevision] = useState(initial?.revision)
  const secrets = useQuery({
    queryKey: ['secrets'],
    queryFn: ({ signal }) => api<Items<SecretInfo>>('/secrets', { signal }),
  })
  const save = useMutation({
    onMutate: () => onBusyChange(true),
    onSettled: () => onBusyChange(false),
    mutationFn: async () => {
      if (!draft.events.length) throw new Error('Choose at least one event subscription.')
      const body = {
        ...draft,
        name: draft.name.trim(),
        signing_secret_ref: draft.signing_secret_ref || undefined,
        ...(initial ? { revision } : {}),
      }
      await api<unknown>(initial ? hookPath(initial.id) : '/webhooks', {
        method: initial ? 'PUT' : 'POST',
        body: JSON.stringify(body),
      })
    },
    onSuccess: async () => {
      await refreshWebhooks()
      onSaved()
    },
  })
  const conflict = save.error instanceof ApiError && save.error.status === 409
  async function reload() {
    const latest = await api<Items<Webhook>>('/webhooks')
    const item = latest.items?.find((hook) => hook.id === initial?.id)
    if (!item)
      throw new Error('This webhook is no longer available. Close the editor and refresh the list.')
    setDraft({
      name: item.name,
      enabled: item.enabled,
      url_secret_ref: item.url_secret_ref,
      signing_secret_ref: item.signing_secret_ref ?? '',
      events: [...item.events],
    })
    setRevision(item.revision)
    save.reset()
    await refreshWebhooks()
  }
  return (
    <form
      className="space-y-5"
      onSubmit={(event) => {
        event.preventDefault()
        save.mutate()
      }}
    >
      <fieldset disabled={save.isPending} className="space-y-5">
        <div className="field">
          <Label htmlFor="webhook-name">Name</Label>
          <Input
            id="webhook-name"
            required
            maxLength={128}
            value={draft.name}
            onChange={(event) => setDraft({ ...draft, name: event.target.value })}
            autoComplete="off"
          />
        </div>
        <div className="notice">
          <p>
            First store the complete destination URL, including any token, as an encrypted secret.
            Select its reference below. This panel never retrieves secret values.
          </p>
          <Link className="data-link" to="/secrets">
            Manage secrets
          </Link>
        </div>
        <ErrorNotice error={secrets.error} retry={() => void secrets.refetch()} />
        {secrets.isPending && (
          <p className="help" role="status">
            Loading secret references…
          </p>
        )}
        {secrets.data && !secrets.data.items?.length && (
          <p className="help">
            The vault is empty. Create a destination URL secret before saving a webhook.
          </p>
        )}
        <div className="field">
          <Label htmlFor="webhook-url-ref">Destination URL secret</Label>
          <select
            id="webhook-url-ref"
            className="native-select"
            required
            value={draft.url_secret_ref}
            onChange={(event) => setDraft({ ...draft, url_secret_ref: event.target.value })}
          >
            <option value="">Select a secret reference</option>
            {draft.url_secret_ref &&
              !secrets.data?.items?.some((item) => item.name === draft.url_secret_ref) && (
                <option value={draft.url_secret_ref}>
                  {draft.url_secret_ref} (current reference)
                </option>
              )}
            {secrets.data?.items?.map((item) => (
              <option key={item.name} value={item.name}>
                {item.name}
              </option>
            ))}
          </select>
        </div>
        <div className="field">
          <Label htmlFor="webhook-signing-ref">Signing secret (optional)</Label>
          <select
            id="webhook-signing-ref"
            className="native-select"
            value={draft.signing_secret_ref ?? ''}
            onChange={(event) => setDraft({ ...draft, signing_secret_ref: event.target.value })}
          >
            <option value="">No signature</option>
            {draft.signing_secret_ref &&
              !secrets.data?.items?.some((item) => item.name === draft.signing_secret_ref) && (
                <option value={draft.signing_secret_ref}>
                  {draft.signing_secret_ref} (current reference)
                </option>
              )}
            {secrets.data?.items?.map((item) => (
              <option key={item.name} value={item.name}>
                {item.name}
              </option>
            ))}
          </select>
          <p className="help">
            Adds an HMAC-SHA256 signature to each request. The receiving service must verify the
            signature and timestamp.
          </p>
        </div>
        <fieldset className="space-y-3">
          <legend className="mb-3 text-sm font-medium">Send these events</legend>
          {webhookEvents.map((eventType) => (
            <label key={eventType} className="flex items-center gap-3 text-sm">
              <input
                type="checkbox"
                className="size-4 accent-primary"
                checked={draft.events.includes(eventType)}
                onChange={(event) =>
                  setDraft({
                    ...draft,
                    events: event.target.checked
                      ? [...draft.events, eventType]
                      : draft.events.filter((item) => item !== eventType),
                  })
                }
              />
              <span>
                {eventLabels[eventType]}{' '}
                <span className="mono text-muted-foreground">({eventType})</span>
              </span>
            </label>
          ))}
        </fieldset>
        <label className="flex items-start gap-3 rounded-md border border-border p-3">
          <input
            type="checkbox"
            className="mt-1 size-4 accent-primary"
            checked={draft.enabled}
            onChange={(event) => setDraft({ ...draft, enabled: event.target.checked })}
          />
          <span>
            <span className="font-medium">Enable deliveries</span>
            <span className="mt-1 block text-xs leading-5 text-muted-foreground">
              Saving with this option enabled allows automatic delivery of selected events to the
              referenced destination. A test is never sent automatically.
            </span>
          </span>
        </label>
      </fieldset>
      <ErrorNotice error={save.error} />
      {initial && save.isError && (
        <div className="space-y-2">
          <p className="help">
            {conflict
              ? 'This webhook changed since you opened it. Your input has been preserved. Reload the latest version before saving again.'
              : 'Your input has been preserved. Correct it and retry, or explicitly reload the saved version.'}
          </p>
          <ConfirmAction
            title="Replace your edits with the saved version?"
            description="Your unsaved form input will be discarded. The current server version and revision will be loaded."
            label="Reload saved version"
            trigger={
              <Button type="button" variant="outline" size="sm" disabled={save.isPending}>
                Reload saved version
              </Button>
            }
            action={reload}
          />
        </div>
      )}
      <DialogFooter>
        <Button type="button" variant="outline" disabled={save.isPending} onClick={onCancel}>
          Cancel
        </Button>
        <Button
          type="submit"
          disabled={
            save.isPending ||
            secrets.isPending ||
            secrets.isError ||
            !draft.url_secret_ref ||
            !draft.events.length ||
            conflict
          }
        >
          {save.isPending ? 'Saving…' : initial ? 'Save webhook' : 'Create webhook'}
        </Button>
      </DialogFooter>
    </form>
  )
}

function DeliveryHistory({ hook }: { hook: Webhook }) {
  const date = useDate()
  const limit = usePageSize()
  const [status, setStatus] = useState<DeliveryStatus | ''>('')
  const [offset, setOffset] = usePageOffset(limit)
  const [notice, setNotice] = useState('')
  const deliveries = useQuery({
    queryKey: ['webhooks', hook.id, 'deliveries', status, offset, limit],
    queryFn: ({ signal }) =>
      api<Page<Delivery>>(`${hookPath(hook.id)}/deliveries?${params({ status, limit, offset })}`, {
        signal,
      }),
    refetchInterval: 10_000,
  })
  return (
    <div className="space-y-4">
      <p className="help">
        Delivery is at least once. Receivers should deduplicate by event ID. Pending retries are not
        terminal failures. Endpoint changes apply to future attempts.
      </p>
      {hook.deleted_at ? (
        <p className="notice">This webhook was deleted. Delivery history is read-only.</p>
      ) : (
        !hook.enabled && (
          <p className="notice">
            This webhook is disabled. No new attempts can begin; manual retries are unavailable.
          </p>
        )
      )}
      <div className="toolbar">
        <Label htmlFor="delivery-status" className="sr-only">
          Delivery status
        </Label>
        <select
          id="delivery-status"
          className="native-select"
          value={status}
          onChange={(event) => {
            setStatus(event.target.value as DeliveryStatus | '')
            setOffset(0)
          }}
        >
          <option value="">All delivery statuses</option>
          <option value="pending">Pending / retry pending</option>
          <option value="delivering">Delivering</option>
          <option value="delivered">Delivered</option>
          <option value="failed">Failed (terminal)</option>
          <option value="cancelled">Cancelled</option>
        </select>
        <Button
          size="sm"
          variant="outline"
          disabled={deliveries.isFetching}
          onClick={() => void deliveries.refetch()}
        >
          <ArrowClockwiseIcon />
          Refresh
        </Button>
      </div>
      {notice && (
        <p role="status" className="notice text-primary">
          {notice}
        </p>
      )}
      <ErrorNotice error={deliveries.error} retry={() => void deliveries.refetch()} />
      {deliveries.isPending ? (
        <Loading label="Loading deliveries" />
      ) : (
        deliveries.data && (
          <>
            {deliveries.data.items?.length ? (
              <div className="table-frame">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Event / time</TableHead>
                      <TableHead>Status</TableHead>
                      <TableHead>Attempts / HTTP</TableHead>
                      <TableHead>Details / action</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {deliveries.data.items.map((delivery) => (
                      <TableRow key={delivery.id}>
                        <TableCell className="align-top">
                          <p className="mono">{delivery.event_type}</p>
                          <time
                            className="mt-1 block text-xs text-muted-foreground"
                            dateTime={delivery.created_at}
                          >
                            {date(delivery.created_at)}
                          </time>
                        </TableCell>
                        <TableCell className="align-top">
                          <Badge
                            variant="outline"
                            className={
                              delivery.status === 'failed'
                                ? 'text-destructive border-destructive/30'
                                : delivery.status === 'delivered'
                                  ? 'text-primary border-primary/25'
                                  : 'text-muted-foreground'
                            }
                          >
                            {delivery.status === 'pending'
                              ? delivery.attempts > 0
                                ? 'Retry pending'
                                : 'Pending'
                              : delivery.status === 'failed'
                                ? 'Failed (terminal)'
                                : delivery.status === 'delivering'
                                  ? 'Delivering'
                                  : delivery.status === 'delivered'
                                    ? 'Delivered'
                                    : 'Cancelled'}
                          </Badge>
                          {delivery.status === 'pending' && delivery.next_attempt_at && (
                            <p className="mt-2 text-xs text-muted-foreground">
                              Next attempt: {date(delivery.next_attempt_at)}
                            </p>
                          )}
                          {delivery.delivered_at && (
                            <p className="mt-2 text-xs text-muted-foreground">
                              Delivered: {date(delivery.delivered_at)}
                            </p>
                          )}
                        </TableCell>
                        <TableCell className="align-top text-xs">
                          <p>
                            {delivery.attempts} attempt{delivery.attempts === 1 ? '' : 's'}
                          </p>
                          <p className="mt-1 text-muted-foreground">
                            {delivery.last_status
                              ? `HTTP ${delivery.last_status}`
                              : 'No HTTP response'}
                          </p>
                        </TableCell>
                        <TableCell className="max-w-sm whitespace-normal align-top">
                          <details className="text-xs">
                            <summary className="cursor-pointer text-muted-foreground">
                              Delivery details
                            </summary>
                            <dl className="mt-2 space-y-2 break-all">
                              <div>
                                <dt className="text-muted-foreground">Event ID</dt>
                                <dd className="font-mono">{delivery.event_id}</dd>
                              </div>
                              <div>
                                <dt className="text-muted-foreground">Delivery ID</dt>
                                <dd className="font-mono">{delivery.id}</dd>
                              </div>
                            </dl>
                          </details>
                          {delivery.last_error && (
                            <p className="mt-2 break-words text-xs text-destructive">
                              {delivery.last_error}
                            </p>
                          )}
                          {delivery.status === 'failed' && (
                            <ConfirmAction
                              title={`Retry delivery to “${hook.name}”?`}
                              description={
                                <>
                                  This queues a new attempt using destination secret{' '}
                                  <code className="mono">{hook.url_secret_ref}</code>. The same
                                  event and delivery IDs are retained; receivers must deduplicate
                                  events.
                                </>
                              }
                              label="Queue retry"
                              trigger={
                                <Button
                                  variant="outline"
                                  size="sm"
                                  className="mt-3"
                                  disabled={!hook.enabled}
                                >
                                  <ArrowClockwiseIcon />
                                  Retry failed
                                </Button>
                              }
                              action={async () => {
                                await api<unknown>(
                                  `${hookPath(hook.id)}/deliveries/${encodeURIComponent(delivery.id)}/retry`,
                                  { method: 'POST', body: '{}' },
                                )
                                setNotice('The failed delivery was queued for retry.')
                                await refreshWebhooks()
                              }}
                            />
                          )}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            ) : (
              <Empty
                title="No deliveries in this view"
                description={
                  status
                    ? 'Choose another status or clear the filter.'
                    : 'Subscribed events and explicitly requested tests appear here after they are queued.'
                }
              />
            )}
            <Pager
              total={deliveries.data.total}
              limit={deliveries.data.limit}
              offset={deliveries.data.offset}
              onChange={setOffset}
              busy={deliveries.isFetching}
            />
          </>
        )
      )}
    </div>
  )
}

export function WebhooksPage() {
  const date = useDate()
  const returnFocus = useRef<HTMLElement | null>(null)
  const [editing, setEditing] = useState<{ hook: Webhook | null } | null>(null)
  const [editingBusy, setEditingBusy] = useState(false)
  const [history, setHistory] = useState<Webhook | null>(null)
  const [notice, setNotice] = useState('')
  const [includeDeleted, setIncludeDeleted] = useState(false)
  const hooks = useQuery({
    queryKey: ['webhooks', 'list', includeDeleted],
    queryFn: ({ signal }) =>
      api<Items<Webhook>>(`/webhooks${includeDeleted ? '?include_deleted=true' : ''}`, { signal }),
  })
  function openEditor(hook: Webhook | null) {
    returnFocus.current =
      document.activeElement instanceof HTMLElement ? document.activeElement : null
    setEditing({ hook })
  }
  function openHistory(hook: Webhook) {
    returnFocus.current =
      document.activeElement instanceof HTMLElement ? document.activeElement : null
    setHistory(hook)
  }
  return (
    <div className="page">
      <PageHeader
        title="Webhooks"
        description="Send selected activity events to explicitly configured destinations. Secrets stay in the encrypted vault; delivery attempts are recorded durably."
        actions={
          <>
            <Button
              variant="outline"
              disabled={hooks.isFetching}
              onClick={() => void hooks.refetch()}
            >
              <ArrowClockwiseIcon />
              Refresh
            </Button>
            <Button onClick={() => openEditor(null)}>
              <PlusIcon />
              Create webhook
            </Button>
          </>
        }
      />
      <div className="notice">
        Store each complete destination URL in{' '}
        <Link className="data-link" to="/secrets">
          Secrets
        </Link>{' '}
        first, then select its reference. No URL or signing key is returned by this page. Referenced
        secrets cannot be deleted.
      </div>
      <label className="flex items-center gap-2 text-sm text-muted-foreground">
        <input
          type="checkbox"
          className="accent-primary"
          checked={includeDeleted}
          onChange={(event) => setIncludeDeleted(event.target.checked)}
        />
        Include deleted webhooks
      </label>
      {notice && (
        <p className="notice text-primary" role="status">
          {notice}
        </p>
      )}
      <ErrorNotice error={hooks.error} retry={() => void hooks.refetch()} />
      {hooks.isPending ? (
        <Loading label="Loading webhooks" />
      ) : (
        hooks.data &&
        (hooks.data.items?.length ? (
          <div className="table-frame">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name / destination reference</TableHead>
                  <TableHead>Events</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {hooks.data.items.map((hook) => (
                  <TableRow key={hook.id}>
                    <TableCell className="align-top">
                      <p className="font-medium">{hook.name}</p>
                      <p className="mono mt-1 break-all text-muted-foreground">
                        {hook.url_secret_ref}
                      </p>
                      <p className="mt-2 text-xs text-muted-foreground">
                        {hook.signing_secret_ref
                          ? `Signed with ${hook.signing_secret_ref}`
                          : 'Unsigned'}{' '}
                        · Revision {hook.revision}
                      </p>
                      <p className="mt-1 text-xs text-muted-foreground">
                        Updated {date(hook.updated_at)}
                      </p>
                    </TableCell>
                    <TableCell className="align-top">
                      <div className="space-y-1">
                        {hook.events.map((eventType) => (
                          <p key={eventType} className="mono text-muted-foreground">
                            {eventType}
                          </p>
                        ))}
                      </div>
                    </TableCell>
                    <TableCell className="align-top">
                      <Badge
                        variant="outline"
                        className={
                          hook.enabled ? 'text-primary border-primary/25' : 'text-muted-foreground'
                        }
                      >
                        {hook.deleted_at ? 'Deleted' : hook.enabled ? 'Enabled' : 'Disabled'}
                      </Badge>
                      {hook.deleted_at && (
                        <p className="mt-2 text-xs text-muted-foreground">
                          {date(hook.deleted_at)}
                        </p>
                      )}
                    </TableCell>
                    <TableCell className="align-top">
                      <div className="flex max-w-md flex-wrap justify-end gap-2">
                        {!hook.deleted_at && (
                          <Button variant="outline" size="sm" onClick={() => openEditor(hook)}>
                            <PencilSimpleIcon />
                            Edit<span className="sr-only"> {hook.name}</span>
                          </Button>
                        )}
                        <Button variant="outline" size="sm" onClick={() => openHistory(hook)}>
                          <ClockCounterClockwiseIcon />
                          Deliveries<span className="sr-only"> for {hook.name}</span>
                        </Button>
                        {!hook.deleted_at && (
                          <>
                            <ConfirmAction
                              title={`${hook.enabled ? 'Disable' : 'Enable'} “${hook.name}”?`}
                              description={
                                hook.enabled ? (
                                  'No new delivery attempts can begin while this webhook is disabled. An already-started HTTP request may still finish. The configuration and delivery history are retained.'
                                ) : (
                                  <>
                                    Automatic delivery of the selected events will be enabled using
                                    destination secret{' '}
                                    <code className="mono">{hook.url_secret_ref}</code>. Queued
                                    attempts may begin. No test request is sent by this action.
                                  </>
                                )
                              }
                              label={hook.enabled ? 'Disable webhook' : 'Enable webhook'}
                              trigger={
                                <Button variant="ghost" size="sm">
                                  {hook.enabled ? 'Disable' : 'Enable'}
                                  <span className="sr-only"> {hook.name}</span>
                                </Button>
                              }
                              action={async () => {
                                await api<unknown>(hookPath(hook.id), {
                                  method: 'PUT',
                                  body: JSON.stringify({
                                    name: hook.name,
                                    enabled: !hook.enabled,
                                    url_secret_ref: hook.url_secret_ref,
                                    signing_secret_ref: hook.signing_secret_ref,
                                    events: hook.events,
                                    revision: hook.revision,
                                  }),
                                })
                                setNotice(
                                  `“${hook.name}” was ${hook.enabled ? 'disabled' : 'enabled'}.`,
                                )
                                await refreshWebhooks()
                              }}
                            />
                            <ConfirmAction
                              title={`Send a test to “${hook.name}”?`}
                              description={
                                <>
                                  This queues one real <code className="mono">webhook.test</code>{' '}
                                  HTTP delivery to the destination stored in secret{' '}
                                  <code className="mono">{hook.url_secret_ref}</code>. It uses the
                                  configured signing secret, if any, and may retry on transient
                                  failure.
                                </>
                              }
                              label="Send test"
                              trigger={
                                <Button variant="ghost" size="sm" disabled={!hook.enabled}>
                                  <PaperPlaneTiltIcon />
                                  Send test<span className="sr-only"> to {hook.name}</span>
                                </Button>
                              }
                              action={async () => {
                                await api<unknown>(`${hookPath(hook.id)}/test`, {
                                  method: 'POST',
                                  body: '{}',
                                })
                                setNotice(
                                  `A test delivery for “${hook.name}” was queued. Open Deliveries to track its result.`,
                                )
                                await refreshWebhooks()
                              }}
                            />
                            <ConfirmAction
                              title={`Delete webhook “${hook.name}”?`}
                              description="No future deliveries will begin for this webhook. An already-started request may still finish. Use Include deleted webhooks to view retained delivery history. Referenced secrets are not deleted. If another session changed this webhook, refresh the list before trying again."
                              label="Delete webhook"
                              destructive
                              trigger={
                                <Button
                                  variant="ghost"
                                  size="icon-sm"
                                  aria-label={`Delete webhook ${hook.name}`}
                                >
                                  <TrashIcon />
                                </Button>
                              }
                              action={async () => {
                                await api<void>(hookPath(hook.id), {
                                  method: 'DELETE',
                                  body: JSON.stringify({ revision: hook.revision }),
                                })
                                setNotice(
                                  `“${hook.name}” was deleted. Delivery history was retained.`,
                                )
                                await refreshWebhooks()
                              }}
                            />
                          </>
                        )}
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        ) : (
          <Empty
            title="No webhooks configured"
            description="Create a destination URL secret, then add an event subscription. No events are sent until you explicitly enable a webhook."
            action={
              <Button onClick={() => openEditor(null)}>
                <PlusIcon />
                Create webhook
              </Button>
            }
          />
        ))
      )}
      <Dialog
        open={Boolean(editing)}
        onOpenChange={(open) => {
          if (!open && !editingBusy) setEditing(null)
        }}
      >
        <DialogContent
          showCloseButton={!editingBusy}
          onInteractOutside={(event) => event.preventDefault()}
          onEscapeKeyDown={(event) => {
            if (editingBusy) event.preventDefault()
          }}
          onCloseAutoFocus={(event) => {
            event.preventDefault()
            returnFocus.current?.focus()
          }}
        >
          <DialogHeader>
            <DialogTitle>{editing?.hook ? 'Edit webhook' : 'Create webhook'}</DialogTitle>
            <DialogDescription>
              Configure secret references and event subscriptions. Saving does not send a test
              request.
            </DialogDescription>
          </DialogHeader>
          {editing && (
            <WebhookEditor
              key={editing.hook?.id ?? 'new'}
              initial={editing.hook}
              onBusyChange={setEditingBusy}
              onCancel={() => setEditing(null)}
              onSaved={() => {
                setNotice(editing.hook ? 'Webhook changes were saved.' : 'Webhook was created.')
                setEditing(null)
              }}
            />
          )}
        </DialogContent>
      </Dialog>
      <Dialog
        open={Boolean(history)}
        onOpenChange={(open) => {
          if (!open) setHistory(null)
        }}
      >
        <DialogContent
          className="sm:max-w-5xl"
          onCloseAutoFocus={(event) => {
            event.preventDefault()
            returnFocus.current?.focus()
          }}
        >
          <DialogHeader>
            <DialogTitle>Deliveries: {history?.name}</DialogTitle>
            <DialogDescription>
              Durable attempts, response status and sanitized errors. Updates every 10 seconds while
              open.
            </DialogDescription>
          </DialogHeader>
          {history && (
            <DeliveryHistory
              key={history.id}
              hook={hooks.data?.items?.find((hook) => hook.id === history.id) ?? history}
            />
          )}
        </DialogContent>
      </Dialog>
    </div>
  )
}
