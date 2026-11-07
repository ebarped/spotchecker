# turislab-doctor — web UI

SvelteKit app (Svelte 5 + Tailwind CSS 4) that renders the scan report with
an aurora-style dark/light theme: the inspector visit queue, the
per-establishment evidence behind every score and the pipeline view. It reads
the JSON from the turislab-doctor server's `GET /api/report` and talks to the
agent chat endpoints when `agent.enabled` is set.

## Developing

The UI expects the report API from the Go server:

```sh
make serve      # repo root, terminal 1: scanner + API + UI on :8090
make ui-dev     # repo root, terminal 2: this UI on :5173, proxies /api
```

- `DOCTOR_URL` controls what `/api` proxies to (default `http://localhost:8090`).
  Both targets share the `PORT` variable in the root Makefile, so `make serve
  PORT=8091` + `make ui-dev PORT=8091` keep them in sync.
- `npm run check` type-checks the app. `npm test` runs the unit tests.

## Building

`npm run build` produces the static SPA straight into `../app/ui/build`
(gitignored), which the Go binary embeds via `go:embed` and serves at `/`.
From the repo root, `make ui-build` runs this and restores the tracked
`placeholder.html` — see the root [README](../README.md). A clean clone builds
the Go binary without Node and serves the placeholder until the real UI is
compiled.

## Layout

```
src/
├── app.css                  # Tailwind 4 theme tokens + aurora helpers
├── lib/
│   ├── turislab.ts          # report JSON types + fetch logic
│   ├── agent.svelte.ts      # chat controller (messages, sessions, catalog)
│   ├── agent-stream.ts      # SSE event parsing for the chat stream
│   ├── markdown.ts          # sanitized markdown rendering for chat answers
│   ├── theme.svelte.ts      # dark / light toggle (persisted)
│   ├── index.ts             # public exports
│   └── components/
│       ├── AuroraBackground.svelte
│       ├── ThemeToggle.svelte
│       └── agent/           # AgentChat, AgentMessage, AgentView,
│                            # ToolGraph, ToolSidebar, FlowNode, ...
└── routes/
    ├── +page.svelte         # redirects to /turislab
    ├── turislab/+page.svelte  # the report: queue, evidence, pipeline
    └── how-it-works/+page.svelte
```

The adapter is `@sveltejs/adapter-static` (pure SPA, `ssr = false`). Runes mode
is forced project-wide.
