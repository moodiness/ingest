# Sharing and remote catalogs

[Back to README](../README.md) · [Source configuration](CONFIGURATION.md) · [Security policy](../SECURITY.md)

## Publish a read-only catalogue

1. Configure a trusted HTTPS reverse proxy and set `INGEST_PUBLIC_URL` to its exact HTTPS origin. Keep the backend port private or loopback-only. This setting declares the deployment origin; it does **not** terminate TLS. Forwarded headers cannot enable sharing, and an absent/non-HTTPS public origin prevents activation.
2. Open **Sharing → Create share**. Give each recipient their own share. Select individual providers, or explicitly authorize **all current and future providers**. The latter includes retained published data and subsequently added sources, including imported catalogues.
3. Select exportable fields: `title`, `size`, `info_hash`, `seeders`, `leechers`, `published_at`, `category`, `categories`. The server enforces this allowlist; recipients cannot expand it or select extra providers.
4. Save the one-time generated password, then explicitly enable the share. Send its catalogue URL and password through a secure channel. Never append the password to the URL or reuse the administrator password.

New shares created in the panel are disabled; API clients explicitly control activation with `enabled`. Passwords contain 256 bits of randomness and are stored only as domain-separated hashes. They authorize only `GET`/`HEAD` on their catalogue feed, not administration, source definitions, secrets, raw archives or writes. Rotation replaces the password; disabling/revoking a share denies subsequent requests. Permission checks run on every page, and responses use `Cache-Control: no-store`.

Expiration is optional; an expired share rejects access without deleting published or imported data. Per-share limits apply across instances: 1–3600 requests per rolling minute (default 120) and 1–16 simultaneous catalogue downloads (default 2). Valid `GET` and `HEAD` requests consume the request allowance; invalid passwords do not. A download holds its concurrency lease until its response finishes or disconnects; interrupted leases expire. Limit responses are HTTP 429 with `Retry-After`. Policy changes, password rotation and revocation are recorded in the security audit without secret values.

Download/magnet URL fields and arbitrary nested objects are not exportable. Native source identifiers are opaque hashes because an original Torznab GUID or JSON ID may itself contain credentials. Authorized field values remain unchanged, including zero, null and large integers; do not place secrets in free-text fields you authorize. Imported provenance is retained across subsequent sharing.

**Revocation is not remote deletion or DRM.** It stops future access, not copies already downloaded. A recipient can retain or re-share those copies outside this application's controls. “Last completed download” means the server delivered the final page, not that the recipient committed its import.

## Import and schedule a friend's catalogue

Open **Remote catalogs → Add remote catalog** and enter the supplied HTTPS URL and sharing password, or an existing vault reference. Use a new local source identifier: a namespace containing native records cannot be replaced by a remote import, even if its old source definition was deleted. New passwords receive independent encrypted vault entries; changing one remote does not rotate a reference used by another source.

The editor writes ordinary strict source JSON with `"adapter": "http_json"` and `"catalog": true` inside `http`; it does not introduce another adapter or configuration store. The fixed versioned envelope, provenance identity, field policy and cursor mapping are validated rather than user-remapped. An endpoint cannot contain credentials, query parameters or a fragment. The same private definition is available through **Sources → Visual stages / JSON** and JSON import/export; importing a definition is not importing catalogue records. Source revisions protect concurrent edits, and existing runs keep their immutable source snapshot. See the [configuration guide](CONFIGURATION.md) and [JSON reference](JSON_REFERENCE.md); legacy definitions require [offline migration](CONFIGURATION.md#migrating-existing-sources).

The default schedule is **daily incremental synchronization (`24h`)**. Hourly, manual, custom interval and cron modes are also available. Source activation and schedule activation are separate controls. Validating or saving the source definition makes no source HTTP request and does not start a manual sync; an enabled schedule waits until its next due time. **Sync now**, **Full resync**, **History**, **Logs** and **View imported data** reuse the existing durable jobs and administration views. Scheduled/manual outcomes generate the normal notifications and configured webhook events.

The first sync downloads a consistent full snapshot. Later syncs use signed, share-bound checkpoints for additions, exact replacements and deletion tombstones. Both full and incremental imports stage privately; only successful traversal publishes the remote namespace and checkpoint in the same transaction. Missing fields are removed rather than merged with stale values. Failed, cancelled, paused and preview runs leave the last published copy unchanged. A paused run resumes its saved cursor after a restart; an older run cannot overwrite a newer committed mirror.

Changing permissions or rotating a password invalidates unfinished page cursors. After updating the vault credential when necessary, start a new sync: an authentic older checkpoint requests an authoritative full snapshot, removing records or fields no longer authorized. Changing the endpoint also starts a fresh snapshot. Imported values and provenance are read-only in the panel; records originating from this installation are ignored to prevent replication loops.

## Transport and storage boundaries

- Remote connections require normal certificate and hostname verification. There is no insecure TLS option. Private LAN/loopback HTTPS is supported with a certificate trusted by the receiving process.
- Remote redirects and environment HTTP proxies are disabled. DNS destinations are checked and pinned at connection time; unspecified, multicast and link-local destinations are rejected.
- `GET /api/catalogs/{share-id}` accepts `Authorization: Bearer <sharing-password>`, an optional `limit` from 1 to 1000 (default 100), and either `checkpoint` or `cursor`. A non-final page has `next_cursor`; only the final page provides the next durable `checkpoint`. Version 1 responses contain `instance_id`, `mode`, `items` and opaque origin identities.
- Publication history is immutable and currently has no automatic compaction. Plan disk capacity for the change journal, compact observations, existing historical raw archives and staging as well as live torrents. New raw bodies are not stored, and historical archives are not deleted or reclaimed.
- Back up PostgreSQL and the state volume together. The installation identity, signing key, shares, journal and checkpoints live in PostgreSQL; vault decryption and source definitions also require the state volume.
