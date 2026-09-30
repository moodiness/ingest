import { useRef, useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Link, useParams, useSearchParams } from 'react-router-dom'
import {
  ArrowRightIcon,
  DownloadSimpleIcon,
  MagnifyingGlassIcon,
  XIcon,
  FileTextIcon,
} from '@phosphor-icons/react'
import { api, ApiError, downloadBytes, params, rawPage } from '@/lib/api'
import type { Items, Page, ProviderSummary, RawDetail, RawRecord, Torrent } from '@/lib/types'
import { useDate, number } from '@/lib/format'
import { normalizePageOffset, usePageSize } from '@/lib/display-preferences'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Badge } from '@/components/ui/badge'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import {
  Table,
  TableHeader,
  TableRow,
  TableHead,
  TableBody,
  TableCell,
} from '@/components/ui/table'
import { CodeEditor } from '@/components/code-editor'
import { Empty, ErrorNotice, Loading, PageHeader, Pager } from '@/components/common'
import { TorrentSearchForm, SavedTorrentViews } from '@/components/torrent-filters'
import { OccurrenceDialog, TorrentTable } from '@/components/torrent-history'
import { readTorrentFilters, torrentFilterParams, type TorrentFilters } from '@/lib/search-types'
import { decodeSearchJSON, exactField, exactJSON } from '@/lib/search-json'

function BrowseFilters({
  provider,
  q,
  onFilter,
  hideProvider = false,
}: {
  provider: string
  q: string
  onFilter: (provider: string, q: string) => void
  hideProvider?: boolean
}) {
  const providers = useQuery({
    queryKey: ['providers'],
    queryFn: ({ signal }) => api<Items<ProviderSummary>>('/providers', { signal }),
    enabled: !hideProvider,
  })
  return (
    <div className="space-y-3">
      <form
        className="toolbar"
        onSubmit={(event) => {
          event.preventDefault()
          const values = new FormData(event.currentTarget)
          onFilter(String(values.get('provider') ?? provider), String(values.get('q') ?? '').trim())
        }}
      >
        <div className="relative min-w-48 flex-1 sm:max-w-md">
          <Label htmlFor={hideProvider ? 'raw-search' : 'library-search'} className="sr-only">
            Search
          </Label>
          <MagnifyingGlassIcon
            className="pointer-events-none absolute left-3 top-2.5 text-muted-foreground"
            size={16}
          />
          <Input
            key={q}
            id={hideProvider ? 'raw-search' : 'library-search'}
            name="q"
            defaultValue={q}
            placeholder="Search data…"
            className="pl-9"
          />
        </div>
        {!hideProvider && (
          <>
            <Label htmlFor="library-provider" className="sr-only">
              Source
            </Label>
            <select
              id="library-provider"
              name="provider"
              className="native-select min-w-44"
              key={provider}
              defaultValue={provider}
            >
              <option value="">All sources</option>
              {provider && (
                <option value={provider}>
                  {providers.data?.items?.find((item) => item.id === provider)?.name || provider}
                </option>
              )}
              {providers.data?.items?.map((item) =>
                item.id === provider ? null : (
                  <option key={item.id} value={item.id}>
                    {item.name || item.id}
                  </option>
                ),
              )}
            </select>
          </>
        )}
        <Button variant="outline" type="submit">
          Search
        </Button>
        {(q || provider) && (
          <Button
            variant="ghost"
            size="icon-sm"
            type="button"
            aria-label="Clear filters"
            onClick={() => onFilter('', '')}
          >
            <XIcon />
          </Button>
        )}
      </form>
      <ErrorNotice error={providers.error} retry={() => void providers.refetch()} />
    </div>
  )
}

function DownloadButton({
  path,
  filename,
  label,
}: {
  path: string
  filename: string
  label: string
}) {
  const download = useMutation({ mutationFn: () => downloadBytes(path, filename) })
  return (
    <div className="space-y-2">
      <Button variant="outline" disabled={download.isPending} onClick={() => download.mutate()}>
        <DownloadSimpleIcon />
        {download.isPending ? 'Downloading…' : label}
      </Button>
      <ErrorNotice error={download.error} />
    </div>
  )
}

