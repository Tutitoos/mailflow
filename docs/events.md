# Real-time events

Mailflow exposes a standard authenticated WebSocket at `/api/v1/events`. The connection uses the same short-lived bearer JWT and current-user resolution as the REST API. Native clients send the normal `Authorization: Bearer` header. Browser clients, which cannot set that header through the WebSocket API, request `mailflow.v1` and `mailflow.bearer.<JWT>` as subprotocols; the server negotiates only `mailflow.v1` and passes the token to the same JWT validator. Tokens are never accepted in the URL. Browser clients must also use the installation origin; native clients may omit `Origin`.

Every persisted message has the same versioned envelope:

```json
{
  "version": 1,
  "cursor": "1788744000000-0",
  "type": "system.status",
  "timestamp": "2026-09-07T12:00:00Z",
  "payload": { "status": "ready" }
}
```

Phase 1 reserves `mail.changed`, `sync.progress`, `draft.changed`, `admin.alert`, and `system.status`. Payloads are bounded JSON and reject credential, token, cookie, email, recipient, subject, and message-body fields. Redis stream keys use a truncated SHA-256 digest of the user ID rather than personal data.

## Resume protocol

The client stores the cursor only after applying an event. On reconnect it sends that value as `?cursor=<cursor>`. The server replays only later events, so the acknowledged event is never duplicated. A connection without a cursor starts at the current stream tail and receives only new events.

The replay window retains at most 2,000 events for 24 hours per user and returns at most 500 events in one reconnect. If trimming, expiry, or that replay limit makes the cursor unusable, the server sends `system.resync_required` with `{"reason":"cursor_expired"}`. The client must refresh its REST state, record the cursor included in that control envelope, and continue consuming live events.

## Connection limits

- The server reads live events in batches of at most 32 and writes them directly; it never creates an unbounded per-client queue.
- Each write has a five-second deadline. A client that cannot keep up is disconnected and resumes from its last applied cursor.
- Ping frames are sent every 20 seconds and a missing pong closes the connection after 45 seconds.
- Client data frames are limited to 1 KiB and ignored; the channel is server-to-client only.
- Graceful API shutdown sends WebSocket close code 1001 before the process exits.
