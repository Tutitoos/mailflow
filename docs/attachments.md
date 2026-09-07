# Attachment lifecycle

Mailflow downloads Gmail attachments only after the owner requests them. The public API keeps the message attachment UUID stable while the CDN module serializes concurrent misses, fetches the remote Gmail part, validates its declared MIME type and installation size limit, and writes an atomic local cache object. A cache hit never calls Gmail and renews the cache retention window. Expired data is removed and can be recovered into a new cache object through the same domain attachment UUID.

Composer uploads use `POST /api/v1/attachments` and return an opaque object reference. Draft persistence verifies that the object is cached, belongs to the same owner account, and still has the reported MIME type and size. Active draft objects are excluded from expiry because their original upload cannot be recovered from a provider. Sending reopens every object through that authorization boundary and emits a deterministic `multipart/mixed` message. The combined attachment payload is limited to 20 MiB before Gmail is called.

Downloads require the normal bearer token and support a single byte range, `ETag`, `Content-Disposition`, progress, and cancellation. Uploads expose progress and cancellation as well. Provider IDs, object paths, message content, filenames, and recipient data are never written to operational logs or errors.

The per-object limit is configured with `MAILFLOW_CDN_MAX_BYTES` and defaults to 25 MiB. Rolling back migration `00016_gmail_attachments.sql` removes only the draft-to-CDN foreign key; message attachment identity and existing cache objects remain intact.
