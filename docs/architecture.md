# Arquitectura

## Objetivo

Mailflow será una aplicación personal y self-hosted. La arquitectura prioriza claridad y facilidad de operación sobre escalado distribuido: una sola instancia, un solo usuario y un único método oficial de instalación.

## Componentes

```text
Internet
   │
   ▼
Gateway Caddy ─────► SPA React
   ├───────────────► Better Auth (Bun)
   ├───────────────► API Fiber (Go)
   └───────────────► WebSocket / CDN
                          │
                ┌─────────┴─────────┐
                ▼                   ▼
             PostgreSQL          Redis
                ▲                   ▲
                └──── Worker Go ────┘
```

Docker Compose ejecutará siete servicios:

| Servicio | Responsabilidad |
| --- | --- |
| `gateway` | TLS interno, rutas, cabeceras y entrega de la SPA |
| `auth` | Better Auth y sesiones |
| `api` | API REST, WebSocket y CDN local |
| `worker` | Sincronización, reintentos y trabajos programados |
| `postgres` | Correo normalizado, configuración, traducciones y observabilidad |
| `redis` | Colas, locks por cuenta y coordinación entre API y worker |
| `backup` | Exportación diaria con `pg_dump` y Restic |

Cloudflare Tunnel podrá publicar el gateway mediante HTTPS, pero no será un requisito del código.

## Monorepo previsto

```text
apps/
  web/
  desktop/
  ios/
services/
  api/
  auth/
packages/
  ui/
  api-client/
openapi/
deploy/
  compose/
  repos.lock
```

## Reproducibilidad con `repos.lock`

Mailflow seguirá siendo un monorepo. El SHA del repositorio raíz fija conjuntamente `web`, `desktop`, `ios`, `api`, `auth`, los paquetes compartidos y la configuración de despliegue.

`deploy/repos.lock` no incluirá esos componentes ni el propio repositorio. Su única función será fijar cualquier repositorio fuente externo que resulte imprescindible en el futuro mediante tres campos separados por tabuladores:

```text
external/<name>    git@github.com:<owner>/<repo>.git    <40-character-sha>
```

- `scripts/repos-lock.sh validate` valida el formato sin acceder a la red.
- `scripts/repos-lock.sh verify` exige que cada checkout externo exista, tenga el remoto y SHA fijados, y esté limpio.
- `scripts/repos-lock.sh clone-missing` clona únicamente los repositorios ausentes. Nunca resetea, cambia de commit ni sobrescribe un checkout existente.
- `external/` está ignorado por el Git raíz; no se usarán submódulos ni gitlinks.
- Toda actualización del lock debe ser explícita, contener SHAs completos y revisarse junto con el cambio que la necesita.

Mientras no existan dependencias fuente externas, el lock permanecerá válidamente vacío. Las imágenes de contenedor se fijarán por digestos en la configuración de despliegue, no en `repos.lock`.

## API modular

La API será un monolito modular. Fiber se limitará al transporte HTTP; los módulos no recibirán ni conservarán `fiber.Ctx`.

```text
services/api/
  cmd/
    api/
    worker/
  modules/
    authbridge/
    accounts/
    mail/
    sync/
    cdn/
    translations/
    sentry/
    metrics/
    logs/
    admin/
    settings/
  platform/
    config/
    crypto/
    database/
    http/
    logging/
    redis/
```

Cada módulo será propietario de su lógica y sus tablas. La comunicación síncrona utilizará interfaces Go explícitas; los trabajos asíncronos utilizarán Redis. La composición de dependencias será manual.

## Contratos

- REST JSON bajo `/api/v1`.
- OpenAPI 3.1 como fuente de verdad.
- Cliente TypeScript generado y cliente Swift en la fase móvil.
- WebSocket autenticado para cambios de buzón y progreso de sincronización.
- Paginación por cursor.
- Errores Problem Details con código estable y `requestId`.
- `Idempotency-Key` para envíos y acciones repetibles.
- UUIDv7 para identificadores propios.

## Autenticación

Better Auth vivirá en un servicio Bun separado y compartirá PostgreSQL con la aplicación.

- El asistente inicial creará el único usuario mediante un token temporal.
- Acceso con email y contraseña.
- Passkey opcional.
- Registro desactivado después del primer usuario.
- Cookies seguras para web.
- JWT cortos y JWKS para que Fiber valide sesiones sin llamar a Bun en cada petición.
- Sesiones nativas renovables y revocables almacenadas en Keychain.

Las identidades de Mailflow estarán separadas de las credenciales utilizadas para acceder a los buzones.

## Proveedores

### Google

- OAuth configurado por la propia instalación.
- Gmail API.
- Sincronización inicial e incremental mediante historial.
- Notificaciones mediante Pub/Sub.
- Renovación de suscripciones y reconciliación periódica.

### Microsoft

- OAuth configurado por la instalación.
- Microsoft Graph.
- Delta queries y webhooks.
- Reconciliación ante notificaciones perdidas.

### iCloud e IMAP

- IMAP y SMTP sobre TLS.
- Contraseña específica de aplicación para iCloud.
- Detección de capacidades para servidores genéricos.
- IMAP IDLE cuando esté disponible y polling como respaldo.

Los proveedores se implementarán en orden: Google, Microsoft e IMAP.

## Sincronización y almacenamiento

- Worker Go separado de la API.
- Lock por cuenta para impedir sincronizaciones concurrentes.
- Operaciones idempotentes y reintentos con backoff.
- El proveedor seguirá siendo la fuente definitiva.
- Ventana local móvil de 90 días.
- Mensajes antiguos recuperados bajo demanda y cacheados temporalmente.
- Adjuntos entrantes cacheados durante 30 días.
- Tokens y secretos cifrados a nivel de aplicación.
- Base de datos, backups y conexiones cifrados por la infraestructura.

## CDN local

El módulo `cdn` servirá los archivos desde un volumen persistente local.

- Rutas internas no derivadas de nombres aportados por usuarios.
- Escrituras atómicas.
- Validación de MIME y tamaño.
- Protección contra path traversal.
- Entrega autenticada por defecto.
- Firmas temporales para descargas puntuales.
- Soporte de `Range`, `ETag` y `Content-Disposition`.
- Sin R2, S3, MinIO ni almacenamiento distribuido.

## Métricas, logs y Sentry

Mailflow tendrá observabilidad local y sencilla.

- Métricas agregadas en memoria y escritas en PostgreSQL cada minuto.
- Logs JSON con `slog`, emitidos a stdout e insertados por lotes en PostgreSQL.
- Retención de métricas y logs de 30 días.
- Prohibido registrar cuerpos, asuntos, destinatarios, tokens o URLs firmadas.
- Sentry será opcional y solo se activará si existe un DSN.
- El módulo Sentry usará el adaptador oficial para Fiber v3.
- No se usarán Prometheus, Grafana, Loki ni trazas distribuidas.

## Backups

Un job diario realizará:

1. `pg_dump` consistente.
2. Inclusión del volumen CDN.
3. Backup cifrado con Restic.
4. Limpieza según retención.
5. Registro del resultado en el panel.

El destino Restic será configurable. Los snapshots de Proxmox podrán complementar este proceso, pero estarán fuera del alcance de Mailflow.
