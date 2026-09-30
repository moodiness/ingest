# Deployment and settings

[Back to README](../README.md) · [Source configuration](CONFIGURATION.md) · [Backups and recovery](BACKUPS.md)

## Docker Compose

Start from [`.env.example`](../.env.example). Compose uses these values:

| Variable                 | Default    | Purpose                                                                       |
| ------------------------ | ---------- | ----------------------------------------------------------------------------- |
| `POSTGRES_PASSWORD`      | Required   | Use a URL-safe random password, such as the output of `openssl rand -hex 24`. |
| `POSTGRES_DB`            | `ingest`   | Database created by PostgreSQL. Preserve the existing value on upgrade.       |
| `POSTGRES_USER`          | `ingest`   | PostgreSQL role. Preserve the existing value on upgrade.                      |
| `POSTGRES_PORT`          | `5432`     | Host-side PostgreSQL port, bound to loopback.                                 |
| `INGEST_PORT`            | `8080`     | Host-side panel port.                                                        |
| `INGEST_BIND_IP`         | `127.0.0.1` | Host interface for the panel; loopback-only by default.                      |
| `INGEST_ADMIN_PASSWORD`  | Generated  | Optional administrator password, 12–72 bytes.                                |
| `INGEST_PUBLIC_URL`      | Unset      | Exact external origin; HTTPS is required to activate sharing.                |
| `INGEST_VERSION`         | `latest`   | GHCR image tag; pin a published version without its leading `v`.             |
| `POSTGRES_VOLUME`        | `ingest_postgres_data` | PostgreSQL volume name. Preserve the actual existing volume on upgrade. |
| `INGEST_STATE_VOLUME`    | `ingest_state` | Private state volume name. Preserve the actual existing volume on upgrade. |
| `COMPOSE_PROJECT_NAME`   | `ingest`   | Compose project identity; preserve it when upgrading an existing deployment. |

