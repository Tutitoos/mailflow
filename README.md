# Mailflow

Mailflow is a personal, open-source and self-hosted email client. It combines a familiar, high-density mail structure with a restrained, dark visual language inspired by Vercel.

> [!IMPORTANT]
> Mailflow is under active development. The current foundation includes an interactive web shell, the macOS Tauri host, Better Auth service, modular Fiber API, worker, initial database migration, OpenAPI contract, Docker Compose and CI.

## Principles

- Personal first: one installation and one user.
- Self-hosted: mail, credentials, metrics, logs and files remain under the owner's control.
- Multi-account: Google, Microsoft and iCloud/generic IMAP and SMTP.
- Multi-platform: web and macOS for 1.0; iPhone and iPad afterwards.
- Fast by design: dense information, keyboard access, restrained motion and WCAG 2.2 AA.
- No product tiers, subscriptions or mandatory telemetry.

## Stack

| Área | Tecnología |
| --- | --- |
| Web | TypeScript 7, Bun, React Router, Tailwind CSS, shadcn/ui |
| Escritorio | Tauri reutilizando la aplicación web |
| iOS/iPadOS | SwiftUI y GRDB/SQLite |
| API | Go 1.26+, Fiber v3, pgx, sqlc and Goose |
| Autenticación | Better Auth |
| Datos | PostgreSQL |
| Colas | Redis y worker Go |
| Archivos | CDN local sobre un volumen persistente |
| Despliegue | Docker Compose |

The root commit identifies every first-party component. If source repositories become necessary later, [`deploy/repos.lock`](deploy/repos.lock) will pin them reproducibly without ever referencing this monorepo itself.

## Run the current foundation

Requirements: Bun 1.4, Go 1.26, Rust 1.85+ and Docker.

```bash
bun install
bun run dev
```

The web application is available at `http://127.0.0.1:4310`. Run the complete local verification suite with:

```bash
make check
```

For Compose, copy `deploy/.env.example`, create the secret files documented in `deploy/secrets/README.md`, and run Docker Compose from the repository root.

## Version 1.0

The first stable version covers:

- Google, Microsoft and iCloud/IMAP account connection and synchronization.
- Unified inbox and account-scoped folders without cross-account thread merging.
- Reading, local search, actions, drafts, sending and attachments.
- Web and signed/notarized macOS applications.
- A local Admin area for health, queues, metrics, logs, Sentry, translations and backups.
- English and Spanish, with English as the default and fallback language.

Advanced productivity features, Android, collaboration, calendars and AI remain outside 1.0.

## Documentation

- [Arquitectura](docs/architecture.md)
- [Diseño Gmail × Vercel](docs/design.md)
- [Roadmap](docs/roadmap.md)
- [Flujo Git y repos.lock](docs/git-workflow.md)
- [Pruebas locales y de integración](docs/testing.md)
- [Eventos WebSocket y replay](docs/events.md)
- [Shell de escritorio macOS](docs/desktop.md)
- [Actualizador firmado de macOS](docs/desktop-updater.md)
- [Pipeline de release macOS](docs/macos-release.md)
- [Caché cifrada offline de macOS](docs/offline-cache.md)
- [Contribuir](CONTRIBUTING.md)
- [Seguridad](SECURITY.md)

## Status

`foundation` — the platform skeleton and interactive product shell are runnable; provider synchronization and persistence are not implemented yet.

## License

Mailflow is released under the [GNU Affero General Public License v3.0](LICENSE).
