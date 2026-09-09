# Guía de operaciones

Esta es la guía completa en español para una instalación de producción de
Mailflow con un único usuario. La versión canónica es la [guía en inglés](../operator-runbook.md).
Ejecuta el proceso desde un commit verificado. Los comandos suponen un host
Linux, el repositorio en `/srv/mailflow/source` y los archivos privados bajo
`/srv/mailflow`. Sustituye localmente los valores entre ángulos y nunca pegues
secretos en comandos, issues, logs, capturas, URLs ni registros de release.

## 1. Registrar las identidades de recuperación

Antes de cambiar el host, crea un registro offline con el commit fuente exacto
de 40 caracteres, tag y SHA-256 de las evidencias, los cinco digests de Mailflow,
los digests revisados de Traefik, PostgreSQL y Redis, el snapshot Restic actual
y la fecha de su restauración probada, el entorno anterior y la decisión de
compatibilidad de base de datos.

No continúes sin el registro anterior, la contraseña Restic, el código de
recuperación y la clave maestra cifrada guardados offline. Un tag no identifica
por sí solo un artefacto. El entorno no contiene credenciales, pero es privado.

## 2. Preparar el host y el DNS

Instala Docker Engine y Docker Compose 2.24.4 o posterior en un host Linux
compatible. Apunta el DNS al host y permite TCP 80 y 443. No publiques
PostgreSQL, Redis, el dashboard de Traefik ni el socket Docker.

```sh
sudo install -d -m 755 -o "$(id -un)" -g "$(id -gn)" /srv/mailflow/source
git clone https://github.com/Tutitoos/mailflow.git /srv/mailflow/source
cd /srv/mailflow/source
MAILFLOW_SOURCE_COMMIT='REPLACE_WITH_VERIFIED_40_HEX_COMMIT'
printf '%s\n' "$MAILFLOW_SOURCE_COMMIT" | grep -Eq '^[0-9a-f]{40}$'
git checkout --detach "$MAILFLOW_SOURCE_COMMIT"
test "$(git rev-parse HEAD)" = "$MAILFLOW_SOURCE_COMMIT"
sudo install -d -m 700 -o "$(id -un)" -g "$(id -gn)" \
  /srv/mailflow/secrets /srv/mailflow/runtime /srv/mailflow/releases \
  /srv/mailflow/backups
```

El destino de backup debe ser un NAS/disco montado u otra ubicación fuera de
los volúmenes de la aplicación.

## 3. Crear secretos y el entorno de release

Genera los secretos en el host con umask privado. Los archivos opcionales de
proveedor y SMTP deben existir, aunque pueden estar vacíos hasta configurarlos.

```sh
umask 077
openssl rand -base64 48 > /srv/mailflow/secrets/better_auth_secret
openssl rand -base64 32 > /srv/mailflow/secrets/bootstrap_token
openssl rand -base64 32 > /srv/mailflow/secrets/recovery_code
openssl rand -base64 32 > /srv/mailflow/secrets/master_key
openssl rand -base64 32 > /srv/mailflow/secrets/postgres_password
openssl rand -base64 32 > /srv/mailflow/secrets/restic_password
install -m 600 /dev/null /srv/mailflow/secrets/google_oauth_client_secret
install -m 600 /dev/null /srv/mailflow/secrets/microsoft_oauth_client_secret
install -m 600 /dev/null /srv/mailflow/secrets/alert_smtp_password
chmod 600 /srv/mailflow/secrets/*
cp deploy/.env.production.example /srv/mailflow/releases/vX.Y.Z.env
chmod 600 /srv/mailflow/releases/vX.Y.Z.env
```

Custodia por separado `recovery_code`, `master_key` y `restic_password`. Sin las
dos últimas no se recuperan credenciales o backups. Configura el dominio,
email, IDs OAuth y los ocho digests verificados. Usa
`MAILFLOW_SECRETS_PATH=/srv/mailflow/secrets`,
`MAILFLOW_TRAEFIK_CONFIG_PATH=/srv/mailflow/runtime/traefik-dynamic.yml` y
`BACKUP_PATH=/srv/mailflow/backups`. Todas las imágenes deben usar
`repositorio@sha256:<64-hex-minúsculas>`; nunca tags ni `latest`.

## 4. Configurar proveedores de correo

- **Google:** sigue [Google OAuth](../providers/google.md), configura
  `GOOGLE_OAUTH_CLIENT_ID` y guarda solo el secreto en
  `/srv/mailflow/secrets/google_oauth_client_secret`. Callback:
  `https://<mailflow-domain>/api/v1/oauth/google/callback`.
- **Microsoft:** sigue [Microsoft OAuth](../providers/microsoft.md), configura
  `MICROSOFT_OAUTH_CLIENT_ID` y `MICROSOFT_OAUTH_AUTHORITY`, y guarda el secreto
  en `/srv/mailflow/secrets/microsoft_oauth_client_secret`. Callback:
  `https://<mailflow-domain>/api/v1/oauth/microsoft/callback`.
- **iCloud:** sigue [la configuración de iCloud](../providers/icloud.md) y usa
  una contraseña específica para apps, nunca la contraseña principal de Apple.
- **IMAP/SMTP genérico:** sigue [la guía IMAP](../providers/imap.md). Solo se
  admiten endpoints TLS verificados y contraseñas específicas para apps.

Las aplicaciones OAuth pertenecen a la instalación y usan exactamente su
dominio HTTPS. Deja vacío el ID y secreto del proveedor que no necesites.

