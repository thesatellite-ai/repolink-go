# Usage

Comprehensive command + workflow reference. For the pitch and pain
points, see [USE-CASES.md](./USE-CASES.md).

## Contents

- [Mental model](#mental-model)
- [Global flags](#global-flags)
- [Setup & configuration](#setup--configuration)
- [Adding mappings: `link`](#adding-mappings-link)
- [Keeping consumers synced: `sync`](#keeping-consumers-synced-sync)
- [Inspection: `status`, `map list`, `verify`, `state`](#inspection)
- [Removing mappings: `unlink` + `cleanup`](#removing-mappings-unlink--cleanup)
- [Temporary removal: `unsync`, `pause`, `resume`](#temporary-removal-unsync-pause-resume)
- [Renaming sources: `map mv`](#renaming-sources-map-mv)
- [Hard delete: `map purge`](#hard-delete-map-purge)
- [Cross-machine identity: `meta`](#cross-machine-identity-meta)
- [Configuration: `config`](#configuration-config)
- [Nuclear reset](#nuclear-reset)
- [JSON output + error codes](#json-output--error-codes)
- [Auto-gitignore](#auto-gitignore)
- [Workflows (end-to-end recipes)](#workflows-end-to-end-recipes)

---

## Mental model

- **Private-repo** — source of truth. One git repo containing shared
  content as folders. Your `repo.db` (SQLite) sits at its root and
  tracks every mapping.
- **Profile** — per-machine registration of a private-repo clone. Lives
  in `~/.repolink/config.jsonc`. You can have multiple (e.g. `work` +
  `personal`, or `team-docs` + `research`).
- **Consumer repo** — any GitHub project that wants a folder from the
  private-repo visible locally. Tracked via mappings in the private-repo's
  `repo.db`. Optionally commits a `.repolink.jsonc` pin so fresh clones
  know which private-repo they reference.
- **Mapping** — one row in `repo_mappings`: source folder inside the
  private-repo, consumer repo URL, target path inside consumer,
  filename, state, audit fields.
- **State machine** — every mapping is `active`, `paused` (experiment
  mode), or `trashed` (soft-deleted). Transitions are reversible until
  `map purge`.

```
active  ─unlink→  trashed  ─purge→ gone
active  ←resume──  paused  ←pause── active
paused  ─unlink→  trashed
```

## Global flags

Every command accepts these:

| Flag | Behavior |
|---|---|
| `--profile`, `-p <name>` | Override `default_profile`. Comma-separated = multi-source (`-p work,personal`). |
| `--json` | Machine-readable output envelope. Implies `--non-interactive`. |
| `--non-interactive` | Never prompt; error closed if confirmation would be needed. |
| `--dry-run` | Preview mutations without applying them. Available on every command that writes. |
| `--yes` | Skip the interactive "are you sure?" prompt. |

---

## Setup & configuration

### `repolink setup` — register a private-repo clone on this machine

```sh
# Zero-flag: uses CWD as the private-repo dir, basename as profile name.
cd ~/private-repo
repolink setup

# Explicit:
repolink setup --dir /Volumes/work/private-repo --name work
repolink setup --dir /Users/aman/notes --name personal --make-default

# Idempotent: re-running on the same dir updates the profile entry.
```

**Behavior matrix:**

| State | Effect |
|---|---|
| `~/.repolink/config.jsonc` missing | Created. |
| Profile name already exists | `dir` updated. |
| `default_profile` unset in config | Set to this profile (first-profile convenience). |
| `<dir>/repo.db` missing | Created + migrated. `repo_meta` singleton inserted. |
| `<dir>/repo.db` exists but no `repo_meta` | Migrated + singleton back-filled. |
| `<dir>/repo.db` exists and has `repo_meta` | Pending migrations only. |

Also writes `<dir>/.gitignore` adding `repo.db-wal` + `repo.db-shm`
(idempotent; de-duped) so SQLite sidecars never land in your git history.

### Config file: `~/.repolink/config.jsonc`

```jsonc
{
  // Which profile is active by default. Override per-command with --profile/-p.
  "default_profile": "work",

  "profiles": {
    "work": {
      "dir": "/Volumes/D/www/projects/khanakia/private-repo"
    },
    "personal": {
      "dir": "/Users/aman/personal-notes",
      "scan_roots": ["/Users/aman/code"]
    }
  }
}
```

JSONC — comments + trailing commas OK. Parsed via
[`tailscale/hujson`](https://github.com/tailscale/hujson). Writes
preserve comments.

---

## Adding mappings: `link`

```sh
repolink link <source-rel> [dest]
```

- `<source-rel>` — path **relative to active profile's dir** (the
  private-repo root). Example: `research/consensus`.
- `[dest]` — where inside the current consumer repo to place the
  symlink. Follows `ln -s` rules (see below).

### Dest-path resolution (matches `ln -s`)

| `dest` value | `<repoRoot>/<dest>` check | Result |
|---|---|---|
| (omitted) | — | Symlink placed at repo root, name = `basename(src)`. |
| `docs/` (trailing `/`) | Always dir mode | Placed inside: `docs/<basename(src)>`. |
| `docs` | Exists as a directory | Placed inside: `docs/<basename(src)>`. |
| `docs` | Doesn't exist | Filename mode: symlink at `docs`, named `docs`. |
| `docs/NOTES.md` | Parent `docs/` exists | Symlink at `docs/NOTES.md`. |

```sh
# Examples:
repolink link research docs              # if docs/ exists → docs/research; else → ./docs named "docs"
repolink link research docs/              # force dir mode regardless
repolink link templates/ci-go .github/workflows/ci.yml   # explicit filename
repolink link prompts/summary.md SUMMARY.md   # single-file mapping
```

### Flags

| Flag | Purpose |
|---|---|
| `--note "<text>"` | Free-form note stored on the mapping (shown in `map list --long`). |
| `--force` | Clobber an existing regular FILE at the target before symlinking. Refuses existing directories. |
| `--no-gitignore` | Skip the auto-`.gitignore` update for this mapping. |
| `--profile <n>` | Operate under a specific profile (overrides default). |
| `--json` / `--dry-run` | As usual. |

### What `link` does atomically

1. Walks up from CWD to find `.git/config`, extracts the origin remote
   URL, normalizes (strips scheme/`.git`/`git@host:` format).
2. Validates `<src>` exists inside the active profile's `dir` — rejects
   `..` escapes and paths outside the private-repo.
3. Detects `kind` (dir vs file) via `os.Stat`.
4. Checks for DB-level collision on `(repo_url, target_rel, link_name)`.
   On collision, prints a state-specific hint (e.g. "paused → use
   `repolink resume <name>`").
5. Stamps `created_by_email` + `created_by_name` from your git config
   (`<repoRoot>/.git/config` → `~/.gitconfig`).
6. Inserts the DB row. If fs symlink creation fails afterward, flips
   the row to `trashed` so audit trail survives.
7. Updates the consumer's `.gitignore` block.

---

## Keeping consumers synced: `sync`

```sh
repolink              # bare form = sync current repo (headline UX)
repolink sync
repolink sync --dry-run
repolink sync --repo github.com/khanakia/abc   # sync a specific repo from anywhere
```

Idempotent. Reads every active mapping for the current repo's URL,
materializes any missing symlinks, leaves already-correct ones alone,
warns on collisions. Skips `state=paused` rows (they come back with
`resume`).

Output markers in human mode:

| Marker | Meaning |
|---|---|
| `+` | created |
| `=` | skipped (already correct) |
| `~` | replaced (old symlink pointed elsewhere) |
| `!` | refused (collision or source missing) |

### Multi-source sync

If your consumer repo needs mappings from *multiple* private-repos:

```sh
# Option A: CLI flag
repolink sync -p work,personal

# Option B: commit a .repolink.jsonc pin in the consumer repo
# .repolink.jsonc can use three forms:
{ "profile": "work" }                         # legacy — local profile name
{ "sources": ["Work Notes", "Team Docs"] }    # portable — match by display_name
{ "sources": ["019dac77-f202-..."] }          # bulletproof — UUID prefix
```

Bare `repolink` in a consumer with a `.repolink.jsonc` pin auto-resolves
the source(s). Cross-profile collisions (same target claimed by two
different private-repos) fail loudly with both claimants named.

---

## Inspection

### `status` — current repo, read-only

```sh
repolink status           # compact
repolink status --long    # full UUIDs + fs_detail
repolink status --json
```

Per-row fs_state classifier:

| `fs_state` | Meaning |
|---|---|
| `ok` | Symlink present + points at correct source |
| `missing` | No symlink at target (active mapping) |
| `wrong_target` | Symlink points somewhere else |
| `collision` | Non-symlink (real file/dir) occupies target |
| `stale` | Symlink present but mapping is `paused`/`trashed` |

### `map list` — across all repos in the active profile

```sh
repolink map list                         # default: state=live (active+paused)
repolink map list --state all
repolink map list --state trashed
repolink map list --repo github.com/khanakia/abc
repolink map list --source research
repolink map list --long                  # full UUIDs + notes
repolink map list --json
```

### `verify` — drift report, read-only

```sh
repolink verify               # compact
repolink verify --long
repolink verify --json
```

Walks every active + paused mapping in the active profile. Reports any
row with a non-`ok` fs_state. Exit code 0 if every row is healthy.

### `state` — full machine-state snapshot (for scripts / AI)

```sh
repolink state --json
```

One-call snapshot: config path, default profile, active profile, CWD's
detected repo, and for every configured profile:
`repo_meta` + all mappings + counts (active/paused/trashed) + reachable flag.

Read-only guard: `state` never creates a `repo.db`. A profile added via
`config --add-profile` but never `setup` shows as `reachable: false`
with a guiding error string.

---

## Removing mappings: `unlink` + `cleanup`

Two-step by design — soft-delete first, fs removal is a separate step.
Gives a reversal window via `map restore`.

```sh
# Step 1: soft-delete (state → trashed, fs untouched)
repolink unlink research            # by link_name (scoped to current repo)
repolink unlink 019dac72-7cbb       # by UUID prefix (≥4 hex)
repolink unlink --all-in-repo --yes # bulk for current repo

# Step 2: remove fs symlinks for trashed rows
repolink cleanup              # current repo
repolink cleanup <id>         # single mapping
repolink cleanup --all --yes  # sweep across every repo in active profile
```

Sources in the private-repo are never touched. That's Safety Rule S-00,
enforced by the `symlinker.RemoveSymlink` helper refusing any path that
isn't a symlink.

---

## Temporary removal: `unsync`, `pause`, `resume`

Three subtly-different verbs:

### `unsync` — remove fs symlinks, keep DB intact

```sh
repolink unsync               # current repo's active mappings
repolink unsync <id|name>     # single
repolink unsync --all --yes   # sweep across every repo in active profile
```

Use case: *"I'm testing something and want these symlinks out of the
way temporarily."* Next `sync` restores them.

### `pause` / `resume` — state flip + fs change

```sh
repolink pause <id|name>              # active → paused (symlink removed)
repolink pause --all-in-repo          # every active row for current repo
repolink resume <id|name>             # paused → active (symlink recreated)
repolink resume --all-in-repo
```

Use case: *"Try life without this mapping for a while. Experiment, then
resume or unlink for real."* `sync` skips paused rows.

---

## Renaming sources: `map mv`

When you rename a source folder in the private-repo, every mapping that
used the old path needs rewriting + its symlink recreated to point at
the new location.

```sh
# In private-repo:
mv old-name new-name

# Then:
repolink map mv old-name new-name --yes        # prefix match by default
repolink map mv old-name new-name --exact      # only top-level, skip descendants
repolink map mv old-name new-name --dry-run    # preview
```

Transactional DB rewrite across all matching rows. Best-effort symlink
refresh in the current repo's consumer (others report `fs_action=needs_sync`
— run `repolink` in each to pick up new source paths).

---

## Hard delete: `map purge`

```sh
repolink map purge <id> --yes          # single (must be state=trashed)
repolink map purge --all --yes         # every trashed row in active profile
```

Hard DELETE of `repo_mappings` rows plus best-effort symlink removal.
Audit-trail rows in `run_logs` preserved; their `mapping_id` reference
column is nulled inside the same transaction.

**Irreversible.** Refuses non-trashed rows (run `unlink` first).

---

## Cross-machine identity: `meta`

Every private-repo has a `repo_meta` singleton row containing:

- `private_repo_id` — UUID v7 generated once at `setup`, immutable,
  globally unique. Portable across machines.
- `display_name` — human-readable label, editable. Default = basename
  of the `dir` at `setup` time.

Both travel with the private-repo via `git` (the `repo.db` is committed),
so fresh clones on other machines see the same identity.

```sh
repolink meta                     # show private_repo_id + display_name + created_at
repolink meta rename "Work Notes" # update display_name
repolink meta --json
```

This is what the `.repolink.jsonc` `sources: ["<display-name>"]` and
`sources: ["<uuid>"]` pin forms resolve against. Profile names stay
local aliases — only `display_name` + `private_repo_id` are portable.

---

## Configuration: `config`

Shorthand keys resolve against the **active profile** (set by
`default_profile` or `--profile`):

- `dir` → `profiles.<active>.dir`
- `scan_roots` → `profiles.<active>.scan_roots`

```sh
# Show the resolved active config:
repolink config

# Show all profiles + default:
repolink config --list
repolink config --list --json

# Read one value:
repolink config --get dir
repolink config --get default_profile
repolink config --get profiles.personal.dir

# Write scalar keys:
repolink config --set default_profile personal
repolink config --set profiles.work.dir /new/path

# Remove:
repolink config --unset scan_roots

# Add / remove a profile:
repolink config --add-profile personal --dir /Users/aman/notes
repolink config --rename-profile work,team-docs

# Scan roots (array keys — use these verbs, not --set):
repolink config --add-scan-root /Volumes/D/www/projects
repolink config --remove-scan-root /old/path
```

**Allowlist-enforced.** Unknown keys are rejected with a "did you mean?"
Levenshtein hint. Array keys cannot be `--set` — use the `--add-*` /
`--remove-*` verbs.

Every config write goes through `hujson.Value.Format()` so the file stays
canonically indented, comments preserved.

---

## Nuclear reset

When you want to completely unwind repolink state:

```sh
repolink reset --profile <name> --yes    # drop one profile + its repo.db
repolink reset --all --yes               # drop every profile + ~/.repolink/config.jsonc

# Preview:
repolink reset --all --dry-run
```

**Refuses if any targeted profile still has active/paused mappings** —
that protects you from leaving dangling symlinks in consumer repos.
Override with `--force` if you've already run `unsync --all --yes` in
every consumer.

Deletes:

- `<profile.dir>/repo.db` + `-wal` / `-shm` sidecars
- `<profile.dir>/.gitignore` — **only if its content matches exactly
  what `setup` wrote** (marker + `repo.db-wal` + `repo.db-shm` lines
  and nothing else). Any user-authored lines survive.
- For `--all`: `~/.repolink/config.jsonc`

**Never touches sources.** Private-repo contents untouched regardless.
Never touches consumer-repo symlinks — you're expected to run
`unsync --all` first if you want the fs cleaned too.

---

## JSON output + error codes

Every read command supports `--json`:

```json
{ "ok": true, "version": "v0.1.0", "data": { ... } }
```

Errors:

```json
{ "ok": false, "error": { "code": "COLLISION", "message": "...", "context": {...} } }
```

Stable `JSONErrorCode` set (append-only — existing codes never change
meaning):

```
COLLISION          target already claimed (same DB or cross-DB)
UUID_AMBIGUOUS     UUID prefix matches multiple rows
UUID_NOT_FOUND     no row matches the given UUID / prefix
CONFIG_INVALID     config.jsonc failed schema validation
PROFILE_UNKNOWN    --profile / default_profile names a missing profile
DIR_NOT_FOUND      profile's dir does not exist on this machine
SOURCE_MISSING     mapping's source_rel doesn't exist under profile.dir
TARGET_CLOBBER     symlink placement would overwrite a real file
NOT_A_SYMLINK      cleanup/unsync encountered a non-symlink where one was expected
DB_LOCKED          SQLite contention
DB_MIGRATE         ent migration failure
UNKNOWN            catch-all
```

`--dry-run --json` on any mutator gives a structured preview without
applying changes — useful for agent tools, CI gates, etc.

---

## Auto-gitignore

`link` / `sync` / `unlink` / `pause` / `resume` automatically maintain
a marker-bounded block inside the consumer repo's `.gitignore`:

```
# BEGIN repolink (managed — do not edit)
/docs/research
/prompts
# END repolink
```

Lines outside the block are preserved verbatim. The block is a pure
projection of the current active-mappings set — pausing removes a line,
resuming adds it back. Empty desired set = block removed entirely (and
the `.gitignore` file itself if nothing else is there).

`link --no-gitignore` skips the update for a specific mapping. A global
config toggle (`auto_gitignore: false`) is planned for users who want
to disable it wholesale.

---

## Workflows (end-to-end recipes)

### A. Fresh machine — first time

```sh
# 1. Install:
curl -sL https://raw.githubusercontent.com/thesatellite-ai/repolink-go/main/install.sh | sh

# 2. Clone the private-repo:
git clone <private-repo-url> ~/private-repo

# 3. Register it:
cd ~/private-repo && repolink setup

# 4. Any consumer repo with a committed .repolink.jsonc pin:
cd ~/code/my-project && repolink
```

### B. Add a new mapping to a consumer repo

```sh
cd ~/code/my-project
repolink link research/consensus docs/literature
# → DB row + symlink + .gitignore update

# Commit the .repolink.jsonc pin so teammates / other machines can rebuild:
# (repolink writes .repolink.jsonc for you on the first link in a repo)
git add .repolink.jsonc
git commit -m "repolink: add docs/literature mapping"
```

### C. Remove every mapping from a single consumer repo

```sh
cd ~/code/dying-project
repolink unlink --all-in-repo --yes    # flip all to trashed
repolink cleanup --yes                 # remove symlink files
# mapping rows stay as trashed (recoverable). To hard-delete:
repolink map purge --all --yes
```

### D. Rename a source folder used by many consumers

```sh
cd ~/private-repo
git mv old-name new-name
repolink map mv old-name new-name --yes   # rewrites all mappings + refreshes current repo's symlinks
git commit -am "rename old-name → new-name"

# For every OTHER consumer that referenced old-name:
cd ~/code/consumer-a && repolink    # sync picks up new source paths
cd ~/code/consumer-b && repolink
```

### E. Debug "why is this folder missing"

```sh
repolink verify              # cross-repo drift report
repolink status --long       # current repo with fs detail
repolink state --json | jq   # full snapshot
repolink map list --state all    # include trashed rows
```

### F. Temporarily experiment without a mapping

```sh
repolink pause research-notes   # symlink gone, row preserved
# ... experiment ...
repolink resume research-notes  # symlink back
```

### G. Nuclear reset (wrong config, start over)

```sh
# First, clean up consumer-repo symlinks if any exist:
for repo in ~/code/*/; do
  (cd "$repo" && repolink unsync --all --yes 2>/dev/null || true)
done

# Then nuke everything:
repolink reset --all --yes

# Re-register fresh:
cd ~/private-repo && repolink setup
```

---

## Deeper references

- [README.md](../README.md) — pitch + install
- [USE-CASES.md](./USE-CASES.md) — pain points + scenarios
- [../skills/repolink/SKILL.md](../skills/repolink/SKILL.md) — AI agent skill
