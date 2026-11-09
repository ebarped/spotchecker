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

1. **Input** — the establishments come from an export (JSON/CSV/NDJSON) of
   the tourism registry; each record is normalized and deduplicated.
2. **Scan** — a scheduler (sequential or parallel, with rate-limit backoffs)
   sends each establishment through the enabled sources:
   `website`, `osm`, `google_maps_scraper` (the gosom scraper, no API key),
   plus optional `google_places`, `overture`, `phone`, `registry`, `news`,
   `yelp` and `tripadvisor`.
3. **Signals** — each source contributes dated, weighted signals
   (`website down`, `osm disused:tourism`, `maps status permanently closed`…)
   stored with full provenance.
4. **Scoring** — closure risk (direct status, activity, context, official
   sources) and quality risk (reviews, official, product, news, freshness)
   combine into a priority score adjusted by signal confidence, hysteresis and
   staleness.
5. **Queue** — establishments are tiered and the report ranks the visit queue.

## Tiers

| Tier           | Meaning                                                          |
|----------------|------------------------------------------------------------------|
| `P0`           | closure confirmed or the strongest evidence stack — visit first  |
| `P1` / `P2`    | suspected closure / declining quality — schedule a visit         |
| `P3`           | everything looks active — low priority                           |
| `collect_data` | too little evidence to judge — needs more scanning, not a visit  |

The suggested status of each establishment is `active`,
`closure_suspected`, `closure_confirmed`, `closed_temporarily`, `seasonal`
or `unknown`.

## Usage

```bash
make build                                  # compile the binary (UI included)

./turislab --config app/config.turislab.yaml --once              # scan everything once, exit
./turislab --config app/config.turislab.yaml --input base-de-datos-lugo.json --once # try another registry export
./turislab --config app/config.turislab.yaml --establishment <id> # scan one establishment
./turislab --config app/config.turislab.yaml --validate           # compare scores with ground truth
./turislab --config app/config.turislab.yaml --addr :8090         # scanner + API + UI
```

Flags:

| Flag               | Meaning                                                              |
|--------------------|----------------------------------------------------------------------|
| `--config`         | configuration file (default `config.turislab.yaml`)                  |
| `--input`          | input JSON/NDJSON file (overrides `input.path`)                      |
| `--addr`           | listen address of the HTTP server (overrides `server.addr`)          |
| `--once`           | scan every establishment once and exit                               |
| `--establishment`  | scan one establishment by id and exit                                |
| `--validate`       | compare the latest scores with `ground-truth.json` and exit          |
| `--validate-top`   | top-N cut-off used by `--validate` (default 3)                       |
| `--validate-strict`| fail `--validate` when an active establishment is closure-confirmed  |
| `--static-dir`     | serve the web UI from this directory (empty = embedded UI)           |

`make` targets: `build`, `run` (scanner + API + UI on :8090), `scan-once`,
`validate`, `serve` (clean rebuild + serve), `turislab-scraper` (fetch the
gosom Google Maps scraper into `bin/`), plus `test`, `vet`, `fmt`, `lint`,
`tidy`, `clean` and the `ui-*` targets for the web UI.

### API

The binary runs a small [chi](https://github.com/go-chi/chi) server:

- `GET /` — the web UI (the built SvelteKit bundle, embedded in the binary)
- `GET /api/report` — the full report as JSON
- `GET /api/report/export` — the same JSON as a download attachment
- `GET /api/summary` — rollup counts by tier and status
- `GET /api/queue` — the ordered visit queue
- `GET /api/establishments` — the establishment list
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

### Web UI

`ui/` is a SvelteKit app (Svelte 5 + Tailwind CSS) that renders the scan
report: the visit queue, per-establishment evidence and the pipeline view.
The dev server proxies `/api` to the Go server (`DOCTOR_URL`, default
`http://localhost:8090`):

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
