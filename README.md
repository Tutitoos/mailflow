# Mailflow

Mailflow es un cliente de correo personal, open source y self-hosted. Su objetivo es ofrecer una experiencia de correo familiar y eficiente, con la estructura de Gmail y un lenguaje visual oscuro, sobrio y preciso inspirado en Vercel.

> [!IMPORTANT]
> Mailflow está en fase de planificación. Este repositorio contiene por ahora la arquitectura, la especificación de diseño y el roadmap; todavía no incluye una aplicación ejecutable.

## Principios

- Personal primero: una instancia y un usuario.
- Self-hosted: los mensajes, credenciales, métricas, logs y archivos permanecen en la instalación.
- Multicuenta: Gmail, Microsoft y, posteriormente, iCloud e IMAP/SMTP genérico.
- Multiplataforma: web y macOS primero; iPhone y iPad después.
- Interfaz rápida: alta densidad, teclado, pocas animaciones y accesibilidad WCAG 2.2 AA.
- Sin negocio alrededor: no hay suscripciones, planes premium ni telemetría obligatoria.

## Stack previsto

| Área | Tecnología |
| --- | --- |
| Web | TypeScript 7, Bun, React Router, Tailwind CSS, shadcn/ui |
| Escritorio | Tauri reutilizando la aplicación web |
| iOS/iPadOS | SwiftUI y GRDB/SQLite |
| API | Go 1.25+, Fiber v3, pgx, sqlc y Goose |
| Autenticación | Better Auth |
| Datos | PostgreSQL |
| Colas | Redis y worker Go |
| Archivos | CDN local sobre un volumen persistente |
| Despliegue | Docker Compose |

El commit del monorepo identifica toda la aplicación propia. Los repositorios externos, si llegan a ser necesarios, se fijarán de forma reproducible mediante [`deploy/repos.lock`](deploy/repos.lock).

## Primera versión

La primera versión se centrará en Gmail y cubrirá:

- Conexión y sincronización de cuentas.
- Bandeja unificada y carpetas por cuenta.
- Lectura, búsqueda y acciones básicas.
- Redacción, respuesta, reenvío y adjuntos.
- Aplicación web y aplicación macOS con Tauri.
- Panel personal para estado, sincronización, métricas, logs y traducciones.

Las funciones de productividad avanzada, Android, colaboración, calendarios e IA quedan fuera del primer alcance.

## Documentación

- [Arquitectura](docs/architecture.md)
- [Diseño Gmail × Vercel](docs/design.md)
- [Roadmap](docs/roadmap.md)
- [Flujo Git y repos.lock](docs/git-workflow.md)
- [Contribuir](CONTRIBUTING.md)
- [Seguridad](SECURITY.md)

## Estado

`planning` — documentación inicial y decisiones de producto.

## Licencia

Mailflow se publica bajo la [GNU Affero General Public License v3.0](LICENSE).
