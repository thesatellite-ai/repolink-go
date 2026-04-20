# repolink-go

Private-repo ↔ GitHub repo symlink manager. Go + Ent + SQLite. CLI-first; web UI planned post-v0.1.

## Status

Day-0 scaffold. Only `repolink version` works.

See:
- [docs/PROBLEM.md](./docs/PROBLEM.md) — full spec (v21, 2026-04-19 locked).
- [docs/VERIFICATION.md](./docs/VERIFICATION.md) — QA strategy / spec-to-test traceability.

## Build

```sh
go build -o bin/repolink ./cmd/repolink
./bin/repolink version
./bin/repolink version --json
```

## Layout

```
cmd/repolink/         binary entrypoint
internal/app/         DI struct (hygiene G4)
internal/cli/         cobra commands (no ent imports — G1; no fmt.Print* in cmd_*.go — G2)
internal/config/      JSONC config loader + schema validator (MVP-02)
internal/ent/         generated ent client + schema
internal/symlinker/   Compute → Plan → Apply engine (G3)
internal/mcp/         MCP server (v0.2)
internal/types/       named types + validation (MVP-01) ✓
.verification/        spec.yaml + traceability matrix
ci/                   hygiene scripts (G1-G5)
```

## Next

- MVP-02: `internal/config` — load `~/.repolink/config.jsonc` via `tailscale/hujson`
- Seed `.verification/spec.yaml` from PROBLEM.md
- Ent schema stubs: `Profile`, `RepoMapping`, `RunLog`
