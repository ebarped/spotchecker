# Plan de implementación — TurisLab Doctor

## Detección de establecimientos cerrados y de caída de calidad para priorizar inspecciones turísticas

> Documento de trabajo. Versión 0.4 — 5 de noviembre de 2026.
>
> Cambios de la v0.4: configuración de ejemplo completamente comentada (`config.turislab.example.yaml`) con explicación y opciones tras cada valor; nueva sección §18 con todo lo que hay que cambiar para pasar de PoC a producto (Google Places, legal, escala, seguridad, operación).
>
> Cambios de la v0.3: configuración del scan movida de `.env` a `config.yaml`; añadido el scraping de Google Maps sin API de pago (`gosom` y kit) como fuente de la PoC; nueva pestaña "Cómo funciona" con funnel de fases y panel de composición del score.
>
> Cambios de la v0.2: alcance de la PoC cerrado (10 establecimientos, verdad terreno, fuentes gratuitas, escaneo secuencial 1/min, sin offline, solo ordenación, sin bot).
>
> Objetivo: adaptar la base técnica de `turislab-doctor` (Go + chi + SvelteKit/ECharts + agente ADK Go + métricas OpenTelemetry) a un producto que, partiendo de un `base-de-datos.json`, detecte establecimientos que probablemente han cerrado o han bajado su calidad, les asigne un score explicable y ordene la cola de visitas de los inspectores.

---

## 0. Resumen ejecutivo

La app actual audita dashboards de Grafana: recorre paneles, ejecuta queries y produce un informe con estados `ok / empty / error / skipped`. La tecnología subyacente —servidor Go, informe JSON en memoria, UI SvelteKit, agente conversacional con herramientas, métricas y export— encaja casi tal cual con el nuevo problema si se sustituye el concepto de **dashboard/panel/query** por el de **establecimiento/señal/evidencia**.

La propuesta es construir un **motor de evidencia multi-fuente**:

1. Carga y normaliza `base-de-datos.json`.
2. Resuelve la identidad de cada establecimiento contra fuentes externas (Google Place ID, TripAdvisor, Yelp, OSM/Overture, registro turístico).
3. Recoge señales de cierre y de calidad (estado en Google, reseñas, web, teléfono, OTA, redes, registro oficial, noticias).
4. Convierte cada señal en evidencia con **valor, fiabilidad, antigüedad y familia**.
5. Calcula tres salidas separadas:
   - `closure_risk`: probabilidad/riesgo de que esté cerrado.
   - `quality_risk` y `quality_delta`: probabilidad de que haya bajado la calidad y magnitud de la caída.
   - `priority`: prioridad de visita, que combina riesgo, impacto, antigüedad de la última inspección y confianza.
6. Publica un **Audit Report** con la cola ordenada, mapa, filtros, detalle y evidencia; y un **Agent** que explica scores, compara con pares, resume reseñas y ayuda a preparar la visita.

Decisiones clave:

- **Tres scores separados, no uno.** Un establecimiento puede estar abierto pero degradado, o cerrado y sin reseñas recientes. Mezclarlo todo en un único número hace la explicación imposible y la acción ambigua.
- **La evidencia manda.** Cada punto del score debe ser trazable a una señal con fuente, fecha, valor y confianza. El inspector debe poder decir "no estoy de acuerdo" y corregirlo.
- **APIs oficiales primero; scraping solo con revisión legal y como último recurso.** Google Places, Yelp Fusion, Overture, OSM y registros públicos cubren el MVP. TripAdvisor ha retirado su Content API (31-08-2026) y ahora se accede por Terra, con coste por entidad.
- **Confianza explícita y datos faltantes visibles.** Un score alto con poca cobertura no debe enviar a nadie a la carretera: se marca `collect_data` y se pide más evidencia.
- **El sistema prioriza, el inspector decide.** No se cambia el estado administrativo de un establecimiento de forma automática en el MVP; se propone y se confirma con la visita o con revisión humana.

Resultado esperado del MVP: una cola diaria de establecimientos ordenada por prioridad, con el motivo principal de cada uno, el nivel de confianza y un enlace a la evidencia. Los inspectores dejan de recorrer la base en orden alfabético o por territorio y pasan a visitar primero lo que más probablemente ha cambiado.

### 0.1 Alcance de la PoC (decisiones del 2026-11-05)

La primera entrega **no es el MVP completo**, sino una prueba de concepto para validar el algoritmo de scoring y la experiencia de cola:

- **10 establecimientos**: restaurantes y hoteles.
- **Verdad terreno incluida**: al menos un restaurante y un hotel que se sepa que están cerrados, para comprobar si el sistema los detecta y los coloca arriba. El resto sirve de control (abiertos y, si se puede, alguno degradado).
- **Sin APIs contratadas**: la PoC usa fuentes gratuitas o con nivel gratuito (scraper de Google Maps sin API key, OSM/Overpass, Overture, web del establecimiento, comprobación manual del teléfono) y, si se configura una clave, Google Places con su cuota gratuita (requiere cuenta de facturación aunque no se supere la cuota). No se presupone ningún contrato.
- **Escaneo secuencial y rate-limited**: un establecimiento cada `scan.trigger_interval` (inicialmente `1m`), configurable en `config.yaml`, para no saturar APIs públicas ni provocar bloqueos. Un ciclo completo de 10 establecimientos tarda ~10 minutos.
- **Sin offline**: la UI es web responsive; no hay app nativa ni requisito de funcionamiento sin conexión.
- **Solo ordenación**: el sistema calcula y ordena; no cambia estados administrativos, no sanciona y no escribe en ningún sistema externo. La integración con el sistema de inspecciones queda fuera de alcance.
- **Sin bot telefónico en la PoC**: la verificación telefónica es manual (el inspector o el equipo llama y registra el resultado). El bot se evalúa después.
- **Región/norma pendiente**: no está decidido el país, la comunidad autónoma ni la norma de calidad aplicable. El diseño debe ser configurable y no asumir SICTED, SCTE, Hotelstars ni ninguna otra; se deja como decisión abierta (§16).

La PoC se considera validada si los establecimientos cerrados conocidos aparecen en el **top 3** de la cola y si el motivo mostrado es comprensible para un inspector.

---

## 1. Objetivo y alcance

### 1.1 Problema

Los inspectores de calidad turística visitan presencialmente hoteles, restaurantes, alojamientos y otros establecimientos para comprobar:

- si siguen **abiertos** y operando;
- si mantienen el **nivel de calidad** declarado o certificado;
- si han cambiado instalaciones, servicios o titularidad;
- si hay **quejas recurrentes** que justifican una revisión.

El coste de una visita es alto (desplazamiento, tiempo, agenda) y la información disponible hoy es estática: la base de datos se actualiza cuando alguien visita, no cuando el establecimiento cambia. El resultado es que se invierte el mismo esfuerzo en establecimientos sanos que en establecimientos con alta probabilidad de haber cambiado.

### 1.2 Objetivo general

Construir un sistema que, a partir de una base de establecimientos y de señales públicas y privadas, **estime el riesgo de cierre y de degradación de calidad**, lo explique y **ordene la cola de inspección** para maximizar el valor de cada visita.

### 1.3 Objetivos específicos

1. Ingestar y validar `base-de-datos.json` con un contrato versionado.
2. Resolver la identidad de cada establecimiento en las fuentes externas.
3. Recoger señales de forma periódica, idempotente y auditable.
4. Calcular `closure_risk`, `quality_risk`, `quality_delta` y `priority` con fórmulas explicables.
5. Ofrecer una vista de cola, una vista de detalle y un mapa para el inspector.
6. Ofrecer un agente que responda preguntas sobre los datos y prepare la visita.
7. Medir la calidad del sistema contra el resultado real de las visitas (feedback loop).
8. Cumplir con GDPR/LOPDGDD, LSSI, la normativa de consumo y el AI Act en lo que aplique.

### 1.4 Alcance de la PoC

Incluido:

- Un único fichero `base-de-datos.json` con **10 establecimientos** (restaurantes y hoteles), recargable.
- Un fichero de **verdad terreno** (`ground-truth.json`) con el estado esperado de esos 10, incluyendo los cerrados conocidos.
- Fuentes gratuitas o con nivel gratuito: **scraper de Google Maps** (estado, rating), OSM/Overpass, Overture, web del establecimiento y Google Places si hay clave. Teléfono manual.
- Scoring con familias de señales, confianza, decaimiento temporal y estacionalidad.
- Audit Report con cola priorizada, filtros, detalle y evidencia.
- Agent con herramientas **de solo lectura** para consultar, explicar y preparar la visita.
- Ejecución local/servidor único, sin multi-tenant.
- Exportación JSON/CSV de la cola.
- Escaneo secuencial: **1 establecimiento cada `scan.trigger_interval`** (por defecto `1m` en `config.yaml`), con cursor persistido.

Fuera de la PoC (pero diseñado para no bloquearlo):

- Scraping masivo de TripAdvisor o Booking; el scraper de Google Maps se usa de forma acotada (10 establecimientos, ver §3.4).
- Histórico completo de reseñas (requiere licencia o snapshots propios).
- Visión por satélite o imágenes street-level.
- App móvil nativa y modo offline (la PoC es web responsive).
- Integración bidireccional con el sistema administrativo de inspecciones.
- Cambio automático del estado oficial del establecimiento.
- Bot telefónico y llamadas automáticas.
- Optimización de rutas y asignación de inspectores.
- Multi-idioma completo de la UI (se prepara i18n, se entrega en español).

### 1.5 No objetivos

- Sustituir la inspección presencial. El sistema la **dirige**, no la reemplaza.
- Determinar culpabilidad, sanciones o clausuras. Solo estima riesgo y aporta evidencia.
- Ser un producto de reputación online para los establecimientos (no es TrustYou/ReviewPro).
- Monitorizar personas: solo se recogen datos del establecimiento y de reseñas públicas, minimizando datos personales.
- Ordenar por un único score opaco: la PoC prioriza la explicabilidad sobre la precisión máxima.
- Automatizar decisiones administrativas: el sistema solo ordena y explica.

### 1.6 Usuarios

| Usuario | Necesidad | Interacción |
|---|---|---|
| Inspector de campo | Saber a dónde ir primero y por qué | Cola, detalle, mapa, agente, export |
| Coordinador de inspección | Planificar rutas y carga de trabajo | Filtros, agrupación territorial, asignación, métricas |
| Analista de calidad | Validar el modelo y calibrar umbrales | Histórico, feedback, precisión por fuente |
| Responsable de destino | Ver el estado del destino y tendencias | Dashboard agregado, informes |
| Establecimiento (futuro) | Corregir datos y aportar evidencias | Portal de autoservicio (fuera del MVP) |

---

## 2. Qué se reutiliza de la base actual

La base actual es un producto pequeño y bien separado. La estrategia recomendada es **strangler**: construir el nuevo dominio en paralelo (`cmd/turislab`, `internal/turislab/...`) reutilizando los componentes transversales, y retirar el código de Grafana cuando el nuevo flujo tenga paridad funcional.

### 2.1 Mapa conceptual

| Concepto actual | Concepto nuevo | Notas |
|---|---|---|
| Dashboard | Establecimiento | Entidad auditada |
| Panel | Dimensión de calidad | Cierre, reputación, contacto, web, OTA, registro |
| Query | Señal / comprobación | Una llamada o comprobación concreta |
| Resultado de query (`ok/empty/error/skipped`) | Evidencia (`observed/absent/error/not_checked`) | `absent` es información, no fallo |
| Informe de auditoría | Informe de riesgo | Mismo ciclo: cargar → comprobar → puntuar → publicar |
| `rank_dashboards` | `rank_establishments` | Orden por prioridad |
| `list_findings` | `list_evidence` / `list_changes` | Hallazgos con severidad |
| `get_query_detail` | `get_evidence` | Trazabilidad de una señal |
| `get_dashboard_structure` | `get_establishment` | Ficha completa |
| `get_audit_summary` | `get_portfolio_summary` | Agregados |

### 2.2 Componentes que se conservan casi intactos

- **Servidor chi** (`internal/server`): patrón de rutas, middleware, SPA embebida, `atomic.Pointer` al informe, endpoints de salud y métricas.
- **Informe en memoria + export** (`internal/model`, `internal/server`): el patrón "último informe disponible, 503 hasta que exista" funciona igual.
- **UI SvelteKit + Tailwind + ECharts**: sistema de diseño, componentes de tarjetas, tablas, filtros, gráficos, tema claro/oscuro. Se adaptan tipos y textos, no la arquitectura.
- **Agente ADK Go** (`internal/agent`): servicio, sesiones en memoria, catálogo de tools, límites de historial, MCP opcional. Solo cambian prompts y tools.
- **Métricas OTel** (`internal/metrics`): mismo patrón pull `/metrics`; cambian nombres y atributos.
- **Config YAML** (`internal/config`): misma carga/validación; se añaden secciones.
- **Logging** (`internal/logx`): sin cambios.
- **Makefile, embed de UI, pre-commit**: sin cambios.

### 2.3 Componentes que se sustituyen

- `internal/grafana/*`: cliente de Grafana → clientes de fuentes (`internal/sources/...`).
- `internal/checker/*`: orquestador de queries → orquestador de escaneo (`internal/scan`).
- `internal/model/*`: modelo de dashboard/panel/query → modelo de establecimiento/señal/evidencia.
- `internal/resolve`, `internal/varsolver`: lógica específica de Grafana → resolución de identidad de establecimientos.
- `internal/agent/grafana_mcp.go`: MCP de Grafana → MCP opcional de datos internos o se elimina.
- UI: páginas y tipos de dashboard → establecimientos y cola.

### 2.4 Estructura propuesta

```text
app/
  cmd/turislab/main.go              # entrypoint nuevo
  internal/
    turislab/
      model/                        # Establishment, Signal, Evidence, Score, Report
      config/                       # configuración nueva (o extiende la actual)
      source/                       # interfaz Source + registry
        googleplaces/
        website/
        phone/
        osm/
        overture/
        registry/
        news/
        reviews/
      identity/                     # resolución y merge de identidad
      scan/                         # orquestación de un scan completo
      scoring/                      # familias, fórmulas, calibración
      queue/                        # prioridad, tiers, rutas
      store/                        # persistencia (SQLite/Postgres) e histórico
      server/                       # HTTP API + SPA
      agent/                        # tools y prompts
      metrics/
    shared/                         # logx, httpx, cache, ratelimit
  ui/                               # SvelteKit adaptado
```

Mientras dure la transición pueden convivir `internal/checker` y `internal/turislab/scan`; el binario nuevo no importa Grafana. Cuando el nuevo flujo esté validado se elimina el código antiguo y se renombra el módulo.

---

## 3. Estado del arte y referencias

No existe, hasta donde alcanza esta revisión, un producto open source "llave en mano" que haga exactamente esto: **detectar cierres y degradación de calidad de establecimientos turísticos a partir de señales dispersas y ordenar inspecciones**. Lo que sí existe son piezas muy buenas que se pueden componer. La conclusión es construir el motor propio y apoyarse en estas herramientas.

### 3.1 Proyectos open source relevantes

