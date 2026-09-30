# Backups and recovery

[Back to README](../README.md) · [Deployment settings](SETTINGS.md) · [Security policy](../SECURITY.md)

## Create and verify backups

**Backups** creates an age-encrypted archive containing a consistent PostgreSQL custom-format dump, exact source JSON bytes (including invalid definitions), the vault master key and an integrity manifest. Historical/public tables, compact observations, existing historical raw archives, publication history, installation identity, sessions and encrypted MFA state are included. New raw bodies are not retained by collection; backups do not recreate them or delete existing archives. The plaintext administrator password file is not copied. Recovery clears the restored password verifier so a new password can be configured.

1. Generate a recovery identity in **Backups** and save the private identity outside this server. It is shown once; only the public recipient is stored. Losing it makes those encrypted archives unreadable.
2. Use **Back up now**, or enable the daily schedule with an explicit **UTC** time. Scheduling is opt-in, durable and serialized across instances. A missed slot runs when the service returns, without a catch-up burst.
3. Download completed archives and keep encrypted copies on independent storage. The server stores them under `<state-dir>/backups/`; there is no automatic deletion or remote-storage upload.
4. Use **Verify** with the corresponding private identity. Verification really restores into a newly created isolated PostgreSQL database, compares schema versions and every table's row count, and checks vault/MFA decryptability. It never replaces live data and removes its temporary database and plaintext workspace afterwards.

The Docker image contains `pg_dump` and `pg_restore` from the pinned PostgreSQL runtime. Native installations need both tools on `PATH`, or explicit `INGEST_PG_DUMP` / `INGEST_PG_RESTORE` paths. Client, server and archive PostgreSQL major versions must match; use the corresponding application release for recovery. The role needs dump/read permissions and **CREATEDB** for isolated verification or restoration. Connection credentials are passed through a private subprocess environment, not arguments or browser responses. Connection TLS policy is preserved; unsupported security settings fail closed.

Recovery validates the archive's contiguous migration history against the schema versions supported by the running binary, not a fixed historical maximum. An archive requiring a newer, unrecognized schema is rejected before a restore database is created. Integrity checks still compare the archived schema and every table count exactly; accepting a supported later migration does not bypass validation.

Archives and private state need enough disk capacity. Verification/restoration temporarily requires plaintext archive space and a second database. Operations have a two-hour deadline; archive recovery is bounded to 1 TiB, 10,000 source files, 8 MiB per source and 64 MiB of combined source definitions. These recovery limits do not increase the app's 128 KiB active JSON-definition limit. Multiple replicas must share the same private state/source storage as well as PostgreSQL. Downloads support Range/HEAD and a sliding idle-write timeout, not a thirty-second total lifetime.

Interrupted jobs are reconciled by durable ownership. Incomplete temporary ciphertext is removed; completed, checksum-matching archives are finalized without being discarded. Successful or unrelated archives are never swept. Unverifiable completed files remain preserved for operator review. Status, last success/failure and sanitized notifications are available in the panel.

### Local copies outside Docker storage

For local-only operation, bind an existing private host directory to `/data/backups` in the ignored `compose.override.yaml`. This keeps completed encrypted archives outside Docker Desktop's virtual disk. Before adding the mount, stop backup jobs and copy any existing completed archives to that directory with matching checksums: a bind mount hides the old directory contents without moving them.

The non-root application user must be able to traverse `/data` and own both its state volume and the bound backup directory. Use directory mode `0700` and archive mode `0600`; align the container UID/GID with the host owner rather than making backups world-writable. The private recovery identity belongs in a separate `0700` directory with file mode `0600`, and must not be mounted into the application container or committed to the repository.

Enable an explicit daily UTC time, confirm that a completed archive exists on the host, and perform **Verify** with its recovery identity. A successful verification must report that the isolated database and plaintext workspace were removed. Keep the identity for every retained archive; changing the recipient does not re-encrypt older backups.

This is not an off-machine backup. A second directory on the same Mac still cannot protect against loss of that Mac or its disk. Independent encrypted storage and a separately retained identity are required for that protection; the application does not upload archives automatically.

## Restore into a new destination

Only restore archives produced by a trusted installation: PostgreSQL archives contain executable SQL. Recovery is an explicit CLI operation, not a browser upload or an in-place overwrite.

```sh
# Keep the maintenance DSN in the environment, not command-line arguments.
export INGEST_RESTORE_DATABASE_URL='postgresql://USER:PASSWORD@HOST:5432/postgres'
chmod 600 /private/recovery.agekey
./bin/ingest backup restore \
  --archive /private/snapshot.age \
  --identity-file /private/recovery.agekey \
  --database ingest_recovered \
  --state-dir /private/ingest-recovered \
  --trust-archive
```

Both the destination database and state directory **must not exist**; their parents/server must already be available. The CLI authenticates the full encrypted archive, checks its manifest and restored database, and refuses existing destinations.

