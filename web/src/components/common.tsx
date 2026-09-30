import { useRef, useState, type ReactNode } from 'react'
import {
  ArrowLeftIcon,
  ArrowRightIcon,
  WarningCircleIcon,
  ArrowClockwiseIcon,
  DatabaseIcon,
} from '@phosphor-icons/react'
import { Link } from 'react-router-dom'
import { ApiError } from '@/lib/api'
import { number, statusLabels } from '@/lib/format'
import type { Run } from '@/lib/types'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Skeleton } from '@/components/ui/skeleton'
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogCancel,
  AlertDialogTrigger,
} from '@/components/ui/alert-dialog'

export function PageHeader({
  title,
  description,
  actions,
  back,
}: {
  title: string
  description?: string
  actions?: ReactNode
  back?: { to: string; label: string }
}) {
  return (
    <header className="space-y-4">
      {back && (
        <Link
          to={back.to}
          className="inline-flex items-center gap-2 text-xs text-muted-foreground hover:text-foreground"
        >
          <ArrowLeftIcon />
          {back.label}
        </Link>
      )}
      <div className="page-heading">
        <div>
          <h1 className="page-title">{title}</h1>
          {description && <p className="page-description">{description}</p>}
        </div>
        {actions && <div className="toolbar">{actions}</div>}
      </div>
    </header>
  )
}

export function ErrorNotice({ error, retry }: { error: unknown; retry?: () => void }) {
  if (!error) return null
  return (
    <div role="alert" className="rounded-md border border-destructive/30 bg-destructive/5 p-4">
      <div className="flex items-start gap-3">
        <WarningCircleIcon size={20} className="mt-0.5 shrink-0 text-destructive" />
        <div className="min-w-0 flex-1 space-y-2">
          <p className="break-words text-sm">
            {error instanceof Error ? error.message : 'An unexpected error occurred.'}
          </p>
          {error instanceof ApiError && error.details.length > 0 && (
            <ul className="list-disc space-y-1 pl-4 text-xs text-muted-foreground">
              {error.details.map((issue, i) => (
                <li key={i}>{issue}</li>
              ))}
            </ul>
          )}
          {retry && (
            <Button type="button" size="sm" variant="outline" onClick={retry}>
              <ArrowClockwiseIcon />
              Try again
            </Button>
          )}
        </div>
      </div>
    </div>
  )
}

export function Loading({ label = 'Loading data' }: { label?: string }) {
  return (
    <div role="status" aria-label={label} className="space-y-3 py-3">
      <span className="sr-only">{label}</span>
      <Skeleton className="h-9 w-full" />
      <Skeleton className="h-12 w-full" />
      <Skeleton className="h-12 w-full" />
      <Skeleton className="h-12 w-3/4" />
    </div>
  )
}

export function Empty({
  title,
  description,
  action,
}: {
  title: string
  description: string
  action?: ReactNode
}) {
  return (
    <div className="flex flex-col items-start gap-3 rounded-md border border-dashed border-border px-6 py-12">
      <DatabaseIcon size={27} className="text-muted-foreground" />
      <div>
        <h2 className="text-base font-medium">{title}</h2>
        <p className="mt-1 max-w-xl text-sm leading-6 text-muted-foreground">{description}</p>
      </div>
      {action}
    </div>
  )
}

export function Pager({
  total,
  limit,
  offset,
  onChange,
  busy = false,
}: {
  total: number
  limit: number
  offset: number
  onChange: (offset: number) => void
  busy?: boolean
}) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-3 py-3">
      <p className="text-xs text-muted-foreground" aria-live="polite">
        {total === 0
          ? 'No results'
          : `${number(Math.min(offset + 1, total))}–${number(Math.min(offset + limit, total))} of ${number(total)}`}
      </p>
      <div className="flex gap-2">
        <Button
          variant="outline"
          size="sm"
          disabled={busy || offset === 0}
          onClick={() => onChange(Math.max(0, offset - limit))}
        >
          <ArrowLeftIcon />
          Previous
        </Button>
        <Button
          variant="outline"
          size="sm"
          disabled={busy || offset + limit >= total}
          onClick={() => onChange(offset + limit)}
        >
          Next
          <ArrowRightIcon />
        </Button>
      </div>
    </div>
  )
}

export function RunStatusBadge({
  run,
}: {
  run: Pick<Run, 'status' | 'cancel_requested' | 'pause_requested'>
}) {
  const active = run.status === 'queued' || run.status === 'running'
  const pausing = active && run.pause_requested && !run.cancel_requested
  const label =
    active && run.cancel_requested
      ? 'Cancellation requested'
      : pausing
        ? 'Pausing…'
        : statusLabels[run.status]
  return (
    <Badge
      variant="outline"
      className={
        run.status === 'failed' || (active && run.cancel_requested)
          ? 'border-destructive/30 text-destructive'
          : run.status === 'paused' || pausing
            ? 'border-amber-300/30 bg-amber-300/5 text-amber-200'
            : run.status === 'running' || run.status === 'succeeded'
              ? 'border-primary/25 bg-primary/5 text-primary'
              : 'text-muted-foreground'
      }
    >
      {active && <span aria-hidden="true" className="size-1.5 rounded-full bg-current" />}
      {label}
    </Badge>
  )
}

export function ConfirmAction({
  trigger,
  title,
  description,
  label = 'Confirm',
  action,
  destructive = false,
}: {
  trigger: ReactNode
  title: string
  description: ReactNode
  label?: string
  action: () => Promise<unknown>
  destructive?: boolean
}) {
  const [open, setOpen] = useState(false)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const confirming = useRef(false)
  async function confirm() {
    if (confirming.current) return
    confirming.current = true
    setPending(true)
    setError(null)
    try {
      await action()
      setOpen(false)
    } catch (caught) {
      setError(caught)
    } finally {
      confirming.current = false
      setPending(false)
    }
  }
  return (
    <AlertDialog
      open={open}
      onOpenChange={(value) => {
        if (!confirming.current) {
          setOpen(value)
          setError(null)
        }
      }}
    >
      <AlertDialogTrigger asChild>{trigger}</AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription asChild>
            <div className="text-sm leading-6 text-muted-foreground">{description}</div>
          </AlertDialogDescription>
        </AlertDialogHeader>
        <ErrorNotice error={error} />
        <AlertDialogFooter>
          <AlertDialogCancel className="min-h-11 sm:min-h-9" disabled={pending}>
            Back
          </AlertDialogCancel>
          <Button
            variant={destructive ? 'destructive' : 'default'}
            className="min-h-11 sm:min-h-9"
            disabled={pending}
            onClick={() => void confirm()}
          >
            {pending ? 'Processing…' : label}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
