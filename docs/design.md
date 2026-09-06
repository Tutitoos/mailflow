# Diseño: estructura Gmail, lenguaje Vercel

## Dirección

Mailflow conservará la arquitectura de información y la densidad de Gmail sin copiar su marca, logotipo ni recursos gráficos. El lenguaje visual será oscuro, monocromático, tipográfico y preciso, inspirado en los productos de Vercel.

```text
┌──────────────────────────────────────────────────────────────┐
│ Menú · Mailflow        Buscar correo              Acciones   │
├─────────────┬───────────────────────────────────────┬────────┤
│ Redactar    │ Toolbar y paginación                  │ Rail   │
│             ├───────────────────────────────────────┤        │
│ Buzones     │ Principal · Promociones · Social...   │        │
│             ├───────────────────────────────────────┤        │
│ Etiquetas   │                                       │        │
│             │ Lista de mensajes                     │        │
│ Cuentas     │                                       │        │
└─────────────┴───────────────────────────────────────┴────────┘
```

## Tokens

| Token | Valor inicial |
| --- | --- |
| Fondo | `#000000` |
| Superficie | `#0A0A0A` |
| Superficie elevada | `#111111` |
| Hover | `#171717` |
| Borde | `#262626` |
| Borde fuerte | `#3F3F46` |
| Texto principal | `#EDEDED` |
| Texto secundario | `#A1A1AA` |
| Texto terciario | `#71717A` |
| Foco | `#FFFFFF` |
| Peligro | `#EF4444` |
| Aviso | `#F59E0B` |
| Éxito | `#22C55E` |

- Geist Sans para la interfaz.
- Geist Mono para logs, métricas e identificadores técnicos.
- Texto base de 14 px.
- Iconos Lucide de 18 px y trazo fino.
- Bordes de 1 px.
- Radio principal de 8 px.
- Sin degradados, cristal, sombras grandes o color decorativo.
- Tema exclusivamente oscuro.

## Cabecera

Altura: 64 px.

- Izquierda: menú, icono y wordmark Mailflow.
- Centro: buscador dominante de hasta 720 px, atajo `/` y filtros.
- Derecha: sincronización, configuración y avatar.
- La marca se reduce a su icono cuando la sidebar está plegada.

## Sidebar

- 248 px expandida y 64 px plegada.
- Scroll independiente.
- Botón `Redactar` de 172 × 44 px, blanco con texto negro.
- Buzones unificados: Recibidos, Destacados, Importantes, Todos, Borradores y Enviados.
- `Más` despliega Spam y Papelera.
- Etiquetas y cuentas conectadas aparecen debajo.
- Cada cuenta puede plegarse y conserva la jerarquía de carpetas o etiquetas del proveedor.
- La selección utiliza un fondo gris oscuro y un indicador blanco, nunca azul Gmail.

## Panel central

- Fondo `#0A0A0A`.
- Borde de 1 px y radio de 8 px.
- Sin sombra.
- Toolbar de 48 px.
- Barra de categorías de 56 px.
- La selección masiva no debe desplazar otros controles.

Las categorías se mantienen literalmente:

- Principal.
- Promociones.
- Social.
- Notificaciones.
- Foros.

Gmail utilizará categorías nativas. Microsoft e IMAP usarán reglas locales basadas en remitente, dominio y cabeceras. Al corregir una categoría se podrá recordar la elección para el remitente o dominio.

## Lista de mensajes

- Altura base de 44 px.
- Filas con adjuntos de hasta 68 px.
- Columnas: selección, destacado, importante, remitente, asunto/extracto y fecha.
- No leído: remitente y asunto semibold.
- Leído: peso normal y color secundario.
- Hover: fondo `#171717` y acciones rápidas en lugar de la fecha.
- Asuntos y extractos en una línea con truncado.
- Adjuntos como chips compactos.
- El proveedor se identifica con un detalle pequeño, sin teñir la fila.

