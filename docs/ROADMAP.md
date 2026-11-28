# nano-brain-observe roadmap

> Status: proposal, 2026-11-28. The overall plan for both projects: [nano-brain docs/ROADMAP-ENTERPRISE.md](https://github.com/nano-step/nano-brain/blob/master/docs/ROADMAP-ENTERPRISE.md).

## Position
- observe is an opt-in tool for quality R&D: it records what agents ask nano-brain and what they get back, so nano-brain's weak spots can be found.
- It is **not** the audit log or the metrics system. A client can connect to nano-brain directly and skip the proxy, so audit (E4) and `/metrics` (E0) live inside nano-brain.
- What stays unique to observe: capturing payloads (args and responses) for analysis. In an enterprise deployment that is off by default and turned on per workspace.

## Done
- ✅ Logging reverse proxy (MCP + REST) writing daily JSONL files
- ✅ Host guard (#2)
- ✅ Client attribution from the User-Agent, `/.well-known` 404 probes no longer logged, `stats` command (#4, PR #7; memory limits not done)

## O1 — Safe to run on shared data
- #3: redact credentials in args and responses, never log `Authorization` or `Cookie`, a retention setting, file mode 0600 and directory mode 0700 on every path
- #5: installer and release workflow on par with nano-brain
- #4, remaining part: memory and time limits on the proxy

## O2 — Know who made each call (after nano-brain E1/E3)
- A `principal` field: the public id of an `nbt_<id>_<secret>` token (the secret is never logged), or the `sub` of a JWT, marked `claimed` because the proxy does not validate it
- Tests that OAuth discovery (`/.well-known/oauth-protected-resource`), the `Authorization` header and `WWW-Authenticate` pass through unchanged
- Probe filtering stays limited to 404 responses; a 200 discovery response is logged

## O3 — Deployable (after nano-brain E6)
- A stdout sink, so Kubernetes log collectors pick records up
- Sampling, and metadata-only records by default, with payloads opt-in per workspace
- A sidecar option in nano-brain's Helm chart

## O4 — Quality reports
- `stats` grouped by principal and by workspace
- A `report` command: queries that returned nothing, arguments that failed, the slowest tools; the output becomes nano-brain issues

## Open question
- Long term: keep observe as a separate repo, or fold payload capture into nano-brain behind a flag (#728 already moves User-Agent attribution into nano-brain)?
