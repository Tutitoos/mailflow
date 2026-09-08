# Roadmap

El roadmap prioriza obtener un cliente personal útil antes de ampliar proveedores o plataformas.

## Estado de entrega

La Fase 0 está completada en el alcance de fundación: el monorepo, las convenciones y CI, el validador vacío de `deploy/repos.lock`, Docker Compose con Traefik/PostgreSQL/Redis, el contrato OpenAPI inicial con cliente generado y el shell responsive Gmail × Vercel con datos ficticios EN/ES existen y están verificados en `main`.

Este estado confirma la base técnica, no funcionalidad de correo. Better Auth, la API y el worker presentes son esqueletos parciales; persistencia de identidad, colas duraderas, contexto autenticado, dominio de correo y sincronización pertenecen a las fases siguientes y se gestionan mediante Issues pequeñas vinculadas a sus milestones.

## Fase 0 — Fundación

- Monorepo y convenciones.
- Validación de `deploy/repos.lock` para dependencias fuente externas.
- Docker Compose.
- Traefik, PostgreSQL y Redis.
- Contrato OpenAPI 3.1.
- Tokens visuales y shell Gmail × Vercel con datos ficticios.

## Fase 1 — Identidad y núcleo

- Better Auth y asistente del primer usuario.
- API Fiber modular.
- Worker Go y colas Redis.
- Modelos de cuenta, conversación, mensaje y carpeta.
- WebSocket y cliente TypeScript generado.

## Fase 2 — Dominio de correo

- Modelo normalizado, FTS y repositorios.
- Interfaces de proveedor y transporte.
- Acciones idempotentes, borradores, worker, locks y CDN local.

## Fase 3 — Gmail y web

- OAuth local de Google.
- Sincronización completa e incremental.
- Bandeja, categorías y búsqueda.
- Lectura, redacción, respuesta y reenvío.
- Adjuntos mediante CDN local.
- Primera versión web utilizable.

## Fase 4 — Operación personal

- Métricas y logs en PostgreSQL.
- Ingesta Sentry, issues, releases, alertas, tracing, perfiles y Replay.
- Catálogo de traducciones en base de datos.
- Panel Admin.
- Backups diarios con Restic.

## Fase 5 — Microsoft

- OAuth local de Microsoft.
- Microsoft Graph y Delta queries.
- Pruebas con Outlook y Microsoft 365.

## Fase 6 — iCloud e IMAP

- IMAP/SMTP sobre TLS.
- iCloud mediante un preset aislado y contraseña específica para apps, con inglés predeterminado y ayuda equivalente en español.
- IMAP IDLE y polling de respaldo.
- Sincronización MIME por UID, threading estable y reconciliación de MOVE/COPY.
- Acciones, drafts, adjuntos y envío SMTP con resultados ambiguos protegidos por idempotencia.
- Matriz de compatibilidad con servidores genéricos.

## Fase 7 — macOS

- Shell Tauri ligado a un origen HTTPS, sin IPC remoto, con navegación externa, callbacks cerrados, instancia única y estado de ventana gestionados en Rust.
- Caché SQLite.
- Keychain y notificaciones.
- Atajos y menús nativos.
- DMG firmado, notarizado y updater.

## Fase 8 — Release 1.0

- Imágenes GHCR AMD64/ARM64, SBOM, firma y provenance.
- Firma, notarización, updater y DMG universal.
- Backups y restauración, instalación y rollback documentados.

## Después de 1.0 — iPhone y iPad

- SwiftUI y GRDB.
- Caché local y Keychain.
- Navegación adaptada a iPhone/iPad.
- APNs configurado en la instancia.
- Distribución mediante TestFlight privado.

## Posterior al núcleo

Estas funciones no forman parte del primer objetivo:

- Posponer y programar.
- Deshacer envío.
- Reglas avanzadas.
- Firmas y contactos completos.
- Android.
- Colaboración.
- Calendario.
- IA.
