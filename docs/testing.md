# Testing Mailflow

The default verification does not require Docker and skips integration cases when their service variables are absent:

```bash
make check
```

Run the complete Go integration suite with isolated PostgreSQL 18 and Redis 8 containers:

```bash
make integration
```

The harness publishes both services on ephemeral localhost ports, limits readiness to 60 seconds, prints container logs after a failure, and removes its containers and anonymous volumes on every exit. Each PostgreSQL test receives a new database and each Redis test receives a unique key prefix, so repeated or parallel runs cannot share state.

To focus a package while using services you already operate, provide only test infrastructure endpoints. The PostgreSQL base database name must end in `_test`; the helpers refuse any other name.

```bash
cd services/api
MAILFLOW_TEST_DATABASE_URL='postgres://mailflow:mailflow_test@127.0.0.1:5432/mailflow_test?sslmode=disable' \
MAILFLOW_TEST_REDIS_ADDRESS='127.0.0.1:6379' \
go test -race -count=1 ./internal/platform/queue
```

These values are disposable local examples, not production credentials. CI invokes the same Docker harness and needs no private configuration.

## macOS release verification

The protected desktop release workflow builds one arm64 and x86_64 universal
application and gates upload on Developer ID signature, hardened runtime,
notarization tickets, Gatekeeper assessment, DMG mounting, updater signature
and tamper rejection, and a clean temporary install-launch-uninstall smoke test.
See [`macos-release.md`](macos-release.md) for the exact evidence boundary and
the checks that remain impossible without protected Apple credentials.

## Container release verification

The container workflow builds web, auth, API, worker and backup for both
supported Linux architectures. Its separate native drill inspects all five
filesystems, starts the first-party stack with temporary credentials and checks
the applicable internal health endpoints:

```bash
./scripts/verify-container-images.sh inspect
./scripts/verify-container-images.sh health
```

Published tags add manifest-digest, Cosign and GitHub attestation verification.
Release-policy tests prove that pull-request jobs cannot reach protected signing
authority. Evidence-manifest tests validate CycloneDX metadata, SHA-256 bindings,
safe filenames, complete OCI identities and rejection after artifact or SBOM
tampering.
See [`container-releases.md`](container-releases.md) for the evidence boundary.

## Production Compose verification

The production policy tests render the complete base-plus-production model and
reject mutable images, local builds, public data services, Docker socket
access, missing resource bounds and writable roots. The operator-guard tests
also reject permissive or symlinked secret files and ambient environment
overrides. A separate disposable drill starts the pinned Traefik image on
ephemeral localhost ports and proves health, HTTP-to-HTTPS redirection, routing,
security headers, dropped capabilities, read-only root and cleanup:

```bash
bun test scripts/production-compose-policy.test.ts scripts/production-compose-script.test.ts
./scripts/verify-production-edge.sh
```

The drill never requests a public certificate or starts application/data
services. A real-domain certificate and complete healthy stack remain release
acceptance on the target host, using the verified digests and private secrets
described in [`deployment.md`](deployment.md).

The operator documentation is executable policy too. `operator-docs.test.ts`
checks EN/ES lifecycle parity, local links, secret inventory, immutable command
forms and destructive warnings. `verify-operator-runbook.sh` creates two
private temporary release records, validates both through the production guard,
and proves that the generated edge configuration changes atomically without
starting or mutating a live installation.

## Product acceptance

The 1.0 release candidate has a single repeatable gate for the 100,000-message
performance target, six responsive widths, accessibility, keyboard navigation,
reduced motion and durable service restarts:

```bash
make release-acceptance
```

The protected Google, Microsoft and IMAP timing matrix and the sanitized
evidence format are documented in [`release-acceptance.md`](release-acceptance.md).
Real provider accounts and private message data never enter public CI.
