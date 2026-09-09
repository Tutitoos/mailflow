# Private Tailscale testing

This profile runs Mailflow behind HTTPS available only to members of the
installation's tailnet. It is intended for personal acceptance testing before a
public domain, Developer ID signing, notarization, or updater credentials exist.

## Prepare the installation

Install and enrol Tailscale on the server without copying another machine's
Tailscale state. Enable MagicDNS and HTTPS for the tailnet, then set the node's full
MagicDNS name in `deploy/.env`:

```dotenv
MAILFLOW_DOMAIN=mailflow.example-tailnet.ts.net
```

Create every file documented in [`deploy/secrets/README.md`](../deploy/secrets/README.md).
Keep the secret directory outside Git with mode `700` and each value at mode
`600`. Provider and SMTP secret files may remain empty only while their
corresponding non-secret feature configuration is absent.

Take a database dump and a recoverable copy of the Redis and CDN volumes before
applying migrations. Then validate and start the private profile:

```bash
docker compose --env-file deploy/.env \
  -f deploy/compose.yml -f deploy/compose.tailscale.yml config --quiet
docker compose --env-file deploy/.env \
  -f deploy/compose.yml -f deploy/compose.tailscale.yml up -d --build
tailscale serve --bg --yes http://127.0.0.1:8090
```

The overlay keeps Traefik inactive unless the `public-edge` profile is selected.
Only the loopback gateway port is published; Tailscale terminates TLS and makes
the origin available to the tailnet. Do not enable Funnel for this profile.

Verify the persisted Serve configuration and the same-origin routes:

```bash
tailscale serve status
curl --fail "https://${MAILFLOW_DOMAIN}/health/live"
curl --fail "https://${MAILFLOW_DOMAIN}/api/auth/ok"
```

An unauthenticated request to `/api/v1/admin/status` and a WebSocket upgrade to
`/api/v1/events` must return `401`. A signed-in owner should then verify REST and
WebSocket activity through the SPA.

## Build the local macOS client

The private endpoint is embedded as public build metadata. The updater remains
disabled when its endpoint and public key are omitted:

```bash
MAILFLOW_DESKTOP_ORIGIN="https://${MAILFLOW_DOMAIN}" \
  bun run --cwd apps/desktop build
```

Copy `apps/desktop/src-tauri/target/release/bundle/macos/Mailflow.app` to a
separate local test location. An ad hoc signature may be applied for local use;
it is not a Developer ID signature and must never be represented as a release
artifact. The client must open the exact HTTPS origin and reject navigation to
any other origin.

## Stop private access

```bash
tailscale serve --https=443 off
docker compose --env-file deploy/.env \
  -f deploy/compose.yml -f deploy/compose.tailscale.yml down
```

Named PostgreSQL, Redis, CDN, and backup volumes remain intact. Do not add `-v`
unless permanent deletion of those volumes is explicitly intended.
