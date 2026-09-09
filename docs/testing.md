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
See [`container-releases.md`](container-releases.md) for the evidence boundary.
