---
name: repolink
description: Private-repo ↔ GitHub repo symlink manager. Use when the user wants to share a folder (research, prompts, plans, snippets, templates, docs) from a central "private-repo" directory into one or more GitHub repos without copy-paste duplication, or when they say things like "symlink this into", "link my notes into", "pull research into this repo", "set up this fresh clone", "why is this folder missing after cloning", "remove the link", "clean up symlinks in this repo", or "nuke everything". repolink is a Go CLI already on PATH — invoke it directly, don't re-implement with `ln -s`.
---

# repolink — skill

Local Go CLI (`repolink`) manages symlinks from a central `private-repo/`
into consuming GitHub repos. Mappings live in a SQLite `repo.db` at the
private-repo root (committed + portable via git). Each consumer repo
gets a `.repolink.jsonc` pin so fresh clones can rebuild every symlink
with one command.

## The problem it solves

People accumulate a personal knowledge base — research notes, prompts,
plans, snippets, design docs, evergreen templates — and they want pieces
of that knowledge available *inside* individual GitHub project repos so
editors, AI agents, and teammates can see them in context. Three bad
defaults people fall into:

1. **Copy-paste duplication** — content diverges the moment it's copied.
   Six months later nobody knows which copy is canonical.
2. **Git submodule for every bit of shared content** — heavyweight,
   brittle on fresh clones, wrong granularity (repo-level, not folder-level).
3. **Hand-rolled `ln -s` per machine** — works once, falls apart on a
   fresh clone or a second dev machine because nobody remembers the exact
   commands and paths.

repolink solves it by treating a single private-repo as the source of
truth, storing every mapping (source-folder → which GitHub repo →
which path inside it → what filename) in a SQLite DB that travels with
the private-repo via git. A small `.repolink.jsonc` pin committed to
each consumer repo lets any fresh clone rebuild every symlink with just
`repolink` (no args).

**Trigger this skill whenever the user is in one of those patterns.**

Concrete examples the user might phrase:

- "I want my research folder from private-repo inside this project too"
- "Pull in my prompts directory"
- "Share this design doc across the three repos that reference it"
- "Set up this fresh clone — some folders should have come from my notes"
- "Why is my `research/` folder empty after cloning on a new machine?"
- "Remove the link to my notes from this repo"
- "I renamed `old-research/` to `research/` in my private-repo and now everything's broken"

## Check first

Before suggesting commands:

```sh
repolink version    # confirm installed + which version
```

If not on PATH: `curl -sL https://raw.githubusercontent.com/thesatellite-ai/repolink-go/main/install.sh | sh`.

This skill is written against **v0.1.0**. If `repolink version` reports
older, warn the user and point at the release page.

## Core mental model (commit to memory)

- **Private-repo** = the *source* side. One per user typically. A
  regular git repo full of folders like `research/`, `prompts/`,
  `templates/`, `codeskill/`, etc. Committed to some remote (or local
  only — either works).
- **Consumer repo** = any GitHub repo that wants a folder from the
  private-repo visible locally. Each consumer has zero or more
  symlinks pointing *into* the private-repo clone.
- **`setup`** registers a private-repo with repolink on this machine.
  Run once per clone (`cd <private-repo> && repolink setup`).
- **`link` / `sync` / everything else** runs inside a **consumer** repo.
  The `<src>` arg to `link` is always **relative to the active profile's
  dir** (i.e. the private-repo root).
- **Every mutation has `--dry-run`** and a reversible path. Prefer
  `--dry-run` first when unsure.

## Command cheat-sheet

```
repolink                          # bare = sync current consumer repo (headline UX)
repolink setup                    # register current dir as a private-repo
repolink link <src> [dest]        # add one mapping + symlink
                                  #   dest follows ln -s rules:
                                  #     existing dir or trailing slash → place inside
                                  #     otherwise → dest names the link file
repolink link --force             # clobber an existing real FILE at target (refuses dirs)
repolink link --no-gitignore      # skip the auto .gitignore update
repolink sync [--repo <url>]      # create missing symlinks for active mappings
repolink unsync                   # remove symlinks in current repo (DB unchanged; sync restores)
repolink status [--long]          # read-only: mappings + live fs state for current repo
repolink verify [--long]          # drift report across all mappings in active profile
repolink state [--json]           # full machine-state snapshot (config + every profile's db)
repolink meta [rename "<new>"]    # show or edit the private-repo's display_name

repolink unlink <id|name>         # soft-delete (state=trashed, fs unchanged)
repolink unlink --all-in-repo --yes   # bulk soft-delete every active/paused mapping here
repolink cleanup [--yes]          # hard-remove fs symlinks for trashed rows (never targets)
repolink pause  <id|name>         # active → paused (symlink gone, row preserved)
repolink resume <id|name>         # paused → active (symlink back)

repolink map list [--state live|active|paused|trashed|all] [--repo <url>] [--long]
repolink map purge <id|--all> --yes   # HARD delete trashed rows
repolink map mv <old> <new> [--exact] --yes   # bulk rename source_rel + refresh symlinks

repolink config                   # show resolved active config
repolink config --list            # every profile + default
repolink config --set <key> <val> # scalar keys only
repolink config --add-profile <n> --dir <path>
repolink config --rename-profile <old>,<new>
repolink config --add-scan-root <path> / --remove-scan-root <path>

repolink reset --profile <n> --yes      # remove one profile + its repo.db
repolink reset --all --yes              # nuke config + every profile's repo.db
                                        # (both refuse if live mappings exist; --force overrides)

# Global flags on every command:
#   --profile / -p <name>[,<name>]  override default_profile (comma = multi-source)
#   --json                           machine-readable envelope {ok, version, data} or {ok:false, error}
#   --non-interactive                never prompt (implied by --json)
#   --dry-run                        preview plan, no mutations
#   --yes                            skip confirmations on mutators
```

