import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  FingerprintIcon,
  GlobeIcon,
  RowsIcon,
  ArrowsLeftRightIcon,
  TimerIcon,
  BracketsCurlyIcon,
  FlowArrowIcon,
} from '@phosphor-icons/react'
import { parseProviderJSON, type ProviderJSON } from '@/lib/provider-json'
import { Button } from '@/components/ui/button'
import { CodeEditor } from '@/components/code-editor'
import {
  AdvancedSection,
  ArrayField,
  BooleanField,
  FieldGrid,
  FieldSection,
  JSONField,
  MapField,
  SelectField,
  SourceEditorContext,
  StringArrayField,
  TextField,
  useSourceEditor,
} from '@/components/source-editor-fields'

const stages = [
  {
    id: 'identity',
    label: 'Identity',
    icon: FingerprintIcon,
    description: 'Name the source and select its adapter.',
  },
  {
    id: 'connection',
    label: 'Connection',
    icon: GlobeIcon,
    description: 'Define the endpoint, authentication and requests.',
  },
  {
    id: 'pagination',
    label: 'Pagination',
    icon: RowsIcon,
    description: 'Control how the collector advances through a listing.',
  },
  {
    id: 'mapping',
    label: 'Mapping',
    icon: ArrowsLeftRightIcon,
    description: 'Translate source records into catalogue fields.',
  },
  {
    id: 'collection',
    label: 'Collection',
    icon: TimerIcon,
    description: 'Set schedules, output and bounded traversal.',
  },
] as const

function IdentitySettings({ sourceID }: { sourceID?: string }) {
  return (
    <FieldSection
      title="Source identity"
      description="The saved identifier links runs, schedules and history. Changing settings never rewrites existing run snapshots."
    >
      <FieldGrid>
        <TextField path={['name']} label="Display name" placeholder="Archive name" />
        <TextField
          path={['id']}
          label="Source identifier"
          help={
            sourceID
              ? 'Keep this identifier stable. Invalid definitions can be repaired before saving.'
              : 'Use a unique, safe identifier such as archive-main.'
          }
        />
        <SelectField path={['adapter']} label="Adapter" choices={['http_json', 'torznab']} />
        <JSONField
          path={['version']}
          label="Definition version"
          integer
          help="Current definition version: 1."
        />
        <BooleanField
          path={['enabled']}
          label="Source enabled"
          required
          help="Allows manual and scheduled runs. Saving does not start collection."
        />
      </FieldGrid>
    </FieldSection>
  )
}

function ConnectionSettings() {
  return (
    <>
      <FieldSection
        title="Endpoint"
        description="Use credential-free URLs. Secrets are resolved from the vault when a run starts."
      >
        <TextField
          path={['url']}
          label="Source URL"
          placeholder="https://archive.example/api/items"
        />
        <FieldGrid>
          <SelectField
            path={['http', 'method']}
            label="HTTP method"
            choices={['GET', 'POST']}
            fallback="Default · GET"
          />
          <BooleanField
            path={['http', 'catalog']}
            label="Normalized catalogue"
            help="Enable only for a compatible catalogue endpoint, not an ordinary JSON listing."
          />
        </FieldGrid>
      </FieldSection>
      <FieldSection
        title="Authentication"
        description="Enter vault reference names, never passwords or API keys. Only fields required by the chosen authentication type should be set."
      >
        <FieldGrid>
          <SelectField
            path={['auth', 'type']}
            label="Authentication type"
            choices={['none', 'basic', 'bearer', 'cookie', 'query', 'header', 'api_key']}
            fallback="Default · none"
          />
          <TextField
            path={['auth', 'secret_ref']}
            label="Secret reference"
            help="Bearer, cookie, query, header or API key authentication."
          />
          <TextField
            path={['auth', 'username_ref']}
            label="Username reference"
            help="Basic authentication only."
          />
          <TextField
            path={['auth', 'password_ref']}
            label="Password reference"
            help="Basic authentication only."
          />
          <SelectField
            path={['auth', 'in']}
            label="Credential placement"
            choices={['header', 'query', 'bearer', 'cookie']}
            help="API keys require header or query; otherwise leave unset unless required by the adapter."
          />
          <TextField
            path={['auth', 'name']}
            label="Credential parameter name"
            help="For header, query and API key authentication."
          />
        </FieldGrid>
        <Link className="data-link inline-block text-xs" to="/secrets">
          Manage secret references
        </Link>
      </FieldSection>
      <FieldSection
        title="Request parameters & ordering"
        description="Full uses the base query. Incremental overlays its query values without changing Full ordering; use the source API’s actual sort parameter names."
      >
        <MapField
          path={['http', 'query']}
          label="Base query · Full and all modes"
          help="Shared filters and Full ordering. Values are strict JSON, including strings in quotes."
        />
        <MapField
          path={['http', 'incremental_query']}
          label="Incremental query overrides"
          help="Applied only during Incremental collection. Configure newest-first ordering independently from Full here."
        />
        <JSONField
          path={['http', 'body']}
          label="Request body · JSON"
          help="Optional POST body. Leave empty to omit. Arbitrary JSON and numeric precision are preserved."
          multiline
        />
      </FieldSection>
      <AdvancedSection
        title="Headers & search"
        description="Ordinary headers contain non-secret values. Secret headers map a header name to a vault reference."
      >
        <MapField path={['http', 'headers']} label="HTTP headers" strings />
        <MapField path={['http', 'secret_headers']} label="Secret header references" strings />
        <TextField path={['search', 'query']} label="Search query" />
        <ArrayField
          path={['search', 'categories']}
          label="Categories"
          initial={0}
          help="Numeric category identifiers, especially for Torznab."
        >
          {(path, index) => <JSONField path={path} label={`Category ${index + 1}`} integer />}
        </ArrayField>
      </AdvancedSection>
    </>
  )
}

