import { BugIcon, InfoIcon, WarningIcon, WarningCircleIcon } from '@phosphor-icons/react'
import { Link } from 'react-router-dom'
import type { ActivityLevel } from '@/lib/activity-types'
import { Badge } from '@/components/ui/badge'

export function LevelBadge({ level }: { level: ActivityLevel }) {
  const Icon = { debug: BugIcon, info: InfoIcon, warn: WarningIcon, error: WarningCircleIcon }[
    level
  ]
  return (
    <Badge
      variant="outline"
      className={
        level === 'error'
          ? 'border-destructive/30 text-destructive'
          : level === 'warn'
            ? 'border-amber-300/30 text-amber-200'
            : level === 'info'
              ? 'border-primary/25 text-primary'
              : 'text-muted-foreground'
      }
    >
      <Icon aria-hidden="true" />
      {level}
    </Badge>
  )
}

export function ActivityLinks({
  provider_id,
  run_id,
  kind,
}: {
  provider_id?: string
  run_id?: string
  kind?: string
}) {
  const operationsPage = kind?.startsWith('backup.')
    ? { to: '/backups', label: 'Backups' }
    : kind?.startsWith('health.')
      ? { to: '/health', label: 'System health' }
      : undefined
  return (
    <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs">
      {provider_id && (
        <Link className="data-link break-all" to={`/providers/${encodeURIComponent(provider_id)}`}>
          Source: {provider_id}
        </Link>
      )}
      {run_id && (
        <Link
          className="data-link font-mono"
          title={run_id}
          to={`/runs/${encodeURIComponent(run_id)}`}
        >
          Run: {run_id.slice(0, 8)}
        </Link>
      )}
      {operationsPage && (
        <Link className="data-link" to={operationsPage.to}>
          {operationsPage.label}
        </Link>
      )}
      {!provider_id && !run_id && !operationsPage && (
        <span className="text-muted-foreground">Service</span>
      )}
    </div>
  )
}
