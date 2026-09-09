# Production deployment

Mailflow production uses the base Compose model plus
`deploy/compose.production.yml`. The production overlay removes every local
build, requires immutable image digests, exposes only Traefik on host ports 80
and 443, and places PostgreSQL, Redis, the worker, migrations and backups on an
internal Docker network.

Traefik uses a Compose-managed file-provider configuration. It does not mount
the Docker socket and its dashboard/API remain disabled. HTTP redirects to
HTTPS; ACME, HSTS, content-type and frame protections, compression, and bounded
rate limits are configured at the edge.

## Host prerequisites

- A supported Linux host with Docker Engine and Docker Compose 2.24.4 or newer.
- Public DNS for `MAILFLOW_DOMAIN` pointing to the host.
- Inbound TCP 80 and 443; PostgreSQL and Redis must not be published or opened
  in the host firewall.
- Persistent storage sized for PostgreSQL, Redis, cached attachments, ACME and
  backup staging, plus a backup target outside the application volumes.
- A verified Mailflow release evidence set and independently verified vendor
  digests for Traefik, PostgreSQL and Redis.

## Prepare immutable inputs

Download and verify the Mailflow release evidence as described in
[`supply-chain.md`](supply-chain.md). Copy the example environment without
placing credentials in it:

```sh
cp deploy/.env.production.example deploy/.env.production
chmod 600 deploy/.env.production
sudo install -d -m 700 -o "$(id -un)" -g "$(id -gn)" \
  /srv/mailflow/secrets /srv/mailflow/runtime /srv/mailflow/releases
```

Set the five `MAILFLOW_*_IMAGE` values from the verified release manifest. Pin
Traefik, PostgreSQL and Redis to their reviewed `sha256` manifest digests too.
Tags, short SHAs and mutable aliases such as `latest` are rejected.

Create every file documented in [`deploy/secrets/README.md`](../deploy/secrets/README.md)
under the absolute `MAILFLOW_SECRETS_PATH`, with mode `600` and ownership by the
account operating Docker.
`MAILFLOW_TRAEFIK_CONFIG_PATH` must point inside the private runtime directory;
the operator script renders it atomically from the reviewed template. That
generated file contains no credentials and is mode `644` so Traefik's non-root
process can read the Compose-backed config; its parent directory remains `700`.
Optional provider and alert secret files may be empty, but the authentication,
bootstrap, recovery, PostgreSQL, Restic and master-key files may not. Keep the
Restic password, recovery code, master key and the exact release environment in
separate offline custody.

## Validate and deploy

The operator entrypoint validates file modes, required values, digest
identities and the fully merged Compose model without printing the environment
or secret contents. Values inherited from the operator shell are cleared for
deployment keys so they cannot override the reviewed release file:

```sh
./scripts/production-compose.sh check deploy/.env.production
./scripts/production-compose.sh apply deploy/.env.production
```

Each invocation renders `traefik-dynamic.yml.template` into the private,
absolute `MAILFLOW_TRAEFIK_CONFIG_PATH` using the already validated DNS name.
The write is atomic, contains no credential and is mounted read-only by
Compose; a symlink or unsafe parent directory is rejected.

`apply` pulls every digest before starting with `--no-build --wait`. Migrations
must complete successfully before auth, API and dependent services start. A
missing image, invalid secret, failed migration or unhealthy service stops the
operation instead of falling back to a local build or mutable tag.

Verify the external boundary and service state from a second terminal:

```sh
docker compose --env-file deploy/.env.production \
  -f deploy/compose.yml -f deploy/compose.production.yml --profile backup ps
curl --fail --silent --show-error --head "https://mail.example.com/"
```

Only `traefik` may show published host ports. The first certificate request can
take a short time; repeated failure requires inspecting Traefik JSON logs and
DNS/firewall state, never enabling insecure HTTP or the dashboard.

## Update and rollback

Store each verified environment as a read-only release record outside Git,
alongside its evidence and compatible backup identifier. Validate a new record
before applying it. Do not edit the active record in place.

Rollback is explicit and still verifies every previous digest and local secret
before pulling and starting:

```sh
./scripts/production-compose.sh rollback /srv/mailflow/releases/vX.Y.Z.env
```

An image rollback does not reverse a database migration. Confirm compatibility
in the release notes; when it is not guaranteed, stop the stack and restore the
matching tested backup according to [`backups.md`](backups.md). Never overwrite
published evidence or reuse a release tag.

## Spanish operator summary

Producción combina `compose.yml` con `compose.production.yml`, exige imágenes
por digest, no monta el socket Docker y solo publica 80/443 mediante Traefik.
El archivo de entorno y todos los secretos deben tener permisos privados. Usa
`production-compose.sh check` antes de `apply`; para volver atrás utiliza un
archivo de release anterior ya verificado y restaura su backup compatible si
hubo migraciones incompatibles. No sustituyas digests por tags ni abras
PostgreSQL, Redis o el dashboard de Traefik.