After restoration, active `providers/` is empty and exact source files are quarantined in `recovered-sources/`. New archives contain JSON; historical archives may contain `.yml`/`.yaml`, accepted only for recovery into quarantine, not normal source loading. Runs are paused, sessions revoked, pending MFA enrollment cleared, outbound deliveries cancelled, and source schedules, shares, webhooks, automatic backups and automatic interrupted-run recovery disabled. Active MFA protection is preserved. Configure a new administrator password, retain the restored `vault.key`, review source files before moving them into `providers/`, and explicitly re-enable only the automation you intend. Keep the authenticator/recovery codes and old age identities needed by older backups.

For historical YAML sources, keep the recovered installation stopped and run the [offline source migration](CONFIGURATION.md#migrating-existing-sources) against `recovered-sources/` before activating anything. Preserve that directory and its `.source-migration/` originals/journal, review and validate the resulting JSON, and move only the reviewed `.json` definitions into active `providers/`. Do not copy legacy YAML into the active directory or treat recovery acceptance as runtime compatibility. Migration does not rewrite restored run snapshots or checkpoints; Resume still uses the saved run configuration.

Normal backups capture top-level JSON files from the source directory, including invalid definitions and example JSON, not the offline migrator's `.source-migration/` recovery directory. Retain a separate protected copy of migration originals and their journal alongside your pre-migration backup; do not rely on a new application archive to preserve that recovery material.

### Docker-only recovery

No host Go compiler or PostgreSQL client is needed. Use an **available, pinned** Ingest image compatible with the archive's schema and PostgreSQL major version. The example uses the Compose network `ingest_default` and its `postgres` host; substitute the destination server's network/hostname if recovering elsewhere. The database role needs `CREATEDB`.

Choose an unused database name and a **new** state volume. Never mount the original installation's state volume into the recovery command. `docker volume create` does not reject an existing volume name, so check that the chosen name is unused. The volume mount itself already exists inside the container; restore into its nonexistent `/data/recovered` child, not `/data`.

Create two private environment files, both mode `0600`:

- `/private/restore.env`: `INGEST_RESTORE_DATABASE_URL=postgresql://USER:PASSWORD@postgres:5432/postgres`
- `/private/recovered.env`: `DATABASE_URL=postgresql://USER:PASSWORD@postgres:5432/ingest_recovered`

Keep those files, the downloaded archive and its recovery identity outside the repository. Use URL-encoded credentials in the DSNs. The destination database must not have been pre-created by another application or Compose startup.

```sh
INGEST_IMAGE=ghcr.io/moodiness/ingest:0.1.0-rc.1
chmod 600 /private/restore.env /private/recovered.env /private/recovery.agekey
docker volume create ingest_recovered_state
docker run --rm --user 0:0 --network ingest_default \
  --env-file /private/restore.env \
  --mount type=volume,src=ingest_recovered_state,dst=/data \
  --mount type=bind,src=/private/snapshot.age,dst=/run/snapshot.age,readonly \
  --mount type=bind,src=/private/recovery.agekey,dst=/run/recovery.agekey,readonly \
  "$INGEST_IMAGE" backup restore \
  --archive /run/snapshot.age \
  --identity-file /run/recovery.agekey \
  --database ingest_recovered \
  --state-dir /data/recovered \
  --trust-archive
docker run --rm --network none --user 0:0 \
  --mount type=volume,src=ingest_recovered_state,dst=/data \
  --entrypoint chown "$INGEST_IMAGE" -R 65532:65532 /data/recovered
```

Root is used only for the trusted one-off restore and ownership handoff: it can read the host's private recovery identity and initialize the fresh volume. Start the application normally as its non-root image user, without mounting the identity or restore environment file:

```sh
docker run -d --name ingest-recovered --network ingest_default \
  --env-file /private/recovered.env \
  -e INGEST_DATA_DIR=/data/recovered \
  -e INGEST_PROVIDERS_DIR=/data/recovered/providers \
  --mount type=volume,src=ingest_recovered_state,dst=/data \
  --tmpfs /var/lib/postgresql \
  -p 127.0.0.1:8081:8080 \
  "$INGEST_IMAGE"
docker exec ingest-recovered /ingest password
```

Set **both** directory variables: the image's default source directory is the absolute `/data/providers`, not a path relative to `INGEST_DATA_DIR`. Log in at `http://localhost:8081` with the newly generated password (or set a new `INGEST_ADMIN_PASSWORD` in the private runtime environment).

The quarantine and disabled-automation rules above still apply. Review `/data/recovered/recovered-sources/` and import only the intended JSON definitions through the panel. Preview verifies that restored vault credentials work before collecting again. Native identities, linked Incremental/Metadata runs and committed cursors survive recovery; explicitly Resume the existing paused run instead of creating a replacement merely to continue its work.

