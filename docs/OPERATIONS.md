# Operations

[Back to README](../README.md) · [Source configuration](CONFIGURATION.md) · [Backups and recovery](BACKUPS.md)

Understand what gets published, inspect changes and monitor the installation without discarding its history.

## Console navigation

**Overview** remains at the top of the menu. Pages are grouped under **Collection**, **Library**, **Monitoring** and **Administration**. Each group can be expanded independently; Administration starts collapsed and the other groups start expanded.

The console remembers group choices in the current browser. Entering a page opens its group without resetting the other choices, including source, run and archived-response detail pages. Navigation remains usable if browser preference storage is unavailable.

The desktop sidebar and mobile panel share the same **Ingest** header: a 24 px title and 32 px database icon, vertically centered without increasing the header height.

On small screens, **Open navigation** opens a side panel. Escape returns focus to the opening button; selecting a page closes the panel and moves focus to the page content. The logo and **Sign out** remain fixed while the navigation list scrolls.

## Runs and storage

**Sources** stores strict JSON and offers five **Visual stages** plus advanced **JSON** editing. **Import JSON** changes only the draft, and **Export JSON** downloads that draft; neither publishes a source or starts collection. Validate/save make no source HTTP request and start no run. Fix invalid local field/JSON drafts before saving or switching stages. Revision conflicts preserve local edits for comparison and explicit reconciliation; see the [configuration workflow](CONFIGURATION.md#configure-your-sources). Existing YAML definitions require [offline migration](CONFIGURATION.md#migrating-existing-sources), which leaves historical run snapshots and checkpoints unchanged.

- **Preview:** defaults to three complete nonempty pages, configurable in Settings; stores a parsed sample without publishing torrents or retaining raw response/record bodies.
- **Incremental:** discovers newest native identities and merges valid records into the published dataset as pages commit. For ordered bounded JSON sources, each selected scope stops after X consecutive pages entirely known from collections completed before run creation. Remote catalogues keep their existing atomic synchronization and checkpoint semantics.
- **Full:** exhaustively discovers the selected catalogue, stages records privately and replaces that source's published membership atomically only after traversal completes. Detail requests may recover genuinely missing identities, but Full does not run missing-metadata enrichment. Previously collected detail-only metadata is preserved for an unchanged native ID and hash.
- **Metadata:** a separate job for supported native HTTP/JSON sources. A standalone manual or scheduled run walks the entire published catalogue present at run creation, including old records. With **Metadata after Incremental** enabled, successful Incremental completion instead queues a linked Metadata run only for its newly added native torrents still missing configured fields. No eligible new records means no follow-up; paused, failed or cancelled Incrementals do not trigger it. Metadata fills absent/null fields and missing attribute keys without overwriting known values. It never discovers, inserts or removes torrents.
- Successful publication uses the collection context, not the short shutdown-cleanup deadline. Large atomic publications may take longer than page collection. If shutdown interrupts publication, the transaction rolls back and the completed traversal remains resumable; Resume retries publication without downloading committed pages again.
- A Full/Incremental/Metadata page or duration budget ends **Paused**, not Succeeded. Resume grants fresh per-attempt budgets and keeps the original run snapshot, rather than silently switching to edited JSON or newer execution defaults. A scheduled budget pause can continue at a later tick while its source revision and mode still match. Scheduled Metadata follow-ups continue under their originating Incremental schedule; manual follow-ups require explicit Resume.
- **Pause** is a durable manual hold. A queued run stops before fetching; a running run shows **Pausing…**, commits its in-flight result and stops at the next committed page boundary without retaining the raw body. An accepted pause on the final page also holds Full publication until explicit Resume.
- Manual holds survive restarts and block later scheduled work for the same source. **Keep paused** converts an automatic budget pause into a manual hold. If the in-flight request fails, the run remains **Failed** with its failure details and hold intact; Resume or Cancel releases that hold.
- **Cancel** interrupts source requests and terminates paused or failed-held runs; it takes precedence over Pause and cannot publish a partial Full run. Interrupted workers recover without taking over another process's live provider lock. The optional automatic recovery setting resumes technical interruptions, never manual, quota/no-progress or authentication/certificate/configuration holds. It is off by default.
- Automatic recovery also requires a currently valid, enabled source definition. Source deletion is serialized with manual, scheduled and recovery admission, so a deleted or disabled source cannot silently restart from an old snapshot. Unavailable sources retain their interrupted checkpoint for operator review.

**Records** in run details and run lists counts distinct valid source IDs retained from accepted pages in the run's current traversal. Repeated observations of an ID count once; different IDs remain separate even when their info hashes match. Ignored, auxiliary and invalid records do not contribute. The count survives pause/resume; an accepted traversal reset starts a new identity set. Bounded reconciliation can temporarily decrease this derived count while stale scopes are re-enumerated, or retire an ID whose latest valid observation leaves the selected scopes. Unaffected scope coverage is preserved. Existing runs are reconstructed from their retained history when upgrading. The run API exposes this metric as `distinct_records`; `records` remains the cumulative primary-observation count, including repeats. No original observations are removed.

On a run page, open **Run details** for timestamps and the per-attempt page budget. Publication totals and committed history are grouped in the **Published changes** tab, separate from collection progress.

An automatic Metadata follow-up inherits its successful parent's immutable configuration, policy, budgets and trigger. The parent remains **Succeeded**: its **Events** contain **Open Metadata follow-up · new torrents only**, and the child links back to the Incremental. The run API includes `metadata_parent_run_id` only for linked follow-ups. Enabling the source option does not rewrite existing snapshots or enqueue work for historical completed runs.

The authenticated lifecycle endpoints are `POST /api/runs/{id}/pause`, `/resume` and `/cancel`. They return the updated run. `pause_requested` identifies a durable manual hold; `pause_reason` distinguishes `manual`, `budget`, `quota`, `no_progress` and `interrupted`. A manual hold survives pausing, restart and an in-flight failure. The CLI offers the same `pause`, `resume` and `cancel` commands with `--run ID`.

Opaque continuation state is private to the backend: run responses and the console never expose the stored cursor, which may contain a source token or signed URL. **Resume point** shows the immutable source revision and explains server-managed continuation; hiding the cursor does not remove or reset it.

For a bounded source that cannot establish complete coverage, inspect the `source_error` event's `coverage_expected`, `coverage_observed` and configured `minimum_total`. This evidence is retained even when the failing completeness check made no HTTP request. Historical archives and earlier events are not rewritten.

### Diagnosing rejected pages and historical errors

HTTP 200 means the request succeeded, not that its pagination was accepted. New ordinary HTTP/JSON and Torznab pagination rejections retain a finite `failure_reason` alongside the existing recovery category `failure_code`. **Runs → Events** shows the reason, safe numeric context and remediation without needing an archived body. `expected_total` / `actual_total` compare the committed total with the returned total; `expected_position` / `actual_position` compare requested or next positions. For a page that exceeds or ends before its advertised bound, `expected_total` is that bound and `actual_position` is the observed cumulative offset. HTTP status and requested offset are included when available. The event's `page` remains the retained observation index, not the source's page number.

Reasons are `invalid_position`, `position_mismatch`, `invalid_total`, `total_changed`, `records_exceed_total`, `empty_before_total`, `invalid_continuation`, `continuation_repeated`, `continuation_mismatch`, `premature_end`, `unexpected_continuation`, `pagination_overflow` and `publication_order_invalid`. Context is restricted to nonnegative, browser-safe integers; malformed values, upstream messages, URLs, opaque cursors and credentials are never copied into these diagnostics. Malformed Torznab XML or pagination attributes that the SDK cannot decode retain the coarse parse failure rather than guessing a detailed cause from raw text. Bounded-window, Metadata and remote-catalogue collectors retain their specialized diagnostics.

On rejection, the last successful checkpoint remains fixed and Full does not publish partial results. Check the expected/actual values against the source's ordering, pagination fields and total policy. If the upstream response is corrected, **Resume** retries the rejected page. If configuration must change, edit the source and start a new run; Resume always uses the original immutable configuration. Detailed diagnostics do not relax total consistency or alter automatic recovery categories.

Historical `publication_order_invalid` events identify date inversions rejected by an older publication-mode collector. Their `requested_page` and one-based `record_index` remain useful evidence, but dates no longer act as an Incremental completion boundary. The corrected collector requests the same newest-first listing and checks consecutive pages of native IDs accepted by collections completed before the original run began. **Resume** continues the existing scope/page and known-page streak without a timestamp frontier. It does not sort, drop offending records, reduce the configured boundary or erase old errors.

**Historical errors** is a cumulative count across attempts and rejected records, not a current health indicator. A **Succeeded** run can retain errors from earlier failures, retries or rejected individual records. Its status is still Succeeded; use Events and Observations to distinguish recovered attempts from record omissions. Current failures are shown by **Failed** and the current failure notice. Existing history is not rewritten, and older generic failures cannot be retroactively assigned a precise reason.

**Known-page baseline:** a published native ID qualifies only when accepted by a Full or Incremental run completed before the current run was created, without skipped invalid primary records on committed pages. Recovered request failures are compatible with a complete collection. Merely publishing a prefix before failure or Pause cannot hide the unvisited remainder from the next Incremental. A run completed after the current run began cannot change its baseline during Resume.

This guard does not rewrite historical successful runs or retroactively fill gaps they may have missed. If an older Incremental stopped prematurely, use a source-appropriate Incremental backfill with **Known pages = 0**. Use Full only when the source can prove exhaustive coverage; a capped Torznab query union is not such proof.

Bounded JSON sources can explicitly set `traversal.total_mode` to `"at_least"` for cumulative discovery against each scope's first advertised total. The default remains strict reconciliation. Cumulative mode avoids scope retirement solely because totals change; it does not promise an exact current-catalogue mirror. Changing this policy or pacing in source JSON does not mutate an existing run's snapshot.

Ordered bounded JSON Incremental scans each selected scope until its configured known-page boundary instead of repeating the exhaustive Full crawl. `traversal.incremental_order` selects strict descending numeric `id` validation by default, or a publication-sorted listing (`published_at`) that tolerates date reordering. Every identity on each consecutive boundary page must belong to the completed-collection baseline above; current-run writes and shared hashes cannot satisfy it. Invalid records, repeated pages and unproven window ends remain failures. In **Start a run**, **Known pages (X)** accepts an integer from 0 to 10000: blank inherits `source.schedule.known_pages`, while 0 disables early stopping. This override is submitted only for Incremental, captured in that run's immutable snapshot and never written back to the source. Full and Metadata do not use it.

For generic paginated Incremental sources, the consecutive-known-page count is committed with the checkpoint. Pause, per-attempt budgets and Resume preserve it. A newly encountered identity resets the count; auxiliary requests and rejected pages cannot silently reset it.

Ordinary HTTP/JSON can keep Full oldest-first and use `http.incremental_query` for newest-first Incremental without changing listing filters. With `"allow_total_growth": true`, a positive **Known pages (X)** requires this explicit override; simply enabling early stopping on an ascending feed could miss all recent additions. Use ordering parameters verified against the upstream API. With this option, Incremental accepts both increases and decreases in advertised totals, including differently aged cached counts; Full and Preview still reject decreases. Disabling the option keeps exact total consistency in every mode. Returned positions, record bounds and completion evidence are still checked, and known-page stopping is unchanged. The override and base query survive Resume in the immutable run snapshot; save JSON changes and start a new run to use them. Flexible totals do not provide an atomic catalogue snapshot: removals/reordering can shift offsets and records prepended after an Incremental has begun may wait until the next run.

Torznab Incremental with `category_scope` set to `"each"` or `"advertised"` also applies **Known pages (X)** separately to every category. At X consecutive entirely known pages, it moves to the next category and starts that category's counter at zero. The counter and category offset are saved together, so a budget pause cannot reset or carry a boundary into another category. Only identities in the original completed-collection baseline qualify, even when a new identity appears in several categories. Full remains exhaustive.

When a server returns a retryable 429, the shared transport saves its status, failure and deadline before its next attempt, not the raw response body. The run respects the full source deadline without advancing its checkpoint. Saved quota policy controls retry count and maximum automatic wait; a longer wait pauses safely, including after a successful response that exhausts quota. Explicit Resume accepts that saved wait but never sends early. Recovered quota failures remain in the error history.

The useful-progress guard counts successful data responses without a native ID previously unseen within the run. It excludes quota errors and auxiliary/control traffic, including Metadata responses that update existing identities rather than discover new ones. A warning is emitted once per stalled streak; the alternative pause action requires explicit Resume and then grants a fresh allowance. Records sharing a hash still represent distinct progress if their native IDs differ.

**Runs → Events** uses the browser's rows-per-page preference (50 by default), with First/Previous/Next/Latest controls above and below the list, the current page, total pages and exact event count. Latest jumps directly to the last available page. Live updates refresh the count and visible events without changing the selected page. The API uses `GET /api/runs/{id}/events?limit=50&offset=0` and returns `items`, `total`, `limit` and `offset`; `limit` accepts 1–200. Rows and total come from the same database snapshot.

New response bodies and original record bytes are **not stored**, including failure responses. Non-Preview observations also omit duplicate interpreted-field snapshots; actual fields remain in the native catalogue or private Full staging, while Preview retains its parsed sample. Compact observation rows, native IDs, errors, fingerprints, totals and durable resume state remain available. `output.fields` controls catalogue presentation, not persisted metadata.

**Observations** and **Runs → Observations / archives** distinguish unavailable payloads explicitly with `payload_retained: false`; new `page_saved` events carry the same flag and do not offer response downloads. `GET /api/raw/{id}` still returns the observation, with `raw`, `byte_length` and `valid_utf8` set to null when the payload was not stored. Raw-record and response download endpoints return HTTP 404 for unavailable payloads. Existing historical archives remain byte-for-byte readable and downloadable, and historical response events without the flag keep their links. Nothing is purged: stopping new raw retention does not reclaim the disk already used by historical archives. Those archives may contain sensitive source metadata, so protect database access and backups.

Explicit historical deduplication can replace strictly identical physical copies with `raw_records.payload_id` or `pages.payload_id` references. Observation IDs, run/page provenance, errors and checkpoints remain independent; archive APIs resolve the retained original, so downloads and interpreted-field searches stay unchanged. Raw copies must match the provider, native source ID, content type, exact bytes and interpreted fields—not merely an info hash. References point directly to an older inline original, which must not be removed or turned into another reference.

Deleting duplicate physical copies makes space reusable by PostgreSQL. Returning that space to the filesystem requires table compaction such as `VACUUM FULL`, which takes exclusive table locks and needs temporary free disk space. Pause collection at a committed checkpoint before this maintenance and resume the same run afterward; no new Full or Metadata traversal is needed.

For native Incremental and Preview discovery, an individual normalisation error does not stop otherwise valid page traversal. The rejected observation retains its ID and error, not new raw bytes, and is excluded from publication; a running or successful status alone does not prove that every observed record was accepted. Correcting a source definition does not change an existing run's immutable snapshot or retry earlier rejected records.

Full discovery fails closed when a primary record cannot be interpreted or retained. It retains the error observation once without advancing the last successful checkpoint or partially changing that page's staging, scope coverage or identity set, including when the page requested a reset. The previously published catalogue remains unchanged. After a source-response correction, Resume retries the rejected page; a definition correction requires a new Full because the existing snapshot is immutable. Earlier runs and historical archives remain intact.

A Metadata response whose interpreted fields still cannot be represented in the database—for example, a NUL in a field other than `metadata.nfoContent`—fails the run after retaining its error observation once. Binary NFO text is retained using the reversible Base64 representation documented in [Configuration](CONFIGURATION.md#standalone-metadata-collection), so it does not block the run. For other rejected fields, the last successful checkpoint remains unchanged: the failed candidate is neither silently skipped nor automatically requested in a loop, and the rejected page does not partially enrich the catalogue. After the source response is corrected, Resume retries that candidate without replaying earlier committed work. Editing source JSON still does not alter the failed run's immutable snapshot.

### Search, saved views and occurrence history

**Torrents** searches PostgreSQL directly. Combine full-text search with multiple providers, category, exact size bounds, publication dates, seeder counts and an exact info hash. Sort by recency, age, size, seeders or relevance. Numeric bounds remain decimal strings so values larger than JavaScript's safe integer range stay exact. Date inputs use the browser's local time zone; saved bounds retain their exact instant and fractional precision. Full-text indexing uses a bounded UTF-8 prefix of title, provider, source ID and hash; complete field values remain stored.

Save named combinations in **Saved views**. They persist in PostgreSQL and use revisions to reject conflicting updates.

**An info hash is not a record identity.** Every `(provider_id, source_id)` remains independent, including different IDs within one provider. **Related occurrences** offers navigation to matching hashes; it never merges, deletes or hides another tracker's record.

Each occurrence has publication history and provenance, including deleted records. **Runs → Published changes** counts actual additions, field/provenance changes and deletions; a successful unchanged run reports zero changes. Before/after views distinguish missing fields from explicit values and preserve exact numbers. History starts with the current occurrence baseline when upgrading; earlier revisions are not invented. Preview runs never publish. Full and remote incremental runs publish only on successful completion; native Incremental and Metadata record each committed page, with Metadata restricted to updates of existing matching identities.

## Logs, notifications and webhooks

**Logs** provides a persistent, paginated view filtered by severity (`debug`, `info`, `warn`, `error`), source, run, text and time range. Page-level progress is debug; collection failures are errors. Entries link back to their runs. Credentials, cookies, original response bodies and source JSON are not copied into activity logs.

The panel's notification bell and **Notifications** page show notable run outcomes, useful-progress warnings, scheduling failures, backup/restore outcomes and health transitions. Read/unread acknowledgements persist in PostgreSQL for the installation's administrator. Live updates use the existing authenticated event stream; a browser does not need to remain open for jobs, notifications or webhook deliveries to execute.

Configure destinations in **Webhooks**:

1. In **Secrets**, store the complete destination URL under a reference such as `operations_hook_url`. URLs can contain private tokens, so the application does not return their values in webhook metadata.
2. Create a webhook referencing that secret, choose events, and enable it. An optional signing-secret reference enables HMAC verification.
3. Use the explicit **Send test** action to queue a test delivery. Inspect delivery history, HTTP status, retry timing and terminal failures in the panel.

Available subscriptions are `run.succeeded`, `run.failed`, `run.paused`, `run.cancelled`, `run.no_progress`, `schedule.failed`, `backup.succeeded`, `backup.failed`, `backup.restore_succeeded`, `backup.restore_failed`, `health.warning` and `health.recovered`. Requests are JSON POSTs with a versioned event envelope. They do not contain raw source data, recovery keys or credentials. Redirects are not followed. Explicit HTTP destinations are supported for local/LAN receivers; use HTTPS with a normally trusted certificate for remote destinations.

Deliveries are durable and **at least once**: receivers should deduplicate `X-Ingest-Event-ID`. `X-Ingest-Delivery-ID` identifies a delivery across attempts. When signing is enabled:

```text
X-Ingest-Timestamp: <Unix seconds>
X-Ingest-Signature: sha256=<hex HMAC-SHA256>
signed bytes = timestamp + "." + exact request body
```

Verify the signature using the shared secret and enforce an appropriate timestamp tolerance. Network failures, temporary secret-resolution failures, HTTP 408/425/429 and 5xx responses receive bounded retries, up to five attempts. Most other 4xx responses and redirects are permanent failures. An explicit retry grants a new five-attempt budget while preserving delivery identity. Disabling or deleting a webhook prevents new attempts; an already-sent request cannot be recalled. Disabling remains possible after an invalid secret rotation. Delivery history is retained and deleted webhooks remain visible through **Include deleted webhooks**.

## System health

**System health** measures PostgreSQL storage separately for published/staged records, raw archives, publication/semantic history and other relations, including table and index space. Database overhead is shown separately; these numbers are not WAL or a remote database server's free disk space. Local disk measurements describe the application's state-directory filesystem only.

Daily first-UTC measurements persist across restarts. Growth rates require at least 24 elapsed hours, use actual elapsed time, and are not invented when there is insufficient history. Configurable thresholds flag source freshness, queued/running collections without real progress, local free space and journal growth. Never-successful enabled sources are listed explicitly. Authentication and certificate failures retain a sanitized classification; TLS verification is never bypassed.

Authentication and certificate alerts clear after a later successfully committed page or completed run, including during a long-running resumed collection. Resume alone or another failed page is not recovery evidence. Historical events and error counters remain intact. Publication freshness still requires a completed successful non-preview run; a working connection does not prove the catalogue is up to date.

Warnings and recoveries generate notifications and optional webhooks when their state changes, rather than on every measurement. Recent queue/resume activity starts a fresh progress window; a worker heartbeat or repeated failed request does not count as a successfully committed page.

**Monitoring never purges raw data or the publication journal.** Capacity planning remains the operator's responsibility.

## Existing PostgreSQL data

To import historical `public.torrents` rows in the same database:

```sh
docker compose exec ingest /ingest import-legacy
```

Import is additive and idempotent. It preserves the source table, does not overwrite freshly collected records, and does not fabricate original responses for historical rows. A ledger prevents later imports from resurrecting rows removed by a completed full run.
