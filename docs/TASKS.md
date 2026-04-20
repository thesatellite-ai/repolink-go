# repolink-go — Task List

Source of truth for what's done and what's next. Extracted from
[PROBLEM.md](./PROBLEM.md) MVP list + VERIFICATION.md test plan. Check
items off here whenever a commit ships them; update `.verification/spec.yaml`
to flip `status: implemented` at the same time.

Legend: `[x]` shipped · `[ ]` pending · `[~]` in progress/partial

---

## Foundation

- [x] Day-0 scaffold — `go mod init`, dir layout, `.gitignore`, README
- [x] **MVP-01** — `internal/types` package: `ProfileName`, `AbsPath`, `RepoUUID`, `MappingState`, `SymlinkKind`, `Hostname`, `JSONErrorCode` + constructors + tests
- [x] `internal/app` — DI struct (G4: no package-level mutable globals)
- [x] `internal/cli` root — cobra + global flags (`--profile/-p`, `--json`, `--non-interactive`, `--dry-run`)
- [x] `repolink version` subcommand (human + JSON)

## Config (MVP-02)

- [x] Load `~/.repolink/config.jsonc` via `tailscale/hujson` (comment-preserving)
- [x] Schema allowlist + `KeyKind` (scalar vs array) in `internal/config/schema.go`
- [x] Key-path validator with `<name>` wildcard + Levenshtein "did you mean?" hints
- [x] Validation: required `dir`, absolute paths, `default_profile` must exist, unknown fields rejected
- [x] `AddProfile` via hujson.Patch (RFC 6902) — preserves comments
- [x] `SetDefaultProfile` — comment-preserving
- [x] `WriteFile` — atomic (tempfile + rename)
- [x] `BootstrapEmpty` for first-run from `setup`
- [ ] `--get` / `--set` / `--unset` / `--list` CLI verbs
- [ ] `--add-scan-root` / `--remove-scan-root` auto-generated from schema `ArrayKey`
- [ ] `--add-profile` CLI verb
- [ ] `--rename-profile <old> <new>` (atomic config + DB `profiles.name` update)
- [ ] Type coercion for `--set` (`"true"` → bool, numeric strings → number)

## Data model (MVP-03)

- [x] Ent schema: `RepoMeta`, `RepoMapping`, `Profile`, `RunLog`
- [x] UUID v7 PKs everywhere via `uuid.NewV7()`
- [x] Snake_case plural tables via `entsql.Annotation{Table:"..."}`
- [x] No FKs — `migrate.WithForeignKeys(false)` always
- [x] Unique index on `(repo_url, target_rel, link_name)` within DB
- [x] SQLite pragmas: WAL + `synchronous=NORMAL` + `busy_timeout=5000`
- [x] Smoke test: migrate + insert all 4 entities + collision detection
- [ ] Force `wal_checkpoint(TRUNCATE)` on graceful shutdown
- [ ] `.gitignore` inside profile `dir` auto-adding `repo.db-wal` / `repo.db-shm`

## Store abstraction (G1)

- [x] `internal/store` — `Store` interface + domain types (no ent leakage to CLI)
- [x] `entStore` impl — open/migrate, `EnsureRepoMeta`, `EnsureProfile`, `CreateMapping`, `MappingByTarget`, `ListMappings`, `UpdateMappingState`, `LogRun`, `RenameRepoMeta`
- [x] `ErrNotFound` / `ErrCollision` / `ErrSingletonPresent` sentinels
- [x] Unique-constraint detection mapped to `ErrCollision`

## Commands

- [x] **MVP-04** — `repolink setup`
  - zero-flag (CWD + basename)
  - `--dir` / `--name` / `--make-default`
  - creates/updates config.jsonc
  - opens/creates `<dir>/repo.db`, migrates
  - inserts `repo_meta` singleton
  - logs `op=setup` run_log
  - idempotent
- [ ] **MVP-05** — `repolink link <src> [dest]`
  - walk-up `.git/config` for `repo_url`
  - validate `src` exists inside active profile `dir`, reject `..` escape
  - auto-detect `kind` (dir vs file)
  - transactional insert + symlink; rollback DB row on fs failure
  - collision refusal with owner info
- [ ] **MVP-06** — `repolink sync` (bare `repolink` aliases)
  - auto-detect repo
  - create missing symlinks
  - idempotent
  - skip `paused` rows
- [ ] **MVP-07** — `repolink status` / `map list`
  - current-repo view + `--all-profiles`
  - `--long` / `--json`