function PaginationSettings() {
  return (
    <>
      <FieldSection
        title="Page strategy"
        description="These settings configure the existing collector. The stages are a fixed configuration sequence, not an executable graph."
      >
        <FieldGrid>
          <SelectField
            path={['pagination', 'type']}
            label="Pagination type"
            choices={['none', 'page', 'offset', 'cursor']}
            fallback="Default · none"
          />
          <SelectField
            path={['pagination', 'in']}
            label="Parameter location"
            choices={['query', 'body']}
            fallback="Default · query"
            help="Body pagination requires POST."
          />
          <JSONField
            path={['page_size']}
            label="Page size"
            integer
            help="Requested number of records per page."
          />
          <JSONField path={['pagination', 'start']} label="Starting page or offset" integer />
        </FieldGrid>
      </FieldSection>
      <FieldSection
        title="Request parameter names"
        description="Set names used by the selected strategy; unused parameter fields may remain empty."
      >
        <FieldGrid>
          <TextField
            path={['pagination', 'page_param']}
            label="Page parameter"
            placeholder="page"
          />
          <TextField
            path={['pagination', 'offset_param']}
            label="Offset parameter"
            placeholder="offset"
          />
          <TextField
            path={['pagination', 'cursor_param']}
            label="Cursor parameter"
            placeholder="cursor"
          />
          <TextField
            path={['pagination', 'size_param']}
            label="Page-size parameter"
            placeholder="limit"
          />
        </FieldGrid>
      </FieldSection>
      <FieldSection
        title="Response pointers"
        description="RFC 6901 JSON Pointers into the response. A pointer starts with /; an empty pointer selects the root."
      >
        <FieldGrid>
          <TextField
            path={['pagination', 'next_path']}
            label="Next page / cursor pointer"
            placeholder="/meta/next"
          />
          <TextField
            path={['pagination', 'total_path']}
            label="Total pointer"
            placeholder="/meta/total"
          />
          <TextField
            path={['pagination', 'current_path']}
            label="Current page pointer"
            placeholder="/meta/current_page"
          />
        </FieldGrid>
      </FieldSection>
    </>
  )
}

