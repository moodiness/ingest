# Development

[Back to README](../README.md) · [Deployment settings](SETTINGS.md) · [Source configuration](CONFIGURATION.md)

The production binary embeds the frontend. Node.js is needed to build or develop the panel, not to run the resulting binary.

## Local development

Requirements: Go 1.26.8+, Node.js 22.19+ and PostgreSQL. CI reads the Go version from `go.mod`; keep both modules and the Docker compiler aligned when updating security patches.

```sh
make build
export DATABASE_URL='postgresql://USER:PASSWORD@127.0.0.1:5432/ingest'
./bin/ingest init
./bin/ingest serve
```

The default bind address is `127.0.0.1:8080`. State is stored in `.ingest/`, and private strict JSON definitions in `providers/`. Override with `--data-dir`, `--providers`, `--bind` and `--workers`, or `INGEST_DATA_DIR`, `INGEST_PROVIDERS_DIR` and `INGEST_BIND`. Normal loading accepts `.json` only (case-insensitive) and skips `.example.json`; legacy source YAML requires the explicit [offline migration](CONFIGURATION.md#migrating-existing-sources). Compose and CI keep their YAML configuration.

The unauthenticated liveness endpoint is `GET /healthz`. Detailed database diagnostics require an authenticated session.

For frontend hot reload, keep the Go service running and use another terminal:

```sh
npm --prefix web run dev
# For a non-default API address:
API_PROXY_TARGET=http://127.0.0.1:8080 npm --prefix web run dev
```

CLI examples:

```sh
./bin/ingest validate
printf '%s' 'YOUR_SECRET' | ./bin/ingest secret set my_index_token
./bin/ingest enqueue --provider my_index --mode preview --max-pages 3
./bin/ingest enqueue --provider all --mode incremental
# Requires a native HTTP/JSON source with ID recovery and enrich_fields.
./bin/ingest enqueue --provider my_index --mode metadata
./bin/ingest pause --run RUN_ID
./bin/ingest resume --run RUN_ID
./bin/ingest cancel --run RUN_ID
```

`enqueue` only queues durable work; a running service performs it. `pause` commits a durable hold without aborting an in-flight request; `resume` releases the hold and queues the same saved run; `cancel` interrupts active work. These commands share the service's `DATABASE_URL`, data directory and provider directory. Secret input is read from stdin and never echoed. Use `ingest help` or a command's `--help` for available flags.

### Source editor and API

**Sources** edits the same JSON through five connected **Visual stages** (Identity, Connection, Pagination, Mapping and Collection) or advanced **JSON** mode. Import/export works on editor documents, not run snapshots. Validation and saving do not contact the source or start a run; source revisions reject conflicting writes without discarding the local draft. See [Source configuration](CONFIGURATION.md#configure-your-sources) for the complete workflow.

The authenticated source API uses `GET /api/providers/{id}` → `{provider, json, revision, issues}`, `PUT /api/providers/{id}` with `{json, revision}`, and `POST /api/providers/validate` with `{json}`. Here `json` is the source document as a string, not a nested object. There is no `yaml` alias. `GET /api/provider-schema` returns the authenticated JSON Schema; its route is deliberately outside `/api/providers/{id}`, so a source whose ID is `schema` remains addressable. The schema supports editor tooling but does not replace runtime connector and credential-reference validation. App definitions have a 128 KiB limit and reject duplicate keys, unknown fixed fields, wrong types, scalar nulls and decoded NUL; arbitrary values are supported only in declared open maps/body, with exact numeric lexemes retained.

## Local Docker build

The distributed `compose.yaml` consumes GHCR. Building from a checkout is an explicit development choice:

```sh
docker compose -f compose.yaml -f compose.build.yaml up -d --build
```

This builds `ingest-local:dev` with version `dev`, never the GHCR application tag. Explicit `-f` arguments do not automatically include a private `compose.override.yaml`; add it explicitly if required. Preserve existing database credentials, mounts and volume names when working on an existing installation. Never point verification at production volumes.


## Verification

```sh
make check
```

PostgreSQL integration checks are opt-in locally. Set `INGEST_TEST_DATABASE_URL` to a **disposable PostgreSQL instance** with permission to create databases. Each test creates a random isolated database and removes only that database. CI runs these checks, including compact observations without new raw payloads, historical archive access, metadata-only updates, publication/cancellation boundaries, restart recovery, vault authentication, JSON revision conflicts, catalogue scope/credential isolation, native-identifier privacy, TLS refusal, immutable snapshots, atomic remote checkpoints, quota handling and durable schedules.

The independently reusable Torznab SDK remains in `torznab/`; its own tests run separately as part of `make check`.

### Production browser regressions

`make check` keeps its existing behavior: Go/PostgreSQL checks and frontend checks, with database integration opt-in locally. Browser coverage is a separate, **database-required** command; an absent `INGEST_TEST_DATABASE_URL` is an error, not a skip.

Build the embedded application, install Chromium once, and point the suite at a disposable local PostgreSQL service:

```sh
make build
npm --prefix web exec -- playwright install chromium
export INGEST_TEST_DATABASE_URL='postgresql://TEST_USER:TEST_PASSWORD@127.0.0.1:5432/postgres'
make e2e
# Equivalent runner, including Playwright arguments:
npm --prefix web run e2e -- --headed
```

The environment contract is deliberately separate from development and production:

| Variable | Contract |
| --- | --- |
| `INGEST_TEST_DATABASE_URL` | Mandatory PostgreSQL maintenance URI on `127.0.0.1`, `localhost` or `[::1]`, using database `/postgres`, on a **disposable** service. The role needs `CREATEDB` and permission to drop its own databases. Omitted port means `5432`; ports `55485` and `55486` are refused. Only SSL query options are accepted; host/database query overrides are rejected. |
| `INGEST_E2E_BINARY` | Optional executable production binary. Defaults to `../bin/ingest`; relative paths are resolved from `web/`, independent of the shell's current directory. The binary must already embed the built frontend; no Vite server or external web directory is used. |

Do not point this command at a production PostgreSQL service. `DATABASE_URL`, existing private providers, `.ingest/`, and an already-running HTTP service are never selected as test inputs. The runner creates a random `ingest_e2e_<random>` database, binds its own backend to a random loopback port between 20000 and 48999, and waits for that exact child process's readiness before accessing it. The child receives a minimal environment, private temporary working/state/provider/home directories, and a random temporary administrator password, not the developer's runtime configuration.

The Chromium suite uses the normal login form, HttpOnly session cookies, CSRF-protected API writes, the production UI, and real PostgreSQL storage. Every synthetic source is disabled and has no schedule. Its URL is an owned loopback tripwire, not a real upstream; any attempted source request fails the suite. Browser egress outside the owned app origin is blocked and also fails the suite. The durable cases are:

- JSON file import, visual edits, download/export, API save and page reload preserve huge integers, long decimals and exponent spellings exactly.
- Another authenticated writer creates a stale revision; the local draft survives rejection and comparison, and cannot overwrite the server until explicit resolution.
- Invalid raw JSON and structured CodeMirror drafts block save, validation, export and unsafe stage/mode changes. Input survives focus/keyboard navigation; explicit discard or repair releases the guard.
- Clearing numeric array/map entries preserves editable rows and siblings, blocks saving until repaired, and retains exact numeric values.
- Adding a source with an existing ID cannot adopt its revision or overwrite it; selecting a new ID creates a separate source.

One worker runs without automatic retries. Worker teardown first stops only its owned backend, then checks the random database name, PostgreSQL OID and owner before dropping it. Private-directory removal requires its ownership marker and guarded temporary path. Setup failures use the same cleanup path; a guard failure is reported rather than deleting an unowned resource. An uncatchable runner kill or machine shutdown can still leave a random test database or `ingest-e2e-*` OS temporary directory; inspect ownership before manually removing such leftovers.

Authentication state is not written to the repository. Downloads are read and removed, private state is removed at teardown, and traces, videos, screenshots and persistent per-test reports are disabled. Generated Playwright output/auth/download directories are ignored. CI deliberately uploads no browser artifacts or backend logs because recordings may contain cookies, CSRF tokens or temporary credentials.

The CI quality job runs `make check` and `make security`, builds `bin/ingest` with `-tags production`, installs Chromium with `npx playwright install --with-deps chromium` from `web/`, then runs `make e2e` against its PostgreSQL service. A missing browser, binary, database or login fails the job.

## Release verification and publication

The local release gate does not push images, tags or commits and does not start a Compose deployment:

```sh
export INGEST_TEST_DATABASE_URL='postgresql://TEST_USER:TEST_PASSWORD@127.0.0.1:5432/postgres'
npm --prefix web exec -- playwright install chromium
make release-check VERSION=0.1.0-rc.1
```

Use a disposable PostgreSQL service satisfying the browser contract above, a running Docker engine with Buildx, and a complete Git checkout. `make release-check` runs Go race/vet checks for both modules, frontend build/format checks, full-history Gitleaks, both source-aware Go vulnerability scans, npm audit, the embedded-browser suite, Compose validation, native container command smoke checks, a Linux `amd64`/`arm64` build, and `make image-security`. The latter scans the native application image with pinned Trivy and fails on HIGH/CRITICAL findings, including unfixed ones. It exposes only a temporary image archive to the scanner, not the Docker socket, repository or private state. CI also validates workflow and Dockerfile syntax. A local history scan covers committed history; review and scan intended publication files before committing them as well.

The application image uses the pinned PostgreSQL 18 Alpine variant for compatible backup clients, without PostgreSQL's unused root entrypoint or `gosu`. The separate Compose database remains on the Debian variant: do not switch an existing cluster between libc implementations to improve scanner counts, because locale/collation behavior can change. Keep its minor version patched and review the database image separately; a clean application-image scan is not a CVE-free claim for the entire deployment. Assess package/module findings against vendor advisories and actual imported code rather than adding blanket ignores.

Before the first release, also exercise a **fresh isolated deployment**: log in, add a synthetic source, run Preview, collect, pause/resume the same run, create an encrypted backup, and restore it into a new database and private state directory. Check exact catalogue identities, quarantined source files and decryptable secrets. Do not substitute successful archive creation for a real restoration.

If the public repository starts empty while the working repository has private history, publish a reviewed clean tree as its first public commit or explicitly review every historical object first. Ignoring a file does not remove it from old commits. Do not push unreviewed branches or legacy tags with `--all` or `--tags`.

The [release workflow](../.github/workflows/publish-image.yml) checks pull requests, `main` pushes and manual dispatches without publishing. An explicit pushed `vMAJOR.MINOR.PATCH[-PRERELEASE]` tag triggers publication only after all gates pass:

- GHCR receives the exact version without `v` and `sha-FULL_COMMIT-VERSION`; existing exact tags are refused.
- Prereleases never update `latest`. A stable release updates `latest` and creates the corresponding GitHub release. Same-commit prerelease-to-stable promotion is supported.
- Images contain both Linux architectures, OCI source/version/revision labels, provenance and an SBOM.
- The workflow requires package-write permission for publishing and content-write permission only for creating release notes. Version tags must not be moved or reused.

No image exists merely because these files are present. After the first publication, ensure the `ingest` package is **public** and test an unauthenticated pull of the versioned image before announcing it. Repository visibility alone does not guarantee package visibility. Use a new version for a corrected release; `latest` is a moving channel, not an immutable deployment pin.


## Standalone Torznab SDK

[`torznab/`](../torznab/) is independently reusable and has no database dependency. Its [`examples/crawl`](../torznab/examples/crawl/) command discovers an endpoint, traverses pages and streams metadata as JSON Lines to stdout, with diagnostics on stderr.

```sh
go -C torznab run ./examples/crawl -help
```

The example does not download torrents, write to PostgreSQL or use Ingest's vault and durable jobs. It accepts an endpoint or a strict SDK provider JSON file and resolves credentials from environment variables. The SDK's smaller JSON schema and 64 KiB limit are separate from Ingest's 128 KiB app source definitions; see [`torznab/doc.go`](../torznab/doc.go) and the [JSON reference](JSON_REFERENCE.md). SDK definitions are not accepted by the app's source migrator; convert legacy SDK configuration separately to the SDK JSON schema. The application builds against the checked-out `torznab/` module; users of the prebuilt image do not install it separately.
