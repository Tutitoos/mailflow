# Mailflow 1.0 product acceptance

This gate proves the performance, accessibility, provider-latency and restart
criteria for a release candidate without collecting private mail. A pass is
bound to one source commit and the immutable image digests built from it.

## Declared server profile

The supported acceptance floor is a dedicated Linux host with four logical CPU
cores, 8 GiB RAM, SSD-backed persistent volumes, PostgreSQL 18 and Redis 8.
Record the CPU model, architecture, RAM, storage type, operating-system version,
source commit and all image digests with each result. Faster hardware may pass,
but must not be used to claim support for this floor.

The 100,000-message fixture is synthetic. The PostgreSQL test creates the data,
refreshes planner statistics, requires the full-text index and rejects a search
that takes one second or more. Playwright loads 100,000 thread summaries in the
largest desktop project and rejects a rendered list of 100 rows or more, proving
that list work remains virtualized rather than proportional to mailbox size.

## Automated gate

From a clean checkout of the candidate commit:

```sh
make check
make release-acceptance
```

`make release-acceptance` builds all five native acceptance images, runs the
PostgreSQL/Redis integration suite, all web journeys at these six widths, and
the native first-party container drill:

| Project | Viewport |
| --- | --- |
| desktop-large | 1440 x 900 |
| desktop | 1280 x 800 |
| tablet-landscape | 1024 x 768 |
| tablet-portrait | 768 x 1024 |
| mobile-large | 430 x 932 |
| mobile | 390 x 844 |

The browser suite uses the reduced-motion media preference, checks the `/`,
Escape, Enter and mail-action keyboard paths, and rejects serious or critical
Axe findings in the shell, conversation, composer and Admin surfaces. Manual
keyboard review still covers Tab/Shift+Tab order, visible focus, menus, dialogs
and the screen-reader announcements listed below.

The container drill starts the complete first-party stack. It writes only
synthetic cursor and pending-action records, restarts API and worker, stops both,
restarts PostgreSQL and Redis, verifies the exact committed state, then starts
API and worker again and requires healthy services. Its temporary project and
volumes are removed on exit. Rerunning it is the rollback procedure; it never
targets a live Compose project.

## Protected provider timing

Real provider accounts are intentionally excluded from public CI. Run this
matrix with dedicated test accounts after the automated gate passes:

| Provider path | Trigger | Required observation |
| --- | --- | --- |
| Google History | create, read and label a test message remotely | visible in Mailflow within 5 minutes |
| Microsoft Delta | create, read and move a test message remotely | visible in Mailflow within 5 minutes |
| IMAP IDLE | deliver and flag a test message while IDLE is available | visible in Mailflow within 1 minute |
| IMAP fallback | disconnect IDLE, then deliver a test message | recovered by the configured 5-minute poll |

Use monotonic elapsed time from completed remote mutation to the matching local
WebSocket or persisted state. Repeat each case three times and record the worst
duration. A provider outage, throttling response or unavailable IDLE capability
is an explicit not-run result, never a pass. Public evidence contains only the
provider, operation, capability, timestamps, elapsed duration and result—never
an address, subject, body, account identifier, token, screenshot or provider
response.

## Manual accessibility matrix

At every viewport, verify keyboard-only access to navigation, categories,
selection, conversation actions, search help, composer, attachments, settings
and Admin. Focus must remain visible and return to its invoker after a dialog.
Confirm headings, landmarks, labels, status/alert announcements, zoom at 200%,
dark-theme contrast and no information conveyed only by color. Repeat once with
VoiceOver or an equivalent screen reader and once with reduced motion enabled.
Any WCAG 2.2 AA failure blocks the candidate.

## Evidence record

Store a sanitized record containing:

- source commit, release version and immutable image/artifact digests;
- declared hardware and software profile;
- command, start/end timestamps, duration and exit result for each automated gate;
- per-provider capability, three durations and worst duration;
- browser project, keyboard matrix, assistive technology and Axe result;
- restart observations for API, worker, PostgreSQL and Redis;
- reviewer, date, known exclusions and an explicit overall pass or fail.

Do not archive databases, Redis files, browser traces, screenshots or raw logs
from real accounts. A failure keeps the candidate unpublished; correct it on a
new issue/branch, rebuild immutable artifacts and repeat the full gate.
