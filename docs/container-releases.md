# Container releases

Mailflow publishes five first-party images from the same monorepo revision:

| Image | Runtime responsibility |
| --- | --- |
| `ghcr.io/tutitoos/mailflow-web` | Static SPA and health endpoint |
| `ghcr.io/tutitoos/mailflow-auth` | Better Auth service |
| `ghcr.io/tutitoos/mailflow-api` | Fiber API and migration command |
| `ghcr.io/tutitoos/mailflow-worker` | Queue and synchronization worker |
| `ghcr.io/tutitoos/mailflow-backup` | PostgreSQL, CDN and Restic backup runner |

API and worker are separate OCI configurations built from shared Go layers. This
keeps their entrypoints explicit while avoiding duplicate compilation work.

## Pull-request contract

Changes to container inputs trigger the `Container release` workflow. Each
image must build for both `linux/amd64` and `linux/arm64` without registry or
release credentials. A separate native job builds all five images, inspects
their filesystems for repository metadata and common secret paths, and starts
the complete first-party stack with temporary credentials. PostgreSQL, Redis,
migrations, auth, API, worker, web and backup must remain healthy and answer
their applicable internal health endpoints. The job destroys its isolated
containers, volumes and credentials on every exit.

## Publishing a version

Publishing is intentionally tag-only. Before creating `vX.Y.Z` (or the allowed
`-alpha.N`, `-beta.N` and `-rc.N` prereleases), set the root `package.json`
version to the exact value without `v` and merge that change to `main`. The tag
must identify that reviewed commit.

The protected `container-release` environment then builds a manifest list for
each image and publishes:

- the full version, minor version and immutable `sha-<40-character-commit>` tags;
- `linux/amd64` and `linux/arm64` variants;
- BuildKit SBOM and maximum provenance attestations;
- a keyless Cosign signature bound to this repository, workflow and tag;
- a GitHub artifact attestation; and
- `container-release-manifest.json`, containing the exact manifest digest for
  every first-party image.

The workflow also attaches a durable evidence set to the draft GitHub release:
one CycloneDX JSON SBOM per image, a signed
`container-release-evidence.json`, `container-SHA256SUMS`, and the Sigstore verification
and GitHub provenance bundles. The same set remains available as a 90-day workflow artifact for
diagnostics. See [`supply-chain.md`](supply-chain.md) for the trust boundary and
independent verification order.

A stable SemVer tag also advances the convenience aliases `X.Y` and `latest`;
prereleases do not. The complete `X.Y.Z` and `sha-<commit>` identities are
immutable and are the only tags suitable for verification. If a release is only
partially published, fix the cause and issue a new patch or prerelease number
rather than mutating registry history.

## Verify before installation

Download `container-release-manifest.json` from the workflow artifact. Verify
the workflow provenance and each digest before copying the five references into
`deploy/.env`:

```bash
sha256sum --check container-SHA256SUMS
bun scripts/release-evidence.ts verify container-release-evidence.json .
cosign verify-blob \
  --bundle container-release-evidence.sigstore.json \
  --certificate-identity \
  'https://github.com/Tutitoos/mailflow/.github/workflows/container-release.yml@refs/tags/vX.Y.Z' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  container-release-evidence.json
```

```bash
gh attestation verify \
  'oci://ghcr.io/tutitoos/mailflow-api@sha256:<digest>' \
  --repo Tutitoos/mailflow

cosign verify \
  --certificate-identity \
  'https://github.com/Tutitoos/mailflow/.github/workflows/container-release.yml@refs/tags/vX.Y.Z' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  'ghcr.io/tutitoos/mailflow-api@sha256:<digest>'
```

Repeat the verification for web, auth, worker and backup. Configure only digest
references, never mutable tags, in a private copy of
`deploy/.env.production.example`:

```dotenv
MAILFLOW_WEB_IMAGE=ghcr.io/tutitoos/mailflow-web@sha256:<digest>
MAILFLOW_AUTH_IMAGE=ghcr.io/tutitoos/mailflow-auth@sha256:<digest>
MAILFLOW_API_IMAGE=ghcr.io/tutitoos/mailflow-api@sha256:<digest>
MAILFLOW_WORKER_IMAGE=ghcr.io/tutitoos/mailflow-worker@sha256:<digest>
MAILFLOW_BACKUP_IMAGE=ghcr.io/tutitoos/mailflow-backup@sha256:<digest>
```

Pin the reviewed Traefik, PostgreSQL and Redis images by digest as well. Then use
the fail-closed production entrypoint, which validates the complete deployment
before pulling or starting anything:

```bash
./scripts/production-compose.sh check deploy/.env.production
./scripts/production-compose.sh apply deploy/.env.production
```

The migration job intentionally reuses the API digest. Keep the previous
manifest file alongside every backup. Rollback means selecting the prior,
unchanged release environment and running
`./scripts/production-compose.sh rollback PREVIOUS_ENV_FILE`, restoring its
compatible backup first when required. Database compatibility and the restore
drill remain mandatory; an image rollback never reverses data by itself. See
[`deployment.md`](deployment.md) for the complete operator boundary.

## Local builds

Leaving every `MAILFLOW_*_IMAGE` variable empty preserves local development.
Compose assigns local names and builds the Dockerfiles from the checkout:

```bash
docker compose --env-file deploy/.env -f deploy/compose.yml build
docker compose --env-file deploy/.env -f deploy/compose.yml up -d
```

No GHCR login, signing identity or release token enters a build context.
Registry authentication exists only in the tag workflow.

Primary references: [Docker multi-platform GitHub
Actions](https://docs.docker.com/build/ci/github-actions/), [GitHub artifact
attestations](https://docs.github.com/en/actions/concepts/security/artifact-attestations),
and [Cosign's GitHub Action](https://github.com/sigstore/cosign-installer).
