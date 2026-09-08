# Internal metrics

Mailflow owns its metrics pipeline and does not expose a Prometheus endpoint. API and worker processes aggregate bounded series in memory, flush one transactional delta per minute to PostgreSQL, and continue collecting if a flush fails. Counter and histogram deltas are restored after a failed transaction; gauges retain their latest value.

Only `service`, `module`, `provider`, `operation`, and `result` dimensions are accepted. Names and values use short lowercase identifiers, each process accepts at most 2,048 series definitions during its lifetime, and query responses are capped at 5,000 points. Account IDs, email addresses, message IDs, subjects, recipients, bodies, tokens, cookies, credentials, and signed URLs must never be supplied as metric dimensions.

## Storage and rollups

- Minute points are kept for 30 days.
- Hour points are kept for one year.
- Day points have no automatic expiry.
- Histograms use 13 fixed latency buckets and expose approximate p50, p95, p99, minimum, maximum, and average values.
- Counter responses include a per-second rate for their resolution.

The maintenance pass replaces a completed hour or day from its source points, so retrying after a restart cannot double count a rollup. The raw minute flush uses drained deltas in one database transaction, allowing multiple processes to contribute to the same bucket without replacing each other.

Current producers cover HTTP requests and latency, queue outcomes, Google synchronization pages, mail actions, CDN cleanup, Sentry envelope ingestion, backup outcomes and duration, alert delivery outcomes, and Go heap/goroutine gauges. Provider implementations must use the same registry rather than create a second telemetry path.

## Admin query

`GET /api/v1/admin/metrics` is protected by the normal owner authentication boundary. It accepts `resolution`, `name`, `from`, `until`, and `limit`. Minute and hour queries are bounded to their retention windows; daily history is queried in bounded chunks. Invalid or oversized queries return Problem Details without echoing input values.

Migration `00017_metric_series.sql` adds histogram and aggregate columns plus the query index. Rolling it back removes those additions while preserving the original minute metric table.
