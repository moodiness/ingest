import { useRef, useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { api, queryClient } from '@/lib/api'
import type { Items, ProviderSummary } from '@/lib/types'
import {
  torrentFilterKeys,
  torrentSortOptions,
  type SavedView,
  type TorrentFilters,
} from '@/lib/search-types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ConfirmAction, ErrorNotice, Loading } from '@/components/common'

const rangeFields = [
  ['min_size', 'Minimum size (bytes)', '0'],
  ['max_size', 'Maximum size (bytes)', 'No maximum'],
  ['min_seeders', 'Minimum seeders', '0'],
  ['max_seeders', 'Maximum seeders', 'No maximum'],
] as const

export function TorrentSearchForm({
  filters,
  onApply,
}: {
  filters: TorrentFilters
  onApply: (filters: TorrentFilters) => void
}) {
  const providers = useQuery({
    queryKey: ['providers'],
    queryFn: ({ signal }) => api<Items<ProviderSummary>>('/providers', { signal }),
  })
  const [error, setError] = useState<Error | null>(null)
  const dateInputs = useRef<Record<string, { input: HTMLInputElement; value: string }>>({})
  const choices = new Map((filters.providers ?? []).map((id) => [id, id]))
  for (const provider of providers.data?.items ?? [])
    choices.set(provider.id, provider.name || provider.id)
  const advanced = torrentFilterKeys.some((key) => !['q', 'sort'].includes(key) && filters[key])
  return (
    <form
      className="space-y-4"
      onSubmit={(event) => {
        event.preventDefault()
        setError(null)
        const data = new FormData(event.currentTarget)
        const next: TorrentFilters = {}
        for (const key of torrentFilterKeys) {
          const value = String(data.get(key) ?? '').trim()
          if (value) Object.assign(next, { [key]: value })
        }
        next.providers = data.getAll('provider').map(String)
        for (const key of ['published_after', 'published_before'] as const) {
          const initial = dateInputs.current[key]
          // Compare the browser's actual initial value, which may normalize or
          // truncate the displayed timestamp, not the original RFC3339 string.
          if (
            filters[key] &&
            initial &&
            initial.input.value === initial.value &&
            !Number.isNaN(new Date(filters[key]).getTime())
          ) {
            next[key] = filters[key]
            continue
          }
          if (next[key]) {
            const parsed = new Date(next[key])
            if (Number.isNaN(parsed.getTime())) {
              setError(new Error('Enter a valid publication date and time.'))
              return
            }
            next[key] = parsed.toISOString()
          }
        }
        onApply(next)
      }}
    >
      <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_13rem_auto] sm:items-end">
        <div className="field">
          <Label htmlFor="torrent-search">Full-text search</Label>
          <Input
            id="torrent-search"
            name="q"
            defaultValue={filters.q ?? ''}
            placeholder='Words, "exact phrase", or -excluded'
          />
        </div>
        <div className="field">
          <Label htmlFor="torrent-sort">Sort results</Label>
          <select
            id="torrent-sort"
            name="sort"
            className="native-select w-full"
            defaultValue={filters.sort ?? 'recent'}
          >
            {filters.sort && !torrentSortOptions.some((item) => item.value === filters.sort) && (
              <option value={filters.sort}>{filters.sort}</option>
            )}
            {torrentSortOptions.map((item) => (
              <option key={item.value} value={item.value}>
                {item.label}
              </option>
            ))}
          </select>
        </div>
        <Button type="submit">Search</Button>
      </div>
      <p className="help">
        Full-text search covers titles, info hashes, provider IDs and source IDs, using up to 128
        KiB of indexed text per occurrence. Original values stay intact. Category, date and numeric
        filters are independent of this text limit.
      </p>
      <details
        className="rounded-md border border-border"
        open={advanced || Boolean(filters.providers?.length) || undefined}
      >
        <summary className="cursor-pointer px-4 py-3 text-sm font-medium">Combined filters</summary>
        <div className="space-y-5 border-t border-border p-4">
          <fieldset className="space-y-3">
            <legend className="text-sm font-medium">Sources</legend>
            <p className="help">
              Select any number of sources. None selected includes all sources.
            </p>
            {providers.isPending && (
              <p className="help" role="status">
                Loading available sources…
              </p>
            )}
            <ErrorNotice error={providers.error} retry={() => void providers.refetch()} />
            <div className="grid max-h-56 gap-3 overflow-y-auto sm:grid-cols-2 xl:grid-cols-3">
              {[...choices].map(([id, name]) => (
                <label key={id} className="flex min-w-0 cursor-pointer items-start gap-2 text-sm">
                  <input
                    type="checkbox"
                    className="mt-1 size-4 shrink-0 accent-primary"
                    name="provider"
                    value={id}
                    defaultChecked={filters.providers?.includes(id)}
                  />
                  <span className="min-w-0 break-words">
                    {name}
                    {name !== id && (
                      <span className="mono block break-all text-muted-foreground">{id}</span>
                    )}
                  </span>
                </label>
              ))}
            </div>
            {!providers.isPending && !providers.error && choices.size === 0 && (
              <p className="help">No sources are configured yet.</p>
            )}
          </fieldset>
          <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
            <div className="field sm:col-span-2">
              <Label htmlFor="torrent-category">Category</Label>
              <Input
                id="torrent-category"
                name="category"
                defaultValue={filters.category ?? ''}
                placeholder="Exact category, ignoring case"
              />
            </div>
            <div className="field sm:col-span-2">
              <Label htmlFor="torrent-info-hash">Info hash</Label>
              <Input
                id="torrent-info-hash"
                name="info_hash"
                defaultValue={filters.info_hash ?? ''}
                placeholder="Exact info hash"
                className="font-mono"
              />
            </div>
            {rangeFields.map(([key, label, placeholder]) => (
              <div className="field" key={key}>
                <Label htmlFor={`torrent-${key}`}>{label}</Label>
                <Input
                  id={`torrent-${key}`}
                  name={key}
                  defaultValue={filters[key] ?? ''}
                  inputMode="numeric"
                  pattern="[0-9]*"
                  title="Enter a nonnegative whole number without separators."
                  placeholder={placeholder}
                />
              </div>
            ))}
            {(['published_after', 'published_before'] as const).map((key) => {
              const value = filters[key]
              const timestamp = value ? new Date(value) : undefined
              const valid = timestamp && !Number.isNaN(timestamp.getTime())
              const local = valid
                ? new Date(timestamp.getTime() - timestamp.getTimezoneOffset() * 60000)
                    .toISOString()
                    .slice(0, -1)
                : ''
              return (
                <div className="field sm:col-span-2" key={key}>
                  <Label htmlFor={`torrent-${key}`}>
                    {key === 'published_after' ? 'Published after' : 'Published before'} (browser
                    local time)
                  </Label>
                  <Input
                    id={`torrent-${key}`}
                    name={key}
                    type="datetime-local"
                    step="0.001"
                    defaultValue={local}
                    ref={(input) => {
                      if (input && dateInputs.current[key]?.input !== input) {
                        dateInputs.current[key] = { input, value: input.value }
                      }
                    }}
                  />
                  {value && !valid && (
                    <p className="text-xs text-destructive">
                      Invalid date in URL. Choose a valid date or clear filters.
                    </p>
                  )}
                </div>
              )
            })}
          </div>
          <p className="help">
            All filters are combined on the server. Size and seeder bounds are exact integers;
            missing or invalid mapped values do not match those bounds. Date inputs use browser
            local time, not the display timezone, and are edited to milliseconds; unchanged bounds
            retain their full precision.
          </p>
          <div className="flex flex-wrap gap-2">
            <Button type="submit" variant="outline">
              Apply filters
            </Button>
            <Button type="button" variant="ghost" onClick={() => onApply({})}>
              Clear all
            </Button>
          </div>
        </div>
      </details>
      <ErrorNotice error={error} />
    </form>
  )
}

