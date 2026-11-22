# nano-brain-observe — design

Status: decided 2026-11-22. Standalone, local-only repo; nano-brain itself is not modified.

## Goal

Log every call an agent makes to nano-brain (tool, args, full response, latency,
outcome) to files, so the data can be mined to improve nano-brain. Opt-in
"plugin": if the proxy is installed and agents point at it there is data; if
not, nothing changes.

## Facts this is built on (measured)

- nano-brain logs only 3 REST search handlers to `telemetry_logs`; the 19 MCP
  tools record nothing.
- Agents in use (Claude Code, Codex, Pi, OpenCode, Devin) all reach nano-brain
  over streamable HTTP at `:3100/mcp`.
- `POST /mcp` answers with `Content-Type: text/event-stream` (`data: {jsonrpc...}`),
  and `initialize` returns an `Mcp-Session-Id` header.

## Decisions

| Topic | Decision |
|---|---|
| Mechanism | Transparent HTTP reverse proxy in front of nano-brain. Only way to cover all five agents without touching nano-brain. |
| Default ports | listen `127.0.0.1:3101` -> upstream `http://127.0.0.1:3100` |
| What is logged | args + full response (capped, default 256 KiB, `response_truncated` flag) |
| Storage | JSONL, one file per day: `~/.nano-brain-observe/logs/calls-YYYY-MM-DD.jsonl`, dir 0700, files 0600 (responses contain private workspace content) |
| Analysis tooling | none in this round; read with jq |
| Dependencies | Go stdlib only |
| Failure policy | observing must never break or noticeably slow a call: streaming is passed through unbuffered, sink errors are logged and swallowed |

## Layout

- `internal/record` — `Record` + `Sink` contract (fixed; do not change without updating both sides)
- `internal/sink` — JSONL daily-rotating file sink
- `internal/proxy` — reverse proxy + MCP/REST capture
- `cmd/nano-brain-observe` — flags/env, wiring, graceful shutdown
- `README.md`, `docs/AGENTS-SETUP.md` — install / per-agent switch-over / uninstall

## Open questions

- Agent identity: depends on clients echoing `Mcp-Session-Id` (server is stateless); fallback is User-Agent.
- Legacy `/sse` transport: requests are logged, responses arrive on a separate stream and are not correlated.

## Status (2026-11-22)

Implemented, reviewed and installed on the owner's machine: launchd job
`local.nano-brain-observe` runs `~/.nano-brain-observe/bin/nano-brain-observe`,
and the Claude Code, Codex, Pi and OpenCode configs point at port 3101 (Devin
reads its nano-brain entry from `~/.claude.json`, so it follows Claude Code).
Backups of the edited configs: `<file>.pre-observe-20261122-003726`.

Agent test after install (measured from the log):
- Codex and Pi: a real `memory_status` tool call was logged with the agent's client name.
- Claude Code and OpenCode: MCP `initialize` through the proxy was logged; no
  tool call could be made because Claude's login had expired and OpenCode's
  configured model was not found (both unrelated to the proxy).
- Devin: not tested, the CLI was not logged in.

Measured:
- `go vet ./...` clean, `go test -race -count=3 ./...` passes (proxy, sink, cmd).
- Smoke through the built binary against the live server with four read-only
  calls: the client received the same `data:` payload as a direct call, and the
  log held `initialize`, an `ok` tool call, an `error` tool call (missing
  document) and an `empty` search (`result_count: 0`), with `client` and
  `session_id` filled in. File mode 0600.

Independent review found three defects, all fixed with tests: `"error": null`
counted as a failure; the self-proxy guard missed 127.0.0.1 vs ::1; MCP request
bodies over 1 MiB were logged as `rest` (cap raised to 8 MiB).

Known limitations:
- Request bodies over 8 MiB are forwarded but logged as a `rest` record without args.
- Responses over 4 MiB cannot be matched by JSON-RPC id; status then comes from HTTP only.
- The request body (up to 8 MiB) is read before forwarding starts.
- Modes 0700/0600 apply only to directories/files the proxy creates.
- No ReadTimeout/IdleTimeout on the listener.

## Real-usage test and packaging (2026-11-22)

- Real test: Codex and Pi each answered five code/memory questions about a
  workspace using only nano-brain tools, through the proxy: 70 tool calls
  across 11 tools were logged. Claude Code could not be tested (expired login).
- That data exposed a proxy bug: `memory_get` responses were marked `empty`
  because the document's own empty `tags` array was counted as a result list.
  Fixed (`tags` removed from the counted fields; `impacted`, `chain` and
  `"found": false` added) with tests. Records written before the fix still
  carry the wrong `empty` status for `memory_get`.
- Distribution: GitHub Actions `ci.yml` (fmt, vet, race tests, cross-build,
  npm wrapper tests) and `release.yml` (on a `v*` tag: six binaries +
  SHA256SUMS as a GitHub release, then npm publish of
  `@nano-step/nano-brain-observe` and the unscoped alias `nano-brain-observe`).
  The npm package is a thin wrapper that downloads and SHA-256-verifies the
  binary for the platform.
