# Arquitectura

## Objetivo

Mailflow será una aplicación personal y self-hosted. La arquitectura prioriza claridad y facilidad de operación sobre escalado distribuido: una sola instancia, un solo usuario y un único método oficial de instalación.

## Componentes

```text
Internet
   │
   ▼
Traefik ───────────► SPA React
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

Docker Compose ejecutará ocho servicios principales y dos jobs efímeros de inicialización:

| Servicio | Responsabilidad |
| --- | --- |
| `traefik` | HTTPS, ACME, rutas, límites y cabeceras |
| `web` | Distribución inmutable de la SPA |
| `auth` | Better Auth y sesiones |
| `api` | API REST, WebSocket y CDN local |
| `worker` | Sincronización, reintentos y trabajos programados |
| `postgres` | Correo normalizado, configuración, traducciones y observabilidad |
| `redis` | Colas, locks por cuenta y coordinación entre API y worker |
| `backup` | Restic sobre volcados consistentes preparados por el worker; nunca lee el directorio de datos PostgreSQL en uso |
| `postgres-preflight` | Rechazo preventivo de volúmenes con el layout anterior a PostgreSQL 18 |
| `migrate` | Aplicación única de migraciones Goose antes de arrancar los procesos de aplicación |

La configuración de producción expondrá únicamente Traefik en los puertos 80 y 443.

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
  compose.yml
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
  internal/modules/
    authbridge/
    accounts/
    events/
    mail/
    sync/
    cdn/
    translations/
    sentry/
    metrics/
    logs/
    admin/
    settings/
  internal/platform/
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

El contrato WebSocket versionado, su replay Redis y el comportamiento de reconexión se detallan en [Eventos en tiempo real](events.md).

## Autenticación

Better Auth vivirá en un servicio Bun separado y compartirá PostgreSQL con la aplicación.

Goose es la única autoridad de migraciones. El job efímero `migrate` termina antes de que arranquen Better Auth, la API y el worker; Better Auth nunca modifica el esquema durante el arranque.

La tabla `users` es la identidad canónica y usa UUID de extremo a extremo. Better Auth escribe directamente en ella y conserva sus sesiones, credenciales, verificaciones, passkeys y claves JWT en tablas `auth_*`. La API resuelve el `sub` del JWT contra el mismo `users.id`; no existe un segundo perfil ni una conversión entre identificadores. Las cuentas de correo de `accounts` son entidades independientes y siempre pertenecen a ese perfil.

- El asistente inicial creará el único usuario mediante un token temporal.
- Acceso con email y contraseña.
- Passkey opcional.
- Registro desactivado después del primer usuario.
- Cookies seguras para web.
- JWT EdDSA de 15 minutos y JWKS para que Fiber valide sesiones sin llamar a Bun en cada petición.
- Better Auth y Fiber comparten un emisor configurado y la audiencia fija `mailflow-api`; la API exige `iss`, `aud`, `sub` y `exp` antes de resolver el perfil.
- Las claves JWKS se refrescan periódicamente y de inmediato ante un `kid` nuevo para tolerar rotaciones sin relajar la validación.
- El usuario actual se expone a los servicios mediante `context.Context`; los módulos de dominio no importan Fiber.
- Sesiones nativas renovables y revocables almacenadas en Keychain.

Las identidades de Mailflow estarán separadas de las credenciales utilizadas para acceder a los buzones.

## Proveedores

### Google

- OAuth configurado por la propia instalación según [la guía de Google](providers/google.md); el client secret se monta como Docker Secret.
- PKCE S256 y un `state` aleatorio de un solo uso conservado en Redis durante diez minutos.
- Scopes mínimos de identidad y `gmail.modify`; los tokens se cifran mediante el vault antes de persistirse.
- La reautorización reemplaza las credenciales cifradas sin cambiar la identidad local. Desconectar intenta la revocación remota y siempre persiste `disabled_at` para cortar y auditar el acceso local sin registrar direcciones ni tokens.
- Gmail API.
- Sincronización inicial e incremental mediante historial.
- Polling incremental adaptativo y reconciliación diaria.

### Microsoft

- OAuth configurado por la instalación.
- Microsoft Graph.
- Delta queries mediante polling adaptativo y reconciliación diaria.

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
- Las credenciales de cada cuenta usan AES-256-GCM con un nonce aleatorio y AAD ligado al usuario y al ID de cuenta. La clave maestra se monta como Docker Secret en base64 y nunca se almacena en PostgreSQL.
- Base de datos, backups y conexiones cifrados por la infraestructura.

### Cola durable

El worker consume una cola Redis Streams versionada con entrega **at least once**. Los handlers deben ser idempotentes; confirmar un trabajo elimina de forma atómica su entrada, mientras que un proceso caído deja un claim pendiente que otro consumer recupera después del visibility timeout.

- Un idempotency key opcional, almacenado únicamente como SHA-256 y con caducidad, evita enqueues duplicados y devuelve el ID original.
- Los fallos pasan a un sorted set hasta que vence un backoff exponencial acotado; la promoción a la cola lista es atómica.
- Al agotar los intentos, el envelope pasa a un stream de dead letters consultable. Solo se persiste un código de error acotado, nunca respuestas de proveedor ni datos del mensaje.
- Al recibir una señal, el worker deja de reclamar trabajo. El handler activo dispone de una gracia acotada y, si no termina, el claim se libera sin consumir un intento.
- La profundidad lista, pendiente, diferida y muerta puede consultarse por el sistema de salud. Los eventos de claim, éxito, retry, dead letter y release alimentan métricas propias con dimensiones acotadas.
- `MAILFLOW_QUEUE_CLAIM_TIMEOUT`, `MAILFLOW_QUEUE_HANDLE_TIMEOUT` y `MAILFLOW_QUEUE_SHUTDOWN_GRACE` permiten ajustar los límites operativos sin cambiar el contrato del envelope. El visibility timeout debe superar siempre al tiempo máximo de handler para impedir que otro consumer reclame trabajo aún activo.

### Orquestación de sincronización

PostgreSQL, mediante `sync_runs`, es la fuente durable del progreso de cada sincronización; Redis solo transporta entregas *at least once*. Cada trabajo referencia un `runId` y una versión esperada. Una entrega repetida u obsoleta no vuelve a aplicar cambios.

- La primera sincronización procesa primero los últimos 90 días y, al terminar, encadena el histórico anterior a esa ventana.
- El histórico completo encadena el modo incremental. Una ejecución incremental completada programa la siguiente comprobación dos minutos después.
- La reconciliación completa se programa cada 24 horas y utiliza el mismo modelo reanudable.
- Un lease Redis por cuenta evita llamadas simultáneas al proveedor. La restricción única de PostgreSQL impide además dos ejecuciones activas de la misma fase.
- Cada página del proveedor se aplica y avanza su checkpoint en una única transacción PostgreSQL. Si el worker cae antes del commit, la página se repite; si cae después, su versión ya no puede volver a aplicarse.
- Un error recuperable devuelve la ejecución a `queued` sin perder el checkpoint. La cola aplica el backoff y su límite de intentos; la cancelación se comprueba entre páginas.
- `sync.progress` publica únicamente identificadores internos, fase, estado y contadores. Las métricas usan dimensiones acotadas de fase y resultado, nunca direcciones, asuntos ni IDs de mensajes.

Los adaptadores Google History, Microsoft Delta e IMAP implementarán el ejecutor paginado sobre este orquestador en sus respectivas fases. Sus cursores específicos seguirán almacenándose en `sync_cursors`.

## CDN local

El módulo `cdn` sirve los archivos desde el volumen persistente local `/data/cdn`. PostgreSQL conserva metadatos owner-scoped, el hash ETag, la caducidad y la referencia opaca necesaria para volver a descargar el adjunto desde su proveedor.

- Rutas internas no derivadas de nombres aportados por usuarios.
- Escrituras atómicas.
- Validación de MIME y tamaño.
- Protección contra path traversal.
- Entrega autenticada por defecto.
- Soporte de `Range`, `ETag` y `Content-Disposition`.
- Retención renovable de 30 días; el worker marca el blob como ausente y borra el archivo caducado, pero conserva su referencia de recuperación.
- Limpieza diaria de metadatos huérfanos con un periodo de gracia. Las consultas de limpieza quedan limitadas al namespace `attachments` y nunca alcanzan objetos Sentry.
- `MAILFLOW_CDN_ROOT` y `MAILFLOW_CDN_MAX_BYTES` fijan la raíz absoluta y el límite por archivo.
- Sin R2, S3, MinIO ni almacenamiento distribuido.

## Métricas, logs y Sentry

Mailflow tendrá observabilidad local y sencilla.

- Métricas agregadas en memoria y escritas en PostgreSQL cada minuto.
- Logs JSON con `slog`, emitidos a stdout e insertados por lotes en PostgreSQL.
- Retención de métricas y logs de 30 días.
- Prohibido registrar cuerpos, asuntos, destinatarios, tokens o URLs firmadas.
- Los SDK oficiales enviarán eventos a la ingesta compatible integrada en Mailflow.
- Fiber usará el adaptador oficial Sentry para capturar errores del transporte cuando exista un DSN.
- Eventos, trazas, perfiles y Replay tendrán retenciones separadas y cargas grandes en el namespace Sentry del CDN local.
- No se usarán Prometheus, Grafana, Loki ni R2.

## Backups

Un job diario realizará:

1. `pg_dump` consistente.
2. Inclusión del volumen CDN.
3. Backup cifrado con Restic.
4. Limpieza según retención.
5. Registro del resultado en el panel.

Restic respaldará tanto un destino NAS/disco como uno S3 compatible, con restauraciones periódicas verificadas.
