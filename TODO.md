# TODO

## Navegador / capturas

- **Probar [agent-browser](https://github.com/vercel-labs/agent-browser) en vez del montaje actual con Chrome/Playwright MCP.** El sidecar `make mcp-browser` y el cliente `internal/turislab/capture` funcionan, pero agent-browser promete una capa más ligera para que el agente navegue y captured pantallas. Comparar: arranque, flags de Chromium, coste por captura, guardado del PNG en `artifacts_dir` y encaje con `agent.mcp` / `screenshots.mcp_url`. Si convence, sustituir el sidecar y simplificar `capture`.

## Agent chat

- **SSE heartbeat.** A slow tool call leaves the `/api/agent/chat` stream silent, and a reverse proxy with an idle timeout could cut it. Send a `: ping` comment every ~15s. `emit` is only called from the run loop today, so the writes need a mutex once a heartbeat goroutine exists. Only matters behind a proxy.
- **Cursor pagination for `list_findings`.** It pages by `offset`, which is fine for a fixed report. If a new audit finishes mid-conversation, `SetReport` swaps the report and a `next_offset` from the old one points at the wrong place. Fix: an opaque `next_cursor` carrying a report generation (counter bumped in `SetReport`) plus the position, and a clear error when the generation changed. Revisit if audits are relaunched often while chats are open.
- **Multi-user.** All chats share `userID = "doctor"` and the only protection of a session is its 128-bit id. Needs authentication in front if exposed to more than one person.
- **Multiple replicas.** Sessions live in memory, so each replica has its own; needs session affinity (a restart already starts conversations fresh, and the UI handles it).
