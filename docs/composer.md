# Composer, drafts, and delivery

Mailflow saves composer changes locally on the server after a two-second debounce. A provider checkpoint runs every 15 seconds and is also requested when focus leaves the composer or the user closes it. Closing waits for that checkpoint; a failed or conflicting save leaves the composer open with its content intact. The browser keeps only the active draft identifier for close recovery. Message bodies, subjects, and recipients are never written to logs, metrics, URLs, or event payloads.

Draft updates use `expectedRevision`. A stale update returns `draft_conflict` and cannot overwrite newer content. English is the default interface language and Spanish uses the same recovery states. Rich-text edits produce both sanitized HTML and an equivalent plain-text alternative.

Every `POST /api/v1/send` requires an `Idempotency-Key`. The API records the draft, content hash, and key before the Gmail request. A repeated key returns the existing delivery and never invokes Gmail again. If the process cannot prove whether Gmail accepted a request, the delivery becomes `ambiguous`; clients must ask the user to inspect Sent mail instead of retrying automatically.

Reply drafts retain their owner-scoped source message and Gmail thread. Forward drafts retain source context for recovery but create a new provider thread. Discard is always explicit; closing a composer is a save operation, not deletion.
