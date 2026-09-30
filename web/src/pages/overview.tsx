import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { ArrowRightIcon, PlusIcon } from '@phosphor-icons/react'
import { api } from '@/lib/api'
import type { Health, Overview } from '@/lib/types'
import { number } from '@/lib/format'
import { Button } from '@/components/ui/button'
import { CollectDialog } from '@/components/collect-dialog'
import { Empty, ErrorNotice, Loading, PageHeader } from '@/components/common'
import { RunsTable } from '@/pages/runs'

export default function OverviewPage() {
  const overview = useQuery({
    queryKey: ['overview'],
    queryFn: ({ signal }) => api<Overview>('/overview', { signal }),
  })
  const health = useQuery({
    queryKey: ['health'],
    queryFn: ({ signal }) => api<Health>('/health', { signal }),
    refetchInterval: 60_000,
  })
  return (
    <div className="page">
      <PageHeader
        title="Overview"
        description="Monitor sources, durable runs and published catalogue data."
        actions={<CollectDialog />}
      />
      <ErrorNotice error={overview.error} retry={() => void overview.refetch()} />
      {overview.isPending ? (
        <Loading />
      ) : (
        overview.data && (
          <>
            <dl className="metric-strip">
              <div className="metric">
                <dt className="text-xs text-muted-foreground">Configured sources</dt>
                <dd className="mt-3 font-mono text-3xl font-medium tracking-tight">
                  <Link className="hover:text-primary" to="/providers">
                    {number(overview.data.providers)}
                  </Link>
                </dd>
              </div>
              <div className="metric">
                <dt className="text-xs text-muted-foreground">Active runs</dt>
                <dd className="mt-3 font-mono text-3xl font-medium tracking-tight">
                  <Link className="hover:text-primary" to="/runs">
                    {number(overview.data.active_runs)}
                  </Link>
                </dd>
              </div>
              <div className="metric">
                <dt className="text-xs text-muted-foreground">Published torrents</dt>
                <dd className="mt-3 font-mono text-3xl font-medium tracking-tight">
                  <Link className="hover:text-primary" to="/torrents">
                    {number(overview.data.torrents)}
                  </Link>
                </dd>
              </div>
              <div className="metric">
                <dt className="text-xs text-muted-foreground">Observations</dt>
                <dd className="mt-3 font-mono text-3xl font-medium tracking-tight">
                  <Link className="hover:text-primary" to="/raw">
                    {number(overview.data.raw_records)}
                  </Link>
                </dd>
              </div>
            </dl>
            <section className="space-y-4">
              <div className="flex items-center justify-between gap-3">
                <h2 className="section-title">Recent runs</h2>
                <Button variant="ghost" size="sm" asChild>
                  <Link to="/runs">
                    Full history
                    <ArrowRightIcon />
                  </Link>
                </Button>
              </div>
              {overview.data.recent_runs?.length ? (
                <RunsTable runs={overview.data.recent_runs} />
              ) : (
                <Empty
                  title="No runs recorded"
                  description={
                    overview.data.providers === 0
                      ? 'Start by configuring a source from an adapter template.'
                      : 'Your sources are ready. Start a preview to inspect data before publication.'
                  }
                  action={
                    overview.data.providers === 0 ? (
                      <Button asChild variant="outline">
                        <Link to="/providers/new">
                          <PlusIcon />
                          Configure a source
                        </Link>
                      </Button>
                    ) : (
                      <CollectDialog />
                    )
                  }
                />
              )}
            </section>
          </>
        )
      )}
      <section className="space-y-4 border-t border-border pt-6">
        <div className="flex flex-wrap items-start justify-between gap-6">
          <div>
            <h2 className="section-title">Service status</h2>
            <p className="mt-1.5 text-xs text-muted-foreground">
              Authenticated diagnostics for the run server.
            </p>
          </div>
          {health.isPending ? (
            <span role="status" className="text-xs text-muted-foreground">
              Checking…
            </span>
          ) : (
            health.data && (
              <dl className="flex flex-wrap gap-8 text-xs">
                <div>
                  <dt className="text-muted-foreground">PostgreSQL</dt>
                  <dd className="mt-2 flex items-center gap-2 text-primary">
                    <span className="size-1.5 rounded-full bg-current" />
                    {health.data.database === 'ok' ? 'Available' : health.data.database}
                  </dd>
                </div>
                <div>
                  <dt className="text-muted-foreground">Server version</dt>
                  <dd className="mono mt-2">{health.data.version}</dd>
                </div>
              </dl>
            )
          )}
        </div>
        <ErrorNotice error={health.error} retry={() => void health.refetch()} />
      </section>
      <div className="grid gap-6 border-t border-border pt-6 text-xs leading-6 text-muted-foreground md:grid-cols-2">
        <p>
          <span className="font-medium text-foreground">Preview without publication.</span> Inspect
          parsed samples before adding data to the published torrent dataset. Raw bodies are not
          stored.
        </p>
        <p>
          <span className="font-medium text-foreground">Resume after interruption.</span> Paused,
          cancelled, or failed runs can resume from their last validated point.
        </p>
      </div>
    </div>
  )
}