export function TorrentsPage() {
  const returnFocus = useRef<HTMLElement | null>(null)
  const [search, setSearch] = useSearchParams()
  const filters = readTorrentFilters(search)
  const filterKey = torrentFilterParams(filters).toString()
  const explicitLimit = search.get('limit')
  const limit = usePageSize(explicitLimit)
  const offset = normalizePageOffset(search.get('offset'), limit)
  const occurrenceId = search.get('occurrence') ?? ''
  const [selected, setSelected] = useState<Torrent | null>(null)
  const query = torrentFilterParams(filters)
  query.set('limit', String(limit))
  query.set('offset', String(offset))
  const torrents = useQuery({
    queryKey: ['torrents', query.toString()],
    queryFn: ({ signal }) =>
      api<Page<Torrent>>(`/torrents?${query}`, { signal }, decodeSearchJSON<Page<Torrent>>),
  })
  const current =
    torrents.data?.items?.find((item) => item.occurrence_id === occurrenceId) ??
    (selected?.occurrence_id === occurrenceId ? selected : undefined)
  const hasFilters = Object.entries(filters).some(([key, value]) =>
    key === 'sort' ? value !== 'recent' : Array.isArray(value) ? value.length > 0 : Boolean(value),
  )
  function applyFilters(nextFilters: TorrentFilters) {
    const next = torrentFilterParams(nextFilters)
    if (explicitLimit) next.set('limit', explicitLimit)
    setSearch(next)
  }
  function openOccurrence(torrent: Torrent, target: HTMLElement) {
    if (!occurrenceId) returnFocus.current = target
    setSelected(torrent)
    const next = new URLSearchParams(search)
    next.set('occurrence', torrent.occurrence_id)
    next.delete('occurrence_tab')
    next.delete('history_offset')
    next.delete('related_offset')
    setSearch(next)
  }
  return (
    <div className="page">
      <PageHeader
        title="Torrents"
        description="Published source occurrences. Every record stays independent, even when info hashes match. Previews never publish; Full publishes only after completion."
      />
      <TorrentSearchForm key={filterKey} filters={filters} onApply={applyFilters} />
      <SavedTorrentViews filters={filters} onApply={applyFilters} />
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="help" role="status">
          {torrents.isFetching && !torrents.isPending
            ? 'Refreshing results…'
            : 'All matching occurrences are shown separately.'}
        </p>
        <div className="flex items-center gap-2">
          <Label htmlFor="torrent-page-size" className="text-xs">
            Per page
          </Label>
          <select
            id="torrent-page-size"
            className="native-select"
            value={limit}
            onChange={(event) => {
              const next = new URLSearchParams(search)
              next.set('limit', event.target.value)
              next.delete('offset')
              setSearch(next)
            }}
          >
            {[25, 50, 100].map((value) => (
              <option key={value} value={value}>
                {value}
              </option>
            ))}
          </select>
        </div>
      </div>
      <ErrorNotice error={torrents.error} retry={() => void torrents.refetch()} />
      {torrents.isPending ? (
        <Loading label="Searching published occurrences" />
      ) : (
        torrents.data && (
          <>
            {torrents.data.items?.length ? (
              <TorrentTable items={torrents.data.items} onOpen={openOccurrence} />
            ) : (
              <Empty
                title={offset ? 'No torrents on this page' : 'No torrents in this view'}
                description={
                  hasFilters || offset
                    ? 'Change the combined filters or return to the first page.'
                    : 'Incremental runs and completed full runs populate this dataset. Previews leave it unchanged.'
                }
                action={
                  hasFilters || offset ? (
                    <Button variant="outline" onClick={() => applyFilters({})}>
                      Clear filters
                    </Button>
                  ) : (
                    <Button variant="outline" asChild>
                      <Link to="/providers">Choose a source</Link>
                    </Button>
                  )
                }
              />
            )}
            <Pager
              total={torrents.data.total}
              limit={torrents.data.limit}
              offset={torrents.data.offset}
              busy={torrents.isFetching}
              onChange={(value) => {
                const next = new URLSearchParams(search)
                next.set('offset', String(value))
                setSearch(next)
              }}
            />
          </>
        )
      )}
      {occurrenceId && (
        <OccurrenceDialog
          key={occurrenceId}
          id={occurrenceId}
          torrent={current}
          onOpen={openOccurrence}
          onClose={() => {
            const next = new URLSearchParams(search)
            for (const key of ['occurrence', 'occurrence_tab', 'history_offset', 'related_offset'])
              next.delete(key)
            setSearch(next)
            setSelected(null)
          }}
          onReturnFocus={() => {
            if (returnFocus.current?.isConnected) returnFocus.current.focus()
            else document.getElementById('torrent-search')?.focus()
          }}
        />
      )}
    </div>
  )
}

