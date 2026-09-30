# Security policy

## Supported code

Security fixes target the current `main` branch. Older commits and forks are not maintained as separate security branches. Include the output of `ingest version` or your commit/image digest when reporting a problem.

## Report a vulnerability privately

**Do not publish vulnerability details in issues, pull requests or discussions.**

Use [GitHub's private vulnerability reporting](https://github.com/moodiness/ingest/security/advisories/new), enabled for this repository. If that channel is unavailable, [request a private reporting contact](https://github.com/moodiness/ingest/issues/new?title=Private%20security%20contact%20request). That request must contain only a request for contact: no exploit, affected endpoint, credentials or technical details. Wait for a private channel before sending the report.

A useful private report includes:

- Affected version or commit, deployment method and relevant configuration with secrets removed.
- Expected behavior, observed behavior and security impact.
- Minimal reproduction steps or a proof of concept using synthetic data.
- Any known mitigation or proposed fix.

Never attach a live database dump, unredacted raw responses, passwords, API keys, cookies, `vault.key`, an age recovery identity, MFA secrets or recovery codes. Test only systems you own or are authorized to assess. Coordinate disclosure with a maintainer; no response-time or remediation SLA is promised.

## Deployment responsibilities

- **Keep the backend and PostgreSQL private.** For remote access, use a trusted HTTPS reverse proxy and configure `INGEST_PUBLIC_URL` to its exact origin. The setting does not provide TLS itself.
- **Limit administrator access at the edge.** Prefer a private network or VPN. Login throttling deliberately uses the backend connection's remote address, not arbitrary forwarded headers: a reverse proxy therefore shares one application-level attempt bucket across its clients. Configure per-client throttling on the trusted proxy to reduce temporary lockouts caused by another client; never assume an untrusted `X-Forwarded-For` value establishes identity.
- **Enable two-step verification** and keep recovery codes outside the server. Revoke unused administrator sessions.
- **Protect source definitions, raw archives and backups.** Source JSON, exported editor drafts and `.source-migration/` recovery originals/journals are private administrator configuration, not a public provider catalogue. Original source data can contain sensitive metadata even when a shared catalog exposes only a small field allowlist.
- **Grant the minimum sharing scope.** Use separate recipients and appropriate expiration/limits. Revocation stops future requests, not copies already downloaded or re-shared.
- **Keep the backup recovery identity off-server.** The vault key and encrypted credentials must be backed up together. Test recovery before relying on an archive.
- **Restore only trusted archives.** PostgreSQL dumps contain executable SQL. Encryption and integrity checks do not establish who created an archive; anyone with the public age recipient can encrypt one.

## Security boundaries

The vault encrypts credentials at rest; it does not protect against an attacker controlling the running application or its host and key material. The audit is append-only through the application and protected against ordinary database changes, but it is not a cryptographic guarantee against a privileged database administrator.

Source definitions and webhook destinations are trusted administrator configuration. They can cause outbound requests, including to private networks where supported. Read-only catalog access is not administrator access and does not authorize source configuration, secrets or raw archives.

The source editor's five visual stages configure the existing collector, not arbitrary code or an executable graph. Strict JSON validation rejects malformed definitions but does not establish that an upstream service is trustworthy. Saving or validating a source does not contact its endpoint or start collection; enabling its schedule authorizes later requests. Keep credentials in **Secrets** and use vault references, including in imported/exported JSON. Normal loading accepts only source `.json` files; legacy YAML belongs only in the explicit [offline migration](docs/CONFIGURATION.md#migrating-existing-sources) or historical backup recovery quarantine. Stop all services and other source writers before migration, and protect its retained originals as carefully as live configuration.

Successful logout and detected session expiry remove the authenticated interface without waiting for a later refresh. The panel cancels in-flight queries and discards private query and mutation caches; an older session response cannot restore the signed-out interface. This does not erase files or other copies already saved outside the panel.

See [deployment settings](docs/SETTINGS.md), [sharing boundaries](docs/SHARING.md) and [backup recovery](docs/BACKUPS.md) for operational details.

## Interpreting dependency reports

Run source-aware checks as well as package/version scans. A reported module is not necessarily an imported package or a reachable vulnerable call, and a clean scan is not a security certification.

[GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932) applies to the unmaintained `golang.org/x/crypto/openpgp` package family. The application uses other `golang.org/x/crypto` primitives but does not import that OpenPGP family. Source-aware `govulncheck -tags production ./...` and the Linux production import graph were checked during this audit; no affected imported package or reachable call was found. Binary scanning can still report this package-wide advisory conservatively. Do not suppress the entire crypto module or replace unrelated primitives merely to hide that report; reassess it if dependencies or imports change.

Release gates scan the full Git history with redacted Gitleaks output, run source-aware vulnerability checks for both Go modules, and audit frontend dependencies. Secret scanning, push protection and Dependabot security updates are enabled on the public repository. These checks complement review; neither ignored local files nor a clean scan prove that every credential is absent.
