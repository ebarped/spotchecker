# turislab-doctor

<div align="center">
  <img src="assets/logo.png" width="130" alt="turislab-doctor logo">
</div>

Detects closed or deteriorated tourist establishments from open data and
ranks the inspector visit queue: every establishment is scanned periodically,
its closure and quality signals are scored, and the report orders who to
visit first.

Nothing is invented: every score is backed by stored evidence (the state of
the website, the OSM/Overpass tags, the Google Maps listing status…) with the
source and timestamp of each signal, so an inspector can audit the machine's
verdict before driving anywhere.

## How it works

1. **Input** — drop your `.csv` files into `app/datasets/`; on startup the
   platform lists every CSV there and loads all its records, normalized and
   upserted by id. See [Datasets](#datasets).
2. **Scan** — a scheduler (sequential or parallel, with rate-limit backoffs)
   sends each establishment through the enabled sources:
   `website`, `osm`, `google_maps_scraper` (the gosom scraper, no API key),
   plus optional `google_places`, `overture`, `phone`, `registry`, `news`,
   `yelp` and `tripadvisor`.
3. **Signals** — each source contributes dated, weighted signals
   (`website down`, `osm disused:tourism`, `maps status permanently closed`,
   `last Google review 2 years ago`…), stored with full provenance. When the
   registry carries no website, the one Google Maps knows is handed to the
   website collector, so the site is checked anyway.
4. **Scoring** — closure risk averages the signals that say something about
   the business being open: the direct status (Google Maps, OSM) and the
   activity signals (website status, age of the newest review, OSM tags),
   with a small prior when there is no direct status. Reviews and quality
   signals do not move the closure risk. Priority adds staleness and is
   adjusted by signal confidence and hysteresis.
5. **Queue** — establishments are tiered and the report ranks the visit queue.
6. **Judge (optional)** — an LLM reviews the same dossier, issues the final
   status with a rationale and its own closure risk over every signal. The
   detail screen shows both numbers side by side: the engine's risk and the
   judge's.

`Signal.Value` has one meaning across every family: `0` looks alive, `1`
looks closed. When no signal measured the operational state the score says
`risk_known: false` and the UI shows `—` instead of a misleading `0%`. The
judge's risk carries the same guard (`judge_risk_known`).

## Tiers

| Tier           | Meaning                                                          |
|----------------|------------------------------------------------------------------|
| `P0`           | closure confirmed or the strongest evidence stack — visit first  |
| `P1` / `P2`    | suspected closure / declining quality — schedule a visit         |
| `P3`           | everything looks active — low priority                           |
| `collect_data` | too little evidence to judge — needs more scanning, not a visit  |

The suggested status of each establishment is `active`,
`closure_suspected`, `closure_confirmed`, `closed_temporarily`, `seasonal`
or `unknown` (nothing measured whether it is still open — not the same as
"looks fine").

## Usage

```bash
make build                                  # compile the binary (UI included)

./turislab --config app/config.turislab.yaml --once              # scan everything once, exit
./turislab --config app/config.turislab.yaml --input export.json --once     # optional: one JSON/NDJSON file instead of datasets/
./turislab --config app/config.turislab.yaml --establishment <id> # scan one establishment
./turislab --config app/config.turislab.yaml --validate           # compare with input.ground_truth_path (if set)
./turislab --config app/config.turislab.yaml --addr :8090         # scanner + API + UI
```

Flags:

| Flag               | Meaning                                                              |
|--------------------|----------------------------------------------------------------------|
| `--config`         | configuration file (default `config.turislab.yaml`)                  |
| `--input`          | single JSON/NDJSON file (overrides `input.path`, disables `datasets_dir`) |
| `--addr`           | listen address of the HTTP server (overrides `server.addr`)          |
| `--once`           | scan every establishment once and exit                               |
| `--establishment`  | scan one establishment by id and exit                                |
| `--validate`       | compare scores with the file in `input.ground_truth_path` and exit   |
| `--validate-top`   | top-N cut-off used by `--validate` (default 3)                       |
| `--validate-strict`| fail `--validate` when an active establishment is closure-confirmed  |
| `--static-dir`     | serve the web UI from this directory (empty = embedded UI)           |

`make` targets: `build`, `run` (scanner + API + UI on :8090), `scan-once`,
`validate`, `serve` (clean rebuild + serve), `turislab-scraper` (fetch the
gosom Google Maps scraper into `bin/`), plus `test`, `vet`, `fmt`, `lint`,
`tidy`, `clean` and the `ui-*` targets for the web UI.

### Datasets

**Put your CSV files in `app/datasets/` and start the platform.** That is the
only input it needs. On every startup it lists all `*.csv` files in that
directory (in filename order), loads one row per establishment and upserts it
by `id`. The log shows the directory and a per-file count:

```
INFO seed  datasets dir: app/datasets (1 csv files)
INFO seed    lugo.csv: 10/10 registros (0 omitidos)
INFO seed  loaded 10/10 establishments (0 skipped)
```

`input.datasets_dir` in the config is the directory to use: `./datasets`
relative to the config file, which with `app/config.turislab.yaml` means
`app/datasets/`. `input.path` / `--input <file>` are a separate, optional
single-file mode (JSON or NDJSON) for one-off imports.

#### CSV format

- UTF-8, one header row, one row per establishment.
- Delimiter `,` or `;` (a UTF-8 BOM is fine).
- Any preamble before the header row is skipped, so official exports can be
  dropped in as they arrive.
- The `name` column is the only mandatory one; if `id` is missing or empty,
  the platform generates a stable `gen-…` id from name + address + municipality.
- Quote fields containing the delimiter or line breaks (`"Calle Falsa, 1"`).
- Empty cells are allowed; `input.default_country` fills in `country` when empty.

Canonical column names (Spanish aliases such as `nombre`, `denominación`,
`concello`, `latitud`, `lonxitude`, `tipoloxía`, `teléfono`, `código_postal`
also work; extra columns are ignored):

| Column | Type | Notes |
|--------|------|-------|
| `id` | text | Stable identifier; generated if absent |
| `name` | text | **Required**; rows with an empty name are skipped |
| `category` | text | `restaurant`, `hotel`, `bar` or `other`; anything else becomes `other` |
| `stars` | integer | `4`, or `4 ESTRELAS` from a rating column |
| `address`, `municipality`, `province`, `postal_code`, `country` | text | `country`: ISO 3166-1 alpha-2 |
| `lat`, `lng` | decimal | e.g. `43.0112`, `-7.5572` (also `43,0112` with `;` delimiter) |
| `phone`, `email`, `website` | text | |
| `registry_id` | text | Registry / inspection identifier |
| `certifications` | list | `\|`-separated (`A\|B`), or `,` in `;`-delimited files |
| `last_inspection_at` | date | `2024-05-14T00:00:00Z`, `2024-05-14` or `14/05/2024` |
| `last_inspection_result` | text | e.g. `Correcto` |
| `google_place_id`, `tripadvisor_id`, `yelp_id`, `osm_id` | text | External ids |
| `notes` | text | Never ground truth: the judge reads it |
| `id_generated` | boolean | `true` / `false` |

Minimal example (`app/datasets/ejemplo.csv`):

```csv
id,name,category,address,municipality,lat,lng,phone,website
rest-001,Restaurante El Ancla,restaurant,"Calle Falsa, 1",Alicante,38.34,-0.49,+34965000000,https://example.es
,Bar Sin Identificador,bar,"Rúa Nova, 2",Lugo,43.0112,-7.5572,,
```

The second row has no `id`, so one is generated. `app/datasets/lugo.csv` is a
complete example with all the columns. `input.strict_validation: true` makes
the startup fail on an invalid row; with `false`, invalid rows are skipped and
listed in the log.

#### Official source: REAT

The bundled dataset is built from the
[REAT registry](https://aei.turismo.gal/es/rexistro-de-empresas-e-actividades-turisticas)
of Turismo de Galicia (reference date 2026-11-01):

```bash
make datasets    # regenerate app/datasets/lugo.csv from the official CSVs
```

`app/cmd/reat2csv` downloads the restaurant and accommodation directories,
filters by municipality and area and writes the canonical CSV. Accommodation
rows are selected by distance from an anchor (VUTs are excluded: none of them
carry coordinates); restaurants have no coordinates at all, so they are
selected by street. `make datasets REAT_FLAGS="..."` overrides the defaults.

See [`AGENTS.md`](AGENTS.md) for the full dataset contract, the mapping rules
and the checklist to follow when adding a new one.

Because loading is an upsert, removing a CSV does not remove its rows from an
existing database. Run `make clean` to drop `turislab.db` when swapping
datasets.

### API

The binary runs a small [chi](https://github.com/go-chi/chi) server:

- `GET /` — the web UI (the built SvelteKit bundle, embedded in the binary)
- `GET /api/report` — the full report as JSON
- `GET /api/report/export` — the same JSON as a download attachment
- `GET /api/summary` — rollup counts by tier and status
- `GET /api/queue` — the ordered visit queue (`tier`, `category`,
  `municipality`, `q`, `limit`, `offset`; `q` matches the name or the id)
- `GET /api/establishments` — the establishment list
- `GET /api/establishments/{id}` — one queue entry in full: registry fields,
  score breakdown (judge verdict included) and its primary evidence. 404 when
  the id is not in the current report, 503 before the first report
- `POST /api/establishments/{id}/refresh` — scan one establishment in the
  background
- `GET /api/scan/status` — whether a scan is running
- `GET /api/pipeline` — the pipeline configuration as seen by the server
- `GET /api/health` — liveness
- `GET /metrics` — Prometheus exposition, when `metrics.enabled` is set

### Agent

With `agent.enabled: true`, the server exposes an inspection assistant: a
chat agent that investigates the report through local tools
(`get_report_summary`, `list_establishments`, `get_establishment_detail`) and
external MCP servers configured by URL under `agent.mcp`. It is read-only by
default. The LLM is Gemini or any OpenAI-compatible endpoint — the API key
always comes from an environment variable (`agent.model.api_key_env`).

- `GET /api/agent/info` — `{enabled, model, suggestions}` (404 when disabled)
- `GET /api/agent/tools` — the tool catalog
- `POST /api/agent/chat` — `{session_id?, message}` answered as a
  `text/event-stream` of JSON events

#### Browser MCP: consultar webs y capturar pantallas

`make mcp-browser` levanta [Playwright MCP](https://github.com/microsoft/playwright-mcp)
en `http://127.0.0.1:8931/mcp` con el Chromium instalado en `/usr/bin/chromium`.
Configúralo en `agent.mcp` (ver el YAML de ejemplo) y el chat gana
`browser_navigate` + `browser_snapshot` para leer webs y
`browser_take_screenshot` para guardar fotos. El prompt del agente le indica
que guarde cada captura como `<artifacts_dir>/<establishment_id>.png`;
`artifacts_dir` debe apuntar a la misma carpeta en la que escribe el sidecar
(`app/artifacts` por defecto, ignorada por git).

`agent.mcp[].tools` es un allowlist opcional de herramientas visibles para el
modelo (y en `/api/agent/tools`): vacío expone todas. La config local deja solo
`browser_navigate`, `browser_navigate_back`, `browser_snapshot`,
`browser_wait_for` y `browser_take_screenshot`, para que el agente no cargue
esquemas de clics, JavaScript, cookies, grabaciones ni tests. La captura
automática usa su propio cliente MCP y no depende de esta lista.

Cuando el modelo completa `browser_take_screenshot`, el stream emite un evento
`{"type":"image","image_url":"..."}` y el chat pega la foto **en línea**, justo
debajo de la respuesta (clicable para abrirla a tamaño completo). El evento solo
se envía si el PNG existe, así que una captura fallida no deja una imagen rota.

La imagen se sirve en `GET /api/establishments/{id}/screenshot` y aparece en la
sección «Website» de `/detalle?id=<id>`, junto a la URL clickable y la fecha del
fichero. Un `HEAD` a esa ruta comprueba si existe; no hay signals ni cambios en
la BD.

Con `screenshots.enabled: true` (default), **cada sync** —el botón de
re-escaneo y el ciclo programado— captura la web conocida (o la descubierta en
Google Maps) y sobrescribe `<artifacts_dir>/<id>.png`. `screenshots.min_interval`
permite espaciar las recapturas (`0s` = siempre). `make run` y `make serve`
arrancan el sidecar (y esperan a que responda) si no está ya escuchando, y lo
apagan al salir. Los targets aceptan
`MCP_BROWSER_PORT`, `ARTIFACTS_DIR` y `CHROME` como variables de entorno.

### Web UI

`ui/` is a SvelteKit app (Svelte 5 + Tailwind CSS) that renders the scan
report: the visit queue, per-establishment evidence and the pipeline view.
Every row of the queue links its id (leftmost column) to the detail screen,
`/detalle?id=<id>`, which shows the whole registry record of the
establishment (sinatura, address, coordinates, contacts, certifications,
external ids…) next to its score breakdown, the two closure risks (engine
and LLM judge) with the judge verdict and the evidence table. The id travels in the query string so the route stays static
and needs no SPA fallback. The dev server proxies `/api` to the Go server
(`DOCTOR_URL`, default `http://localhost:8090`):

```bash
make serve      # terminal 1: build everything and serve on :8090
make ui-dev     # terminal 2: UI dev server on :5173
```

The build writes into `app/ui/build`, the Go embed folder, so the binary is
self-contained.

## Configuration

See [`config.turislab.example.yaml`](config.turislab.example.yaml) for every
option with comments: persistence, input export, the scan scheduler, every
source and its rate limits, identity matching weights, scoring weights, tier
thresholds, the HTTP server, the agent, metrics and logging.

Secrets never live in the file: `api_key_env` / `*_env` options name the
environment variable that holds the value, and `"${VAR}"` inline references
are expanded at load time.

## Development

```bash
make test   # Go tests
make lint   # gofmt + go vet + tests
make ui-check   # type-check the web UI
```

Tests never touch the network: source collectors run against `httptest`
fakes, and the scraper integration tests use a stub binary. The agent tests
script a fake model to pin the tool loop.

### Layout

```
app/cmd/turislab        CLI flags, wiring, exit codes
app/internal/logx       per-step log lines
app/internal/turislab/
  model                 establishments, signals, scores, report
  config                YAML configuration
  source                collectors: website, osm, google_maps_scraper, uuid
  scan                  scheduler: ticks, backoffs, per-establishment runs
  scoring               closure/quality risk, tiers, hysteresis
  store                 sqlite persistence of establishments and signals
  validate              ground-truth comparison
  agent                 the inspection assistant (ADK Go)
  server                chi server: report, queue, agent endpoints
ui/                     the SvelteKit web UI
```

## Dependencies

The Go module keeps a small dependency set: [chi](https://github.com/go-chi/chi)
for routing, a YAML loader, SQLite via `modernc.org/sqlite`, the ADK Go SDK
for the agent and OpenTelemetry for metrics. The web UI uses SvelteKit and
Tailwind CSS, nothing else.
