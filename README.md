# nano-brain-observe

A transparent HTTP reverse proxy that sits in front of the nano-brain memory
server and logs every call an AI agent makes to it: MCP tool name, arguments,
full response, latency and outcome. Records go to daily JSONL files so the
data can be mined to improve nano-brain (which tools are used, which searches
come back empty, what is slow, what fails).

It is opt-in, like a plugin. If the proxy is running and an agent's nano-brain
MCP URL points at it, there is data; otherwise nothing changes. nano-brain
itself is not modified.

## How it works

```
agent (Claude Code, Codex, Pi, OpenCode, Devin)
   |  http://localhost:3101/mcp
   v
nano-brain-observe  127.0.0.1:3101 ----> ~/.nano-brain-observe/logs/calls-YYYY-MM-DD.jsonl
   |  http://127.0.0.1:3100/mcp              (one JSON line per call)
   v
nano-brain          127.0.0.1:3100
```

Requests and responses pass through unchanged and streaming is not buffered.
A failure to write a log line is reported on the proxy's own output and never
fails the proxied call.

## Install

With npm (Node 18+). The package downloads a prebuilt binary for your platform
(macOS, Linux, Windows; x64 and arm64) from the GitHub release and verifies its
SHA-256:

```sh
npm install -g nano-brain-observe     # or: npm install -g @nano-step/nano-brain-observe
nano-brain-observe --version
```

Or run it without installing:

```sh
npx nano-brain-observe
```

Or build from source (Go, standard library only):

```sh
go build -o nano-brain-observe ./cmd/nano-brain-observe
```

## Quick start

1. Start the proxy (nano-brain must be reachable on port 3100):

   ```sh
   nano-brain-observe
   ```

2. Point an agent's nano-brain MCP URL at port 3101 instead of 3100, keeping
   the path and any `?workspace=` query. Per-agent instructions for Claude
   Code, Codex, Pi, OpenCode and Devin: [docs/AGENTS-SETUP.md](docs/AGENTS-SETUP.md).
   If the URL uses `host.docker.internal`, start the proxy with
   `--allowed-hosts host.docker.internal` even when its default loopback
   listener is reachable; only add `--unsafe-expose-lan` if a non-loopback
   bind is needed.

3. Restart the agent session, use nano-brain as usual, then look at the log:

   ```sh
   jq -r 'select(.kind=="mcp_tool") | [.ts, .client, .tool, .status, .latency_ms] | @tsv' \
     ~/.nano-brain-observe/logs/calls-*.jsonl
   ```