Abrir, seleccionar, archivar y navegar mediante teclado será instantáneo y no tendrá animación.

## Conversación

Al abrir un mensaje, la conversación sustituye a la lista dentro del panel central.

- Toolbar con volver, archivar, spam, eliminar, leído/no leído, mover, etiquetar y más.
- Asunto y etiquetas.
- Remitente, destinatarios, fecha y avatar.
- Cuerpo sanitizado y aislado.
- Adjuntos.
- Responder y reenviar.
- Aviso de imágenes remotas bloqueadas.

## Redacción

El compositor aparecerá abajo a la derecha.

- Ancho inicial de aproximadamente 560 px.
- Altura máxima del 70 % del viewport.
- Separación de 16 px respecto a los bordes.
- Estados normal, minimizado y maximizado.
- Destinatarios, CC/BCC, asunto, editor Lexical y adjuntos.
- Botón `Enviar` blanco con texto negro.
- Confirmación al descartar cambios.

## Rail contextual

- 48 px cerrado y unos 320 px abierto.
- Accesos: contacto, adjuntos, cuenta y sincronización.
- En una conversación muestra información del remitente y archivos.
- En una bandeja muestra proveedor, carpeta y última sincronización.
- En pantallas estrechas se superpone al contenido.
- Se cierra con `Escape`.

## Admin

El engranaje abrirá el panel personal dentro del mismo shell.

- Estado.
- Cuentas.
- Sincronización y colas.
- Métricas.
- Logs.
- Traducciones.
- CDN.
- Backups.
- Sentry.
- Configuración y actualizaciones.

Usará navegación secundaria, tablas con bordes finos, gráficas monocromáticas y Geist Mono para logs. No habrá tarjetas grandes cuando una tabla o línea de estado sea suficiente.

## Responsive

| Anchura | Comportamiento |
| --- | --- |
| `≥1280 px` | Sidebar completa, panel central y rail |
| `1024–1279 px` | Sidebar reducida y rail plegado |
| `768–1023 px` | Sidebar como drawer y rail superpuesto |
| `<768 px` | Estructura móvil |

iPhone seguirá Gmail móvil: cabecera compacta, drawer, lista a pantalla completa y botón flotante Redactar. iPad utilizará sidebar más un panel que alterna entre lista y conversación.

## Movimiento

- Navegación, selección y atajos: sin animación.
- Hover y color: 120–160 ms.
- Popovers: 150–180 ms.
- Rail: 200 ms.
- Compositor: 180–220 ms.
- Curva principal: `cubic-bezier(0.23, 1, 0.32, 1)`.
- Salidas más rápidas que entradas.
- Botones con `scale(0.98)` al pulsar.
- Solo se animarán `transform` y `opacity`.
- `prefers-reduced-motion` eliminará desplazamientos.

## Accesibilidad y teclado

Objetivo WCAG 2.2 AA.

- Foco visible de 2 px.
- Navegación completa sin ratón.
- Nombres accesibles para iconos.
- Contadores que no dependan solo del color.
- Targets mínimos de 40 px en escritorio y 44 px en móvil.
- Gestión correcta del foco en compositor, drawers y popovers.

Atajos iniciales: `/` buscar, `c` redactar, `j/k` navegar, `Enter` abrir, `r` responder, `f` reenviar, `e` archivar, `#` papelera, `u` volver y `Escape` cerrar superficies temporales.

## Validación visual

Las pruebas responsive cubrirán exactamente seis viewports: 1440 × 900, 1280 × 800, 1024 × 768, 768 × 1024, 430 × 932 y 390 × 844.

Se probarán bandejas vacías y densas, mensajes leídos/no leídos, selección múltiple, textos largos, adjuntos, sidebar plegada, rail abierto, compositor en sus tres estados, HTML complejo, imágenes bloqueadas, offline, error y navegación mediante teclado.