## Workflows

### 1. Fresh machine — first time setting up repolink

```sh
# Install:
curl -sL https://raw.githubusercontent.com/thesatellite-ai/repolink-go/main/install.sh | sh

# Clone the private-repo (manual step — repolink doesn't do this):
git clone <private-repo-url> ~/private-repo

# Register it:
cd ~/private-repo
repolink setup          # uses CWD, zero flags needed

# For each consumer repo that already has a .repolink.jsonc pin:
cd /path/to/consumer
repolink                 # bare sync — materializes every mapping
```

### 2. Add a new mapping (link a private-repo folder into a consumer)

```sh
cd /path/to/consumer-repo
repolink link research research             # private-repo/research → ./research
repolink link templates/go docs/            # existing dir → symlink placed inside: docs/go
repolink link prompts/summary.md prompt.md  # file mapping (auto-detects kind)
repolink status                              # confirm
```

If the user did `ln -s` manually first: `repolink link --force <src> <dest>`
will clobber the real file (refuses real dirs — too dangerous).

### 3. Remove all links from the current consumer repo

```sh
repolink unlink --all-in-repo --yes     # soft-delete all active/paused
repolink cleanup --yes                  # remove the symlink files
# → Source dirs in private-repo still intact (hard rule: never deletes targets).
```

If they want it nuclear (also drop the DB rows): add `repolink map purge --all --yes` after cleanup.

### 4. Rename a source folder that's linked into multiple consumers

```sh
cd ~/private-repo
mv old-name new-name                       # user does the actual rename
repolink map mv old-name new-name --yes    # rewrites DB rows + refreshes symlinks in CWD's consumer
# For every OTHER consumer repo that referenced old-name:
cd /path/to/other-consumer && repolink     # sync picks up the new path
```

### 5. Debug "why is this folder missing / broken"

```sh
repolink verify              # drift report, read-only
repolink status --long       # current repo's mappings + fs state + detail
repolink state --json        # full snapshot — pipe to jq for AI / inspection
repolink map list --state all   # include trashed rows so nothing is hidden
```

### 6. Nuclear reset (wrong setup, start over)

```sh
# In each consumer repo first, if any symlinks exist:
repolink unsync --all --yes

# Then nuke:
repolink reset --all --yes
# Re-run setup from the correct private-repo root:
cd /path/to/actual/private-repo
repolink setup
```

## Safety invariants (never violate)

- **Never deletes symlink *targets*.** Sources inside `private-repo/`
  are user-owned. `cleanup`, `unsync`, `unlink`, `map purge`, `reset`
  touch symlink files only. If a command would traverse into a source
  dir, it's a bug — refuse.
- **Soft-delete is default.** `unlink` and `map remove` flip
  `state=trashed` without touching fs. `cleanup` is the separate
  fs-remove step. Two-step by design — gives a reversal window via
  `map restore`.
- **`--dry-run` every destructive command you're unsure about.**
- **`reset` is one-way.** Refuses if live mappings exist. `--force`
  only when the user explicitly acknowledges consumer symlinks may
  dangle.
- **Never `rm -rf` inside a private-repo.** `repolink reset` handles
  cleanup; raw rm risks wiping sources.

## Decision guide

- **Add a mapping** → `link` (one-shot: DB row + fs symlink atomically)
- **Multiple consumers linking same source** → link in each separately. Each consumer gets its own DB row.
- **Source folder moved** → `map mv` (bulk rewrite + refresh symlinks in CWD's consumer; sync in others)
- **Temporarily remove symlinks, keep DB** → `unsync` (sync restores)
- **Experiment without a mapping** → `pause` (row preserved, symlink gone; `resume` to restore)
- **Remove a mapping permanently** → `unlink` → `cleanup`
- **Hard-delete a trashed row + log entry** → `map purge` (irreversible)
- **Start over from scratch** → `reset --all --yes` (nuclear)

## `--json` output shape (for scripting / agent use)

Every read command supports `--json`:

```json
{ "ok": true, "version": "v0.1.0", "data": { ... } }
```

Errors:

```json
{ "ok": false, "error": { "code": "COLLISION", "message": "...", "context": {...} } }
```

Stable error codes: `COLLISION`, `UUID_AMBIGUOUS`, `UUID_NOT_FOUND`,
`CONFIG_INVALID`, `PROFILE_UNKNOWN`, `DIR_NOT_FOUND`, `SOURCE_MISSING`,
`TARGET_CLOBBER`, `NOT_A_SYMLINK`, `DB_LOCKED`, `DB_MIGRATE`, `UNKNOWN`.

When an agent needs to parse output: always pass `--json` and inspect
`ok` first. `state --json` is the one-call discovery endpoint.

## Auto-gitignore behavior

`link` / `sync` / `unlink` / `pause` / `resume` automatically manage a
marker-bounded block in the consumer repo's `.gitignore`:

```
# BEGIN repolink (managed — do not edit)
/research
/tools
# END repolink
```

User lines outside the block are preserved. `link --no-gitignore` per-op
opt-out. Empty block = removed entirely (including a solo `.gitignore`
file if nothing else is in it).

## Install path for this skill

```sh
# Manual drop-in (Claude Code):
mkdir -p ~/.claude/skills/repolink
curl -sL https://raw.githubusercontent.com/thesatellite-ai/repolink-go/main/skills/SKILL.md \
  -o ~/.claude/skills/repolink/SKILL.md
```

## More detail

Repo: https://github.com/thesatellite-ai/repolink-go — README covers
install + quickstart; the source is the ultimate reference for flag
semantics.