To keep the proxy running across logins on macOS, see the launchd example in
[docs/AGENTS-SETUP.md](docs/AGENTS-SETUP.md#optional-run-at-login-on-macos).
Vietnamese guide: [docs/HUONG-DAN.md](docs/HUONG-DAN.md).

## Options

All options have flags; only those with an environment variable listed below
can also be set through the environment.

| Flag | Environment variable | Default | Meaning |
|---|---|---|---|
| `--listen` | `NANO_BRAIN_OBSERVE_LISTEN` | `127.0.0.1:3101` | Address the proxy listens on |
| `--upstream` | `NANO_BRAIN_OBSERVE_UPSTREAM` | `http://127.0.0.1:3100` | nano-brain server to forward to |
| `--dir` | `NANO_BRAIN_OBSERVE_DIR` | `~/.nano-brain-observe/logs` | Log directory (created with mode 0700) |
| `--max-response-bytes` | `NANO_BRAIN_OBSERVE_MAX_RESPONSE_BYTES` | `262144` | Cap on the logged response body (256 KiB) |
| `--allowed-hosts` | | empty | Comma-separated additional exact Host names/IPs (no ports or wildcards) |
| `--unsafe-expose-lan` | | off | Explicitly allow a non-loopback listener, including wildcard binds |
| `--version` | | | Print the version and exit |

The proxy rejects requests unless their Host is `localhost`, a loopback IP
(127/8 or `::1`), or an exact entry in `--allowed-hosts`. When Origin is sent,
it must be an http(s) origin matching the request's Host, port and scheme;
requests without Origin (such as CLI/MCP calls) work normally. Forwarded Host
and scheme headers do not override these checks. Rejected calls are not logged.
Binding outside loopback requires `--unsafe-expose-lan` and does **not** relax
the Host check: Docker's `host.docker.internal` needs
`--allowed-hosts host.docker.internal` even on a loopback listener. The proxy
has **no authentication**:
on a LAN listener, a client that supplies an allowed Host can access nano-brain
and its private data. Prefer a dedicated interface and firewall restrictions.
Without `--unsafe-expose-lan`, the listen address must use a literal loopback
IP or exactly `localhost` (case-insensitive); other hostnames require opt-in
even if they currently resolve to loopback.

### Stats

`nano-brain-observe stats [-dir DIR] [-since DURATION]` summarises the logged
`mcp_tool` calls: per tool (calls, error, empty, p50/p95/max latency in ms) and
per client (calls, error), busiest first. `-dir` defaults to the same log
directory as the proxy; `-since` takes a Go duration or days (`36h`, `7d`) and
defaults to `7d`. Malformed log lines are skipped and counted in the header.

When a call carries no `Mcp-Session-Id` (stateless MCP), `client` falls back to
the first token of the `User-Agent`. Failed (404) requests to `/.well-known/`
paths are still proxied but not logged.

Output files are `<dir>/calls-YYYY-MM-DD.jsonl`, one per day, mode 0600.

## What gets logged

One JSON object per line. Fields marked "optional" are omitted when empty.

| Field | Type | Meaning |
|---|---|---|
| `ts` | timestamp | When the call was observed |
| `kind` | string | `mcp_tool`: JSON-RPC `tools/call` over `/mcp`. `mcp`: any other JSON-RPC method over `/mcp` or `/sse`. `rest`: any non-MCP HTTP request (e.g. `/api/v1/query`) |
| `client` | string, optional | MCP `clientInfo` as `name/version`, when known |
| `user_agent` | string, optional | HTTP `User-Agent` |
| `session_id` | string, optional | `Mcp-Session-Id` header, when present |
| `workspace` | string, optional | `arguments.workspace`, else the `?workspace=` query parameter |
| `http_method` | string | HTTP method |
| `path` | string | Request path |
| `rpc_method` | string, optional | JSON-RPC method, e.g. `tools/call` |
| `tool` | string, optional | MCP tool name, e.g. `memory_query` |
| `args` | JSON, optional | Tool arguments, or the REST request body/query |
| `http_status` | number | HTTP status returned by nano-brain |
| `status` | string | `ok`: succeeded. `empty`: succeeded but returned no results. `error`: HTTP status >= 400, a JSON-RPC error, or a tool result with `isError` |
| `error` | string, optional | Error message when `status` is `error` |
| `latency_ms` | number | Time taken by the call, in milliseconds |
| `result_count` | number, optional | Number of results; absent when it cannot be derived |
| `response_bytes` | number | Full response size before truncation |
| `response` | string, optional | Response payload, cut at `--max-response-bytes` |
| `response_truncated` | boolean, optional | `true` when `response` was cut |

## Privacy

The logs contain the full queries agents send and the full responses
nano-brain returns, including content from private workspaces. The log
directory is 0700 and the files are 0600. Never commit or share them. The
default directory is outside this repository, and `*.jsonl` is in
`.gitignore` in case you point `--dir` inside it.

## Reading the logs with jq

Calls per tool:

```sh
jq -r 'select(.kind=="mcp_tool") | .tool' ~/.nano-brain-observe/logs/calls-*.jsonl | sort | uniq -c | sort -rn
```

Empty-result rate per tool (tool, calls, empty, percent):

```sh
jq -rs 'map(select(.kind=="mcp_tool")) | group_by(.tool)[] | (map(select(.status=="empty")) | length) as $e | "\(.[0].tool)\t\(length)\t\($e)\t\($e * 100 / length | round)%"' ~/.nano-brain-observe/logs/calls-*.jsonl
```

Error list:

```sh
jq -c 'select(.status=="error") | {ts, client, tool, rpc_method, path, http_status, error}' ~/.nano-brain-observe/logs/calls-*.jsonl
```

Ten slowest calls:

```sh
jq -cs 'sort_by(-.latency_ms)[:10][] | {latency_ms, tool, path, ts, args}' ~/.nano-brain-observe/logs/calls-*.jsonl
```

p50 / p95 latency per tool, in milliseconds (nearest rank):

```sh
jq -rs 'map(select(.kind=="mcp_tool")) | group_by(.tool)[] | (map(.latency_ms) | sort) as $l | ($l | length) as $n | "\(.[0].tool)\tn=\($n)\tp50=\($l[($n * 0.5 | ceil) - 1])\tp95=\($l[($n * 0.95 | ceil) - 1])"' ~/.nano-brain-observe/logs/calls-*.jsonl
```

Same tool called with the same arguments more than once in a session:

```sh
jq -rs 'map(select(.kind=="mcp_tool" and .session_id != null)) | group_by([.session_id, .tool, .args])[] | select(length > 1) | "\(length)x\t\(.[0].session_id)\t\(.[0].tool)\t\(.[0].args | tojson)"' ~/.nano-brain-observe/logs/calls-*.jsonl
```

## Uninstall

1. Point each agent's nano-brain URL back to port 3100 (see the "Revert"
   steps in [docs/AGENTS-SETUP.md](docs/AGENTS-SETUP.md)).
2. Stop the proxy (Ctrl-C, or unload the launchd job if you installed one).
3. Optionally delete the logs: `rm -rf ~/.nano-brain-observe`.

Do step 1 first: an agent still pointing at 3101 after the proxy stops loses
its nano-brain connection.

## Known limitations

- Agent identity depends on the client echoing `Mcp-Session-Id` (the server
  is stateless). When it does not, `session_id` and `client` may be missing
  and `user_agent` is the only way to tell agents apart.
- Legacy `/sse` transport: requests are logged, but responses arrive on a
  separate stream and are not correlated with them.
- There is no analysis tooling yet; read the files with `jq`.
- `result_count` (and so the `empty` status) is derived only for responses
  that carry a result list (`results`, `symbols`, `edges`, `impacted`,
  `chain`, `workspaces`, ...) or `"found": false`. Other tools are `ok` or
  `error` only.
- Some clients probe OAuth discovery paths (`/.well-known/...`) on startup.
  nano-brain answers 404 and these show up as `rest` records with status
  `error`; filter on `kind == "mcp_tool"` when computing error rates.
- Request bodies over 8 MiB are forwarded but logged without arguments, and
  responses over 4 MiB are logged without a per-call status.