function MappingSettings() {
  return (
    <>
      <FieldSection
        title="Record selection"
        description="HTTP JSON mappings use JSON Pointers (RFC 6901), relative to the response or each item as noted. Torznab uses its native RSS mapping."
      >
        <FieldGrid>
          <TextField
            path={['http', 'items_path']}
            label="Items pointer"
            placeholder="/data"
            help="Array of records in the response. Empty selects the response root."
          />
          <TextField
            path={['mapping', 'id']}
            label="Native source ID pointer"
            placeholder="/id"
            help="Stable identity within this source, independent from the torrent hash."
          />
        </FieldGrid>
        <MapField
          path={['mapping', 'fields']}
          label="Field mappings"
          strings
          help="Map catalogue field names to pointers relative to each item, such as title → /name."
        />
      </FieldSection>
      <FieldSection
        title="Output selection"
        description="Output fields filter presentation, not stored catalogue metadata or historical archives. Leave empty to use the default output."
      >
        <StringArrayField
          path={['output', 'fields']}
          label="Output fields"
          help="Use field names or attributes.NAME selectors."
        />
      </FieldSection>
    </>
  )
}

function TraversalSettings() {
  const { document, change } = useSourceEditor()
  const configured =
    document.kindIn(['traversal']) !== undefined && document.kindIn(['traversal']) !== 'null'
  const metadataSupported =
    document.get('adapter') === 'http_json' &&
    !document.getIn(['http', 'catalog']) &&
    document.kindIn(['traversal', 'id_recovery']) === 'object' &&
    document.lengthIn(['traversal', 'enrich_fields']) > 0
  return (
    <AdvancedSection
      title="Bounded traversal"
      description="Advanced HTTP JSON collection across overlapping search windows. Configure only when a source has a bounded listing and advertised coverage totals."
    >
      {configured ? (
        <>
          <Button
            variant="outline"
            size="sm"
            onClick={() => change((next) => next.delete('traversal'))}
          >
            Remove traversal configuration
          </Button>
          <FieldGrid>
            <JSONField path={['traversal', 'window_pages']} label="Window pages" integer />
            <JSONField
              path={['traversal', 'minimum_total']}
              label="Minimum expected total"
              integer
            />
            <SelectField
              path={['traversal', 'total_mode']}
              label="Coverage total mode"
              choices={['strict', 'at_least']}
              fallback="Default · strict"
            />
            <SelectField
              path={['traversal', 'incremental_order']}
              label="Incremental listing order"
              choices={['id', 'published_at']}
              fallback="Default · id"
              help="ID order is checked strictly. Publication-sorted listings use known source IDs as the stopping boundary and tolerate date reordering."
            />
          </FieldGrid>
          <StringArrayField
            path={['traversal', 'total_paths']}
            label="Total pointers"
            help="Response pointers used to determine expected scope coverage."
          />
          <StringArrayField
            path={['traversal', 'enrich_fields']}
            label="Metadata enrichment fields"
            help="Fields filled by Metadata mode through ID recovery, not by discovery itself. An optional follow-up after Incremental can fill these fields for newly inserted torrents only."
          />
          <BooleanField
            path={['traversal', 'metadata_after_incremental']}
            label="Metadata after Incremental"
            allowEnabled={metadataSupported}
            help={
              metadataSupported
                ? 'Off by default. After a successful Incremental run, queue a separate Metadata run only for newly inserted native torrents still missing configured fields. Existing torrents encountered by Incremental are excluded; no eligible new torrents means no follow-up.'
                : 'Unavailable: requires native HTTP/JSON, ID recovery and configured metadata enrichment fields. Remote catalogues are not supported. Disable this option or configure these prerequisites; it will not enrich this source as configured.'
            }
          />
          <ArrayField
            path={['traversal', 'scopes']}
            label="Scopes"
            initial={{ id: '', match: {} }}
            help="Each matching scope tracks its own observed native IDs."
          >
            {(path) => (
              <>
                <TextField path={[...path, 'id']} label="Scope identifier" />
                <MapField path={[...path, 'query']} label="Scope query" />
                <MapField
                  path={[...path, 'match']}
                  label="Mapped field matches"
                  help="Scalar values to match against mapped fields."
                />
              </>
            )}
          </ArrayField>
          <ArrayField
            path={['traversal', 'partitions']}
            label="Partitions"
            initial={{ parameter: '', start: 0, end: 0 }}
            help="Inclusive ranges. Specify either an end value or an end-year offset, never both."
          >
            {(path) => (
              <FieldGrid>
                <TextField path={[...path, 'parameter']} label="Partition parameter" />
                <JSONField path={[...path, 'start']} label="Partition start" integer />
                <JSONField
                  path={[...path, 'end']}
                  label="Partition end"
                  integer
                  help="Clear when using an end-year offset."
                />
                <JSONField
                  path={[...path, 'end_year_offset']}
                  label="End-year offset"
                  integer
                  help="Clear when using an explicit end."
                />
              </FieldGrid>
            )}
          </ArrayField>
          <ArrayField path={['traversal', 'query_variants']} label="Query variants" initial={{}}>
            {(path) => <MapField path={path} label="Variant parameters" />}
          </ArrayField>
          <TraversalOptions />
          <RecoverySettings />
        </>
      ) : (
        <Button
          variant="outline"
          size="sm"
          onClick={() =>
            change((next) =>
              next.set('traversal', { window_pages: 10, total_paths: [], scopes: [] }),
            )
          }
        >
          Configure bounded traversal
        </Button>
      )}
    </AdvancedSection>
  )
}