- [ ] **MVP-08** — `.repolink.jsonc` resolver
  - CWD-only lookup (no walk-up)
  - three pin forms: `profile`/`profiles` (legacy), `sources: [display_name]`, `sources: [uuid]`
  - ambiguity → error pointing at UUID form
  - runs in `PersistentPreRunE` after `-p` flag
- [ ] **MVP-09** — `repolink unlink` (soft-delete only)
- [ ] **MVP-10** — `repolink cleanup` (fs-only, never touches targets)
- [ ] **MVP-11** — `repolink pause` / `repolink resume`
- [ ] **MVP-12** — `repolink unsync` (fs sweep, DB unchanged)
- [ ] **MVP-13** — `repolink map purge` (hard delete; nulls `run_logs.mapping_id` in same tx)
- [ ] **MVP-14** — `repolink map mv <old> <new>` (bulk `source_rel` rename, `--exact` flag)
- [ ] **MVP-15** — `repolink map add` / `list` / `remove` / `restore`
- [ ] **MVP-16** — `repolink meta` / `repolink meta rename "<new>"`
- [ ] **MVP-17** — `repolink verify` (read-only drift report)
- [ ] **MVP-18** — `repolink config --set/--get/--list/--unset/--add-profile`
- [ ] **MVP-19** — `repolink state` (full machine-state snapshot)
- [ ] Multi-source sync (opens multiple profiles' DBs, unions mappings)
- [ ] Cross-profile collision detection at `link` and `sync`

## Engines

- [ ] `internal/symlinker` — Compute → Plan → Apply (G3)
  - `Compute(desired, currentFS)` → plan
  - action types: `CreateSymlink`, `SkipExisting`, `Collision`, `Clobber`, `Remove`
  - `removeSymlink` safety helper (refuses non-symlink, uses `Lstat`)
  - `Apply(ctx, plan)` → result + rollback hooks
- [ ] `internal/gitremote` — walk-up `.git/config`, remote URL normalization, `ReadIdentity()` for `user.email` / `user.name`
- [ ] `internal/resolver` — `.repolink.jsonc` pin resolver

## Safety + policy

- [x] **S-00** tests (scaffolded) — repolink never deletes symlink targets
- [x] **S-07** tests (scaffolded) — sync/link refuse to clobber; `--force` required
- [ ] All fs deletes go through `removeSymlink` helper
- [ ] `--dry-run` + `--yes` on every mutator
- [ ] `RunLog` entry for every mutation
- [ ] Stable `JSONErrorCode` set (COLLISION, UUID_AMBIGUOUS, etc.) — append-only

## Hygiene gates (CI)

- [x] **G4** — no package-level mutable globals (App DI in place)
- [x] **G5** — `context.Context` first arg on I/O funcs (Store interface enforces)
- [ ] `ci/hygiene.sh` grep-gate script
  - [ ] **G1** — no `internal/ent` imports in `internal/cli|tui|mcp`
  - [ ] **G2** — no `fmt.Print*` in `internal/cli/cmd_*.go`
  - [ ] **G4** grep-check (package-level `var` outside `_test.go`)
  - [ ] **G5** contextcheck linter
- [ ] **G3** — Compute → Plan → Apply enforced by code review (no grep)
- [ ] CI grep-gate: no raw `string` in domain-package signatures
- [ ] CI grep-gate: no `os.RemoveAll` anywhere

## Verification

- [x] `.verification/spec.yaml` seeded (decisions, safety, flows, errors, hygiene, MVP)
- [ ] Extract full `D-01..D-56` IDs from PROBLEM.md Decisions table
- [ ] Extract full `S-01..S-25` safety rules
- [ ] Scaffolded `t.Skip` tests for every spec ID
- [ ] `last_synced` hash check in CI: spec.yaml must update when PROBLEM.md changes

## Documentation

- [x] `docs/PROBLEM.md` copied in
- [x] `docs/VERIFICATION.md` copied in
- [x] `docs/TASKS.md` (this file)
- [ ] `SKILL.md` (v0.3)
- [ ] Install docs / man page

## v0.2+ (post-MVP)

- [ ] `repolink sync --all` with `fd` scan engine + interactive install prompt
- [ ] `repolink mcp` server (biggest AI moat move)
- [ ] `init --template ai-project`
- [ ] `repolink search` cross-project grep
- [ ] `state --watch` live view
- [ ] `internal/tui` bubbletea dashboard
- [ ] Web UI (planned — after MVP)

## Parked / nice-to-have

- [ ] Turso / libSQL cloud backend (`Store` interface already abstracts this)
- [ ] Encrypted private-repo
- [ ] Memory-graph features

---

**Last updated:** 2026-04-20 (commit `4f905ce`)