| Proyecto | Qué aporta | Licencia / estado | Uso propuesto |
|---|---|---|---|
| [gosom/google-maps-scraper](https://github.com/gosom/google-maps-scraper) | Scraper de Google Maps en Go: nombre, dirección, teléfono, web, rating, `review_count`, `status` (open/closed/temporary), `place_id`, lat/lng y reseñas | MIT, ~7,3k estrellas, muy activo | **Fuente principal de la PoC para el estado de cierre** sin API de pago. Se ejecuta como proceso externo y se importa su CSV/JSON (ver §3.4). |
| [Mahanaicoach/google-maps-scraper-kit](https://github.com/Mahanaicoach/google-maps-scraper-kit) | Scraper en Python + Chrome: nombre, dirección, teléfono, web, categoría, rating, `review_count`, redes sociales, `place_id` y "open now" con `--full` | MIT, ~2,2k estrellas, activo | **Fuente secundaria**: rating/reseñas y contraste de estado. Sin Docker, útil como fallback si el scraper Go falla o cambia. |
| [alltheplaces/alltheplaces](https://github.com/alltheplaces/alltheplaces) | Spiders de sedes y locales de cadenas | MIT, activo | Enriquecer POIs de cadenas y detectar desapariciones. |
| [OvertureMaps/data](https://github.com/OvertureMaps/data) | Dataset abierto de places con `operating_status` y confianza | CDLA-Permissive 2.0 | Señal de cierre/reapertura y deduplicación geográfica. |
| [dgtlmoon/changedetection.io](https://github.com/dgtlmoon/changedetection.io) | Monitorización de cambios en webs | Apache-2.0 | Detectar "cerrado por reformas", cambios de web, páginas caídas. |
| [getodk/collect](https://github.com/getodk/collect) + [getodk/central](https://github.com/getodk/central) | Formularios móviles offline y servidor de recogida | Apache-2.0 / NOASSERTION | Modelo para la app de campo y para capturar el resultado de la visita. |
| [kobotoolbox/kpi](https://github.com/kobotoolbox/kpi) | Alternativa a ODK, formularios y API | AGPL-3.0 | Alternativa si se necesita despliegue rápido de formularios. |
| [streetcomplete/StreetComplete](https://github.com/streetcomplete/StreetComplete) | App Android de encuestas OSM | GPL-3.0 | Referencia de UX para preguntas simples en campo y actualización de POI. |
| [pietervdvn/MapComplete](https://github.com/pietervdvn/MapComplete) | Editor OSM temático basado en web | GPL-3.0 | Referencia para mapas editables y flujos de verificación. |
| [google/or-tools](https://github.com/google/or-tools) | Optimización de rutas y asignación | Apache-2.0 | Generación de rutas de inspección y asignación de carga. |
| [pipecat-ai/pipecat](https://github.com/pipecat-ai/pipecat) | Framework de agentes de voz en tiempo real | BSD-2-Clause | Base para el bot telefónico si se opta por open source. |
| [livekit/agents](https://github.com/livekit/agents) | Agentes de voz sobre LiveKit | Apache-2.0 | Alternativa para telefonía y WebRTC. |
| [dograh-hq/dograh](https://github.com/dograh-hq/dograh) | Plataforma de voz self-hosted, alternativa a Vapi/Retell | Open source | Alternativa completa si se quiere evitar SaaS. |
| [PatterAI/Patter](https://github.com/PatterAI/Patter) | SDK de voz open source | Open source | Alternativa ligera. |

### 3.2 Referencias comerciales (qué hacen y qué copiar)

| Producto | Función | Lección para el diseño |
|---|---|---|
| **TrustYou** | Reputación hotelera, análisis de sentimiento por categorías, benchmarks | El valor está en el desglose por aspectos (limpieza, servicio, ubicación), no en la nota global. |
| **Shiji ReviewPro** | GRI (Global Review Index), gestión de reseñas, comparación con competidores | Un índice compuesto normalizado 0–100 es más interpretable que una media de estrellas. |
| **Revinate** | Reputación y marketing hotelero | Integración de reseñas con datos de huésped y encuestas propias. |
| **Birdeye / ReviewTrackers / BrightLocal** | Monitorización de reseñas locales y listados | Alertas de cambios y consistencia de NAP (nombre, dirección, teléfono). |
| **Local Falcon / Yext / Uberall** | Rank tracking local y gestión de listados | La posición en búsquedas locales y la consistencia de datos son señales de actividad. |
| **SafeGraph** | Datos de POI con `closed_on`, `tracking_closed_since` | El cierre se infiere por desaparición repetida de fuentes; no se marcan cierres temporales. |
| **Scrap.io / Outscraper** | Scraping de Google Maps con filtro "permanently closed" | Confirma que `business_status` es la señal más directa, pero el scraping tiene riesgo legal. |

### 3.3 APIs y fuentes oficiales

| Fuente | Qué devuelve | Limitaciones | Coste orientativo |
|---|---|---|---|
| **Google Places API (New) — Place Details** | `businessStatus`, rating, `userRatingCount`, reseñas (máx. 5), horarios, teléfono, web | `businessStatus` es SKU Pro; reseñas son Enterprise + Atmosphere. Solo 5 reseñas. | ~$17/1k Pro, ~$20–25/1k Enterprise+Atmosphere (volumen). |
| **Google Places — Text Search / Nearby** | Descubrimiento y matching | Coste por petición; sesgo de resultados | Similar por SKU |
| **Google Business Profile API** | Reseñas completas y respuestas | Solo para negocios propios o autorizados | Gratis con cuota |
| **Yelp Fusion** | `is_closed`, rating, 3 extractos de reseña | Cobertura desigual fuera de EE. UU. | Plan gratuito limitado / de pago |
| **TripAdvisor (Terra)** | Contenido y reseñas por location ID | Content API retirada el 31-08-2026; paquete Discover con pool pequeño; coste por entidad | Desde ~1.000 entidades gratis una vez; luego pago |
| **Overture Maps** | Places con `operating_status` (open, temporarily_closed, permanently_closed) y confianza | Cobertura y frescura variables; descarga masiva | Gratis |
| **OpenStreetMap / Overpass** | POIs, `disused:*`, `closed`, horarios | Datos comunitarios, huecos y vandalismo | Gratis con uso responsable |
| **Registro oficial de turismo** (según país/región) | Alta, categoría, estado, sanciones | Formato variable; puede requerir convenio | Gratis/convenio |
| **Inspecciones previas** | Fechas, resultados, incidencias | Datos internos | Interno |
| **Noticias / prensa local** | Cierres, reformas, sanciones, cambios de titular | Ruido alto; requiere NLP y verificación | Variable |

**Situación de la PoC (2026-11-05): no hay ninguna API contratada.** Por tanto, el orden de activación es:

1. **Scraper de Google Maps** (`gosom`, ver §3.4): estado (`status`), `place_id`, rating y número de reseñas sin API de pago. Es la señal directa más útil de la PoC.
2. **OSM/Overpass + Overture** (gratis) para contraste de estado y geolocalización.
3. **Web del establecimiento** (gratis) para 404, dominio caído, cambios y menciones de cierre.
4. **Google Places** solo si se configura una clave con facturación y se acepta consumir cuota gratuita; es más fiable que el scraping, pero no se presupone.
5. **Teléfono manual** para verificar los casos dudosos.
6. **Yelp/TripAdvisor/Terra, noticias y registro oficial**: fuera de la PoC.

La arquitectura mantiene la interfaz `Source` para que activar una fuente de pago después sea configuración, no reescritura.

### 3.4 Scraping de Google Maps sin API de pago

La PoC no tiene API contratada, así que el estado de Google Maps se obtiene con un scraper. Se han revisado dos proyectos open source; **ambos funcionan sin API key de pago**, pero ninguno es una API oficial y ambos están sujetos a los Términos de Servicio de Google.

#### Comparativa

| Criterio | `gosom/google-maps-scraper` | `Mahanaicoach/google-maps-scraper-kit` |
|---|---|---|
| Lenguaje / runtime | Go + Playwright/Chromedp; binario, Docker o web UI | Python + Chrome; sin Docker obligatorio |
| API key | No | No |
| Estado del negocio | **Sí**: campo `status` con `open` / `closed` / `temporary` | Parcial: `--full` indica si está abierto ahora; no hay campo explícito de cierre permanente |
| `place_id` | Sí | Sí con `--full` |
| Rating y `review_count` | Sí | Sí |
| Reseñas | Sí, hasta ~300 con `-extra-reviews` | Hasta 5 con `--reviews N` |
| Otras señales | Horarios, teléfono, web, categorías, `cid`, imágenes, `popular_times` | Teléfono, web, categoría, redes sociales, dirección |
| Volumen | Mayor; pensado para extracción masiva | Acotado (~120 listados por búsqueda según su documentación) |
| Proxies / anti-bloqueo | Soporte de proxies y modo distribuido | Rate limits y bloqueos frecuentes; no resuelve CAPTCHA a gran escala |
| Coste | Gratis (infraestructura propia) | Gratis (infraestructura propia) |
| Licencia | MIT | MIT |

#### Recomendación para la PoC

- **`gosom` como fuente principal de cierre**: es el único que expone un `status` explícito, que mapea directamente a las señales C1/C2. Se ejecuta fuera del proceso Go, con una búsqueda por establecimiento (nombre + dirección) y `-depth 1`, y se importa el CSV/JSON resultante.
- **El kit como fuente secundaria**: útil para contrastar rating, `review_count` y "abierto ahora", o como fallback si `gosom` deja de funcionar. No se usa como única prueba de cierre porque no distingue cierre permanente de temporal.
- **Google Places sigue siendo la fuente preferente si algún día hay clave**: el scraper es un puente para la PoC, no el destino.

#### Integración en la arquitectura

El scraper **no se embebe en el binario Go**. Se trata como un proceso externo y su salida se importa detrás de la interfaz `Source`:

```text
scheduler → source/google_maps_scraper
              ├─ ejecuta el scraper con la consulta del establecimiento y depth 1
              ├─ espera con timeout y captura errores
              ├─ parsea out.json → []Place
              └─ emite señales (status, rating, review_count, place_id)
```

Reglas de integración:

1. **Una consulta por establecimiento y tick**, nunca un barrido masivo por ciudad.
2. **Timeout duro** (p. ej. 90 s) y `max_places: 3` para no arrastrar resultados irrelevantes.
3. **Caché por `place_id`** con TTL de 7 días: el estado no cambia cada minuto.
4. **Confianza menor que la API oficial**: `status` del scraper pesa 0,90 con confianza 0,75 (frente a 0,95/0,90 de Places).
5. **Deduplicación**: si hay `place_id`, se usa como clave; si no, nombre normalizado + geo.
6. **Modo degradado**: si el scraper falla o devuelve 0 resultados, la señal se marca `no_data` y no se penaliza al establecimiento.
7. **Sin dependencia de la UI**: el scraper se ejecuta en el scheduler, no en una petición HTTP del usuario.

#### Riesgos legales y operativos

- **Términos de Servicio**: Google prohíbe el scraping automatizado de Maps. `gosom` lo advierte en su propio README. La PoC debe limitarse a 10 establecimientos, uso interno, sin redistribuir los datos y con revisión legal antes de producción.
- **Rate limits y bloqueos**: Google puede devolver CAPTCHA o bloquear IPs. Por eso el scan es secuencial, con jitter y backoff; si aparecen bloqueos, se sube `scan.trigger_interval`.
- **Fragilidad**: el scraping depende del HTML de Google y se rompe cuando cambia. Hay que fijar versión, monitorizar fallos y tener un plan B (Places u OSM).
- **GDPR**: se extraen datos del negocio y reseñas públicas. No se deben almacenar nombres de reseñistas ni perfiles; solo rating agregado, número de reseñas y texto mínimo si se analiza. Aplicar minimización y retención.
- **Calidad del dato**: un `status` scrapeado puede estar desactualizado o ser incorrecto. Nunca debe ser la única evidencia para una decisión administrativa; en la PoC solo ordena.
- **Atribución**: si se muestran datos de Google, respetar sus condiciones de atribución; en la PoC se muestran como evidencia interna con enlace a la ficha.

#### Integración validada (v0.5)

La integración se probó contra el binario real `gosom/google-maps-scraper v1.18.1` (linux-amd64), instalado con `make turislab-scraper` en `bin/google-maps-scraper` (gitignoreado). Hallazgos que la implementación debe respetar:

- **CLI real**: `-input <file> -results <out> -json -depth 1 -lang es -c 1 -exit-on-inactivity 30s`. No existe `-count`: `max_places` se aplica después de parsear. Si el establecimiento tiene coordenadas se añade `-geo lat,lng`; con `headless: false` se añade `-debug`; con proxy, `-proxies`.
- **Salida**: NDJSON (un objeto por línea; también se acepta un array o un objeto único). El nombre del negocio está en **`title`**, no en `name`.
- **`status` no es un enum limpio**: en lugares abiertos contiene el horario (`"Closed ⋅ Opens 12:30 pm Tue"`, `"Cerrado ⋅ Abre a las 13:00"`), la etiqueta del negocio o incluso el precio (`"819 €"`). Solo los cerrados permanentes llevan el enum, que **está localizado según `-lang`**: `"CLOSED"` con inglés, `"CERRADO"` con español.
- **Bug evitado**: clasificar por la subcadena `"closed"`/`"cerrado"` marcaba como cerrado permanente cualquier restaurante fuera de su horario. La clasificación exige el enum exacto o un calificador explícito (`permanently closed`, `cerrado permanentemente`, `temporarily closed`, `cerrado temporalmente`).
- **Rate limit**: tras una consulta correcta, la siguiente inmediata falla con `playwright: ... reading 'scrollHeight'` (Google devuelve una página sin el feed). Espaciando ~60 s vuelve a funcionar, lo que valida el diseño secuencial con `scan.trigger_interval: 1m`. El error se trata como fuente no saludable y no aborta el scan.
- **Primer arranque**: el binario descarga su propio Chromium de Playwright (~115 MB) en `~/.cache/ms-playwright`; conviene hacerlo antes del primer scan real.

Validación end-to-end con `turislab --once` y datos reales:

| Establecimiento | `status` real | Señal | Score |
|---|---|---|---|
| Hotel Palacio del Retiro (Madrid) | `819 €` (etiqueta) | `open`, rating 4.5, 542 reseñas | `closure_risk 0`, P3, `active` |
| Punto MX (Madrid, cerrado en 2022) | `CERRADO` | `permanently_closed` | `closure_risk 1.0`, P0, `closure_confirmed` |

#### Configuración prevista

```yaml
sources:
  google_maps_scraper:
    enabled: true
    provider: gosom              # gosom | kit
    binary: ./bin/google-maps-scraper
    timeout_seconds: 90
    max_places: 3
    depth: 1
    language: es
    cache_ttl_days: 7
    include_reviews: false       # la PoC solo necesita estado y rating
    proxy_url_env: GMAPS_PROXY_URL   # opcional
    fallback_provider: kit
```

`GMAPS_PROXY_URL` es un secreto y va por variable de entorno; el resto es configuración funcional en `config.yaml`.

### 3.5 Limitación estructural que hay que decir en voz alta

**No se puede detectar una tendencia de reseñas con una sola llamada a una API oficial.** Google devuelve como máximo 5 reseñas por lugar; Yelp, 3 extractos; TripAdvisor Discover, un pool pequeño. Para calcular "la nota ha bajado 0,4 en 6 meses" o "las quejas por limpieza se han duplicado" hace falta una de estas tres cosas:

1. **Snapshots propios repetidos en el tiempo** (recomendado): guardar rating, número de reseñas y las reseñas visibles en cada scan. A los 3–6 meses se tiene serie temporal propia. Es barato y legal.
2. **Datos licenciados** de un proveedor de reputación (TrustYou, ReviewPro, etc.) o de un agregador.
3. **Scraping** de reseñas completas: riesgo de incumplimiento de términos de servicio, bloqueos y fragilidad. Solo con revisión legal y nunca como base única.

El plan asume la opción 1 como columna vertebral: **el histórico lo construye el propio sistema**. Las señales de tendencia se activan cuando hay al menos 2–3 snapshots separados en el tiempo; antes de eso, el sistema usa el nivel actual y marca la tendencia como `insufficient_history`.

---

## 4. Modelo conceptual y contrato de datos

### 4.1 Entidades

```text
Establishment (establecimiento)
  ├── Identity        (IDs externos, NAP, geo, matching)
  ├── Signals[]       (observaciones fechadas de una fuente)
  ├── Evidence[]      (interpretación de una señal para un score)
  ├── Scores          (closure_risk, quality_risk, quality_delta, priority)
  ├── Inspections[]   (visitas, resultados, incidencias)
  └── History[]       (snapshots y cambios de estado)
```

- **Establishment**: la entidad de negocio. Tiene un `id` interno estable y un `status` administrativo (`active`, `inactive`, `unknown`, `closed_confirmed`).
- **Identity**: cómo se reconoce el mismo establecimiento en cada fuente. Es la pieza más delicada: un mal match genera falsos positivos.
- **Signal**: una observación cruda y fechada ("Google devolvió `CLOSED_PERMANENTLY` el 2026-11-05", "la web devolvió 404", "el teléfono no existe"). Guarda fuente, método, timestamp, valor y payload.
- **Evidence**: la señal interpretada para el scoring ("señal de cierre directa, peso 0,95, familia `direct_status`, confianza 0,9"). Varias señales pueden producir una evidencia; una señal puede alimentar dos scores (p. ej. rating bajo alimenta calidad y actividad).
- **Score**: resultado calculado con versión de modelo, fecha, componentes y explicación.
- **Inspection**: la visita real y su resultado. Es la verdad terreno para calibrar.

### 4.2 Contrato de `base-de-datos.json`

El fichero real aún no está en el repositorio. El plan define un contrato **tolerante** que acepta un array plano o un objeto con metadatos, y que normaliza alias habituales. La implementación debe validar y reportar errores por registro, no abortar todo el fichero.

```json
{
  "version": "1.0",
  "generated_at": "2026-11-05T00:00:00Z",
  "source": "registro-autonomico",
  "establishments": [
    {
      "id": "EST-000123",
      "name": "Hotel Example",
      "category": "hotel",
      "stars": 4,
      "address": "Calle Mayor 1",
      "municipality": "Example",
      "province": "Example",
      "postal_code": "28001",
      "country": "ES",
      "lat": 40.4168,
      "lng": -3.7038,
      "phone": "+34910000000",
      "email": null,
      "website": "https://example.com",
      "registry_id": "TUR-12345",
      "certifications": ["SICTED", "Q"],
      "last_inspection_at": "2024-05-10",
      "last_inspection_result": "ok",
      "google_place_id": null,
      "tripadvisor_id": null,
      "yelp_id": null,
      "osm_id": null,
      "notes": ""
    }
  ]
}
```

Reglas de normalización:

- `id` obligatorio; si falta, se genera un hash estable de nombre+dirección y se marca `id_generated=true`.
- `name`, `address`, `municipality` y `category` son mínimos para el matching.
- `lat/lng` opcionales pero muy recomendables; sin geo el matching baja de confianza.
- Fechas en ISO-8601; se aceptan `YYYY-MM-DD` y `RFC3339`.
- Teléfonos normalizados a E.164 cuando sea posible; si no, se guarda el original y `phone_e164=null`.
- IDs externos opcionales: si vienen, se usan como verdad; si no, se resuelven.
- Campos desconocidos se conservan en `extra` para no perder información.

Validación mínima por registro: `name` no vacío, `address` o `lat/lng`, `category` reconocida o `other`, y unicidad de `id`. El informe de carga muestra cuántos registros entran, cuántos se descartan y por qué.

### 4.3 Conjunto de validación de la PoC

Además de `base-de-datos.json`, la PoC usa un fichero **`ground-truth.json`** con el estado esperado de los 10 establecimientos. No se usa para calcular el score: se usa para medir si el sistema acierta y para calibrar pesos. Nunca debe filtrarse al motor de scoring en tiempo de ejecución.

El esquema implementado es deliberadamente plano: una lista de establecimientos con su estado verificado y una nota que cita la fuente de esa verificación (prensa, visita o llamada).

```json
{
  "_comment": "Verdad terreno de la PoC. NUNCA se usa para scoring.",
  "_updated": "2026-11-06",
  "establishments": [
    {
      "id": "rest-puntomx",
      "name": "Punto MX",
      "ground_truth_status": "closed_permanently",
      "notes": "CERRADO en 2022. Cierre permanente confirmado por prensa."
    },
    {
      "id": "rest-botin",
      "name": "Sobrino de Botín",
      "ground_truth_status": "active",
      "notes": "Abierto."
    }
  ],
  "poc_success_criterion": "Los cerrados conocidos deben estar en el top 3 y ningún activo debe quedar como closure_confirmed."
}
```

Estados admitidos: `active`, `closed_permanently`, `closed_temporarily`, `seasonal`, `closure_suspected` y `unknown`. Cualquier otro valor hace fallar la carga, para que una errata no pase desapercibida.

La comprobación se ejecuta con:

```bash
./turislab --config app/config.turislab.yaml --validate
./turislab --config app/config.turislab.yaml --validate --validate-strict
```

`--validate` ordena los scores igual que la cola, muestra la posición y el estado predicho de cada establecimiento y calcula:

- **Acierto top-N** (`--validate-top`, por defecto 3): cerrados conocidos que caen en el top.
- **Falsos positivos**: activos verificados que el sistema marca como `closure_confirmed`.
- **Cobertura**: cuántos de los 10 tienen score; si falta alguno, la validación no se considera completa.

Sale con código 0 solo si todos los establecimientos tienen score, todos los cerrados conocidos están en el top-N y (con `--validate-strict`) no hay falsos positivos. Así el comando sirve tal cual en CI.

Composición real de la muestra (Madrid, datos de OSM/Nominatim y cierres verificados con prensa):

| Tipo | Cantidad | Establecimientos |
|---|---|---|
| Restaurante cerrado conocido | 2 | Punto MX (2022), Santceloni (2020) |
| Restaurante abierto y estable | 5 | Botín, Casa Lucio, El Paraguas, La Castela, Horcher |
| Hotel abierto y estable | 3 | The Palace, Mandarin Oriental Ritz, Rosewood Villa Magna |

Los dos cierres se eligieron porque Google Maps todavía devuelve `CERRADO` para ellos, de modo que la señal directa es observable. Un cierre que Google mantiene como abierto (p. ej. Zalacaín) queda fuera de la muestra: la PoC mide el ranking con evidencia disponible, no la capacidad de adivinar cierres invisibles para todas las fuentes.

Métricas de la PoC con esta muestra:

- **Ranking**: posición de cada cerrado conocido en la cola (objetivo: top 3).
- **Detección**: cuántos cerrados tienen `closure_risk ≥ 0,7`.
- **Falsos positivos**: cuántos abiertos estables entran en P0/P1.
- **Explicación**: revisión cualitativa de si el motivo mostrado es correcto.
- **Cobertura**: cuántos de los 10 tienen al menos una señal directa y dos indirectas.

### 4.4 Modelo de datos interno

Tablas recomendadas (SQLite para el piloto, Postgres para producción):

```sql
establishments(id, name, category, stars, address, municipality, province,
               postal_code, country, lat, lng, phone_e164, website, registry_id,
               admin_status, certifications_json, last_inspection_at,
               last_inspection_result, created_at, updated_at)

establishment_sources(establishment_id, source, external_id, match_confidence,
                      match_method, first_seen_at, last_seen_at, raw_json)

signals(id, establishment_id, source, kind, observed_at, value_json,
        numeric_value, confidence, status, payload_json, dedupe_key)

evidences(id, establishment_id, signal_id, score_kind, family, weight,
          direction, confidence, observed_at, explanation)

scores(id, establishment_id, model_version, computed_at, closure_risk,
       quality_risk, quality_delta, priority, tier, confidence,
       components_json, explanation_json)

snapshots(id, establishment_id, source, captured_at, rating, review_count,
          status, payload_json)

inspections(id, establishment_id, inspector_id, visited_at, outcome,
            quality_score, findings_json, notes, created_at)

changes(id, establishment_id, detected_at, kind, from_value, to_value,
        severity, acknowledged_by, acknowledged_at)
```

Índices mínimos: `(establishment_id, observed_at)` en `signals`, `(computed_at)` en `scores`, `(municipality, priority)` para la cola, y `dedupe_key` único en `signals` para idempotencia.

---

## 5. Catálogo de señales de cierre

Cada señal se describe con: qué se observa, cómo se obtiene, qué significa, qué peso inicial tiene y qué fiabilidad se le asigna. Los pesos son **puntos de partida**; se calibran con visitas reales (ver §7.9).

Convención de peso: `0–1`, donde 1 es evidencia casi concluyente. La confianza (`0–1`) refleja la calidad del match y de la fuente, no la probabilidad de cierre.

### 5.1 Señales directas (familia `direct_status`)

| # | Señal | Fuente / método | Interpretación | Peso | Confianza base | Coste |
|---|---|---|---|---|---|---|
| C1 | `businessStatus = CLOSED_PERMANENTLY` | Google Places Details | Cierre declarado por el propietario o aceptado por Google | 0,95 | 0,90 | Pro SKU |
| C2 | `businessStatus = CLOSED_TEMPORARILY` | Google Places Details | Cierre temporal; puede ser reforma o estacional | 0,45 | 0,85 | Pro SKU |
| C1b | `status = permanently_closed` | Scraper Google Maps (`gosom`) | Cierre declarado en la ficha de Maps, sin API oficial | 0,90 | 0,75 | Gratis (scraping) |
| C2b | `status = temporarily_closed` | Scraper Google Maps (`gosom`) | Cierre temporal en Maps; el kit no lo distingue | 0,40 | 0,70 | Gratis (scraping) |
| C3 | `businessStatus = FUTURE_OPENING` | Google Places Details | Aún no ha abierto; no es cierre, es alta pendiente | 0,05 | 0,85 | Pro SKU |
| C4 | Place ID no encontrado / `NOT_FOUND` | Google Places Details | El ID ya no existe; posible fusión o eliminación | 0,55 | 0,75 | Pro SKU |
| C5 | `is_closed = true` | Yelp Fusion | Cierre declarado en Yelp | 0,85 | 0,75 | Fusion |
| C6 | TripAdvisor: página 404 o "closed" | Terra / web | Cierre o retirada del listado | 0,60 | 0,60 | Terra |
| C7 | `operating_status = permanently_closed` | Overture Maps | Cierre inferido por el dataset | 0,70 | 0,65 | Gratis |
| C8 | `operating_status = temporarily_closed` | Overture Maps | Cierre temporal | 0,35 | 0,60 | Gratis |
| C9 | `disused:*`, `closed=yes`, `shop=vacant` | OSM / Overpass | Comunidad marca el local como cerrado o en desuso | 0,60 | 0,55 | Gratis |
| C10 | `closed_on` no nulo | SafeGraph (si se contrata) | Cierre observado por el proveedor | 0,90 | 0,85 | Licencia |
| C11 | Baja o suspensión en registro oficial | Registro turístico | Cierre administrativo | 0,95 | 0,95 | Convenio |
| C12 | Anuncio oficial de cierre/clausura | Boletín, web municipal, prensa oficial | Cierre confirmado | 0,90 | 0,85 | Bajo |
| C13 | El establecimiento desaparece de la búsqueda por nombre+dirección | Google Text Search | Posible cierre o cambio de nombre | 0,50 | 0,65 | Pro SKU |
| C14 | El establecimiento desaparece de la fuente que lo listaba (OTA, cadena) | Scraping/API OTA | Posible cierre, cambio de marca o de gestor | 0,40 | 0,55 | Variable |

Notas:

- **C1 es la señal reina**, pero no es infalible: hay listings marcados como cerrados que reabren, y listings operativos que en realidad están cerrados porque nadie lo ha actualizado. Por eso se combina.
- **C1b es el sustituto de C1 en la PoC** mientras no haya API de pago. Vale casi lo mismo, pero con menos confianza: el scraping puede leer un estado obsoleto o mal parseado, y no tiene el respaldo contractual de la API. Si C1 y C1b coinciden, la confianza sube; si discrepan, se marca conflicto.
- **El kit no aporta C1b/C2b**: solo dice si está abierto en el momento de la consulta. Se usa para rating/reseñas y como contraste, no como prueba de cierre permanente.
- **C2 no debe disparar alarma por sí sola.** En destinos estacionales, `CLOSED_TEMPORARILY` es normal en temporada baja. Se pondera por estacionalidad (§7.6).
- **C13/C14 son señales de ausencia**, más débiles que una declaración explícita. Su valor sube si se repiten en varios scans y en varias fuentes.
- La **fecha de la última reseña** y la **velocidad de reseñas** se tratan en la familia `activity`, porque son señales indirectas de cierre.

### 5.2 Señales de actividad y contacto (familia `activity`)

| # | Señal | Fuente / método | Interpretación | Peso | Confianza base | Coste |
|---|---|---|---|---|---|---|
| C15 | Última reseña > 24 meses | Google/Yelp/TripAdvisor | Puede seguir abierto con poco tráfico, o cerrado | 0,35 | 0,55 | API |
| C16 | Caída > 80 % en reseñas nuevas/mes durante 3 meses | Snapshots propios | Pérdida brusca de actividad | 0,45 | 0,60 | Snapshots |
| C17 | Teléfono: número inexistente / no asignado | Operadora o bot | Línea dada de baja | 0,55 | 0,70 | Bot/telefonía |
| C18 | Teléfono: 3 intentos en horarios distintos sin respuesta | Bot | Posible cierre, pero también saturación | 0,30 | 0,55 | Bot |
| C19 | Teléfono: buzón genérico o "fuera de servicio" | Bot + STT | Señal media | 0,40 | 0,60 | Bot |
| C20 | Web: dominio expirado, 404, parked o error persistente | HTTP + DNS + changedetection | Abandono digital | 0,30 | 0,75 | Bajo |
| C21 | Web: "cerrado", "reformas", "nuevo propietario" | Crawl + NLP | Cierre temporal o cambio | 0,50 | 0,65 | Bajo |
| C22 | Reservas online deshabilitadas en todas las OTAs en temporada alta | OTA/API | Posible cierre o problema comercial | 0,35 | 0,55 | Variable |
| C23 | Redes sociales sin actividad > 12 meses | APIs sociales / web | Abandono, no necesariamente cierre | 0,15 | 0,50 | Bajo |
| C24 | Reseña reciente dice "está cerrado" | NLP de reseñas | Testimonio directo, pero puede ser error | 0,50 | 0,55 | API/scraping |
| C25 | Varias reseñas recientes describen cierre | NLP agregado | Más fiable que una sola | 0,70 | 0,65 | API/scraping |
| C26 | Horarios eliminados o "cerrado permanentemente" en la ficha | Google/OTA | Señal directa débil | 0,45 | 0,70 | API |

### 5.3 Señales de contexto (familia `context`)

| # | Señal | Fuente | Interpretación | Peso | Confianza |
|---|---|---|---|---|---|
| C27 | Noticia local de cierre, quiebra o desalojo | Prensa + NLP | Evidencia externa | 0,60 | 0,60 |
| C28 | El local aparece en un proyecto de reforma o demolición | Prensa/urbanismo | Cierre probable | 0,55 | 0,60 |
| C29 | Cambio de titularidad o de marca no comunicado | Registro/prensa | No es cierre, pero implica revisión | 0,20 | 0,50 |
| C30 | Impago o concurso de acreedores | Boletines oficiales | Cierre probable | 0,65 | 0,70 |
| C31 | El establecimiento se anuncia en venta/traspaso | Portales inmobiliarios | Cierre o cambio próximo | 0,30 | 0,50 |
| C32 | Catástrofe/obra pública que corta el acceso | Prensa/administración | Cierre operativo temporal | 0,40 | 0,65 |

### 5.4 Reglas de combinación para cierre

La familia `direct_status` tiene un **cap** alto porque una declaración explícita vale mucho, pero no se permite que una sola señal dé por cerrado un establecimiento. Reglas:

1. Si `C11` (baja oficial) está presente → `closure_risk ≥ 0,95`, `status_suggested = closed_confirmed`.
2. Si `C1`, `C1b` o `C10` está presente y no hay evidencia contradictoria reciente → `closure_risk ≥ 0,85`.
3. Si hay ≥ 2 señales directas de fuentes distintas (p. ej. C1 + C5) → `closure_risk ≥ 0,90` y `confidence` sube.
4. Si solo hay señales indirectas (actividad, contacto, web) → `closure_risk` máximo 0,75 salvo que sean ≥ 4 y coherentes.
5. Si hay una señal directa de cierre y una señal fuerte de actividad reciente (reseña < 30 días, teléfono contesta, web activa) → se marca **conflicto** y se baja la confianza; el caso va a revisión humana, no a la cola P0.
6. `CLOSED_TEMPORARILY` en temporada baja con historial de cierre estacional → se trata como `seasonal_closure`, no como riesgo.

### 5.5 Señales que se descartan o se usan con cautela

- **"No aparece en Google Maps"**: puede ser un problema de matching o de la propia búsqueda. Solo cuenta si el Place ID existía antes y ahora falla.
- **"La web no carga"**: puede ser un CDN, un bloqueo geográfico o un mantenimiento. Necesita 3 fallos en 7 días y comprobación desde otra red.
- **"El teléfono no contesta"**: la señal más débil de todas. Un hotel puede no contestar por saturación. Solo cuenta con patrón repetido y en horario comercial.
- **Cambios de rating a la baja**: no son cierre, son calidad. No deben contaminar `closure_risk`.
- **Reseñas negativas**: no son cierre. Pueden indicar degradación, pero una reseña enfadada no cierra un hotel.

---

## 6. Catálogo de señales de calidad

El objetivo no es medir "si gusta", sino **detectar una caída respecto a su propio histórico, a su categoría y a su entorno**. Un hotel de 3 estrellas con 4,1 puede estar perfecto; el problema es que hace un año tenía 4,5.

### 6.1 Reputación y tendencia (familia `reviews`)

| # | Señal | Cómo se calcula | Interpretación | Peso | Confianza |
|---|---|---|---|---|---|
| Q1 | Caída de rating | `rating_actual - rating_hace_6m` (o media móvil) | Degradación sostenida | 0,60 | 0,75 |
| Q2 | Caída de rating vs. mismo mes del año anterior | Estacionalidad corregida | Cambio real, no estacional | 0,65 | 0,75 |
| Q3 | Caída de la nota en el último trimestre | Media de snapshots | Aceleración reciente | 0,55 | 0,70 |
| Q4 | Aumento de la proporción de reseñas ≤ 2 estrellas | `neg_share_actual - neg_share_base` | Malestar creciente | 0,55 | 0,70 |
| Q5 | Caída del sentimiento global | NLP sobre reseñas | Detecta deterioro aunque la nota no baje | 0,50 | 0,65 |
| Q6 | Caída de sentimiento por aspecto | Aspect-based sentiment: limpieza, servicio, instalaciones, comida, ubicación, relación calidad-precio | Localiza el problema y mejora la explicación | 0,55 | 0,65 |
| Q7 | Aumento de quejas por un aspecto concreto | Conteo de menciones negativas por aspecto | Señal accionable | 0,50 | 0,65 |
| Q8 | Tasa de respuesta del propietario a la baja | Reseñas respondidas / total | Abandono de gestión | 0,25 | 0,60 |
| Q9 | Tiempo medio de respuesta creciente | Timestamps de respuesta | Menos atención | 0,20 | 0,55 |
| Q10 | Posición en ranking local a la baja | Rank tracking (Local Falcon, Google) | Pérdida de competitividad | 0,30 | 0,55 |
| Q11 | Nota muy por debajo de su categoría | `rating - media_categoria` | Desalineación con lo declarado | 0,45 | 0,70 |
| Q12 | Nota de OTA muy distinta de la de Google | Discrepancia entre fuentes | Posible gestión de reseñas o problema de fuente | 0,20 | 0,50 |
| Q13 | Reseñas que mencionan suciedad, ruido, inseguridad, trato | NLP de aspectos | Señal directa de calidad | 0,50 | 0,65 |
| Q14 | Reseñas que mencionan obras, falta de servicios o cierre parcial | NLP | Degradación operativa | 0,45 | 0,60 |
| Q15 | Reseñas duplicadas o sospechosas | Detección de plantillas/patrones | Puede inflar la nota; baja la confianza | — | — |

### 6.2 Producto, servicio y web (familia `product`)

| # | Señal | Cómo se obtiene | Interpretación | Peso | Confianza |
|---|---|---|---|---|---|
| Q16 | Web sin actualizar > 12 meses | `last-modified`, fechas visibles | Abandono | 0,20 | 0,60 |
| Q17 | Motor de reservas roto o sin disponibilidad | HTTP + flujo de reserva | Pérdida de servicio | 0,35 | 0,65 |
| Q18 | Servicios anunciados que ya no existen | Comparación web vs. ficha/OTA | Degradación de producto | 0,40 | 0,55 |
| Q19 | Cambio de categoría o de servicios sin comunicar | Registro/OTA | Requiere revisión | 0,35 | 0,60 |
| Q20 | Fotos antiguas o de baja calidad en todas las fuentes | Metadatos/fechas | Señal débil de abandono | 0,15 | 0,45 |
| Q21 | Precio muy por debajo de la media de su categoría | OTAs | Posible deterioro o estrategia comercial | 0,20 | 0,50 |
| Q22 | Precio muy por encima sin mejora de servicios | OTAs | Posible desalineación, no calidad | 0,10 | 0,45 |
| Q23 | Falta de accesibilidad declarada | Ficha vs. normativa | Riesgo de incumplimiento | 0,30 | 0,60 |
| Q24 | Cambios en horarios o cierre de servicios (spa, restaurante) | Google/OTA/web | Degradación operativa | 0,35 | 0,60 |

### 6.3 Señales oficiales e inspecciones (familia `official`)

| # | Señal | Fuente | Interpretación | Peso | Confianza |
|---|---|---|---|---|---|
| Q25 | Sanción o expediente firme | Registro/boletín | Riesgo alto | 0,85 | 0,90 |
| Q26 | Inspección oficial con incidencias graves | Sistema interno | Evidencia directa | 0,90 | 0,95 |
| Q27 | Inspección con incidencias leves repetidas | Sistema interno | Degradación | 0,55 | 0,85 |
| Q28 | Certificación suspendida o retirada | SICTED/Q/ISO | Pérdida de calidad reconocida | 0,80 | 0,90 |
| Q29 | Queja formal tramitada | Consumo/registro | Señal de conflicto | 0,60 | 0,80 |
| Q30 | Tiempo desde la última inspección > umbral | Sistema interno | No es calidad, es **frescura**; alimenta prioridad | — | — |
| Q31 | Autoevaluación incumplida o documentación caducada | Registro | Riesgo administrativo | 0,40 | 0,75 |

### 6.4 Contexto y noticias (familia `news`)

| # | Señal | Fuente | Interpretación | Peso | Confianza |
|---|---|---|---|---|---|
| Q32 | Noticia negativa reciente (brotes, accidentes, conflictos laborales) | Prensa + NLP | Riesgo reputacional | 0,55 | 0,60 |
| Q33 | Noticia positiva relevante (premio, reforma) | Prensa | Reduce riesgo | -0,30 | 0,60 |
| Q34 | Cambio de gestor o cadena | Prensa/registro | Incertidumbre | 0,25 | 0,55 |
| Q35 | Conflicto vecinal o denuncia pública | Prensa/ayuntamiento | Señal débil | 0,25 | 0,50 |

### 6.5 Reglas de combinación para calidad

1. **Comparar consigo mismo antes que con la media.** La señal principal es `quality_delta = f(histórico propio, estacionalidad)`. La comparación con pares es contexto, no la base.
2. **No penalizar la variación normal.** Un rating que oscila ±0,1 entre snapshots es ruido. Se exige una caída mínima de **0,3** en 6 meses o **0,2** sostenida en 3 snapshots para activar Q1.
3. **La ausencia de datos no es calidad.** Un establecimiento sin reseñas no es malo; es `unknown`. Se marca `collect_data`.
4. **Las señales oficiales mandan.** Una inspección con incidencias graves o una sanción firme pesan más que cualquier reseña.
5. **Las noticias no cierran casos por sí solas.** Se usan como contexto y para priorizar revisión humana.
6. **El sentimiento por aspectos es la mejor explicación.** "Baja de 4,4 a 4,0, sobre todo por limpieza (18 menciones negativas en 90 días, +240 %)" es accionable; "score 72" no.

### 6.6 Cómo obtener las reseñas y el sentimiento

| Vía | Ventaja | Limitación | Recomendación |
|---|---|---|---|
| Google Places (5 reseñas) | Oficial, estable | Muestra sesgada a las más recientes/relevantes; requiere clave y facturación | PoC: opcional |
| Yelp Fusion (3 extractos) | Oficial | Poca cobertura en España | PoC: no (sin key) |
| TripAdvisor Terra | Oficial | Coste por entidad, pool pequeño | Fase 2 |
| Scraping | Volumen e histórico | Legal/frágil | Solo con revisión legal y proveedor |
| Proveedor de reputación | Histórico y benchmarks | Coste | Fase 2/3 |
| Encuestas propias post-visita | Verdad terreno | Requiere operación | Fase 2 |

Para el análisis de sentimiento se propone empezar con un modelo ligero y explicable:

- **Lexicón + reglas** por aspecto en español (limpieza, ruido, trato, comida, instalaciones, ubicación, precio, seguridad, accesibilidad). Barato, auditable, sin dependencias.
- **Modelo de clasificación** (p. ej. un modelo multilingüe pequeño servido localmente o vía API) cuando el volumen lo justifique.
- **LLM con salida estructurada** solo para resúmenes y para casos ambiguos, con validación y coste controlado.
- Guardar siempre el texto de la reseña y la etiqueta para poder auditar y recalibrar.

---

## 7. Motor de scoring

### 7.1 Principios

1. **Explicable por construcción.** El score es una función documentada de evidencias; no un modelo caja negra. Se puede reproducir a mano.
2. **Trazable.** Cada componente guarda `signal_id`, peso, confianza y fecha.
3. **Conservador.** Ante conflicto o poca cobertura, baja la confianza y no se sube a P0.
4. **Estable.** Un cambio de score exige persistencia (histéresis) para no oscilar con cada scan.
5. **Calibrable.** Los pesos iniciales son hipótesis; se ajustan con resultados de visitas.
6. **Separado por pregunta.** Cierre ≠ calidad ≠ prioridad. Se calculan por separado y se combinan al final.

### 7.2 De señal a evidencia

Para cada señal `i`:

```text
e_i = w_i * d_i * c_i * r_i * s_i
```

Donde:

- `w_i ∈ [0,1]`: magnitud del peso de la señal (tablas §5 y §6).
- `d_i ∈ {-1, 0, +1}`: dirección. `+1` aumenta el riesgo; `-1` lo reduce (señal protectora: reforma reciente, premio); `0` es informativa. Las protectoras se aplican como multiplicador en §7.4, no dentro del noisy-OR.
- `c_i ∈ [0,1]`: confianza en la observación (match de identidad × fiabilidad de la fuente × calidad del dato).
- `r_i ∈ (0,1]`: factor de recencia.
- `s_i ∈ [0.5, 1.5]`: factor de estacionalidad (solo señales sensibles a temporada).

La confianza de la evidencia se calcula aparte:

```text
conf_i = c_match * c_source * c_data
```

Y se guarda junto al valor para poder explicar "pesa poco porque el match es dudoso".

### 7.3 Recencia

Decaimiento exponencial por semivida según el tipo de señal:

| Tipo de señal | Semivida | Ejemplo |
|---|---|---|
| Estado directo (C1, C5, C11) | 180 días | `CLOSED_PERMANENTLY` |
| Estado temporal (C2, C8) | 45 días | `CLOSED_TEMPORARILY` |
| Actividad y contacto (C15–C26) | 90 días | última reseña, teléfono |
| Web y redes (C20–C23) | 60 días | 404, dominio expirado |
| Noticias y contexto (C27–C32) | 120 días | cierre en prensa |
| Calidad de reseñas (Q1–Q14) | 120 días | caída de rating |
| Oficial (Q25–Q31) | 365 días | sanción firme |

```text
r_i = 0.5 ^ (días_desde_observación / semivida)
```

Una señal con más de 4 semividas se ignora (`r_i < 0.06`) y se conserva solo para histórico. Excepción: las señales oficiales firmes no caducan del todo; se mantienen con un suelo de 0,4 hasta que haya una nueva inspección.

### 7.4 Agregación por familias

Las señales correlacionadas se agrupan para que no sumen varias veces el mismo hecho (tres medios citando la misma nota de prensa no son tres cierres).

Familias de cierre:

| Familia | Señales | Cap | Peso de familia |
|---|---|---|---|
| `direct_status` | C1–C14 | 0,95 | 0,45 |
| `activity` | C15–C26 | 0,75 | 0,25 |
| `context` | C27–C32 | 0,65 | 0,15 |
| `official` | C11, C12 | 0,95 | 0,15 |

Familias de calidad:

| Familia | Señales | Cap | Peso de familia |
|---|---|---|---|
| `reviews` | Q1–Q15 | 0,90 | 0,35 |
| `product` | Q16–Q24 | 0,70 | 0,15 |
| `official` | Q25–Q31 | 0,95 | 0,35 |
| `news` | Q32–Q35 | 0,60 | 0,10 |
| `freshness` | Q30 (solo prioridad) | 1,00 | 0,05 |

Dentro de cada familia se usa **noisy-OR** sobre las evidencias positivas, y luego se aplica el cap:

```text
p_familia = 1 - Π (1 - e_i)        para e_i ∈ [0,1]
p_familia = min(p_familia, cap_familia)
```

Las evidencias negativas (protectoras) se aplican como multiplicador:

```text
p_familia = p_familia * Π (1 - |e_j|)
```

Ejemplo: si `direct_status` tiene C1 (0,95 × 0,9 × 1,0 = 0,855) y C5 (0,85 × 0,75 × 0,9 = 0,574), el noisy-OR da `1 - (0,145 × 0,426) = 0,938`; con cap 0,95 se queda en 0,938.

### 7.5 Score de cierre

```text
closure_raw = Σ_f (peso_f * p_f) / Σ_f peso_f
```

Con familias ausentes (sin datos) se renormaliza sobre las presentes y se registra `coverage_closure = Σ peso_f presentes / Σ peso_f total`.

Ajuste por confianza (shrinkage hacia la base rate):

```text
closure_risk = prior + (closure_raw - prior) * confianza_agregada
confianza_agregada = 1 - Π (1 - conf_i)   (sobre evidencias presentes, con cap 0,95)
prior = tasa_base_cierre (0,05 por defecto, configurable por categoría/territorio)
```

Esto evita que un único dato dudoso produzca un 0,9. Si `confianza_agregada < 0,35`, el score se publica pero se marca `low_confidence` y no puede entrar en P0.

### 7.6 Estacionalidad

Tres mecanismos:

1. **Calendario de temporada** por categoría y municipio (`high`, `mid`, `low`). Configurable en YAML.
2. **Corrección de señales**: `CLOSED_TEMPORARILY`, caída de reservas y caída de reseñas se multiplican por `s_i = 0,4` en temporada baja y `1,2` en alta.
3. **Comparación interanual**: las tendencias se comparan con el mismo mes del año anterior cuando hay histórico; si no, con el trimestre.

Un cierre temporal en un hotel de costa en enero no es noticia. El mismo cierre en agosto sí.

### 7.7 Score de calidad y delta

Primero se calcula el **nivel de calidad** actual (0–100, mayor = mejor):

```text
quality_level = 100 * (1 - Σ_f (peso_f * p_f) / Σ_f peso_f)
```

Y la **caída** respecto a la línea base del propio establecimiento:

```text
quality_delta = quality_level_actual - quality_level_base
```

Donde `quality_level_base` es la mediana de los últimos 12 meses (o el primer snapshot si hay menos). `quality_delta` negativo indica degradación.

`quality_risk` es la probabilidad de que la caída sea real y relevante:

```text
quality_risk = sigmoid(α * (-quality_delta) + β * neg_share_delta + γ * aspect_alert - δ)
```

Valores iniciales: `α = 0,08`, `β = 1,2`, `γ = 0,6`, `δ = 1,0`. La sigmoide se calibra después. Si no hay histórico suficiente:

```text
quality_risk = sigmoid(α * (media_categoria - quality_level) + ...)
```

es decir, se compara con la categoría, y se marca `baseline=category` en la explicación.

### 7.8 Prioridad de visita

La prioridad no es "lo peor", sino **dónde rinde más una visita**. Combina riesgo, impacto y frescura:

```text
priority_raw =
    0,40 * closure_risk
  + 0,25 * quality_risk
  + 0,15 * min(1, -quality_delta / 20)
  + 0,10 * staleness
  + 0,05 * impact
  + 0,05 * complaints

priority = 100 * priority_raw * confidence_factor
```

Donde:

- `staleness = min(1, días_desde_última_inspección / 730)`. Sin inspección previa, `staleness = 1`.
- `impact` = importancia del establecimiento: categoría, plazas, certificaciones, visibilidad turística, denuncias. Normalizado 0–1. Configurable.
- `complaints` = quejas formales recientes normalizadas.
- `confidence_factor = 0,7 + 0,3 * confianza_agregada`. No se descarta un caso por baja confianza, pero se atenúa y se marca para recogida de datos.

Tiers:

| Tier | Prioridad | Acción | Plazo |
|---|---|---|---|
| **P0** | ≥ 80 y confianza ≥ 0,5 | Visita urgente / verificación telefónica inmediata | ≤ 7 días |
| **P1** | 65–79 | Incluir en la ruta del mes | ≤ 30 días |
| **P2** | 45–64 | Programar en el trimestre | ≤ 90 días |
| **P3** | < 45 | Rutina / muestreo | Según calendario |
| **Collect data** | confianza < 0,35 | No visitar aún; obtener más señales | ≤ 14 días |

Reglas de promoción y bloqueo:

- `closure_risk ≥ 0,85` y sin conflicto → mínimo P1, aunque la prioridad numérica no llegue.
- `official` con sanción firme o inspección grave → P0 directo.
- Conflicto detectado → baja un tier y se marca `needs_review`.
- `CLOSED_TEMPORARILY` en temporada baja → no puede ser P0.
- Un establecimiento con `admin_status = closed_confirmed` sale de la cola activa y pasa a verificación de reapertura.

### 7.9 Histéresis y estabilidad

Para evitar que un scan aislado mueva la cola:

- Un cambio de tier se confirma con **2 scans consecutivos** o con una señal directa nueva.
- El score publicado es la **media ponderada** del score actual y el anterior (`0,7 * actual + 0,3 * anterior`) cuando la diferencia es menor de 15 puntos; si es mayor, se publica el actual pero se marca `volatile`.
- Los cambios de estado (`active → closed_suspected`) requieren 2 scans o una señal oficial.
- Se guarda `score_history` para auditoría y para explicar "subió porque el 12/10 Google lo marcó cerrado".

### 7.10 Calibración

El sistema debe aprender de las visitas:

1. **Etiqueta real**: al cerrar una inspección, el inspector marca `open`, `closed`, `degraded`, `no_change`, `inaccessible`.
2. **Métricas**:
   - `precision@k`: de los k primeros de la cola, cuántos tenían un cambio real.
   - `recall` de cierres: de los cerrados conocidos, cuántos estaban en el top 20 %.
   - `Brier score` y curva de calibración para `closure_risk`.
   - `MAE` entre `quality_delta` predicho y observado.
3. **Ajuste**:
   - **PoC (10 establecimientos)**: no se entrena ningún modelo; solo se ajustan pesos y umbrales a mano con la verdad terreno. Cualquier ajuste debe documentarse y validarse en un ciclo nuevo para evitar sobreajuste.
   - Fase 2 (más datos): regresión logística sobre evidencias (interpretable) o gradient boosting con SHAP para comparar; se queda el modelo que mejor calibre y explique.
   - Fase 3: recalibración trimestral con validación temporal (entrenar con pasado, validar con futuro).
4. **Salvaguarda**: nunca se automatiza una decisión administrativa; el modelo solo ordena y sugiere.

### 7.11 Arranque en frío

Al principio no hay histórico. Estrategia:

- Usar `quality_level` actual y comparación con la categoría (`baseline=category`).
- Usar señales directas y oficiales, que no requieren histórico.
- Marcar `insufficient_history` en las tendencias.
- Priorizar la **recogida de datos**: el primer scan es barato (estado + rating + web + teléfono) y ya permite ordenar.
- A los 90 días, activar tendencias; a los 180, comparación interanual.

---

## 8. Arquitectura técnica

### 8.1 Visión general

```text
                    ┌──────────────────────────────┐
                    │  base-de-datos.json          │
                    └──────────────┬───────────────┘
                                   │ load + validate
                                   ▼
┌──────────────┐        ┌──────────────────────┐
│  Scheduler   │───────▶│  Scan Orchestrator   │
│ (interval)   │        │ (sequential, retries)│
└──────────────┘        └──────┬───────────────┘
                               │
        ┌───────────────┬──────────────┬─────────┬─────────────┬──────────────┐
        ▼               ▼              ▼         ▼             ▼
  Google Maps     Google Places    Website    Phone      OSM/Overture
  scraper (ext.)   (opcional)                 (manual)
        │               │              │         │             │
        └───────────────┴──────────────┴─────────┴─────────────┘
                               │ signals
                               ▼
                    ┌──────────────────────┐
                    │  Signal Store        │
                    │  (SQLite/Postgres)   │
                    └──────────┬───────────┘
                               │
                               ▼
                    ┌──────────────────────┐
                    │  Scoring Engine      │
                    │  families + priors   │
                    └──────────┬───────────┘
                               │ scores + evidence
              ┌────────────────┼─────────────────┐
              ▼                ▼                 ▼
       Audit Report UI     Agent (ADK)      /metrics + export
```

### 8.2 Interfaz `Source`

Cada fuente implementa una interfaz común. Esto permite añadir TripAdvisor, Yelp o un proveedor de pago sin tocar el orquestador.

```go
// Package source defines the contract every evidence source implements.
type Source interface {
    Name() string
    // Capabilities declares which signal kinds the source can produce.
    Capabilities() []SignalKind
    // Collect fetches signals for one establishment. It must be idempotent:
    // the same observation must produce the same dedupe key.
    Collect(ctx context.Context, est Establishment) ([]Signal, error)
    // Health reports quota, latency and last error for /api/sources.
    Health(ctx context.Context) SourceHealth
}
```

Requisitos:

- **Idempotencia**: `dedupe_key = hash(source, kind, establishment_id, ventana_temporal, valor)`. Reintentos no duplican.
- **Timeouts y reintentos** con backoff exponencial y jitter; nunca bloquear el scan completo por una fuente.
- **Rate limiting** por fuente y por API key; cola con concurrencia configurable.
- **Caché** con TTL por tipo: Place Details 7 días, web 3 días, teléfono 30 días, OSM 30 días.
- **Errores parciales**: una fuente caída produce `Signal{status: error}` y no invalida el resto.
- **Coste**: cada llamada registra unidades/coste estimado; el scan tiene un presupuesto máximo configurable.
- **Procesos externos**: un scraper (Google Maps) se ejecuta como subproceso con timeout, salida JSON/CSV y parseo defensivo. Su `Source` no bloquea el scan si el binario no está instalado: devuelve `no_data` y lo reporta en `/api/sources`.

### 8.3 Orquestación del scan

**Diseño para la PoC: secuencial y rate-limited.** El objetivo es no saturar APIs públicas ni provocar bloqueos. En lugar de lanzar 10 establecimientos a la vez, el planificador procesa **un establecimiento por tick**:

```text
cada scan.trigger_interval (por defecto 1m):
  1. Leer el cursor persistido (último establecimiento escaneado).
  2. Elegir el siguiente establecimiento en round-robin.
  3. Resolver identidad si falta.
  4. Ejecutar las fuentes habilitadas para ese establecimiento,
     con concurrencia máxima 2 y respetando el rate limit de cada fuente.
  5. Persistir señales y snapshots.
  6. Detectar cambios respecto al scan anterior de ese establecimiento.
  7. Recalcular scores y tier de ese establecimiento.
  8. Aplicar histéresis y reglas de promoción.
  9. Actualizar el cursor y publicar el informe.
 10. Registrar errores, cobertura y coste.
```

Con 10 establecimientos y `1m`, un ciclo completo tarda ~10 minutos. Es suficiente para la PoC y deja margen para subir el intervalo si una fuente devuelve `429` o `403`.

Parámetros en `config.yaml` (sección `scan`):

| Clave | Default | Efecto |
|---|---|---|
| `scan.trigger_interval` | `1m` | Tiempo entre establecimientos |
| `scan.targets_per_tick` | `1` | Establecimientos por tick (subir solo si las fuentes lo permiten) |
| `scan.jitter` | `0.1` | Variación aleatoria ±10 % para no ir en cadencia perfecta |
| `scan.max_concurrent_sources` | `2` | Fuentes en paralelo por establecimiento |
| `scan.backoff_on_429` | `10m` | Pausa tras un rate limit |
| `scan.max_attempts` | `3` | Reintentos por fuente |

Modos de ejecución:

- **Daemon** (por defecto): tick cada intervalo, cursor persistido, sobrevive a reinicios.
- **`--once`**: un ciclo completo (útil para tests y para la carga inicial).
- **`--establishment <id>`**: fuerza el scan de uno solo (botón "Actualizar" de la UI), respetando el rate limiter global.
- **Incremental** (post-PoC): prioriza los que tienen señales caducadas o tier alto.

Salvaguardas:

- Si una fuente devuelve `429`, se aplica backoff y se salta esa fuente en los siguientes ticks hasta que pase la ventana.
- Los ticks **no se solapan**: si el scan de un establecimiento tarda más que `scan.trigger_interval`, el siguiente tick espera a que termine. El intervalo es un mínimo, no una promesa de cadencia exacta.
- Si el proceso se reinicia, retoma desde el cursor; no reinicia el ciclo desde cero.
- El scan manual nunca debe saltarse el rate limit global: comparte el mismo limitador.
- Se registra `last_scan_at` por establecimiento y por fuente para detectar datos obsoletos.

### 8.4 Resolución de identidad

Es la parte que más falsos positivos puede generar. Estrategia en cascada:

1. **IDs explícitos** del fichero (`google_place_id`, `tripadvisor_id`, `osm_id`): confianza 1,0.
2. **Búsqueda por nombre + dirección + geo** en Google Text Search: candidatos con score.
3. **Normalización**: minúsculas, sin tildes, sin formas societarias (`S.L.`, `S.A.`), abreviaturas de vía, teléfono E.164.
4. **Scoring de match**:
   ```text
   match = 0,35 * similitud_nombre
         + 0,30 * similitud_direccion
         + 0,20 * proximidad_geo
         + 0,10 * coincidencia_teléfono
         + 0,05 * coincidencia_web
   ```
   con `similitud_nombre` tipo Jaro-Winkler o token-set ratio.
5. **Umbrales**: `≥ 0,85` auto-aceptado; `0,65–0,84` aceptado con `match_confidence` media y revisión en la UI; `< 0,65` no se acepta y se marca `identity_unresolved`.
6. **Revisión humana**: la UI permite confirmar, rechazar o corregir un match. Se guarda para no repetir.

Nunca se debe usar un match dudoso para una señal de cierre directa sin marcar la confianza. Un `CLOSED_PERMANENTLY` del hotel equivocado es peor que no tener dato.

### 8.5 Persistencia

- **Piloto**: SQLite embebido (modernc.org/sqlite, sin CGO) para no complicar el despliegue.
- **Producción**: Postgres si hay varios procesos o volumen alto.
- **Interfaz `Store`** para no acoplar el motor a la BD.
- **Migraciones** versionadas y testeadas.
- **Retención**: señales crudas 24 meses; snapshots 5 años; scores 5 años; payloads grandes (HTML) 90 días.

### 8.6 Configuración

Se mantiene el patrón YAML actual y se añaden secciones. El ejemplo completo, con una explicación y las opciones posibles después de cada valor, está en [`config.turislab.example.yaml`](config.turislab.example.yaml); aquí se muestra el extracto que afecta al diseño.

```yaml
database:
  driver: sqlite
  dsn: ./turislab.db

input:
  path: ./base-de-datos.json
  ground_truth_path: ./ground-truth.json   # solo para validación, no para scoring
  format: json
  default_country: ""                     # pendiente de decidir (§16)

scan:
  mode: sequential              # PoC: un establecimiento por tick
  trigger_interval: 1m          # tiempo entre establecimientos
  targets_per_tick: 1           # establecimientos por tick
  jitter: 0.1                   # ±10 % para no ir en cadencia perfecta
  max_concurrent_sources: 2     # fuentes en paralelo por establecimiento
  backoff_on_429: 10m           # pausa tras rate limit
  max_attempts: 3               # reintentos por fuente
  budget_eur_per_scan: 0        # PoC con fuentes gratuitas
  stale_after_days: 7
  max_signals_per_establishment: 40

sources:
  google_places:
    enabled: false              # activar solo si hay GOOGLE_PLACES_API_KEY
    api_key_env: GOOGLE_PLACES_API_KEY
    language: es
    region: ""                  # pendiente de decidir
    details_ttl_days: 7
    include_reviews: true
  google_maps_scraper:
    enabled: true               # fuente principal de la PoC
    provider: gosom             # gosom | kit
    binary: ./bin/google-maps-scraper
    timeout_seconds: 90
    max_places: 3
    depth: 1
    language: es
    cache_ttl_days: 7
    include_reviews: false
    proxy_url_env: GMAPS_PROXY_URL   # opcional; secreto en entorno
    fallback_provider: kit
  website:
    enabled: true
    timeout_seconds: 10
    user_agent: "TurisLabDoctor/0.1 (+https://example.org/bot)"
    max_pages: 3
  phone:
    enabled: false              # PoC: verificación manual
    provider: manual            # manual | vapi | retell | dograh
    call_window: "09:00-18:00"
    max_attempts: 3
    disclose_ai: true
    record_calls: false
  osm:
    enabled: true
    overpass_url: https://overpass-api.de/api/interpreter
  overture:
    enabled: true               # dataset gratuito; descarga/consulta local
    release: "2026-10-01"
  registry:
    enabled: false              # sin acceso en la PoC
    url: ""
    api_key_env: ""
  news:
    enabled: false
    provider: ""
  yelp:
    enabled: false              # sin API contratada
    api_key_env: YELP_API_KEY
  tripadvisor:
    enabled: false              # sin API contratada
    api_key_env: TRIPADVISOR_API_KEY

scoring:
  model_version: "2026-11-mvp"
  prior_closure: 0.05
  min_confidence_for_p0: 0.50
  min_confidence_to_publish: 0.20
  hysteresis_scans: 2
  tiers:
    p0: 80
    p1: 65
    p2: 45
  weights:
    closure: 0.40
    quality: 0.25
    decline: 0.15
    staleness: 0.10
    impact: 0.05
    complaints: 0.05
  seasonality:
    enabled: true
    default_profile: coastal

server:
  addr: ":8090"
  read_timeout: 15s
  write_timeout: 60s

metrics:
  enabled: true

agent:
  enabled: true
  model: gemini-2.5-flash
  max_history_chars: 120000
```

La configuración funcional (scan, fuentes, scoring, tiers, servidor) vive en `config.yaml`. Los **secretos** (API keys, credenciales de proxies) van por variable de entorno o fichero de secretos, nunca en el YAML versionado. Precedencia: `flags > secretos de entorno > config.yaml > defaults`. No se usan variables de entorno para parámetros del scan.

### 8.7 API HTTP

Se conservan las rutas actuales que tengan sentido y se añaden las nuevas:

| Método | Ruta | Descripción |
|---|---|---|
| GET | `/api/health` | Salud del proceso |
| GET | `/api/report` | Informe completo (cola + agregados) |
| GET | `/api/report/export` | Export JSON |
| GET | `/api/summary` | Agregados para cabecera |
| GET | `/api/establishments` | Lista paginada, filtros `tier`, `municipality`, `category`, `q`, `confidence` |
| GET | `/api/establishments/{id}` | Ficha completa |
| GET | `/api/establishments/{id}/evidence` | Evidencias con fuente y fecha |
| GET | `/api/establishments/{id}/history` | Serie temporal de scores y señales |
| GET | `/api/establishments/{id}/reviews` | Resumen de reseñas y aspectos |
| POST | `/api/establishments/{id}/refresh` | Fuerza un scan de ese establecimiento |
| POST | `/api/establishments/{id}/identity` | Confirma/corrige un match |
| GET | `/api/queue` | Cola ordenada por prioridad (con `limit`, `tier`) |
| GET | `/api/route` | Ruta propuesta para un conjunto de establecimientos |
| POST | `/api/inspections` | Registra resultado de visita |
| GET | `/api/inspections` | Historial de inspecciones |
| GET | `/api/changes` | Cambios detectados desde una fecha |
| POST | `/api/changes/{id}/ack` | Reconoce un cambio |
| GET | `/api/sources` | Estado, cuota y coste por fuente |
| GET | `/api/data-quality` | Cobertura, matches dudosos, campos vacíos |
| GET | `/api/pipeline` | Fases, pesos y configuración vigente para la pestaña "Cómo funciona" |
| POST | `/api/scan` | Lanza un scan completo |
| GET | `/api/scan/status` | Estado del scan en curso |
| GET | `/metrics` | Métricas Prometheus |
| GET | `/api/agent/info` | Capacidades del agente |
| GET | `/api/agent/tools` | Catálogo de tools |
| POST | `/api/agent/chat` | Chat del agente |

Convenciones: JSON, errores `{"error": {"code","message","details"}}`, paginación por `cursor`, filtros repetibles, y `ETag`/`If-None-Match` en informes grandes.

**Endpoints de la PoC**: `health`, `report`, `report/export`, `summary`, `establishments`, `establishments/{id}`, `.../evidence`, `.../history`, `.../refresh`, `queue`, `changes`, `sources`, `data-quality`, `pipeline`, `scan/status`, `metrics`, `agent/info`, `agent/tools`, `agent/chat`. El resto (`route`, `inspections`, `identity`, `changes/{id}/ack`) queda diseñado para post-PoC.

### 8.8 Métricas

Sustituir las métricas de dashboards por:

| Métrica | Tipo | Atributos |
|---|---|---|
| `turislab.establishments` | gauge | `tier`, `admin_status` |
| `turislab.closure_risk` | histogram | `category` |
| `turislab.priority` | histogram | `tier` |
| `turislab.scan.duration` | histogram | `source`, `status` |
| `turislab.scan.signals` | counter | `source`, `kind`, `status` |
| `turislab.scan.cost_eur` | counter | `source` |
| `turislab.source.errors` | counter | `source`, `reason` |
| `turislab.identity.unresolved` | gauge | — |
| `turislab.inspections.completed` | counter | `outcome` |
| `turislab.model.precision_at_k` | gauge | `k`, `model_version` |

Cardinalidad controlada: nunca etiquetar por `establishment_id`.

---

## 9. Adaptación del Audit Report (UI)

### 9.1 Concepto

El Audit Report deja de ser "el informe de dashboards rotos" y pasa a ser **la mesa de trabajo del coordinador y del inspector**: una cola priorizada, un mapa y una ficha por establecimiento. El sistema de diseño actual (tema oscuro Aurora, paneles glass, ECharts, tipografía y espaciado) se conserva; cambian la estructura de navegación, los tipos y los textos.

### 9.2 Navegación

```text
┌──────────────────────────────────────────────────────────────────────┐
│  TurisLab Doctor   Cola  Mapa  Cambios  Cómo funciona  Datos  Agente │
└──────────────────────────────────────────────────────────────────────┘
```

| Vista | Contenido | Usuario principal |
|---|---|---|
| **Cola** | Tabla ordenada por prioridad, filtros, resumen | Inspector / coordinador |
| **Mapa** | Establecimientos geolocalizados, color por tier, clusters | Coordinador |
| **Cambios** | Señales nuevas desde la última visita, agrupadas | Analista / coordinador |
| **Cómo funciona** | Funnel visual de fases, qué ocurre en cada una y cómo afecta al score | Todos (transparencia) |
| **Datos** | Cobertura, matches dudosos, fuentes, coste | Analista |
| **Agente** | Chat contextual | Inspector |
| **Detalle** | Ficha del establecimiento (ruta `/establishments/{id}`) | Inspector |

### 9.3 Pantalla de cola

Estructura:

1. **Cabecera**: título, fecha del último scan, botón "Actualizar", selector de municipio/categoría, búsqueda.
2. **Tarjetas de resumen** (4–6): total, P0, P1, cierres confirmados, confianza media, coste del último scan.
3. **Barra de filtros**: tier, categoría, municipio, rango de prioridad, confianza, "solo cambios desde mi última visita", "sin inspección > 2 años".
4. **Tabla**:
   - Prioridad (número + badge de tier)
   - Establecimiento (nombre, categoría, municipio)
   - Motivo principal (texto corto: "Google lo marca cerrado", "rating −0,5 en 6 meses", "web caída")
   - Cierre (barra/porcentaje)
   - Calidad (nivel + delta)
   - Confianza
   - Última inspección
   - Acciones (ver, asignar, llamar, descartar)
5. **Paginación** o scroll virtual.
6. **Panel lateral de detalle rápido** al seleccionar una fila (sin perder la cola).

Orden por defecto: `priority DESC, confidence DESC, last_inspection_at ASC`.

### 9.4 Ficha de establecimiento

Bloques:

- **Cabecera**: nombre, categoría, dirección, estado sugerido, tier, prioridad, confianza, botones (refrescar, abrir en mapa, marcar para revisión). En la PoC no hay asignación ni registro de visita.
- **Resumen del score**: donut o barras de `closure_risk`, `quality_risk`, `quality_delta`, `priority`; debajo, la frase explicativa generada a partir de los componentes.
- **Evidencia**: lista agrupada por familia, con icono de fuente, fecha, valor, peso y confianza. Cada evidencia se puede expandir para ver el payload y el enlace a la fuente.
- **Tendencia**: gráfico ECharts de rating, reseñas/mes, sentimiento y prioridad en el tiempo. Bandas de temporada.
- **Reseñas**: resumen por aspecto (positivo/negativo), últimas reseñas visibles, evolución.
- **Identidad**: IDs externos, confianza del match, botón para corregir.
- **Inspecciones**: historial, última visita, incidencias, notas.
- **Acciones**: descartar con motivo, marcar como falso positivo, solicitar más datos, crear incidencia.

La ficha debe responder en 5 segundos: *¿por qué está aquí y qué hago con él?*

### 9.5 Mapa

- Mapa base (MapLibre GL o Leaflet; evitar dependencia de Google Maps si se puede).
- Puntos coloreados por tier (rojo P0, naranja P1, amarillo P2, gris P3).
- Clusters por municipio/zoom.
- Filtros sincronizados con la cola.
- Al hacer clic, panel lateral con la ficha resumida.
- Capa opcional de calor por `closure_risk`.
- Leyenda y accesibilidad: no depender solo del color; usar forma/tamaño.

### 9.6 Cambios

Vista de "qué ha cambiado desde la última vez":

- Agrupado por día/semana.
- Tipos: `status_changed`, `rating_dropped`, `review_spike`, `website_down`, `phone_unreachable`, `new_sanction`.
- Severidad y establecimientos afectados.
- Acción: reconocer, crear tarea, descartar.

### 9.7 Datos y calidad

- Cobertura por fuente y por campo.
- Matches dudosos pendientes de revisión.
- Errores de fuente y cuotas.
- Coste acumulado y por scan.
- Distribución de scores y calibración (curva de fiabilidad).
- Enlaces a export CSV/JSON.

### 9.8 Adaptación técnica de la UI

- `ui/src/lib/report.ts` → `ui/src/lib/types.ts` con `Establishment`, `Evidence`, `Score`, `Tier`, `QueueResponse`.
- `ui/src/routes/+page.svelte` → `ui/src/routes/queue/+page.svelte` y `+page.svelte` como redirect o dashboard.
- `ui/src/routes/establishments/[id]/+page.svelte` → ficha.
- `ui/src/routes/map/+page.svelte`, `changes/+page.svelte`, `data/+page.svelte`, `how-it-works/+page.svelte`.
- Componentes nuevos: `PriorityBadge`, `ScoreBar`, `EvidenceList`, `EstablishmentTable`, `TrendChart`, `ReviewAspects`, `ConfidenceMeter`, `TierLegend`, `PipelineFunnel`, `PipelineStage`, `PipelineDetail`, `ScoreCompositionPanel`, `SourceChip`.
- Componentes actuales reutilizables: `StatCard`, `EChart`, `AgentView`, `FilterBar`, `EmptyState`, `Skeleton`.
- Estados: `loading`, `empty` (sin establecimientos), `error` (scan fallido), `partial` (algunas fuentes caídas), `stale` (datos antiguos). Mantener el lenguaje visual actual.
- SPA estática embebida: se conserva `adapter-static` + `ssr=false`; el servidor Go sirve `app/ui/build`.
- Rendimiento: virtualizar la tabla si hay > 500 filas; cargar detalle bajo demanda; cachear respuestas.
- Accesibilidad: contraste AA, foco visible, navegación por teclado, `aria-label` en badges, no solo color.
- Responsive: la cola debe ser usable en tablet; la ficha en móvil; el mapa ocupa pantalla completa con panel deslizante.

### 9.9 Sistema de diseño

Se mantiene el brief actual (calma, legibilidad, tema oscuro Aurora) con estos añadidos:

- **Rojo P0**: reservado para riesgo alto; no usar en decoración.
- **Naranja P1**, **amarillo P2**, **gris P3**.
- **Verde** solo para "sin cambios / verificado".
- **Badges de confianza**: alta (lleno), media (medio), baja (contorno).
- **Iconografía de fuente**: Google, web, teléfono, OSM, registro, prensa, OTA.
- **Microcopy**: "Cerrado según Google el 12/10", "Sin reseñas nuevas en 14 meses", "La web no responde desde el 03/11".

### 9.10 Pestaña "Cómo funciona" (funnel de fases)

**Objetivo**: que cualquier persona —inspector, coordinador, responsable legal— entienda en menos de un minuto qué le pasa a un establecimiento desde que entra en el sistema hasta que aparece en la cola, y por qué su score es el que es. No es documentación técnica: es una explicación visual y trazable del pipeline.

#### Estructura visual

Un **funnel horizontal** de 6 fases en escritorio y un **stepper vertical** en móvil/tablet. Cada fase es una tarjeta conectada a la siguiente por una línea con gradiente sutil; el ancho de la tarjeta decrece ligeramente para sugerir el embudo (entran 10, se descartan ruidos, se priorizan 2–3). La última fase desemboca en la cola.

```text
┌────────────┐   ┌────────────┐   ┌────────────┐   ┌────────────┐   ┌────────────┐   ┌────────────┐
│ 1. Ingesta │──▶│ 2. Identidad│──▶│ 3. Señales │──▶│ 4. Evidencia│──▶│ 5. Scoring │──▶│ 6. Cola    │
│ 10 registros│   │ match      │   │ 5 fuentes  │   │ peso+conf. │   │ 3 scores   │   │ tier + motivo│
└────────────┘   └────────────┘   └────────────┘   └────────────┘   └────────────┘   └────────────┘
                                                                                    │
                                                                        feedback humano
                                                                                    ▼
                                                                              calibración
```

Cada tarjeta muestra, sin necesidad de hacer clic:

- **Número y nombre** de la fase.
- **Qué ocurre** en una frase (p. ej. "Normalizamos nombre, dirección y categoría").
- **Qué produce**: un chip con el artefacto (`Establishment`, `Match`, `Signal`, `Evidence`, `Score`, `QueueItem`).
- **Impacto en el score**: un badge cualitativo (`—`, `confianza`, `directo`, `prioridad`) con color semántico.
- **Fuentes implicadas**: iconos pequeños (scraper, Google, OSM, web, teléfono).

Al hacer clic o pulsar `Enter` sobre una fase, se despliega un panel con:

1. **Detalle**: reglas concretas y umbrales (p. ej. "match ≥ 0,85 automático; 0,65–0,85 revisión manual").
2. **Ejemplo real**: un establecimiento de la muestra y el valor que produjo esa fase.
3. **Fórmula o peso**: si aplica, la fórmula exacta y de dónde sale el número.
4. **Enlace**: "Ver en la cola" con el filtro correspondiente (p. ej. `confidence=low`).

#### Contenido de las fases

| # | Fase | Qué ocurre | Artefacto | Impacto en el score |
|---|---|---|---|---|
| 1 | **Ingesta y normalización** | Se carga `base-de-datos.json`, se validan campos, se normalizan nombre/dirección/teléfono y se deduplican registros | `Establishment` | No puntúa; define cobertura y `impact` |
| 2 | **Resolución de identidad** | Se empareja cada establecimiento con `place_id`, OSM y otros IDs; se calcula la confianza del match | `IdentityMatch` | Multiplica la confianza final (`confidence_factor`) |
| 3 | **Recolección de señales** | Se ejecutan las fuentes habilitadas (scraper de Maps, OSM/Overture, web, teléfono manual) con rate limit y caché | `Signal` | Aporta la materia prima; sin señales no hay score |
| 4 | **Evidencia** | Cada señal se convierte en evidencia con valor, fiabilidad, antigüedad y familia; se deduplica y se detectan conflictos | `Evidence` | Pondera por familia y decae con el tiempo |
| 5 | **Scoring** | Se calculan `closure_risk`, `quality_risk` y `quality_delta`; se combinan en `priority` y se asigna tier | `Score` | **Directo**: aquí nace el número |
| 6 | **Cola y revisión** | Se ordena por prioridad, se explica el motivo y se marca para visita o verificación | `QueueItem` | **Directo**: decide el orden; el humano valida |

#### Panel lateral de composición del score

A la derecha del funnel (o debajo en móvil) hay un panel fijo con:

- **Donut de prioridad** y los tres scores.
- **Barras de contribución** por familia: `direct_status`, `activity`, `context`, `reviews`, `official`, `product`.
- **Toggle "Ver fórmula"** que muestra la ecuación de §7.8 con los pesos actuales leídos de `config.yaml`.
- **Nota de transparencia**: "El sistema ordena; no cambia el estado administrativo. La decisión final es humana."

El panel se alimenta de `GET /api/pipeline` (nuevo endpoint de solo lectura) para que los pesos mostrados sean siempre los vigentes y no queden desincronizados de la configuración.

#### Interacciones

- **Hover** sobre una fase: resalta las fuentes y el artefacto que produce; atenúa el resto.
- **Clic**: expande detalle en un `details`/`dialog` accesible, sin recargar.
- **Teclado**: cada tarjeta es un botón; `Enter`/`Space` expande; `Esc` cierra; foco visible.
- **Deep link**: `/how-it-works#scoring` abre la fase 5 expandida.
- **Filtro cruzado**: desde una fase se puede saltar a la cola con el filtro relevante.
- **Animación**: la línea del funnel se "llena" al entrar en pantalla, una sola vez; respeta `prefers-reduced-motion`.

#### Componentes y consistencia visual

- `PipelineFunnel.svelte`: contenedor y layout responsive.
- `PipelineStage.svelte`: tarjeta de fase con número, icono, chips y badge de impacto.
- `PipelineDetail.svelte`: panel expandido con reglas, ejemplo y fórmula.
- `ScoreCompositionPanel.svelte`: donut + barras + fórmula.
- `SourceChip.svelte`: icono + nombre de fuente, reutilizable en la ficha.

Se reutilizan los tokens actuales (fondo Aurora, paneles glass, radios, tipografía y espaciado). Los colores de impacto usan la misma semántica que los tiers: neutro para "no puntúa", azul/cian para "confianza", ámbar para "señal", rojo para "prioridad". No se introduce una paleta nueva.

#### Responsive y accesibilidad

- **Escritorio ≥ 1.280 px**: funnel horizontal de 6 tarjetas + panel lateral.
- **Tablet 768–1.279 px**: funnel en 2 filas de 3 + panel debajo.
- **Móvil < 768 px**: stepper vertical con línea a la izquierda; panel de score colapsado al final.
- Contraste AA, texto alternativo en iconos, `aria-expanded` en tarjetas, navegación completa por teclado y sin dependencia exclusiva del color.
- La explicación debe seguir siendo comprensible sin animaciones y sin JavaScript de gráficos (texto y tablas siempre presentes).

---

## 10. Adaptación del Agent

### 10.1 Concepto

El agente actual responde sobre dashboards con tools como `rank_dashboards` o `get_query_detail`. El nuevo agente debe ser un **asistente de inspección**: explica por qué un establecimiento está en la cola, resume su evidencia y prepara la visita.

En la PoC el agente es **de solo lectura**: no registra inspecciones, no cambia estados y no escribe en ningún sistema. Eso elimina riesgos y permite validar primero la calidad de las explicaciones.

No debe inventar datos: solo puede hablar de lo que devuelven las tools. Si no hay evidencia, debe decirlo y proponer cómo obtenerla.

### 10.2 Tools nuevas

Se mantiene el patrón de `functiontool` con structs de entrada/salida, paginación y límites de tamaño. La columna **PoC** indica si entra en la primera entrega.

| Tool | Entrada | Salida | Uso | PoC |
|---|---|---|---|---|
| `rank_establishments` | `tier`, `municipality`, `category`, `limit`, `offset` | Lista ordenada con prioridad, motivo y confianza | "¿A quién visito primero en X?" | Sí |
| `get_establishment` | `id` | Ficha resumida | "Dame el detalle de EST-123" | Sí |
| `get_score_breakdown` | `id` | Componentes, familias, pesos, contribución | "¿Por qué tiene 82?" | Sí |
| `get_evidence` | `id`, `family`, `limit` | Evidencias con fuente, fecha, valor, confianza | "¿Qué pruebas hay?" | Sí |
| `list_changes` | `since`, `municipality`, `severity` | Cambios detectados | "¿Qué ha cambiado esta semana?" | Sí |
| `get_review_summary` | `id`, `aspects`, `period` | Resumen de reseñas y aspectos | "¿De qué se quejan?" | Sí, si hay reseñas |
| `draft_call_script` | `id`, `objective` | Guion de llamada con datos y preguntas | "Prepárame la llamada" | Sí |
| `draft_visit_brief` | `id` | Briefing de visita: qué comprobar, qué preguntar | "Prepárame la visita" | Sí |
| `search_establishments` | `query`, `filters` | Coincidencias | "Busca el Hotel X" | Sí |
| `get_data_coverage` | `municipality`, `source` | Cobertura y huecos | "¿Qué datos nos faltan?" | Sí |
| `explain_priority` | `id` | Explicación en lenguaje natural | "Explícame la prioridad" | Sí |
| `compare_with_peers` | `id`, `radius_km`, `category` | Mediana del entorno y diferencias | "¿Está peor que la competencia?" | No (post-PoC) |
| `get_contact_history` | `id` | Llamadas, intentos, resultados | "¿Hemos contactado?" | No (post-PoC) |
| `record_inspection` | `id`, `outcome`, `findings`, `notes` | Confirmación | "Registra que está cerrado" (con confirmación) | No (post-PoC) |
| `export_route` | `ids`, `start`, `date` | Ruta optimizada y enlace | "Organiza la ruta del jueves" | No (post-PoC) |

Tools que **no** debe tener en la PoC: cambiar el estado administrativo, sancionar, enviar comunicaciones masivas, borrar datos.

### 10.3 System prompt (borrador)

```text
Eres el asistente de inspección de TurisLab Doctor. Ayudas a inspectores y
coordinadores de calidad turística a decidir qué establecimientos visitar y
por qué.

Reglas:
- Responde en español, de forma breve y operativa.
- Usa SIEMPRE las herramientas para obtener datos. No inventes cifras,
  establecimientos ni evidencias.
- Distingue entre "cerrado confirmado", "riesgo de cierre" y "calidad
  degradada". No los mezcles.
- Cuando des una prioridad, explica los 2–3 factores principales y la
  confianza. Si la confianza es baja, dilo antes de recomendar una visita.
- No afirmes que un establecimiento ha cerrado: di "hay evidencia de" o
  "el sistema estima".
- Para acciones que escriben datos (registrar inspección), pide confirmación
  explícita y muestra un resumen de lo que se va a guardar.
- Si falta información, propone qué fuente consultar o qué dato pedir.
- No reveles datos personales de reseñistas ni información interna que no
  esté en las herramientas.
```

### 10.4 Contexto y sesiones

- El agente recibe `establishment_id` y `tier` como contexto de la vista actual (si el usuario está en una ficha).
- Se mantiene el patrón de sesiones en memoria con TTL; para producción, sesiones persistentes y auditoría.
- El historial se limita igual que hoy (`max_history_chars`) y las tools devuelven páginas pequeñas.
- Se registra cada tool call (nombre, duración, éxito) para métricas y para auditar respuestas.
- El agente puede usar MCP externo (p. ej. el sistema de inspecciones) si el cliente lo configura; no es obligatorio.

### 10.5 Guardrails

- **Anti-alucinación**: tools obligatorias para datos; respuesta con "no consta" si la tool no devuelve nada.
- **Confirmación** antes de escribir, cuando existan tools de escritura (post-PoC: `record_inspection`, `ack_change`). En la PoC el agente es de solo lectura.
- **Permisos**: el agente hereda el rol del usuario; un inspector no puede ver datos de otra comunidad si no le corresponde.
- **PII**: no se muestran emails/teléfonos de reseñistas; los teléfonos del establecimiento solo si el usuario tiene permiso.
- **Prompt injection**: las reseñas y webs son contenido no confiable; se tratan como datos, nunca como instrucciones.
- **Coste**: límite de tokens y de tool calls por sesión.

---

## 11. Flujo operativo del inspector

### 11.1 Flujo de la PoC

```text
1. El equipo abre la cola de los 10 establecimientos.
2. Revisa el top 3 y el motivo que muestra el sistema.
3. Ejecuta `./turislab --config app/config.turislab.yaml --validate` y contrasta la cola con `ground-truth.json` (los cerrados conocidos).
4. Para los casos dudosos, llama o visita y anota el resultado real.
5. Actualiza ground-truth.json (o un formulario mínimo) con lo observado.
6. Revisa falsos positivos y explicaciones.
7. Ajusta pesos, umbrales y familias.
8. Repite el ciclo hasta que el ranking sea estable y comprensible.
```

No hay rutas, ni asignación, ni registro automático de inspecciones en la PoC: el objetivo es validar el ranking, no operar.

### 11.2 Rutas (post-PoC)

- Agrupación por municipio y proximidad geográfica.
- Optimización con OR-Tools (`VRP` con ventanas de tiempo y capacidad).
- Restricciones: horario del inspector, duración máxima, prioridad, accesibilidad.
- Salida: orden de visita, tiempo estimado, distancia, enlace a mapa.
- Reoptimización si se añade un P0.

### 11.3 Llamada telefónica (post-PoC)

Fases:

1. **Prellamada**: comprobar que el número es del establecimiento y que no está en lista de exclusión.
2. **Divulgación**: "Le llamo de [organismo]. Esta llamada es atendida por un asistente de voz con IA. ¿Desea continuar?" (obligatorio AI Act art. 50 desde 02-08-2026).
3. **Objetivo**: confirmar que sigue abierto, horario, si ha cambiado la titularidad, si hay reformas, y ofrecer un canal para incidencias.
4. **Registro**: resultado estructurado (`answered`, `no_answer`, `wrong_number`, `closed`, `reopened`, `info`), transcripción mínima y sin grabación salvo consentimiento.
5. **Postllamada**: si no contesta, reintentar según política (máx. 3 intentos, ventana horaria); si contesta y confirma cierre, generar evidencia.

Reglas: no marketing, no insistencia, horario laboral, identificarse, respetar la oposición, no grabar por defecto, y posibilidad de atención humana. En la PoC la llamada es **manual**: el equipo llama, registra el resultado en `ground-truth.json` y lo usa como validación.

### 11.4 Feedback del inspector

En producción, cada visita produce una etiqueta que alimenta la calibración:

- `open_ok`: abierto y sin cambios.
- `open_degraded`: abierto pero con pérdida de calidad.
- `closed`: cerrado.
- `closed_temporarily`: cierre temporal.
- `reopened`: había sido marcado y ha reabierto.
- `wrong_data`: los datos de la ficha no corresponden.
- `inaccessible`: no se pudo visitar.

En la PoC, el feedback es el `ground-truth.json` actualizado a mano. La UI puede incluir un formulario mínimo para no editar JSON, pero no es un requisito bloqueante. En producción, la UI debe pedir el mínimo esfuerzo: 2–3 toques y, opcionalmente, notas/fotos.

---

## 12. Roadmap por fases

La PoC se organiza en **4 fases cortas (4–6 semanas en total)**. El resto del roadmap queda como post-PoC y no bloquea la validación del algoritmo.

### Fase P0 — Datos y verdad terreno (2–3 días)

Entregables:

- `base-de-datos.json` con los 10 establecimientos (restaurantes y hoteles).
- `ground-truth.json` con el estado esperado, incluidos los cerrados conocidos.
- Decisión de fuentes gratuitas a usar (OSM, Overture, web; Google opcional).
- Revisión rápida de legalidad y privacidad de la PoC.

Criterios de aceptación:

- Los 10 registros cargan y validan.
- Hay al menos 1 restaurante y 1 hotel cerrados conocidos, y 2 controles abiertos por categoría.
- Está claro cómo se actualiza la verdad terreno tras una llamada o visita.

### Fase P1 — Motor y scan secuencial (1–2 semanas)

Entregables:

- `cmd/turislab` + `internal/turislab/{model,config,source,identity,scan,scoring,store,server}`.
- Carga de JSON, SQLite, snapshots.
- Interfaz `Source`; fuentes OSM/Overpass, Overture, web y **scraper de Google Maps** (`gosom` como principal, kit como fallback); Google Places opcional si hay clave.
- Resolución de identidad con revisión manual.
- Scoring de cierre y calidad con familias, confianza y recencia.
- Scheduler secuencial con `scan.trigger_interval: 1m` y cursor persistido.
- API mínima: `/api/queue`, `/api/establishments/{id}`, `/api/scan/status`.
- Export JSON/CSV.

Criterios de aceptación:

- Los 10 establecimientos se escanean en ~10 minutos sin bloqueos.
- Cada score tiene al menos una evidencia trazable o marca `no_data`.
- La cola es reproducible y estable entre ciclos.
- El sistema sobrevive a un reinicio y retoma el cursor.

### Fase P2 — Audit Report y Agent (1–2 semanas)

Entregables:

- Cola con filtros, resumen, detalle lateral y ficha.
- **Pestaña "Cómo funciona"** con funnel de fases, detalle, impacto en el score y panel de composición.
- Evidencia agrupada por familia, con fuente y fecha.
- Gráfico de tendencia (cuando haya ≥ 2 snapshots) y resumen de reseñas si hay.
- Vista de cobertura y datos.
- Agent de solo lectura con las tools de la PoC.
- Responsive para tablet/móvil; sin offline.

Criterios de aceptación:

- Un inspector entiende por qué un establecimiento está en el top 3 en < 30 s.
- El agente responde con datos reales y cita la evidencia.
- No inventa establecimientos ni cifras en pruebas adversariales.
- La UI funciona en tablet y móvil.

### Fase P3 — Validación y refinamiento (1 semana, iterativa)

Entregables:

- Comparación cola vs. `ground-truth.json`.
- Análisis de falsos positivos y falsos negativos.
- Ajuste de pesos, umbrales y familias.
- Informe de la PoC con recomendaciones.

Criterios de aceptación:

- Los cerrados conocidos aparecen en el **top 3**.
- Los controles abiertos no copan los primeros puestos.
- El motivo mostrado es correcto y comprensible.
- Se documenta qué señal aportó más valor y cuál generó más ruido.

### Post-PoC — Fases 4–6

- **Fase 4 — Fuentes de pago y operación**: Yelp/TripAdvisor Terra, registro oficial, noticias, bot telefónico, NLP de reseñas por aspectos, rutas con OR-Tools, registro de inspecciones.
- **Fase 5 — Piloto real**: 200–500 establecimientos, 8 semanas, comparación con el orden habitual, calibración con visitas.
- **Fase 6 — Productización**: multi-destino, roles, SSO, app móvil/offline, integración con el sistema de inspecciones, portal del establecimiento, monitorización de deriva.

---

## 13. Métricas de éxito y diseño del piloto

### 13.1 Métricas de la PoC

Con 10 establecimientos no tiene sentido hablar de `precision@20`; se usan métricas de ranking y de explicación:

| Métrica | Definición | Objetivo PoC |
|---|---|---|
| Cerrados en top 3 | Posición de los cerrados conocidos | Todos en top 3 |
| Detección de cierre | Cerrados con `closure_risk ≥ 0,7` | 100 % |
| Falsos positivos P0/P1 | Abiertos estables en los primeros puestos | ≤ 1 |
| Cobertura | Establecimientos con ≥ 1 señal directa y ≥ 2 indirectas | ≥ 8 de 10 |
| Identidad resuelta | Matches aceptados | 10 de 10 |
| Explicación correcta | Revisión cualitativa del motivo principal | ≥ 8 de 10 |
| Estabilidad | Mismo top 3 en 3 ciclos consecutivos | Sí |
| Tiempo de ciclo | 10 establecimientos con intervalo 1m | ≤ 12 min |

### 13.2 Métricas de producto (post-PoC)

| Métrica | Definición | Objetivo MVP |
|---|---|---|
| Cobertura de datos | % de establecimientos con ≥ 1 señal directa y ≥ 2 indirectas | ≥ 70 % |
| Identidad resuelta | % con match aceptado | ≥ 85 % |
| Cola útil | % de P0/P1 con motivo claro | ≥ 95 % |
| Frescura | % de establecimientos escaneados en los últimos 30 días | ≥ 90 % |
| Coste | € por establecimiento y scan | Según presupuesto |
| Tiempo de scan | Duración de un scan completo | < 30 min para 1.000 |
| Estabilidad | % de establecimientos que cambian de tier sin evidencia nueva | < 5 % |

### 13.3 Métricas de modelo (post-PoC)

| Métrica | Definición | Objetivo |
|---|---|---|
| `precision@20` | Aciertos reales en el top 20 | ≥ 0,60 |
| `recall@20%` | Cierres reales capturados en el top 20 % | ≥ 0,70 |
| Brier score | Calibración de `closure_risk` | < 0,15 |
| MAE calidad | Error de `quality_delta` | < 8 puntos |
| Falsos positivos P0 | P0 que resultan abiertos y sin cambios | < 15 % |
| Confianza-calibración | Aciertos por bucket de confianza | Monótona |

### 13.4 Diseño de la validación de la PoC

- **Ciclo**: ejecutar el scan secuencial durante al menos 3 ciclos completos (30 minutos con `1m`).
- **Comparación**: ordenar la cola y comparar con `ground-truth.json`.
- **Verificación**: llamar o visitar los casos dudosos; actualizar la verdad terreno.
- **Análisis**: revisar qué señal aportó más valor y cuál generó ruido.
- **Ajuste**: modificar pesos/umbrales en YAML y repetir.
- **Cierre**: informe de 1–2 páginas con ranking, aciertos, fallos y siguientes pasos.

### 13.5 Diseño del piloto (post-PoC)

- **Destino**: uno con mezcla de urbano, costa y rural; 200–500 establecimientos.
- **Duración**: 8 semanas.
- **Diseño**: comparar la cola del sistema con el orden habitual del equipo. Asignar aleatoriamente la mitad de las visitas a la cola y la mitad al flujo normal (si es ético y operativamente posible); si no, comparación histórica.
- **Recogida**: cada visita registra resultado, tiempo, coste y utilidad percibida.
- **Revisión semanal**: falsos positivos, fuentes caídas, matches dudosos.
- **Cierre**: informe con precisión, ahorro, cobertura y recomendaciones.

### 13.6 Criterios de no-éxito (para parar o rediseñar)

- La cola no supera al orden habitual en `precision@20`.
- Más del 30 % de P0 son falsos positivos.
- El coste por visita evitada supera el ahorro.
- Los inspectores no confían en el sistema o no lo usan.
- Aparecen problemas legales por llamadas o scraping.

---

## 14. Legalidad, privacidad y ética

### 14.1 Marco aplicable

- **GDPR (UE 2016/679)** y **LOPDGDD (LO 3/2018)**.
- **LSSI-CE (Ley 34/2002)** para comunicaciones electrónicas.
- **Directiva ePrivacy** y normativa de cookies si la UI las usa.
- **AI Act (Reglamento UE 2024/1689)**: obligación de informar de que se interactúa con una IA (art. 50) desde el 2 de agosto de 2026; evaluación de impacto y supervisión humana si el sistema afecta a derechos.
- **Normativa de consumo y turismo** de la comunidad autónoma correspondiente.
- **Términos de servicio** de Google, Yelp, TripAdvisor y OSM.

### 14.2 Datos personales

El sistema trata datos de establecimientos, pero también puede tratar datos personales:

- Nombre de la persona de contacto del establecimiento (B2B, pero persona física).
- Teléfono y email.
- Reseñas con nombre de usuario.
- Transcripciones de llamadas (si se graban).

Medidas:

1. **Minimización**: solo los campos necesarios; no guardar reseñistas identificables.
2. **Base legal**: interés legítimo para la función inspectora (art. 6.1.f) o misión de interés público (6.1.e) si el organismo es administración; consentimiento para grabaciones.
3. **Transparencia**: aviso de privacidad accesible desde la UI y en la primera llamada.
4. **Retención**: definir plazos y borrado automático; no conservar grabaciones sin consentimiento.
5. **Derechos**: canal para ejercer acceso, rectificación, supresión y oposición.
6. **Seguridad**: cifrado en tránsito, control de acceso por rol, registro de auditoría, secretos fuera del repo.
7. **DPIA** si el tratamiento es a gran escala o incluye evaluación de personas.
8. **Encargados de tratamiento**: contratos con proveedores (Google, Yelp, voz, cloud) y transferencias internacionales.

### 14.3 Llamadas

- **B2B**: el interés legítimo puede amparar la llamada de verificación, pero debe haber relación con la función inspectora y no ser marketing.
- **B2C** (si se llamara a un particular): consentimiento previo y consulta a **Lista Robinson**.
- **Identificación**: decir quién llama y para qué.
- **IA**: informar al inicio de que es un asistente de voz con IA y permitir pasar a humano.
- **Horario**: solo horario laboral y ventana configurada.
- **Reintentos**: máximo 3, espaciados, y parar tras negativa.
- **Grabación**: desactivada por defecto; si se activa, consentimiento explícito y aviso.
- **Exclusión**: lista de no llamar y respeto de la oposición.

### 14.4 Scraping

- Revisar los ToS de cada portal antes de implementarlo.
- Preferir APIs oficiales y datos abiertos.
- Respetar `robots.txt`, límites de frecuencia y no eludir medidas técnicas.
- No reutilizar contenido protegido más allá de lo necesario para la evidencia.
- Documentar la base legal y la evaluación de riesgo por fuente.
- Si no hay claridad, **no implementar** y buscar proveedor licenciado.
- **Google Maps (PoC)**: el scraping automatizado está prohibido por los ToS de Google. La PoC lo limita a 10 establecimientos, uso interno, sin redistribución, con rate limit y sin almacenar perfiles de reseñistas. Antes de producción se debe sustituir por Places API o por un proveedor licenciado, o contar con aprobación legal explícita.

### 14.5 Ética y sesgos

- **Falsos positivos**: una visita innecesaria tiene coste, pero un falso negativo puede dejar un establecimiento cerrado o inseguro en la base. El sistema debe ser conservador con los cierres y transparente con la incertidumbre.
- **Sesgo geográfico**: los destinos con menos datos digitales parecerán "mejores" o "peores" por cobertura, no por realidad. Se corrige con confianza y cobertura, no con el score bruto.
- **Sesgo de categoría**: hoteles grandes tienen más reseñas que casas rurales. Se compara dentro de categoría y se usa `impact` con cuidado.
- **Presunción de inocencia**: "riesgo de cierre" no es "cerrado". El lenguaje de la UI y del agente debe reflejarlo.
- **Supervisión humana**: ninguna consecuencia administrativa automática. El inspector confirma.
- **Explicabilidad**: el establecimiento (en un futuro portal) debería poder ver y corregir los datos que le afectan.

---

## 15. Riesgos y mitigaciones

| Riesgo | Probabilidad | Impacto | Mitigación |
|---|---|---|---|
| Matching erróneo de establecimientos | Alta | Alto | Cascada de identidad, umbrales, revisión humana, nunca usar match dudoso para cierre |
| Google cambia precios o cuotas | Media | Alto | Caché, presupuesto, proveedor alternativo, no depender de una sola fuente |
| Pocas reseñas por API | Alta | Medio | Snapshots propios, licencia, no prometer tendencias antes de 90 días |
| Scraping bloqueado o ilegal | Media | Alto | Uso acotado a la PoC, revisión legal, rate limit, proxies opcionales, APIs oficiales en cuanto haya presupuesto |
| Scraper frágil o roto por cambios de Google | Alta | Medio | Fijar versión, fallback al kit, modo `no_data`, monitorización de fallos y plan B con Places/OSM |
| Falsos positivos de cierre | Media | Alto | Reglas conservadoras, conflicto, histéresis, verificación telefónica |
| Datos oficiales desactualizados | Alta | Medio | Fecha de última actualización visible, no tratarlos como verdad absoluta |
| Estacionalidad mal modelada | Media | Medio | Calendario configurable, comparación interanual, revisión |
| Coste por scan descontrolado | Media | Medio | Presupuesto, TTL, scan incremental, métricas de coste |
| Baja adopción por inspectores | Media | Alto | Diseño con inspectores, piloto, feedback, no sustituir su criterio |
| Deriva del modelo | Media | Medio | Monitorización, recalibración trimestral, validación temporal |
| Incidente de privacidad | Baja | Muy alto | DPIA, minimización, cifrado, formación, auditoría |
| Dependencia de un proveedor de voz | Media | Medio | Interfaz abstracta, alternativa open source, fallback manual |
| UI sobrecargada | Media | Medio | Priorizar la cola y el "por qué", no dashboards infinitos |

---

## 16. Decisiones tomadas y preguntas abiertas

### 16.1 Decisiones tomadas (2026-11-05)

| Tema | Decisión |
|---|---|
| Alcance inicial | **PoC con 10 establecimientos** (restaurantes y hoteles), incluidos cerrados conocidos para validar |
| Verdad terreno | `ground-truth.json` con el estado esperado; no se usa para scoring |
| APIs | **Ninguna contratada**. Fuentes gratuitas primero; Google Places solo si se configura clave |
| Offline | **No** es requisito; UI web responsive |
| Integración con inspecciones | **Fuera de alcance** de la PoC |
| Objetivo | **Solo ordenar** por scores; sin cambios automáticos de estado |
| Frecuencia | **1 establecimiento cada `scan.trigger_interval`** (inicialmente `1m`), configurable en `config.yaml` |
| Teléfono | Verificación **manual** en la PoC; bot después |
| Región/norma | **Pendiente**; el diseño debe ser configurable y no asumir una norma concreta |

### 16.2 Preguntas abiertas

1. **País/región y norma de calidad aplicable** (SICTED, SCTE Sectores, Hotelstars, Q, ISO 18513, normativa autonómica). Pendiente; afecta categorías, certificaciones y umbrales.
2. **Campos reales de `base-de-datos.json`**: aunque sean 10 registros, falta ver qué campos traen (geo, web, teléfono, IDs externos, última inspección).
3. **Criterio de éxito de la PoC**: se propone "cerrados en top 3 + explicación correcta"; confirmar si es suficiente.
4. **Presupuesto post-PoC**: si se contrata Google Places/Yelp/Terra/voz, ¿cuál es el límite mensual?
5. **Infraestructura**: ¿dónde se despliega la PoC (local, VM, cloud)? ¿Restricciones de red o proveedor?
6. **Datos oficiales**: ¿se podrá acceder al registro turístico y a inspecciones previas más adelante? ¿Con qué base legal?
7. **Idiomas**: ¿solo español o también cooficiales/inglés?
8. **Uso de IA generativa**: ¿se permite enviar reseñas a un LLM externo o se prefiere modelo local?
9. **Responsable de la verdad terreno**: quién actualiza `ground-truth.json` tras cada llamada/visita.
10. **Retención y privacidad**: quién valida el aviso de privacidad y la política de borrado.
11. **Intervalo definitivo**: confirmar `1m` o subirlo si las fuentes gratuitas empiezan a devolver `429`.
12. **Criterio de parada**: qué resultado haría descartar el enfoque o cambiar de fuentes.

---

## 17. Anexos

### 17.1 Ejemplo de informe (resumido)

```json
{
  "generated_at": "2026-11-05T03:12:00Z",
  "model_version": "2026-11-mvp",
  "summary": {
    "total": 10,
    "p0": 2,
    "p1": 2,
    "p2": 3,
    "p3": 3,
    "closed_confirmed": 2,
    "low_confidence": 1,
    "coverage": 0.80,
    "scan_cost_eur": 0.00
  },
  "establishments": [
    {
      "id": "EST-000123",
      "name": "Hotel Example",
      "category": "hotel",
      "municipality": "Example",
      "tier": "P0",
      "priority": 87.4,
      "closure_risk": 0.91,
      "quality_risk": 0.42,
      "quality_delta": -12.0,
      "confidence": 0.83,
      "status_suggested": "closed_suspected",
      "main_reason": "Google lo marca como cerrado permanentemente y no hay reseñas nuevas en 14 meses",
      "last_inspection_at": "2023-04-18",
      "evidence": [
        {
          "source": "google_places",
          "kind": "business_status",
          "value": "CLOSED_PERMANENTLY",
          "observed_at": "2026-11-04T02:10:00Z",
          "family": "direct_status",
          "weight": 0.95,
          "confidence": 0.9
        },
        {
          "source": "google_places",
          "kind": "last_review_age_days",
          "value": 428,
          "observed_at": "2026-11-04T02:10:00Z",
          "family": "activity",
          "weight": 0.35,
          "confidence": 0.7
        }
      ]
    }
  ]
}
```

### 17.2 Configuración de la PoC (`config.yaml`)

La configuración funcional vive en `config.yaml`; no se usan variables de entorno para el scan. El fichero completo, con una explicación y las opciones posibles **después de cada valor**, está en [`config.turislab.example.yaml`](config.turislab.example.yaml). Las etiquetas `[PoC]` y `[Prod]` indican qué cambiar al pasar a producto (§18).

Extracto de los valores que más afectan a la PoC:

```yaml
database:
  driver: sqlite                  # [PoC] sqlite. Opciones: sqlite | postgres. [Prod] postgres.
  dsn: ./turislab.db              # [PoC] ruta local. [Prod] cadena de conexión Postgres.

input:
  path: ./base-de-datos.json      # [PoC] 10 establecimientos. [Prod] export oficial.
  format: json                    # Opciones: json | csv | ndjson.
  ground_truth_path: ./ground-truth.json  # [PoC] solo validación. [Prod] "" (no se usa).
  default_country: ""             # Opciones: "" o ISO-3166 alpha-2 (ES, FR...). Pendiente (§16).

scan:
  mode: sequential                # [PoC] sequential. Opciones: sequential | parallel. [Prod] parallel.
  trigger_interval: 1m            # [PoC] 1m. Opciones: 30s, 1m, 5m, 1h. [Prod] 5-15m o cron diario.
  targets_per_tick: 1             # [PoC] 1. Opciones: 1-100. [Prod] 10-50.
  jitter: 0.1                     # Opciones: 0.0-0.5. [Prod] 0.1-0.2.
  max_concurrent_sources: 2       # Opciones: 1-10. [Prod] 4-8.
  backoff_on_429: 10m             # Opciones: 0s o 1m-1h. [Prod] 10m-30m.
  max_attempts: 3                 # Opciones: 1-5.
  budget_eur_per_scan: 0          # [PoC] 0 = fuentes gratuitas. [Prod] 25-100.

sources:
  google_maps_scraper:
    enabled: true                 # [PoC] true. [Prod] false: sustituir por Places (§18.3).
    provider: gosom               # Opciones: gosom | kit. gosom da status; kit da rating/reviews.
    timeout_seconds: 90           # Opciones: 30-180.
    max_places: 3                 # Opciones: 1-10.
    cache_ttl_days: 7             # Opciones: 1-30.
    include_reviews: false        # Opciones: true | false. true = más datos y más riesgo legal.
    proxy_url_env: GMAPS_PROXY_URL  # Nombre de la env var con el proxy; "" = sin proxy.
  google_places:
    enabled: false                # [PoC] false. [Prod] true con GOOGLE_PLACES_API_KEY.
    api_key_env: GOOGLE_PLACES_API_KEY  # Nombre de la env var, nunca la clave literal.
  osm:
    enabled: true                 # Opciones: true | false. [Prod] true como contraste.
  overture:
    enabled: true                 # Opciones: true | false.
  website:
    enabled: true                 # Opciones: true | false.
  phone:
    enabled: false                # [PoC] verificación manual. [Prod] true si hay bot y base legal.

server:
  addr: ":8090"                   # Opciones: :8080, :8090, 127.0.0.1:8090. [Prod] detrás de TLS/proxy.

legal:
  scraping_ack: true              # [PoC] acepta el riesgo de ToS. [Prod] false: usar APIs oficiales.
```

**Secretos: solo por entorno** (nunca en el YAML versionado):

```bash
GOOGLE_PLACES_API_KEY=      # opcional en la PoC; obligatorio si google_places.enabled=true
YELP_API_KEY=               # post-PoC
TRIPADVISOR_API_KEY=        # post-PoC
GMAPS_PROXY_URL=            # opcional para el scraper
GEMINI_API_KEY=             # opcional para el agente
```

Precedencia: `flags > secretos de entorno > config.yaml > defaults`. El binario falla al arrancar si una fuente habilitada declara una clave que no existe. Los parámetros del scan **no** se leen de `.env`.

### 17.3 Pesos iniciales (resumen)

| Componente | Peso en prioridad |
|---|---|
| `closure_risk` | 0,40 |
| `quality_risk` | 0,25 |
| `quality_delta` normalizado | 0,15 |
| `staleness` | 0,10 |
| `impact` | 0,05 |
| `complaints` | 0,05 |

| Familia de cierre | Peso |
|---|---|
| `direct_status` | 0,45 |
| `activity` | 0,25 |
| `context` | 0,15 |
| `official` | 0,15 |

| Familia de calidad | Peso |
|---|---|
| `reviews` | 0,35 |
| `official` | 0,35 |
| `product` | 0,15 |
| `news` | 0,10 |
| `freshness` | 0,05 |

Estos pesos son el punto de partida y deben revisarse con expertos antes del piloto.

### 17.4 Checklist de la PoC

- [ ] `base-de-datos.json` con 10 establecimientos validado.
- [ ] `ground-truth.json` con cerrados conocidos y controles.
- [ ] Identidad resuelta para los 10.
- [ ] Fuentes gratuitas configuradas; scraper de Google Maps operativo y Google Places opcional con clave.
- [ ] Scoring reproducible y con tests.
- [ ] Scheduler secuencial con `scan.trigger_interval: 1m` y cursor persistido.
- [ ] Cola y ficha funcionando en tablet.
- [ ] Pestaña "Cómo funciona" con funnel y panel de composición revisada por un inspector.
- [ ] Agente de solo lectura con tools y guardrails probados.
- [ ] Vista de cobertura y datos.
- [ ] Aviso de privacidad y política de retención mínima.
- [ ] Informe de validación con ranking y explicaciones.
- [ ] Documentación de operación y runbook.

Checklist post-PoC: registro de inspecciones, rutas, bot con divulgación de IA, DPIA completa, panel de calibración, piloto con 200–500 establecimientos.

### 17.5 Referencias

**APIs y datos**

- Google Places API (New) — Place Details: https://developers.google.com/maps/documentation/places/web-service/place-details
- Google Business Profile APIs: https://developers.google.com/my-business
- Yelp Fusion API: https://docs.developer.yelp.com/
- TripAdvisor Terra: https://www.tripadvisor.com/developers
- Overture Maps — Places: https://docs.overturemaps.org/schema/reference/places/place/
- OpenStreetMap — Overpass API: https://wiki.openstreetmap.org/wiki/Overpass_API
- SafeGraph Places: https://www.safegraph.com/guides/places-data-technical-guide

**Open source**

- google-maps-scraper: https://github.com/gosom/google-maps-scraper
- google-maps-scraper-kit: https://github.com/Mahanaicoach/google-maps-scraper-kit
- AllThePlaces: https://github.com/alltheplaces/alltheplaces
- changedetection.io: https://github.com/dgtlmoon/changedetection.io
- ODK Collect: https://github.com/getodk/collect
- ODK Central: https://github.com/getodk/central
- KoboToolbox: https://github.com/kobotoolbox/kpi
- StreetComplete: https://github.com/streetcomplete/StreetComplete
- MapComplete: https://github.com/pietervdvn/MapComplete
- OR-Tools: https://github.com/google/or-tools
- Pipecat: https://github.com/pipecat-ai/pipecat
- LiveKit Agents: https://github.com/livekit/agents
- Dograh: https://github.com/dograh-hq/dograh
- Patter: https://github.com/PatterAI/Patter

**Estándares y calidad**

- SICTED / SCTE: https://turismo.gob.es/desarrollo-sostenibilidad/Paginas/evolucion-SCTE.aspx
- Hotelstars Union: https://www.hotelstars.eu/
- ISO 18513 (turismo): https://www.iso.org/standard/76856.html
- Q de Calidad Turística: https://www.calidadturistica.es/

**Legal**

- GDPR: https://eur-lex.europa.eu/eli/reg/2016/679/oj
- LOPDGDD: https://www.boe.es/buscar/act.php?id=BOE-A-2018-16673
- LSSI: https://www.boe.es/buscar/act.php?id=BOE-A-2002-13758
- AI Act: https://eur-lex.europa.eu/eli/reg/2024/1689/oj
- AEPD: https://www.aepd.es/
- Lista Robinson: https://www.listarobinson.es/

---

## 18. De la PoC al producto: qué hay que cambiar

La PoC está optimizada para **validar el algoritmo con 10 establecimientos y coste cero**. Un producto real optimiza otras cosas: legalidad, fiabilidad, escala, seguridad, operación y coste controlado. Esta sección enumera lo que **no vale tal cual** y lo que hay que sustituir o añadir.

### 18.1 Principio

No es "subir el mismo binario y meter más establecimientos". Hay tres cambios estructurales:

1. **De scraping a fuentes con contrato**: Google Maps scraping → Google Places API o proveedor licenciado.
2. **De un proceso local a un servicio operado**: SQLite → Postgres, auth, backups, observabilidad, CI/CD y runbooks.
3. **De validar a operar**: ground truth manual → etiquetas de inspección, calibración, auditoría y integración con el sistema de inspecciones.

### 18.2 Cambios por área

| Área | En la PoC | En producto | Por qué | Esfuerzo |
|---|---|---|---|---|
| **Google Maps** | Scraper `gosom`/kit sin API key | Google Places API (New) con facturación, o proveedor licenciado | Legalidad, SLA, estabilidad y datos contractualmente usables | Medio |
| **Otras fuentes** | OSM, Overture, web, teléfono manual | Yelp/TripAdvisor Terra, registro oficial, prensa licenciada, proveedor de voz | Cobertura y señales de calidad contrastadas | Alto |
| **Legal** | Revisión informal y uso interno | DPIA, base legal por fuente, ToS, contratos de encargado, retención, AI Act | Sin esto no se puede operar con datos de terceros | Alto |
| **Escala** | 10 establecimientos, 1/min | Miles, worker pool, colas, sharding, rate limit distribuido | El scan secuencial no escala | Alto |
| **Persistencia** | SQLite local | Postgres con migraciones, backups, réplicas y retención | Concurrencia, durabilidad y recuperación | Medio |
| **Identidad** | Umbrales y revisión manual | Cola de matches, feedback, deduplicación a escala | Los falsos positivos crecen con el volumen | Medio |
| **Scoring** | Pesos ajustados a mano con 10 casos | Calibración con inspecciones reales, validación temporal, modelo interpretable | Evitar sobreajuste y medir deriva | Alto |
| **Verdad terreno** | `ground-truth.json` editado a mano | Etiquetas de inspección y integración con el sistema existente | La validación debe ser continua, no un fichero | Medio |
| **UI** | 10 filas, sin auth | Authn/authz, roles, SSO, auditoría, i18n, mapa, rendimiento | Uso multiusuario y administración pública | Alto |
| **Agente** | Solo lectura, sin persistencia | Tools de escritura con confirmación, permisos, sesiones, límites de coste, pruebas adversariales | Riesgo de acciones incorrectas o fuga de datos | Medio |
| **Operación** | Binario local | Contenedores, CI/CD, IaC, SLOs, alertas, on-call, runbooks | Hay que operarlo 24/7 | Alto |
| **Seguridad** | Secretos en entorno | Gestor de secretos, TLS, WAF, backups cifrados, pentest | Datos y reputación en juego | Medio |
| **Coste** | 0 € | Presupuesto por fuente, cuotas, alertas y facturación | Google Places y voz se pagan por uso | Medio |
| **Datos** | 10 registros | Calidad, duplicados, PII, retención y borrado | GDPR y credibilidad del dato | Medio |
| **Producto** | Solo ordenar | Rutas, asignación, registro de inspecciones, portal del establecimiento, multi-destino | La PoC no opera el flujo completo | Alto |

### 18.3 Migración concreta de Google Maps

**Fase 1 — PoC (actual)**

- `sources.google_maps_scraper.enabled: true`.
- `provider: gosom`, 10 establecimientos, uso interno, sin redistribución.
- `legal.scraping_ack: true` y revisión legal pendiente.

**Fase 2 — Preproducción**

1. Solicitar **Google Places API (New)** con cuenta de facturación y cuota.
2. Implementar la fuente `google_places` con `field_mask` mínimo para reducir coste.
3. Comparar en los mismos 10 establecimientos: `status` del scraper vs. `businessStatus` de Places; medir acuerdos y discrepancias.
4. Medir coste real por establecimiento y por scan.
5. Actualizar la DPIA y los ToS.

**Fase 3 — Producción**

- Desactivar el scraper y activar Places:

```yaml
sources:
  google_maps_scraper:
    enabled: false                # Producción: no scraping de Google Maps
  google_places:
    enabled: true
    api_key_env: GOOGLE_PLACES_API_KEY
    max_requests_per_minute: 600
```

- Mantener el scraper solo como herramienta interna de diagnóstico, nunca como fuente de producción, salvo aprobación legal explícita.
- Si el volumen o el coste no encajan, negociar con un proveedor licenciado (SafeGraph, Outscraper con contrato, etc.) en lugar de volver al scraping.

**Fase 4 — Alternativa si no hay presupuesto**

- Quedarse en OSM/Overture + web + teléfono manual.
- Aceptar menor cobertura y reflejarla en `confidence` y `coverage`.
- No prometer detección de cierre con la misma calidad que Places.

### 18.4 Cambios de configuración típicos al pasar a producción

| Clave | PoC | Producción |
|---|---|---|
| `database.driver` | `sqlite` | `postgres` |
| `database.auto_migrate` | `true` | `false` (migraciones versionadas) |
| `input.ground_truth_path` | `./ground-truth.json` | `""` (no se usa para puntuar) |
| `scan.mode` | `sequential` | `parallel` con límites por fuente |
| `scan.trigger_interval` | `1m` | `5m`–`15m` o cron diario |
| `scan.targets_per_tick` | `1` | `10`–`50` |
| `scan.budget_eur_per_scan` | `0` | `25`–`100` con alertas |
| `sources.google_maps_scraper.enabled` | `true` | `false` |
| `sources.google_places.enabled` | `false` | `true` |
| `sources.phone.enabled` | `false` | `true` si hay bot y base legal |
| `agent.enabled` | `false`/opcional | `true` |
| `agent.read_only` | `true` | `true` salvo tools de escritura explícitas |
| `metrics.enabled` | `true` | `true` |
| `logging.format` | `text` | `json` |
| `legal.scraping_ack` | `true` | `false` |

### 18.5 Checklist go/no-go antes de producción

- [ ] Revisión legal firmada: ToS, base legal, DPIA, retención y AI Act.
- [ ] Google Places contratado, con cuota y presupuesto; scraper desactivado.
- [ ] Postgres con backups, réplicas y plan de recuperación.
- [ ] Autenticación, roles, permisos y auditoría de accesos.
- [ ] Prueba de carga con el volumen objetivo y rate limits por fuente.
- [ ] Calibración con inspecciones reales y validación temporal.
- [ ] Monitorización, alertas, SLOs y runbook de incidencias.
- [ ] Política de retención y borrado ejecutada y verificada.
- [ ] Límite de coste por scan y alertas de desvío.
- [ ] Revisión de seguridad y prueba de penetración.
- [ ] Auditoría de accesibilidad AA y pruebas en móvil/tablet.
- [ ] Pruebas adversariales del agente (no inventar, no filtrar PII).
- [ ] Integración con el sistema de inspecciones definida (o exportación CSV).
- [ ] Plan de comunicación a inspectores y formación.

### 18.6 Qué no cambia

- El **modelo de evidencia** (`Signal` → `Evidence` → `Score`) y la interfaz `Source`.
- Los **tres scores** separados (`closure_risk`, `quality_risk`, `quality_delta`) y la prioridad explicable.
- La **supervisión humana**: el sistema ordena, no decide ni cambia estados administrativos.
- La **configuración funcional en `config.yaml`** y los secretos por entorno.
- La **pestaña "Cómo funciona"** como contrato de transparencia con el usuario.

### 18.7 Fichero de configuración comentado

El ejemplo completo, con una explicación y las opciones posibles después de cada valor, está en [`config.turislab.example.yaml`](config.turislab.example.yaml). Las etiquetas `[PoC]` y `[Prod]` marcan qué valor habría que cambiar al pasar a producto.

---

*Fin del documento. Versión 0.4 — pendiente de validación con el equipo y con los 10 establecimientos reales.*