function TraversalOptions() {
  const { document, change } = useSourceEditor()
  const configured =
    document.kindIn(['traversal', 'options']) !== undefined &&
    document.kindIn(['traversal', 'options']) !== 'null'
  return (
    <AdvancedSection
      title="Discovered filter options"
      description="Read available filters from an endpoint and use them to refine bounded queries. URL placeholders may reference scope query names."
    >
      {configured ? (
        <>
          <Button
            variant="outline"
            size="sm"
            onClick={() => change((next) => next.deleteIn(['traversal', 'options']))}
          >
            Remove discovered options
          </Button>
          <TextField path={['traversal', 'options', 'url']} label="Options URL" />
          <FieldGrid>
            <TextField path={['traversal', 'options', 'groups_path']} label="Groups pointer" />
            <TextField path={['traversal', 'options', 'values_path']} label="Values pointer" />
            <TextField path={['traversal', 'options', 'value_path']} label="Value pointer" />
            <TextField path={['traversal', 'options', 'query_param']} label="Query parameter" />
            <TextField path={['traversal', 'options', 'priority_path']} label="Priority pointer" />
          </FieldGrid>
        </>
      ) : (
        <Button
          variant="outline"
          size="sm"
          onClick={() =>
            change((next) =>
              next.setIn(['traversal', 'options'], {
                url: '',
                groups_path: '',
                values_path: '',
                value_path: '',
                query_param: '',
              }),
            )
          }
        >
          Configure discovered options
        </Button>
      )}
    </AdvancedSection>
  )
}

function RecoverySettings() {
  const { document, change } = useSourceEditor()
  const configured =
    document.kindIn(['traversal', 'id_recovery']) !== undefined &&
    document.kindIn(['traversal', 'id_recovery']) !== 'null'
  const base = ['traversal', 'id_recovery']
  return (
    <AdvancedSection
      title="ID recovery & detail mapping"
      description="Recover finite native-ID ranges and resolve same-origin detail URLs. These settings configure the existing recovery engine; no custom code is executed."
    >
      {configured ? (
        <>
          <Button variant="outline" size="sm" onClick={() => change((next) => next.deleteIn(base))}>
            Remove ID recovery
          </Button>
          <MapField path={[...base, 'discovery_query']} label="Discovery query" />
          <FieldGrid>
            <JSONField path={[...base, 'first']} label="First native ID" integer />
            <TextField
              path={[...base, 'resolve_url']}
              label="Resolve URL"
              help="Use {id} for the requested numeric ID."
            />
            <TextField path={[...base, 'resolve_pattern']} label="Resolve path pattern" />
            <TextField
              path={[...base, 'detail_url']}
              label="Detail URL"
              help="Use {value} for the resolved last path segment."
            />
            <TextField path={[...base, 'detail_path']} label="Detail record pointer" />
            <TextField path={[...base, 'mapping', 'id']} label="Detail native ID pointer" />
          </FieldGrid>
          <MapField path={[...base, 'mapping', 'fields']} label="Detail field mappings" strings />
        </>
      ) : (
        <Button
          variant="outline"
          size="sm"
          onClick={() =>
            change((next) =>
              next.setIn(base, {
                discovery_query: {},
                first: 1,
                resolve_url: '',
                resolve_pattern: '',
                detail_url: '',
                detail_path: '',
                mapping: { id: '', fields: {} },
              }),
            )
          }
        >
          Configure ID recovery
        </Button>
      )}
    </AdvancedSection>
  )
}

