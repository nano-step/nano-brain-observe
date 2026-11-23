# Issue #2: inbound Host/Origin and listener safety

## Summary

The proxy previously forwarded every request without checking its inbound
Host or Origin. `ReverseProxy.SetURL` correctly rewrites the outbound Host to
the upstream, so an upstream Host check could not protect the proxy from
requests with attacker-controlled inbound Host headers. A wildcard bind could
also expose the unauthenticated proxy to the LAN.

The inbound guard now runs before any proxying or recording, including GET
`/mcp` and `/sse`. Only exact `localhost`, loopback IP literals or explicitly
listed `--allowed-hosts` are accepted. A supplied Origin must match the
request's scheme, Host and port. No DNS lookup is used for inbound Host
validation, and forwarded host/protocol headers are not trusted.
`--unsafe-expose-lan` explicitly opts into non-loopback binds without relaxing
the Host check; listener validation precedes log-directory creation and bind.
The preflight classifies the literal listen host without DNS resolution so
an alias that initially resolves to loopback cannot change resolution between
validation and `net.Listen`. Only loopback IP literals and exact `localhost`
avoid the opt-in.
For agents using `host.docker.internal`, the hostname must be in
`--allowed-hosts` even if the loopback listener is reachable. Keep that
listener and omit `--unsafe-expose-lan` when possible; a wider bind requires
both flags. See `docs/AGENTS-SETUP.md` and `docs/HUONG-DAN.md`.
The launchd template in `docs/AGENTS-SETUP.md` also includes
`--allowed-hosts host.docker.internal` for its documented OpenCode URL;
the historical installation note in `docs/DESIGN.md` points to that template.

## Evidence

- `internal/proxy/proxy.go`: guard in `ServeHTTP` before the streaming fast
  path and sink operations; upstream `SetURL` is unchanged.
- `internal/proxy/host.go`: strict Host/Origin parsing and exact allowlist.
- `cmd/nano-brain-observe/main.go`: startup flag parsing and bind check before
  opening the sink or listener.
- `internal/proxy/host_test.go`, `cmd/nano-brain-observe/main_test.go`: hostile
  and valid Host/Origin cases, no upstream/sink access on rejection, upstream
  Host rewriting, and listener guard tests.
- Validation: `go test -race ./...`, `go vet ./...`, `go build ./...` and
  `git diff --check` passed in the issue worktree.

## Remaining risk

The proxy remains unauthenticated. On an opted-in LAN bind, a reachable client
can send an allowed Host header; firewall restrictions or a loopback-only bind
remain necessary for access control. No real server or production data was
used for verification.