export function SavedTorrentViews({
  filters,
  onApply,
}: {
  filters: TorrentFilters
  onApply: (filters: TorrentFilters) => void
}) {
  const views = useQuery({
    queryKey: ['saved-views'],
    queryFn: ({ signal }) => api<Items<SavedView>>('/saved-views', { signal }),
  })
  const [selectedId, setSelectedId] = useState('')
  const [name, setName] = useState('')
  const [message, setMessage] = useState('')
  const selected = views.data?.items?.find((view) => view.id === selectedId)
  const save = useMutation({
    mutationFn: (update: boolean) => {
      if (!name.trim()) throw new Error('Enter a name for this saved view.')
      if (update && !selected) throw new Error('Select an existing saved view first.')
      return api<SavedView>(
        update ? `/saved-views/${encodeURIComponent(selected!.id)}` : '/saved-views',
        {
          method: update ? 'PUT' : 'POST',
          body: JSON.stringify({
            name: name.trim(),
            filters,
            ...(update ? { revision: selected!.revision } : {}),
          }),
        },
      )
    },
    onSuccess: (saved) => {
      queryClient.setQueryData<Items<SavedView>>(['saved-views'], (old) => ({
        items: [...(old?.items ?? []).filter((item) => item.id !== saved.id), saved],
      }))
      setSelectedId(saved.id)
      setName(saved.name)
      setMessage('Saved the currently applied search filters.')
      void queryClient.invalidateQueries({ queryKey: ['saved-views'] })
    },
  })
  return (
    <details className="rounded-md border border-border">
      <summary className="cursor-pointer px-4 py-3 text-sm font-medium">Saved views</summary>
      <div className="space-y-4 border-t border-border p-4">
        <p className="help">
          Save the applied search, not unsent form changes. Views store filters and sorting, never a
          fixed list of records.
        </p>
        <ErrorNotice error={views.error} retry={() => void views.refetch()} />
        {views.isPending ? (
          <Loading label="Loading saved views" />
        ) : (
          views.data && (
            <>
              <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_auto_auto] sm:items-end">
                <div className="field">
                  <Label htmlFor="saved-torrent-view">Saved view</Label>
                  <select
                    id="saved-torrent-view"
                    className="native-select w-full"
                    value={selectedId}
                    disabled={save.isPending}
                    onChange={(event) => {
                      setSelectedId(event.target.value)
                      setName(
                        views.data?.items?.find((item) => item.id === event.target.value)?.name ??
                          '',
                      )
                      setMessage('')
                      save.reset()
                    }}
                  >
                    <option value="">New saved view</option>
                    {views.data.items?.map((view) => (
                      <option key={view.id} value={view.id}>
                        {view.name}
                      </option>
                    ))}
                  </select>
                </div>
                <Button
                  variant="outline"
                  disabled={!selected || save.isPending}
                  onClick={() => {
                    if (selected) {
                      onApply(selected.filters)
                      setMessage(`Applied “${selected.name}”.`)
                    }
                  }}
                >
                  Apply view
                </Button>
                <ConfirmAction
                  title="Delete this saved view?"
                  description={`Delete “${selected?.name ?? ''}”? The records and currently applied filters will not be changed.`}
                  label="Delete view"
                  destructive
                  trigger={
                    <Button variant="ghost" disabled={!selected || save.isPending}>
                      Delete
                    </Button>
                  }
                  action={async () => {
                    if (!selected) return
                    await api<void>(`/saved-views/${encodeURIComponent(selected.id)}`, {
                      method: 'DELETE',
                      body: JSON.stringify({ revision: selected.revision }),
                    })
                    queryClient.setQueryData<Items<SavedView>>(['saved-views'], (old) => ({
                      items: old?.items?.filter((item) => item.id !== selected.id) ?? [],
                    }))
                    setSelectedId('')
                    setName('')
                    setMessage('Saved view deleted. Published records are unchanged.')
                    await queryClient.invalidateQueries({ queryKey: ['saved-views'] })
                  }}
                />
              </div>
              {!views.data.items?.length && (
                <p className="help">
                  No saved views yet. Apply a search, name it below, and save it.
                </p>
              )}
            </>
          )
        )}
        <form
          className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_auto_auto] sm:items-end"
          onSubmit={(event) => {
            event.preventDefault()
            setMessage('')
            save.mutate(false)
          }}
        >
          <div className="field">
            <Label htmlFor="saved-view-name">View name</Label>
            <Input
              id="saved-view-name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              maxLength={120}
              required
              disabled={save.isPending}
              placeholder="Name this search"
            />
          </div>
          <Button type="submit" variant="outline" disabled={save.isPending || !name.trim()}>
            {save.isPending ? 'Saving…' : 'Save as new'}
          </Button>
          <Button
            type="button"
            disabled={!selected || !name.trim() || save.isPending}
            onClick={() => {
              setMessage('')
              save.mutate(true)
            }}
          >
            Update selected
          </Button>
        </form>
        <ErrorNotice error={save.error} retry={() => void views.refetch()} />
        {message && (
          <p role="status" className="help">
            {message}
          </p>
        )}
      </div>
    </details>
  )
}