export function RawTableView({ runId }: { runId?: string }) {
  const date = useDate()
  const [search, setSearch] = useSearchParams()
  const provider = runId ? '' : (search.get('provider') ?? '')
  const qKey = runId ? 'raw_q' : 'q'
  const offsetKey = runId ? 'raw_offset' : 'offset'
  const q = search.get(qKey) ?? ''
  const limit = usePageSize()
  const offset = normalizePageOffset(search.get(offsetKey), limit)
  const run = runId ?? search.get('run') ?? ''
  const records = useQuery({
    queryKey: ['raw', { run, provider, q, offset, limit }],
    queryFn: ({ signal }) =>
      api<Page<RawRecord>>(
        `/raw?${params({ run, provider, q, limit, offset })}`,
        { signal },
        decodeSearchJSON<Page<RawRecord>>,
      ),
  })
  return (
    <div className="space-y-5">
      <BrowseFilters
        provider={provider}
        q={q}
        hideProvider={Boolean(runId)}
        onFilter={(nextProvider, nextQ) => {
          const next = new URLSearchParams(search)
          next.delete(offsetKey)
          nextQ ? next.set(qKey, nextQ) : next.delete(qKey)
          if (!runId) nextProvider ? next.set('provider', nextProvider) : next.delete('provider')
          setSearch(next)
        }}
      />
      {run && !runId && (
        <div className="toolbar text-xs">
          <span>
            Run :{' '}
            <Link className="data-link font-mono" to={`/runs/${encodeURIComponent(run)}`}>
              {run}
            </Link>
          </span>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="Remove run filter"
            onClick={() => {
              const next = new URLSearchParams(search)
              next.delete('run')
              next.delete('offset')
              setSearch(next)
            }}
          >
            <XIcon />
          </Button>
        </div>
      )}
      <ErrorNotice error={records.error} retry={() => void records.refetch()} />
      {records.isPending ? (
        <Loading />
      ) : (
        records.data && (
          <>
            {records.data.items?.length ? (
              <div className="table-frame">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Record / title</TableHead>
                      {!runId && <TableHead>Source</TableHead>}
                      <TableHead>Format</TableHead>
                      <TableHead>Processing</TableHead>
                      <TableHead>Page</TableHead>
                      <TableHead>Observed</TableHead>
                      <TableHead>
                        <span className="sr-only">View</span>
                      </TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {records.data.items.map((record) => (
                      <TableRow key={record.id}>
                        <TableCell className="min-w-52 max-w-xl whitespace-normal">
                          <Link
                            className="font-medium leading-6 hover:text-primary"
                            to={`/raw/${record.id}`}
                          >
                            {record.fields?.title != null
                              ? exactField(record.fields.title)
                              : record.source_id || 'Source ID missing'}
                          </Link>
                          <p className="mono mt-1 text-muted-foreground">
                            Observation #{record.id}
                            {record.source_id && ` · ${record.source_id}`}
                          </p>
                          {record.payload_retained === false && (
                            <p className="mt-1 text-xs text-muted-foreground">
                              Raw payload not stored
                            </p>
                          )}
                        </TableCell>
                        {!runId && (
                          <TableCell className="text-xs">
                            <Link
                              className="data-link"
                              to={`/raw?provider=${encodeURIComponent(record.provider_id)}`}
                            >
                              {record.provider_id}
                            </Link>
                          </TableCell>
                        )}
                        <TableCell className="font-mono text-[11px] text-muted-foreground">
                          {record.content_type || 'Not specified'}
                        </TableCell>
                        <TableCell>
                          {record.error ? (
                            <Badge
                              variant="outline"
                              className="border-destructive/30 text-destructive"
                            >
                              Error
                            </Badge>
                          ) : record.auxiliary ? (
                            <Badge variant="outline">Enrichment</Badge>
                          ) : record.ignored ? (
                            <Badge variant="outline">Ignored</Badge>
                          ) : (
                            <span className="text-xs text-muted-foreground">Accepted</span>
                          )}
                        </TableCell>
                        <TableCell className="font-mono text-xs">
                          {record.page_id > 0 && record.payload_retained !== false ? (
                            <Link className="data-link" to={`/pages/${record.page_id}`}>
                              {number(record.page)}
                            </Link>
                          ) : (
                            number(record.page)
                          )}
                        </TableCell>
                        <TableCell className="text-xs text-muted-foreground">
                          {date(record.created_at)}
                        </TableCell>
                        <TableCell>
                          <Button variant="ghost" size="icon-sm" asChild>
                            <Link to={`/raw/${record.id}`} aria-label={`View record ${record.id}`}>
                              <ArrowRightIcon />
                            </Link>
                          </Button>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            ) : (
              <Empty
                title="No observations in this view"
                description={
                  q || provider
                    ? 'Change the filters to broaden your search.'
                    : 'Observations will appear as responses are processed, including parsed Preview samples.'
                }
              />
            )}
            <Pager
              total={records.data.total}
              limit={records.data.limit}
              offset={records.data.offset}
              busy={records.isFetching}
              onChange={(value) => {
                const next = new URLSearchParams(search)
                next.set(offsetKey, String(value))
                setSearch(next)
              }}
            />
          </>
        )
      )}
    </div>
  )
}

export function RawListPage() {
  return (
    <div className="page">
      <PageHeader
        title="Observations and archives"
        description="Compact collection observations and parsed Preview samples. New raw bodies and duplicate field snapshots are not stored; historical archives remain available."
      />
      <RawTableView />
    </div>
  )
}

export function RawDetailPage() {
  const date = useDate()
  const { id = '' } = useParams()
  const detail = useQuery({
    queryKey: ['raw-record', id],
    queryFn: ({ signal }) =>
      api<RawDetail>(`/raw/${encodeURIComponent(id)}`, { signal }, decodeSearchJSON<RawDetail>),
  })
  if (detail.isPending)
    return (
      <div className="page">
        <Loading />
      </div>
    )
  if (!detail.data)
    return (
      <div className="page">
        <PageHeader title="Raw record" back={{ to: '/raw', label: 'All archives' }} />
        <ErrorNotice error={detail.error} retry={() => void detail.refetch()} />
      </div>
    )
  const { record, raw, byte_length: byteLength } = detail.data
  const payloadRetained = record.payload_retained !== false
  const language = record.content_type.includes('json')
    ? 'json'
    : record.content_type.includes('xml') || raw?.trimStart().startsWith('<')
      ? 'xml'
      : 'text'
  return (
    <div className="page">
      <PageHeader
        title={`Observation #${record.id}`}
        description={`${record.provider_id} · ${record.source_id || 'Source ID missing'}`}
        back={{ to: `/runs/${encodeURIComponent(record.run_id)}?tab=raw`, label: 'Run archives' }}
        actions={
          payloadRetained && (
            <DownloadButton
              path={`/raw/${record.id}/download`}
              filename={`record-${record.id}.${language === 'text' ? 'bin' : language}`}
              label="Download original"
            />
          )
        }
      />
      <ErrorNotice error={detail.error} retry={() => void detail.refetch()} />
      {record.error && <ErrorNotice error={new Error(record.error)} />}
      {!payloadRetained && (
        <p className="notice">
          Raw payload not stored. New response bodies and original record bytes are not retained.
          Catalogue data, processing results and resume points remain; historical archives are
          unchanged.
        </p>
      )}
      {record.auxiliary && (
        <p className="notice">
          Secondary metadata observation. It preserves processing provenance and does not represent
          an additional torrent.
        </p>
      )}
      {record.ignored && (
        <p className="notice">
          Record ignored for publication; its processing result is preserved.
        </p>
      )}
      <dl className="grid gap-5 border-y border-border py-5 text-sm sm:grid-cols-2 xl:grid-cols-4">
        <div>
          <dt className="text-xs text-muted-foreground">Received format</dt>
          <dd className="mono mt-2 break-all">{record.content_type || 'Not specified'}</dd>
        </div>
        <div>
          <dt className="text-xs text-muted-foreground">Original size</dt>
          <dd className="mt-2">
            {payloadRetained && byteLength !== null ? `${number(byteLength)} bytes` : 'Not stored'}
          </dd>
        </div>
        <div>
          <dt className="text-xs text-muted-foreground">Observed</dt>
          <dd className="mt-2">{date(record.created_at)}</dd>
        </div>
        <div>
          <dt className="text-xs text-muted-foreground">Source page</dt>
          <dd className="mt-2">
            {record.page_id > 0 && payloadRetained ? (
              <Link
                className="data-link inline-flex items-center gap-2"
                to={`/pages/${record.page_id}`}
              >
                <FileTextIcon />
                Page {record.page} · #{record.page_id}
              </Link>
            ) : record.page_id > 0 ? (
              `Page ${record.page} · response not stored`
            ) : (
              'Unavailable'
            )}
          </dd>
        </div>
      </dl>
      <Tabs defaultValue={payloadRetained ? 'original' : 'fields'}>
        <TabsList>
          <TabsTrigger value="original">Original content</TabsTrigger>
          <TabsTrigger value="fields">Interpreted fields</TabsTrigger>
        </TabsList>
        <TabsContent value="original" className="mt-5 space-y-3">
          {payloadRetained ? (
            <>
              <p className="help">
                Text view without reformatting. The download preserves the original bytes, including
                non-text data.
              </p>
              <CodeEditor
                value={raw ?? ''}
                language={language}
                label="Original record content"
                height="600px"
                readOnly
              />
            </>
          ) : (
            <p className="notice">Original record content was not stored for this observation.</p>
          )}
        </TabsContent>
        <TabsContent value="fields" className="mt-5 space-y-3">
          {record.fields == null || Object.keys(record.fields).length === 0 ? (
            <p className="notice">
              No parsed fields are stored with this observation. Full, Incremental and Metadata keep
              fields in the catalogue or private Full staging, not duplicate snapshots here. Preview
              keeps its parsed sample here without publication.
            </p>
          ) : (
            <>
              <p className="help">
                Parsed fields are separate from raw payloads. Missing values are not replaced by
                zero.
              </p>
              <CodeEditor
                value={exactJSON(record.fields)}
                language="json"
                label="Interpreted record fields"
                height="460px"
                readOnly
              />
            </>
          )}
        </TabsContent>
      </Tabs>
      {record.page_id > 0 && payloadRetained && (
        <section className="flex flex-wrap items-center justify-between gap-4 border-t border-border pt-5">
          <div>
            <h2 className="section-title">Complete provider response</h2>
            <p className="help mt-1">The page contains this record’s original context.</p>
          </div>
          <div className="toolbar">
            <Button variant="ghost" asChild>
              <Link to={`/pages/${record.page_id}`}>
                View page
                <ArrowRightIcon />
              </Link>
            </Button>
            <DownloadButton
              path={`/pages/${record.page_id}/download`}
              filename={`page-${record.page_id}.bin`}
              label="Download page"
            />
          </div>
        </section>
      )}
    </div>
  )
}

export function RawPageView() {
  const { id = '' } = useParams()
  const page = useQuery({
    queryKey: ['raw-page', id],
    queryFn: ({ signal }) => rawPage(id, signal),
    staleTime: Infinity,
  })
  const text = page.data?.text ?? ''
  const trimmed = text.trimStart()
  const language =
    page.data?.contentType.includes('json') || trimmed.startsWith('{') || trimmed.startsWith('[')
      ? 'json'
      : page.data?.contentType.includes('xml') || trimmed.startsWith('<')
        ? 'xml'
        : 'text'
  return (
    <div className="page">
      <PageHeader
        title={`Original page #${id}`}
        description="Historical response archive. New response bodies are not stored."
        back={{ to: '/raw', label: 'All archives' }}
        actions={
          page.data && (
            <DownloadButton
              path={`/pages/${encodeURIComponent(id)}/download`}
              filename={`page-${id}.bin`}
              label="Download page"
            />
          )
        }
      />
      {page.error instanceof ApiError && page.error.status === 404 ? (
        <p className="notice">
          Response unavailable. New response bodies are not stored; historical archived responses
          remain available when their page exists.
        </p>
      ) : (
        <ErrorNotice error={page.error} retry={() => void page.refetch()} />
      )}
      {page.isPending ? (
        <Loading />
      ) : (
        page.data && (
          <>
            <div className="notice flex flex-wrap items-center justify-between gap-3">
              <span>{number(page.data.byteLength)} original bytes</span>
              <span className="text-xs text-muted-foreground">
                UTF-8 display · no HTML executed · exact download
              </span>
            </div>
            <CodeEditor
              value={page.data.text}
              language={language}
              label="Complete original provider response"
              readOnly
              height="650px"
            />
          </>
        )
      )}
    </div>
  )
}