The container runs as UID/GID `65532:65532`. State lives in `/data`, and private source JSON in `/data/providers`. Compose constructs `DATABASE_URL` from its PostgreSQL settings. Source migration does not change `compose.yaml` or CI YAML. For an existing installation, stop all application replicas and source writers and follow [Migrating existing sources](CONFIGURATION.md#migrating-existing-sources) before restarting with JSON-only loading.

The distributed Compose file pulls `ghcr.io/moodiness/ingest`; it has no implicit local build. See [Local Docker build](DEVELOPMENT.md#local-docker-build) for the explicit development override.

### Migrating the former scraper deployment

The public command, container entrypoint, service and environment prefix are now `ingest`, `/ingest`, `ingest` and `INGEST_`. This is a configuration cutover, **not** a data migration or password rotation:

1. Before replacing deployment files, create and verify a backup. Record the current Compose project name, PostgreSQL database/user/password, published ports, actual database/state volume names and any private mount or UID/GID overrides. Keep the vault key and source files.
2. Using the old deployment configuration, pause active runs at committed checkpoints and stop only the old application service. Leave PostgreSQL and both volumes intact. Do not start the old and new application containers together.
3. Preserve `COMPOSE_PROJECT_NAME`, `POSTGRES_DB`, `POSTGRES_USER`, `POSTGRES_PASSWORD` and `POSTGRES_VOLUME` explicitly in the private `.env`. Set `INGEST_STATE_VOLUME` to the **actual old state volume**, not the new-install default. An older installation may use `torrent_scrapers_postgres_data` and `torrent_scrapers_state`; inspect the running mounts rather than assuming either name.
4. Rename private `SCRAPER_*` settings to their `INGEST_*` equivalents without changing values. Change the application key in a private Compose override from `scraper` to `ingest`, retaining its mounts and user. Remove obsolete build/entrypoint overrides or use the documented explicit build override. Native deployments must select their existing private state directory with `INGEST_DATA_DIR` or `--data-dir`; the new default `.ingest` does not move an old `.scraper` directory.
5. If sources are still YAML, complete the [offline source migration](CONFIGURATION.md#migrating-existing-sources) while every application writer remains stopped. With the new configuration, pull the selected image and start **only** the application with `docker compose up -d --no-deps ingest`. Check authentication, catalogue identities and saved run checkpoints before resuming those same runs. Remove the stopped old application container after successful verification, without removing volumes.

Cryptographic domains and durable database ownership markers intentionally keep their historical values so existing secrets, sessions, archives and recovery cleanup remain readable. There are no old environment-variable or executable aliases.


## Native environment and CLI

These settings are read by the application. Adding them to Compose's `.env` does not pass them into the container unless they are also added to the service's `environment` configuration.

| Variable                       | Native default                   | Purpose                                                                                                            |
| ------------------------------ | -------------------------------- | ------------------------------------------------------------------------------------------------------------------ |
| `DATABASE_URL`                 | Required for database commands   | PostgreSQL connection string. Keep credentials out of command-line arguments.                                      |
| `INGEST_DATA_DIR`             | `.ingest`                        | Private state directory; override with `--data-dir`.                                                               |
| `INGEST_PROVIDERS_DIR`        | `providers`                      | Authoritative private source JSON directory; override with `--providers`.                                         |
| `INGEST_BIND`                 | `127.0.0.1:8080`                  | Listen address; override with `serve --bind`.                                                                      |
| `INGEST_PUBLIC_URL`           | Unset                            | External origin; override with `serve --public-url`. This does not terminate TLS.                                  |
| `INGEST_ADMIN_PASSWORD`       | Generated in the state directory | Optional administrator password, 12–72 bytes.                                                                      |
| `INGEST_MASTER_KEY`           | Generated `vault.key`            | Optional existing 32-byte vault key, encoded as base64 or hexadecimal. Never replace the key of an existing vault. |
| `INGEST_PG_DUMP`              | `pg_dump` on `PATH`               | PostgreSQL backup client executable.                                                                               |
| `INGEST_PG_RESTORE`           | `pg_restore` on `PATH`            | PostgreSQL restore client executable.                                                                              |
| `INGEST_RESTORE_DATABASE_URL` | Falls back to `DATABASE_URL`      | Maintenance connection for CLI recovery into a new database.                                                       |

`serve --workers` seeds the collection limit only when no value has been saved: default **2**, range **1–32**. Subsequent starts retain the database setting, even with a different flag value. `serve --web-dir` overrides the embedded frontend directory. Run `ingest help` or a command's `--help` for the complete CLI.

The unauthenticated liveness endpoint is `GET /healthz`. Detailed database diagnostics require an authenticated session.

## Collection concurrency

Open **Administration → Settings** to change **Simultaneous collections** from **1 to 32**. The setting persists in PostgreSQL and applies immediately to all collection processes sharing that database, without restarting.

- Increasing the limit wakes queued work.
- Decreasing it leaves active runs and their checkpoints intact. New runs wait until the active count falls below the new limit.
- Only one run per source is admitted at a time; each source's request interval and rate-limit handling remain unchanged.
- The page shows running and queued totals, plus this instance's database connection capacity. That local capacity can be lower than the shared limit: two pool connections remain reserved for administration and recovery. If necessary, configure `pool_max_conns` in `DATABASE_URL` and restart to increase the pool itself. Saving a larger collection limit does not silently change the connection pool.

Concurrent edits use revisions. If another administrator changes shared settings, the page preserves the draft and asks whether to use the saved values or keep the edit before saving again.

## Collection execution and defaults

The remaining shared settings also persist in PostgreSQL. Execution policies are captured when a run is created: changing them does not rewrite queued, paused or active runs, and Resume retains the same policy. Runs created before these policies were introduced keep their legacy behavior.

| Setting | Default | Range and behavior |
| --- | --- | --- |
| Automatic quota retries | `3` | `0–10` additional attempts after a 429; `0` records the first rejection and pauses without an automatic retry. |
| Maximum automatic quota wait | `0` | `0–86400` seconds; `0` means unlimited. A longer server-requested wait pauses after saving its status and deadline, not its response body. |
| Responses without new native IDs | `1000` | `0–100000`; `0` disables the useful-progress guard. |
| No-progress action | `warn` | `warn` notifies once per stalled streak and continues; `pause` retains the checkpoint and requires explicit Resume. |
| Default request timeout | `30` | `1–900` seconds, inherited only when a source omits `request_timeout`. |
| Default Preview pages | `3` | `1–10000` complete data pages. |
| Default Full/Incremental/Metadata page budget | `0` | `0–10000` per attempt; `0` means unlimited. |
| Default attempt duration | `0` | `0–604800` seconds; `0` means unlimited. |

**Explicit Resume accepts an already saved quota wait; it never shortens the server deadline.** A successful response that exhausts a quota is committed before pausing. Pause and Cancel remain responsive during cooldowns.

Useful progress means a previously unseen **native source ID within that run**, not another observation, another hash or a changed total. Quota errors and auxiliary/control responses do not consume this allowance. New IDs reset the streak. Explicitly resuming a no-progress pause grants a fresh allowance without forgetting previously seen IDs or deleting archived evidence.

Page and duration budgets apply independently to each attempt. A time limit is checked at safe response boundaries and during cooldown waits; it does not interrupt an in-flight response or impose a timeout on atomic Full publication. The Start a run dialog uses saved defaults when a field is empty; entering `0` explicitly selects unlimited.

**Automatically resume interrupted collections** defaults to **off**. When enabled, technical interruptions can recover after restart. Manual holds, quota/no-progress pauses and authentication, certificate or configuration failures are not automatically released. Restore disables this option until an administrator explicitly enables it again.

## Personal display preferences

**Display time zone** and **Rows per page** are local to the current browser, not shared server configuration. The defaults are the browser's time zone and **50 rows**; supported row counts are **25, 50 and 100**. Changes persist across reloads and synchronize between tabs without discarding unsaved forms.

An explicit pagination limit in a page URL takes precedence over the personal default. Date displays use the chosen zone; date/time input controls still use the browser's local zone. Neither preference changes stored timestamps, source data or the shared settings revision.

## Storage and remote access

New installations use the private `ingest_state` volume for the password and vault master key, and `ingest_postgres_data` for PostgreSQL. `POSTGRES_VOLUME` and `INGEST_STATE_VOLUME` select the actual named volumes; set `COMPOSE_PROJECT_NAME` as well to isolate a separate installation's containers. These volume names are global, not automatically isolated by a different project name. Changing a name does not migrate its contents. Existing installations must explicitly retain their current names through the migration above. Starting the application adds its own `ingest` schema; it does not alter historical `public` tables.

Both published ports bind to loopback by default. For a trusted local network, set `INGEST_BIND_IP=0.0.0.0` and open `http://<server-LAN-address>:<INGEST_PORT>`; localhost remains available and administrator authentication is unchanged. This is unencrypted HTTP and listens on every IPv4 interface, including VPN interfaces. Keep PostgreSQL on loopback and do not forward the HTTP port through an internet router. For native deployments, the equivalent listener is `--bind 0.0.0.0:<port>`.

For internet access, use an HTTPS reverse proxy and set `INGEST_PUBLIC_URL` to the exact public origin, for example `https://ingest.example.com`. This enables secure session cookies and origin validation. Leave it unset when accessing a private instance through multiple local origins. Do not expose the HTTP service directly to the internet.

### Durable operation

Start the installation from its deployment directory with `docker compose up -d`. Pull the selected image explicitly before an upgrade. Keep its private `.env`, any local `compose.override.yaml`, and both named volumes together as the deployment configuration. Both services use `restart: unless-stopped`: they return when the Docker engine restarts, but deliberately stopped containers remain stopped. On macOS, arrange for Docker Desktop to start at login, using its preference or a user LaunchAgent. The machine must remain awake for collection and scheduled backups.

An existing installation must be migrated, not replaced with empty volumes. Create and verify an encrypted backup first, preserve its recovery identity, pause active collection, and stop PostgreSQL before copying a physical cluster. Retain the original storage until the new installation has been checked. Preserve the PostgreSQL major version, source bytes, vault key, run snapshots and checkpoints; changing paths or volume names is not a migration. Never use `docker compose down --volumes` or prune these volumes as a restart procedure.

**Collection settings → Automatically resume technically interrupted runs** enables technical restart recovery. It does not release manual, quota or no-progress holds; budget-paused scheduled runs follow their existing continuation rules. Recovery keeps the same run and immutable source snapshot. See [Backups and recovery](BACKUPS.md) for encrypted archives and isolated restore verification.

## Administrator security

**Security** manages two-step verification, active sessions and the append-only security audit. Enroll an RFC 6238 authenticator, confirm a six-digit code, and save the one-time recovery codes outside this server. Authenticator codes cannot be replayed, including across service instances; each recovery code works once. Enabling, disabling or replacing recovery codes requires the current password, and changes to active protection also require an unused second factor. These changes revoke other sessions.

Sessions persist in PostgreSQL, expire after twelve hours, and can be revoked individually or together. The browser uses an HttpOnly cookie; passwords, authenticator secrets and recovery codes are not kept in browser storage. Device descriptions are deliberately coarse, not stored IP addresses or raw user-agent strings. Password changes invalidate old sessions. The audit records security and share actions with opaque targets, never credentials or historical secret values.

## Vault key

Back up **both** the PostgreSQL database and the state volume. Losing `vault.key` makes stored credentials unrecoverable. Do not regenerate it for an existing vault. A supplied `INGEST_MASTER_KEY` must be exactly 32 bytes encoded as base64 or hexadecimal.