## 5. Validar, desplegar e iniciar al propietario

`check` falla de forma segura y no inicia contenedores. Corrige el origen del
error; no relajes permisos ni sustituyas digests por tags.

```sh
cd /srv/mailflow/source
./scripts/production-compose.sh check /srv/mailflow/releases/vX.Y.Z.env
./scripts/production-compose.sh apply /srv/mailflow/releases/vX.Y.Z.env
docker compose --env-file /srv/mailflow/releases/vX.Y.Z.env \
  -f deploy/compose.yml -f deploy/compose.production.yml --profile backup ps
curl --fail --silent --show-error --head "https://<mailflow-domain>/"
```

Solo Traefik puede publicar puertos. Todos los servicios deben estar sanos y la
migración debe terminar bien. Abre el origen HTTPS, envía una sola vez el token
de bootstrap junto al nombre, email, contraseña e idioma, cierra sesión y vuelve
a entrar. El registro queda cerrado al existir el propietario.

Registra al menos una passkey. Conserva el código de recuperación offline y
pruébalo solo en un entorno aislado: cambia la contraseña, elimina passkeys y
revoca sesiones. Conecta cada proveedor desde **Settings → Accounts**, comprueba
el inicio de sincronización, envía un mensaje de prueba saneado, descarga un
adjunto de prueba y desconecta la cuenta de prueba. No uses correo privado ni
capturas como evidencia.

## 6. Crear backup y demostrar la restauración

Haz un backup tras el bootstrap y antes de cada actualización:

```sh
docker compose --env-file /srv/mailflow/releases/vX.Y.Z.env \
  -f deploy/compose.yml -f deploy/compose.production.yml --profile backup \
  run --rm backup run
docker compose --env-file /srv/mailflow/releases/vX.Y.Z.env \
  -f deploy/compose.yml -f deploy/compose.production.yml --profile backup \
  run --rm --entrypoint restic backup snapshots --latest 1
```

Registra el snapshot completo. El backup no se considera demostrado hasta pasar
la [restauración en entorno vacío](../backups.md#empty-environment-restore-drill)
con proyecto Compose, base de datos, CDN y directorio separados. Nunca apuntes
una prueba a la base de datos o CDN activos.

Verifica login, metadatos, un adjunto saneado y el estado de Admin. Después
destruye únicamente el entorno de prueba:

```sh
docker compose -p mailflow-restore --env-file /srv/mailflow/releases/vX.Y.Z.env \
  -f deploy/compose.yml -f deploy/compose.production.yml --profile backup \
  down --volumes --remove-orphans
```

> **Destructivo:** `down --volumes` elimina los volúmenes del proyecto elegido.
> Confirma que es exactamente `mailflow-restore`; nunca lo ejecutes sobre el
> proyecto real `mailflow`.

## 7. Actualizar y volver atrás

No edites el entorno activo. Verifica el nuevo commit y evidencias, crea otro
`vX.Y.Z.env`, genera y restaura un backup, y revisa la compatibilidad de las
migraciones antes de ejecutar:

```sh
./scripts/production-compose.sh check /srv/mailflow/releases/vNEXT.env
./scripts/production-compose.sh apply /srv/mailflow/releases/vNEXT.env
```

Comprueba HTTPS, salud, login, sincronización, mensaje y adjunto saneados, Admin
y programación de backups. Conserva el entorno y snapshot anteriores.

Si la base de datos es compatible:

```sh
./scripts/production-compose.sh rollback /srv/mailflow/releases/vPREVIOUS.env
```

El rollback de imágenes nunca revierte migraciones. Sin garantía explícita de
compatibilidad, detén el proceso y restaura el snapshot anterior en volúmenes
vacíos; no reutilices la base migrada. Registra commit, digests, snapshot, hora y
resultado saneado.

## 8. Diagnosticar y recuperar

Consulta primero Admin y después estado y logs acotados:

```sh
docker compose --env-file /srv/mailflow/releases/vX.Y.Z.env \
  -f deploy/compose.yml -f deploy/compose.production.yml --profile backup ps
docker compose --env-file /srv/mailflow/releases/vX.Y.Z.env \
  -f deploy/compose.yml -f deploy/compose.production.yml logs --since 15m --tail 200
```

Sanea toda evidencia compartida. Excluye contenido, direcciones, asuntos,
tokens, cookies, URLs firmadas, credenciales, rutas de secretos y respuestas de
proveedor. No habilites el dashboard de Traefik, socket Docker, privilegios,
`chmod 777` ni TLS inseguro.

Usa el código offline si el propietario no puede autenticarse. Reconecta un
proveedor si caduca su concesión. Recupera la clave maestra desde su custodia o
un snapshot probado. Sin contraseña Restic no se recupera el repositorio.

## 9. Desinstalar

Exporta el registro final y restaura su snapshot en aislamiento antes de borrar.
Primero detén el proyecto real sin eliminar datos:

```sh
docker compose -p mailflow --env-file /srv/mailflow/releases/vX.Y.Z.env \
  -f deploy/compose.yml -f deploy/compose.production.yml --profile backup down
```

Revoca las concesiones Google/Microsoft y la contraseña específica de iCloud.
Conserva releases, Restic y claves según la retención decidida. No se ofrece un
comando copiable para borrar `/srv/mailflow` o los volúmenes.

> **Destructivo:** eliminar volúmenes borra la única base de datos activa, CDN,
> Redis y estado ACME. Solo se autoriza tras verificar de forma independiente
> los destinos exactos y el snapshot de recuperación.
