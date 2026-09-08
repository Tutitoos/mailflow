# Translation catalogs

Mailflow has exactly two locales: English (`en`) and Spanish (`es`). English is the source catalog, the installation default, and the unconditional fallback. The shared catalog at `services/api/internal/modules/translations/catalogs.json` is compiled into both the Go API and the web client so their known keys cannot drift.

## Persistence and revisions

On the first database-backed startup, the API writes the built-in catalogs as revision 1. Every accepted administrator update creates an immutable full snapshot in `translation_revisions` and `translation_messages`, then atomically moves `translation_state.active_revision`. At most 100 revisions are retained, including the active revision.

The public `GET /api/v1/translations/{locale}` response contains the active revision, the selected locale, all effective messages, and missing Spanish keys. Unsupported locales resolve to English. A missing Spanish override returns the English value; deleting an English source is never allowed.

## Validation and updates

Authenticated Admin endpoints provide export, dry-run validation, and activation:

- `GET /api/v1/admin/translations` exports both catalogs, source hashes, and diagnostics.
- `POST /api/v1/admin/translations/validate` applies a bounded candidate change set without writing it.
- `PUT /api/v1/admin/translations` activates the same change format when validation succeeds.

Updates require `expectedRevision`; stale writers receive HTTP 409. A change contains `locale`, `key`, `value`, and, for Spanish values, the SHA-256 `sourceHash` exported with the current English message. A JSON `null` removes a Spanish override and restores English fallback. English values cannot be removed.

Before activation, the service rejects unknown or repeated keys, missing English values, stale Spanish source hashes, malformed ICU syntax, mismatched ICU argument names, raw email addresses, bearer/JWT-like credentials, and signed URLs. Requests are limited to 256 changes, 512 known keys per locale, and 4 KiB per message.

## Live invalidation

After commit, the API publishes a bounded `translations.changed` event containing only the new revision. Active web clients fetch the selected catalog again and install it in memory without a page or server restart. If the event transport is unavailable, the database revision remains authoritative and the next catalog request refreshes the cache; the Admin response reports whether publication succeeded.

The web client always retains its compiled English catalog as a last-resort fallback. Remote payloads may override only known keys and cannot remove that fallback.
