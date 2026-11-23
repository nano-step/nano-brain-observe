# Switching agents over to nano-brain-observe

Switching an agent over means changing its nano-brain MCP server URL from
port 3100 to port 3101. Keep the path (`/mcp`) and any `?workspace=` query
exactly as they are. Nothing else in the agent's configuration changes.

Before you start:

1. nano-brain is running on port 3100 as usual.
2. The proxy is running: `./nano-brain-observe` (see the README).
3. Edit only the nano-brain entry shown below. Leave every other entry in
   the file alone.
4. Restart the agent (or start a new session) after editing; agents read
   their MCP configuration at startup.

The snippets below show only the nano-brain entry. Your entry may carry
extra keys; keep them and change only `url`.

## Verify (same for every agent)

Ask the agent to make any nano-brain call (for example a memory query), then
look at the last lines of today's file:

```sh
tail -n 3 ~/.nano-brain-observe/logs/calls-$(date +%F).jsonl | jq -c '{ts, client, user_agent, kind, tool, status}'
```

A new line should appear whose `client` and/or `user_agent` identifies the
agent you just used. To see which agents have been observed today:

```sh
jq -r '[.client // "-", .user_agent // "-"] | @tsv' ~/.nano-brain-observe/logs/calls-$(date +%F).jsonl | sort | uniq -c
```

If no line appears, the agent is still talking to port 3100 directly: check
that you edited the entry it actually uses and that you restarted it.

## Claude Code

Config file: `~/.claude.json`. The nano-brain entry can exist in two places:
the top-level `mcpServers` object (user scope) and
`projects.<project path>.mcpServers` (local scope for one project). A
project-level entry takes precedence in that project, so change every
occurrence. Find them with:

```sh
grep -n -A3 '"nano-brain": {' ~/.claude.json
```

Before:

```json
"nano-brain": {
  "type": "http",
  "url": "http://localhost:3100/mcp"
}
```

After:

```json
"nano-brain": {
  "type": "http",
  "url": "http://localhost:3101/mcp"
}
```

Instead of editing the file by hand you can use the CLI (user scope shown;
run the same pair without `-s user` inside a project that has its own entry):

```sh
claude mcp remove nano-brain -s user
claude mcp add --transport http -s user nano-brain http://localhost:3101/mcp
```

Check with `claude mcp list`. Revert: set the URL back to
`http://localhost:3100/mcp` in every place you changed.

## Codex CLI

Config file: `~/.codex/config.toml`.

Before:

```toml
[mcp_servers.nano-brain]
url = "http://localhost:3100/mcp"
```

After:

```toml
[mcp_servers.nano-brain]
url = "http://localhost:3101/mcp"
```

Revert: set `url` back to `http://localhost:3100/mcp`.

## Pi

Config file: `~/.pi/agent/mcp.json`. The entry has other keys besides
`type` and `url`; leave them unchanged.

Before:

```json
"nano-brain": {
  "type": "http",
  "url": "http://localhost:3100/mcp"
}
```

After:

```json
"nano-brain": {
  "type": "http",
  "url": "http://localhost:3101/mcp"
}
```

Revert: set `url` back to `http://localhost:3100/mcp`.

## OpenCode

If the agent can reach the proxy through the default loopback listener (for
example, `host.docker.internal` maps to `127.0.0.1` on the host and OpenCode
runs on the host), keep the default `--listen` and allow the hostname in the
agent URL:

```sh
./nano-brain-observe --allowed-hosts host.docker.internal
```

Do **not** add `--unsafe-expose-lan` in this case. The flag is needed only
when binding to a non-loopback interface; without `--allowed-hosts`, the proxy
rejects `Host: host.docker.internal` with 403 even when the connection succeeds.

Config file: `~/.config/opencode/opencode.json`.

Before:

```json
"nano-brain": {
  "type": "remote",
  "url": "http://host.docker.internal:3100/mcp",
  "enabled": true
}
```

After:

```json
"nano-brain": {
  "type": "remote",
  "url": "http://host.docker.internal:3101/mcp",
  "enabled": true
}
```

When this agent runs inside a Docker container, it reaches the host through
`host.docker.internal`, not `localhost`. Try the loopback command above first
if the container can reach that listener. If it reports a connection error
(rather than HTTP 403), bind to an address the container can reach:

```sh
./nano-brain-observe --listen 0.0.0.0:3101 \
  --unsafe-expose-lan --allowed-hosts host.docker.internal
```

The proxy refuses wildcard or other non-loopback binds without
`--unsafe-expose-lan`. Use **both** flags for this wider bind.
`--allowed-hosts` accepts exact DNS names or IPs, not
ports/wildcards; add the exact hostname used by the container (or its LAN IP
if it connects by IP). With `0.0.0.0`, the proxy accepts connections from every
network interface. **It has no authentication**: any machine that can reach the
port can send an allowed Host header and call nano-brain, including private
workspace data. Host/Origin checking is not access control. Prefer one of:

- bind to one specific interface address that the container can reach
  instead of `0.0.0.0` (`--listen <interface address>:3101`);
- keep `0.0.0.0` but block inbound port 3101 from other machines with the
  macOS firewall or a packet filter rule.

The other agents keep using `http://localhost:3101/mcp` either way.

Revert: set `url` back to `http://host.docker.internal:3100/mcp`. If you
widened `--listen` only for this agent, go back to the default.

## Devin (Devin Desktop / CLI)

Devin imports MCP servers from other tools' configs. Run `devin mcp list` and
look at the `Source:` line under `nano-brain`: when it is `~/.claude.json`,
the Claude Code change above also switches Devin, and reverting Claude Code
reverts Devin. A session that is already open keeps its old connection until
it is restarted.

## Optional: run at login on macOS

This is an example only; nothing is installed by this repository. Replace
the placeholder path with the absolute path of your built binary (launchd
does not expand `~` or use your shell's `PATH`).

Save as `~/Library/LaunchAgents/local.nano-brain-observe.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>local.nano-brain-observe</string>
  <key>ProgramArguments</key>
  <array>
    <string>/ABSOLUTE/PATH/TO/nano-brain-observe</string>
    <string>--listen</string>
    <string>127.0.0.1:3101</string>
    <string>--upstream</string>
    <string>http://127.0.0.1:3100</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>/tmp/nano-brain-observe.out.log</string>
  <key>StandardErrorPath</key>
  <string>/tmp/nano-brain-observe.err.log</string>
</dict>
</plist>
```

The two `/tmp` files receive only the proxy's own diagnostic output. The
call records still go to `~/.nano-brain-observe/logs`; add `--dir` and an
absolute path to `ProgramArguments` to change that.

Load it:

```sh
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/local.nano-brain-observe.plist
```

Stop and remove it:

```sh
launchctl bootout gui/$(id -u)/local.nano-brain-observe
rm ~/Library/LaunchAgents/local.nano-brain-observe.plist
```

The proxy does not need nano-brain to be up when it starts, but calls fail
until it is. Point agents back to port 3100 before removing the job.