function RequestLimitsSettings() {
  const { document, change } = useSourceEditor()
  return (
    <AdvancedSection
      title="Rolling request budgets"
      description="Optional limits shared by all runs of this source, including retries. Each rolling window counts recent requests, not calendar resets. Request spacing still applies."
    >
      <FieldGrid>
        <JSONField
          path={['request_limits', 'per_minute']}
          label="Requests per minute"
          integer
          help="Previous 60 seconds. 0 or empty disables this window."
        />
        <JSONField
          path={['request_limits', 'per_hour']}
          label="Requests per hour"
          integer
          help="Previous 3,600 seconds. 0 or empty disables this window."
        />
        <JSONField
          path={['request_limits', 'per_day']}
          label="Requests per day"
          integer
          help="Previous 86,400 seconds. 0 or empty disables this window."
        />
      </FieldGrid>
      <p className="help">
        Use whole numbers up to 1,000,000. Keep at least one positive limit, or remove all budgets.
      </p>
      {document.kindIn(['request_limits']) !== undefined && (
        <Button
          variant="outline"
          size="sm"
          onClick={() => change((next) => next.delete('request_limits'))}
        >
          Remove all request budgets
        </Button>
      )}
    </AdvancedSection>
  )
}

function CollectionSettings() {
  return (
    <>
      <FieldSection
        title="Request pacing"
        description="Source-level settings apply to future runs. Existing run snapshots keep their original configuration."
      >
        <FieldGrid>
          <TextField
            path={['request_interval']}
            label="Request interval"
            placeholder="1s"
            help="Duration between requests, such as 1s or 500ms."
          />
          <TextField
            path={['request_timeout']}
            label="Request timeout"
            placeholder="30s"
            help="Leave empty to use the server default."
          />
          <SelectField
            path={['rate_limit_reset']}
            label="Rate-limit reset interpretation"
            choices={['epoch', 'relative']}
            fallback="Default · epoch"
          />
        </FieldGrid>
      </FieldSection>
      <RequestLimitsSettings />
      <FieldSection
        title="Schedule"
        description="An empty schedule stays manual. Configure an interval or a cron expression, not both; the source must also be enabled."
      >
        <FieldGrid>
          <BooleanField
            path={['schedule', 'enabled']}
            label="Schedule enabled"
            help="When unset, a configured interval or cron controls scheduling."
          />
          <SelectField
            path={['schedule', 'mode']}
            label="Scheduled collection mode"
            choices={['incremental', 'full', 'metadata']}
            fallback="Default · incremental"
          />
          <TextField path={['schedule', 'every']} label="Run every" placeholder="30m" />
          <TextField
            path={['schedule', 'cron']}
            label="Cron expression"
            placeholder="0 */6 * * *"
          />
          <TextField path={['schedule', 'timezone']} label="Timezone" placeholder="UTC" />
          <TextField
            path={['schedule', 'full_every']}
            label="Full reconciliation interval"
            placeholder="168h"
            help="Optional Go duration of at least 1m; empty or unset disables it. Requires an Incremental interval or cron schedule. Full runs on the first eligible base tick after its deadline, using the same page budget. Activation or source edits start a fresh interval."
          />
          <JSONField
            path={['schedule', 'max_pages']}
            label="Maximum pages per attempt"
            integer
            help="0 means unlimited."
          />
          <JSONField
            path={['schedule', 'known_pages']}
            label="Known-page stopping threshold"
            integer
            help="Incremental pages containing only known records before stopping."
          />
        </FieldGrid>
      </FieldSection>
      <AdvancedSection
        title="Adapter options"
        description="Adapter-specific options are preserved as arbitrary JSON. Configure only options supported by the selected adapter."
      >
        <MapField path={['options']} label="Options" />
      </AdvancedSection>
      <TraversalSettings />
    </>
  )
}

