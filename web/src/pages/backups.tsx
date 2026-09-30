import { useRef, useState } from 'react'
import { keepPreviousData, useMutation, useQuery } from '@tanstack/react-query'
import { ArchiveIcon, DownloadSimpleIcon, KeyIcon, ShieldCheckIcon } from '@phosphor-icons/react'
import { api, queryClient } from '@/lib/api'
import { useDate } from '@/lib/format'
import { usePageOffset, usePageSize } from '@/lib/display-preferences'
import type { BackupJob, BackupOverview, BackupSettings } from '@/lib/backup-types'
import { Button } from '@/components/ui/button'
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
import { ConfirmAction, Empty, ErrorNotice, Loading, PageHeader } from '@/components/common'

const refresh = () => queryClient.invalidateQueries({ queryKey: ['backups'] })
function size(bytes: number) {
  if (!bytes) return 'Not available'
  if (bytes < 1024 ** 2) return `${(bytes / 1024).toFixed(1)} KiB`
  if (bytes < 1024 ** 3) return `${(bytes / 1024 ** 2).toFixed(1)} MiB`
  return `${(bytes / 1024 ** 3).toFixed(2)} GiB`
}

// Operational panel: preserve the existing Radix/Tailwind form and table system.
// Recovery identities live only in component state, never a query/mutation cache.
export function BackupsPage() {
  const date = useDate()
  const limit = usePageSize()
  const [offset, setOffset] = usePageOffset(limit)
  const overview = useQuery({
    queryKey: ['backups', offset, limit],
    queryFn: ({ signal }) =>
      api<BackupOverview>(`/backups?limit=${limit}&offset=${offset}`, { signal }),
    refetchInterval: 10_000,
    placeholderData: keepPreviousData,
  })
  const [recovery, setRecovery] = useState('')
  const [verifyTarget, setVerifyTarget] = useState<BackupJob | null>(null)
  const [identity, setIdentity] = useState('')
  const [verifyBusy, setVerifyBusy] = useState(false)
  const [verifyError, setVerifyError] = useState<unknown>(null)
  const [verifiedConsent, setVerifiedConsent] = useState(false)
  const [detailSelection, setDetail] = useState<BackupJob | null>(null)
  const [notice, setNotice] = useState('')
  const returnFocus = useRef<HTMLElement | null>(null)
  function restoreFocus(event: Event) {
    event.preventDefault()
    if (returnFocus.current?.isConnected) returnFocus.current.focus()
    else document.getElementById('main-content')?.focus()
  }
  const queue = useMutation({
    gcTime: 0,
    mutationFn: () => api<BackupJob>('/backups', { method: 'POST' }),
    onSuccess: async () => {
      setNotice('Backup queued. You can leave this page; progress is stored on the server.')
      setOffset(0)
      await refresh()
    },
  })
  const data = overview.data
  const detail = data?.items.find((job) => job.id === detailSelection?.id) ?? detailSelection
  const active = data?.items.some((job) => job.status === 'queued' || job.status === 'running')
  async function startVerification() {
    if (verifyBusy || !verifyTarget || !identity.trim() || !verifiedConsent) return
    setVerifyBusy(true)
    setVerifyError(null)
    try {
      await api<BackupJob>(`/backups/${verifyTarget.id}/verify`, {
        method: 'POST',
        body: JSON.stringify({ identity: identity.trim(), confirm: true }),
      })
      setVerifyTarget(null)
      setNotice(
        'Isolated restore verification queued. The recovery identity is held only in server memory for this job.',
      )
      setOffset(0)
      await refresh()
    } catch (error) {
      setVerifyError(error)
    } finally {
      setIdentity('')
      setVerifyBusy(false)
    }
  }
  return (
    <div className="page">
      <PageHeader
        title="Backups"
        description="Encrypted PostgreSQL snapshots, exact source JSON and your vault key in one recoverable archive."
        actions={
          <Button
            disabled={
              !data?.settings.recipient || !data.capability.available || active || queue.isPending
            }
            onClick={() => queue.mutate()}
          >
            <ArchiveIcon />
            Back up now
          </Button>
        }
      />
      {notice && (
        <p className="notice text-primary" role="status">
          {notice}
        </p>
      )}
      <ErrorNotice error={overview.error} retry={() => void overview.refetch()} />
      <ErrorNotice error={queue.error} />
      {overview.isPending ? (
        <Loading />
      ) : (
        data && (
          <>
            <div className="notice flex items-start gap-3">
              <ShieldCheckIcon className="mt-1 shrink-0 text-primary" />
              <div className="space-y-2">
                <p>
                  Source occurrences, observations, historical raw archives and publication history
                  are included. Only encrypted backups are stored in the server backup directory.
                </p>
                <p className={data.capability.available ? 'help' : 'text-destructive'}>
                  {data.capability.message}
                </p>
                {!data.capability.available && (
                  <p className="help">
                    Install matching PostgreSQL clients or configure INGEST_PG_DUMP and
                    INGEST_PG_RESTORE. The production container includes PostgreSQL 18 clients.
                  </p>
                )}
              </div>
            </div>
            <section className="panel space-y-5 p-5 sm:p-6" aria-labelledby="backup-recovery-title">
              <div className="space-y-2">
                <h2 id="backup-recovery-title" className="text-lg font-semibold">
                  Recovery identity
                </h2>
                <p className="text-sm text-muted-foreground">
                  Keep the private identity in a password manager outside this server. It is
                  revealed once and cannot be recovered here. Losing it makes the encrypted backups
                  unreadable.
                </p>
              </div>
              {data.settings.recipient ? (
                <div className="space-y-2">
                  <Label>Public recipient</Label>
                  <p className="break-all rounded-md border bg-muted/30 p-3 font-mono text-xs">
                    {data.settings.recipient}
                  </p>
                </div>
              ) : (
                <p className="text-sm text-muted-foreground">
                  No recovery identity configured. Generate and save one before creating backups.
                </p>
              )}
              <ConfirmAction
                title={
                  data.settings.recipient
                    ? 'Replace the recovery identity?'
                    : 'Generate a recovery identity?'
                }
                description="The new private identity is shown only once. Save it securely outside this server. Existing archives still require their original identity. Generating a new identity disables the schedule until you explicitly enable it again."
                label="Generate identity"
                trigger={
                  <Button
                    variant="outline"
                    onClick={(event) => {
                      returnFocus.current = event.currentTarget
                    }}
                  >
                    <KeyIcon />
                    {data.settings.recipient ? 'Replace identity' : 'Generate identity'}
                  </Button>
                }
                action={async () => {
                  const result = await api<{ identity: string; recipient: string }>(
                    '/backups/recovery',
                    {
                      method: 'POST',
                      body: JSON.stringify({ revision: data.settings.revision, confirm: true }),
                    },
                  )
                  setRecovery(result.identity)
                  await refresh()
                }}
              />
            </section>
            <ScheduleForm key={data.settings.revision} settings={data.settings} />
            <section className="space-y-4" aria-labelledby="backup-history-title">
              <div className="space-y-1">
                <h2 id="backup-history-title" className="text-lg font-semibold">
                  Backup and verification history
                </h2>
                <p className="text-sm text-muted-foreground">
                  One operation runs at a time. Jobs survive page navigation; interrupted jobs are
                  marked failed after their lease expires.
                </p>
              </div>
              {data.items.length ? (
                <div className="table-frame">
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Operation</TableHead>
                        <TableHead>Status</TableHead>
                        <TableHead>Started</TableHead>
                        <TableHead>Size</TableHead>
                        <TableHead className="text-right">Actions</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {data.items.map((job) => (
                        <TableRow key={job.id}>
                          <TableCell>
                            <div className="font-medium">
                              {job.kind === 'backup' ? 'Encrypted backup' : 'Restore verification'}
                            </div>
                            <div className="text-xs text-muted-foreground">
                              {job.trigger === 'scheduled' ? 'Daily schedule' : 'On demand'}
                            </div>
                          </TableCell>
                          <TableCell>
                            <div
                              className={
                                job.status === 'failed'
                                  ? 'text-destructive'
                                  : job.status === 'succeeded'
                                    ? 'text-primary'
                                    : ''
                              }
                            >
                              {job.status}
                            </div>
                            <div className="text-xs text-muted-foreground">
                              {job.status === 'running'
                                ? job.phase.replaceAll('_', ' ')
                                : job.cleanup_pending
                                  ? 'Isolated cleanup pending; scheduler paused'
                                  : job.failure_code ||
                                    (job.report?.verified
                                      ? 'Integrity and decryptability checked'
                                      : '')}
                            </div>
                          </TableCell>
                          <TableCell className="text-xs text-muted-foreground">
                            {date(job.started_at || job.created_at)}
                          </TableCell>
                          <TableCell className="font-mono text-xs">
                            {job.kind === 'backup' ? size(job.bytes) : 'Isolated database'}
                          </TableCell>
                          <TableCell>
                            <div className="flex flex-wrap justify-end gap-2">
                              <Button
                                size="sm"
                                variant="ghost"
                                onClick={(event) => {
                                  returnFocus.current = event.currentTarget
                                  setDetail(job)
                                }}
                              >
                                Details
                              </Button>
                              {job.kind === 'backup' && job.status === 'succeeded' && (
                                <>
                                  <Button size="sm" variant="outline" asChild>
                                    <a
                                      href={`/api/backups/${job.id}/download`}
                                      download={`backup-${job.id}.age`}
                                    >
                                      <DownloadSimpleIcon />
                                      Download
                                    </a>
                                  </Button>
                                  <Button
                                    size="sm"
                                    variant="outline"
                                    disabled={active || !data.capability.available}
                                    onClick={(event) => {
                                      returnFocus.current = event.currentTarget
                                      setVerifyTarget(job)
                                      setIdentity('')
                                      setVerifyError(null)
                                      setVerifiedConsent(false)
                                    }}
                                  >
                                    <ShieldCheckIcon />
                                    Verify
                                  </Button>
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
                  title="No backups yet"
                  description="Save your recovery identity, then create an encrypted backup or enable the daily schedule."
                />
              )}
              <div className="flex flex-wrap items-center justify-between gap-3 text-sm text-muted-foreground">
                <span>
                  {data.total
                    ? `${data.offset + 1}–${Math.min(data.offset + data.items.length, data.total)} of ${data.total} operations`
                    : '0 operations'}
                </span>
                <div className="flex gap-2">
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={overview.isFetching || offset === 0}
                    onClick={() => setOffset(Math.max(0, offset - limit))}
                  >
                    Previous
                  </Button>
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={overview.isFetching || offset + limit >= data.total}
                    onClick={() => setOffset(offset + limit)}
                  >
                    Next
                  </Button>
                </div>
              </div>
            </section>
            <section className="notice space-y-2">
              <h2 className="font-medium">Recover into a new destination</h2>
              <p>
                Use <code className="mono">ingest backup restore --help</code> on a trusted host.
                Recovery refuses existing databases and state directories, verifies the archive and
                disables restored external automation. Review the recovered source definitions in{' '}
                <code className="mono">recovered-sources/</code> before activating sources. This
                panel never restores over the live database.
              </p>
            </section>
          </>
        )
      )}
      <Dialog
        open={Boolean(recovery)}
        onOpenChange={(open) => {
          if (!open) setRecovery('')
        }}
      >
        <DialogContent onCloseAutoFocus={restoreFocus}>
          <DialogHeader>
            <DialogTitle>Save your recovery identity now</DialogTitle>
            <DialogDescription>
              This is the only reveal. Store the complete identity outside this server. Do not send
              it in support messages or paste it into logs.
            </DialogDescription>
          </DialogHeader>
          <pre className="whitespace-pre-wrap break-all rounded-md border bg-muted/30 p-4 font-mono text-xs select-all">
            {recovery}
          </pre>
          <DialogFooter>
            <Button onClick={() => setRecovery('')}>I saved the identity</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <Dialog
        open={Boolean(verifyTarget)}
        onOpenChange={(open) => {
          if (!open && !verifyBusy) {
            setVerifyTarget(null)
            setIdentity('')
            setVerifyError(null)
          }
        }}
      >
        <DialogContent showCloseButton={!verifyBusy} onCloseAutoFocus={restoreFocus}>
          <DialogHeader>
            <DialogTitle>Verify isolated restore</DialogTitle>
            <DialogDescription>
              The server restores this known archive to a new temporary database, checks all table
              counts and vault decryption, then removes only its own temporary database and private
              files. The live database is never replaced.
            </DialogDescription>
          </DialogHeader>
          <form
            className="space-y-5"
            autoComplete="off"
            onSubmit={(event) => {
              event.preventDefault()
              void startVerification()
            }}
          >
            <p className="text-xs text-muted-foreground">
              Backup created {verifyTarget && date(verifyTarget.created_at)}. Use the identity
              matching this backup's recipient, not necessarily the current one.
            </p>
            <div className="field">
              <Label htmlFor="backup-identity">Private recovery identity</Label>
              <Input
                id="backup-identity"
                type="password"
                value={identity}
                onChange={(event) => setIdentity(event.target.value)}
                maxLength={256}
                autoComplete="off"
                spellCheck={false}
                required
                disabled={verifyBusy}
              />
              <p className="help">
                Used in memory for this job, never stored in database settings or browser caches.
              </p>
            </div>
            <label className="flex items-start gap-3 text-sm">
              <input
                type="checkbox"
                className="mt-1"
                checked={verifiedConsent}
                onChange={(event) => setVerifiedConsent(event.target.checked)}
                disabled={verifyBusy}
                required
              />
              I authorize creating and removing an isolated verification database on this server.
            </label>
            <ErrorNotice error={verifyError} />
            <DialogFooter>
              <Button
                variant="outline"
                type="button"
                disabled={verifyBusy}
                onClick={() => {
                  setVerifyTarget(null)
                  setIdentity('')
                }}
              >
                Cancel
              </Button>
              <Button type="submit" disabled={verifyBusy || !identity.trim() || !verifiedConsent}>
                {verifyBusy ? 'Queuing…' : 'Verify restore'}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <Dialog
        open={Boolean(detail)}
        onOpenChange={(open) => {
          if (!open) setDetail(null)
        }}
      >
        <DialogContent onCloseAutoFocus={restoreFocus}>
          <DialogHeader>
            <DialogTitle>Operation details</DialogTitle>
            <DialogDescription>
              Durable progress and integrity evidence. No private keys are included.
            </DialogDescription>
          </DialogHeader>
          {detail && (
            <div className="space-y-4 text-sm">
              <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2">
                <dt className="text-muted-foreground">Identifier</dt>
                <dd className="break-all font-mono text-xs">{detail.id}</dd>
                <dt className="text-muted-foreground">Status</dt>
                <dd>{detail.status}</dd>
                <dt className="text-muted-foreground">Created</dt>
                <dd>{date(detail.created_at)}</dd>
                <dt className="text-muted-foreground">Finished</dt>
                <dd>{detail.finished_at ? date(detail.finished_at) : 'Not finished'}</dd>
                {detail.report?.tables !== undefined && (
                  <>
                    <dt className="text-muted-foreground">Tables</dt>
                    <dd>{detail.report.tables}</dd>
                  </>
                )}
                {detail.report?.source_files !== undefined && (
                  <>
                    <dt className="text-muted-foreground">Exact source files</dt>
                    <dd>{detail.report.source_files}</dd>
                  </>
                )}
                {detail.report?.secrets !== undefined && (
                  <>
                    <dt className="text-muted-foreground">Decryptable secrets</dt>
                    <dd>{detail.report.secrets}</dd>
                  </>
                )}
              </dl>
              {detail.report?.reason && (
                <p className="notice text-destructive">{detail.report.reason}</p>
              )}
              {detail.report?.cleanup && <p className="notice">{detail.report.cleanup}</p>}
              {detail.sha256 && (
                <div>
                  <Label>Ciphertext SHA-256</Label>
                  <p className="mt-2 break-all font-mono text-xs">{detail.sha256}</p>
                </div>
              )}
              {detail.recipient && (
                <div>
                  <Label>Archive recipient</Label>
                  <p className="mt-2 break-all font-mono text-xs">{detail.recipient}</p>
                </div>
              )}
            </div>
          )}
        </DialogContent>
      </Dialog>
    </div>
  )
}

function ScheduleForm({ settings }: { settings: BackupSettings }) {
  const date = useDate()
  const [enabled, setEnabled] = useState(settings.enabled)
  const [clock, setClock] = useState(settings.time_utc)
  const save = useMutation({
    gcTime: 0,
    mutationFn: () =>
      api<void>('/backups/settings', {
        method: 'PUT',
        body: JSON.stringify({ enabled, time_utc: clock, revision: settings.revision }),
      }),
    onSuccess: refresh,
  })
  return (
    <section className="panel space-y-5 p-5 sm:p-6" aria-labelledby="backup-schedule-title">
      <h2 id="backup-schedule-title" className="text-lg font-semibold">
        Daily schedule
      </h2>
      <form
        className="space-y-5"
        onSubmit={(event) => {
          event.preventDefault()
          save.mutate()
        }}
      >
        <div className="flex flex-wrap items-end gap-5">
          <label className="flex items-center gap-3 pb-2 text-sm">
            <input
              type="checkbox"
              checked={enabled}
              onChange={(event) => setEnabled(event.target.checked)}
              disabled={!settings.recipient || save.isPending}
            />
            Enable automatic backups
          </label>
          <div className="field">
            <Label htmlFor="backup-clock">Daily time (UTC)</Label>
            <Input
              id="backup-clock"
              type="time"
              value={clock}
              onChange={(event) => setClock(event.target.value)}
              required
              disabled={save.isPending}
              className="w-40"
            />
          </div>
          <Button
            type="submit"
            variant="outline"
            disabled={save.isPending || (enabled && !settings.recipient)}
          >
            {save.isPending ? 'Saving…' : 'Save schedule'}
          </Button>
        </div>
        <ErrorNotice error={save.error} />
        <dl className="grid gap-4 text-sm sm:grid-cols-3">
          <div>
            <dt className="text-muted-foreground">Next scheduled backup</dt>
            <dd className="mt-1">
              {settings.next_run_at ? date(settings.next_run_at) : 'Disabled'}
            </dd>
          </div>
          <div>
            <dt className="text-muted-foreground">Last successful backup</dt>
            <dd className="mt-1">
              {settings.last_success_at ? date(settings.last_success_at) : 'None yet'}
            </dd>
          </div>
          <div>
            <dt className="text-muted-foreground">Last failed backup</dt>
            <dd className="mt-1">
              {settings.last_failure_at ? date(settings.last_failure_at) : 'None'}
            </dd>
          </div>
        </dl>
        <p className="help">
          Times are always UTC. A missed slot runs when the service returns. Backups are never
          automatically deleted; monitor available storage and keep encrypted copies outside this
          server.
        </p>
      </form>
    </section>
  )
}