export function SourceEditor({
  value,
  onChange,
  readOnly,
  sourceID,
  onBlockedChange,
}: {
  value: string
  onChange: (value: string) => void
  readOnly: boolean
  sourceID?: string
  onBlockedChange: (blocked: boolean) => void
}) {
  const document = useMemo(() => parseProviderJSON(value), [value])
  const [mode, setMode] = useState<'visual' | 'json'>(() =>
    document.errors.length ? 'json' : 'visual',
  )
  const [selected, setSelected] = useState(0)
  const [editError, setEditError] = useState('')
  const [drafts, setDrafts] = useState<Set<string>>(() => new Set())
  const buttons = useRef<(HTMLButtonElement | null)[]>([])
  const id = useId()
  const reportDraft = useCallback((key: string, invalid: boolean) => {
    setDrafts((previous) => {
      if (previous.has(key) === invalid) return previous
      const next = new Set(previous)
      if (invalid) next.add(key)
      else next.delete(key)
      return next
    })
  }, [])
  useEffect(() => {
    onBlockedChange(drafts.size > 0 || Boolean(editError))
  }, [drafts, editError, onBlockedChange])
  const change = useCallback(
    (edit: (document: ProviderJSON) => void) => {
      if (readOnly) return
      try {
        const next = parseProviderJSON(value)
        edit(next)
        onChange(next.toString())
        setEditError('')
      } catch {
        setEditError(
          'This field cannot be changed safely. Repair its containing value in the JSON definition.',
        )
      }
    },
    [value, onChange, readOnly],
  )
  const text = (path: string[], fallback: string) => {
    const entry = document.getIn(path)
    return typeof entry === 'string' && entry ? entry : fallback
  }
  let endpoint = 'No endpoint configured'
  try {
    endpoint = new URL(text(['url'], '')).hostname
  } catch {
    /* Invalid endpoints remain editable. */
  }
  const summaries = [
    `${text(['adapter'], 'Choose adapter')} · ${document.get('enabled') === true ? 'enabled' : 'disabled'}`,
    `${text(['http', 'method'], 'GET')} · ${endpoint}`,
    `${text(['pagination', 'type'], 'none')} · ${document.rawIn(['page_size']) || 'default'} records`,
    `${document.keysIn(['mapping', 'fields']).length} fields · ${text(['http', 'items_path'], 'root items')}`,
    text(['schedule', 'every'], text(['schedule', 'cron'], 'Manual collection')),
  ]
  const current = stages[selected]
  return (
    <SourceEditorContext.Provider value={{ document, change, reportDraft, readOnly }}>
      <div className="space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div
            className="inline-flex gap-1 rounded-md border border-border bg-card p-1"
            role="group"
            aria-label="Definition editor mode"
          >
            <Button
              size="sm"
              variant={mode === 'visual' ? 'secondary' : 'ghost'}
              aria-pressed={mode === 'visual'}
              disabled={document.errors.length > 0 || drafts.size > 0}
              onClick={() => setMode('visual')}
            >
              <FlowArrowIcon />
              Visual stages
            </Button>
            <Button
              size="sm"
              variant={mode === 'json' ? 'secondary' : 'ghost'}
              aria-pressed={mode === 'json'}
              disabled={drafts.size > 0}
              onClick={() => setMode('json')}
            >
              <BracketsCurlyIcon />
              JSON
            </Button>
          </div>
          <p className="help">One definition · five connected stages · no network requests</p>
        </div>
        {drafts.size > 0 && (
          <p className="notice text-destructive" role="status">
            Finish or discard the pending field draft before switching stages, validating, importing
            or saving.
          </p>
        )}
        {editError && (
          <p className="notice text-destructive" role="alert">
            {editError}
          </p>
        )}
        {document.errors.length > 0 && (
          <div className="notice text-destructive" role="alert">
            <p className="font-medium">Repair the JSON to enable visual editing and saving.</p>
            <ul className="mt-2 list-inside list-disc">
              {document.errors.map((error, index) => (
                <li key={index}>{error}</li>
              ))}
            </ul>
          </div>
        )}
        {mode === 'json' || document.errors.length > 0 ? (
          <>
            <CodeEditor
              value={value}
              onChange={(next) => {
                setEditError('')
                onChange(next)
              }}
              label="Source JSON definition"
              height="620px"
              readOnly={readOnly}
            />
            <p className="help">
              Strict JSON object · no comments, duplicate keys or trailing commas · 128 KiB maximum.
              Untouched numeric values retain their exact representation.
            </p>
          </>
        ) : (
          <div className="grid overflow-hidden rounded-lg border border-border bg-card/30 lg:grid-cols-[255px_minmax(0,1fr)]">
            <aside className="border-b border-border bg-background/45 p-4 lg:border-r lg:border-b-0 lg:p-5">
              <div className="mb-5 flex items-center justify-between gap-2">
                <h2 className="text-xs font-medium uppercase tracking-widest text-muted-foreground">
                  Source stages
                </h2>
                <span className="mono text-primary">
                  {String(selected + 1).padStart(2, '0')} / 05
                </span>
              </div>
              <div
                role="tablist"
                aria-label="Source configuration stages"
                aria-orientation="vertical"
                className="space-y-0"
              >
                {stages.map((stage, index) => {
                  const Icon = stage.icon
                  return (
                    <div key={stage.id}>
                      {index > 0 && (
                        <div
                          className="ml-[25px] h-3 w-px bg-primary/35 lg:h-6"
                          aria-hidden="true"
                        />
                      )}
                      <button
                        type="button"
                        role="tab"
                        id={`${id}-${stage.id}`}
                        aria-controls={`${id}-panel`}
                        aria-selected={selected === index}
                        tabIndex={selected === index ? 0 : -1}
                        ref={(element) => {
                          buttons.current[index] = element
                        }}
                        disabled={drafts.size > 0 && selected !== index}
                        className={`flex w-full items-center gap-3 rounded-md border px-3 py-3 text-left transition-colors disabled:opacity-50 ${selected === index ? 'border-primary/60 bg-primary/8' : 'border-border bg-card/70 hover:border-primary/30 hover:bg-accent'}`}
                        onClick={() => setSelected(index)}
                        onKeyDown={(event) => {
                          if (drafts.size > 0) return
                          const next =
                            event.key === 'ArrowDown' || event.key === 'ArrowRight'
                              ? (index + 1) % stages.length
                              : event.key === 'ArrowUp' || event.key === 'ArrowLeft'
                                ? (index + stages.length - 1) % stages.length
                                : event.key === 'Home'
                                  ? 0
                                  : event.key === 'End'
                                    ? stages.length - 1
                                    : -1
                          if (next >= 0) {
                            event.preventDefault()
                            setSelected(next)
                            buttons.current[next]?.focus()
                          }
                        }}
                      >
                        <span
                          className={`flex size-7 shrink-0 items-center justify-center rounded border ${selected === index ? 'border-primary/40 text-primary' : 'border-border text-muted-foreground'}`}
                        >
                          <Icon size={17} />
                        </span>
                        <span className="min-w-0">
                          <span className="block text-sm font-medium">{stage.label}</span>
                          <span
                            className={`mt-1 break-words text-xs leading-5 text-muted-foreground ${selected === index ? 'block' : 'hidden lg:block'}`}
                          >
                            {summaries[index]}
                          </span>
                        </span>
                      </button>
                    </div>
                  )
                })}
              </div>
              <p className="help mt-6 hidden lg:block">
                Select a stage to configure it. Connections show the fixed collection sequence, not
                custom execution logic.
              </p>
            </aside>
            <section
              role="tabpanel"
              id={`${id}-panel`}
              aria-labelledby={`${id}-${current.id}`}
              tabIndex={0}
              className="min-w-0 p-5 sm:p-7"
            >
              <header className="mb-7 flex items-start gap-4 border-b border-border pb-5">
                <span className="mono pt-1 text-primary">0{selected + 1}</span>
                <div>
                  <h2 className="text-lg font-medium tracking-tight">{current.label}</h2>
                  <p className="help mt-1">{current.description}</p>
                </div>
              </header>
              <fieldset disabled={readOnly} className="min-w-0 space-y-7 disabled:opacity-60">
                <legend className="sr-only">{current.label} settings</legend>
                {selected === 0 && <IdentitySettings sourceID={sourceID} />}
                {selected === 1 && <ConnectionSettings />}
                {selected === 2 && <PaginationSettings />}
                {selected === 3 && <MappingSettings />}
                {selected === 4 && <CollectionSettings />}
              </fieldset>
            </section>
          </div>
        )}
      </div>
    </SourceEditorContext.Provider>
  )
}
