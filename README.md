# repolink

Keep your research, prompts, plans, snippets, and templates in one **central
private-repo** and surface them inside every GitHub project that needs them —
without copy-paste duplication, fragile submodules, or hand-rolled `ln -s`
that breaks on every fresh clone.

```
private-repo/
├── research/       ─┐
├── prompts/         │    symlinks travel with each consumer repo
├── codeskill/       │    via a committed .repolink.jsonc pin
└── templates/       ┘
                      │
                      ▼
project-a/research    (symlink → private-repo/research)
project-b/prompts     (symlink → private-repo/prompts)
project-c/notes       (symlink → private-repo/codeskill)
```

Mappings live in a SQLite `repo.db` at the private-repo root — committed +
portable via git. Every machine just needs to know where its local clone is.

## 30-second example

```sh
# Register the private-repo (once per machine):
cd ~/private-repo
repolink setup

# Inside any GitHub project:
cd ~/work/my-project
repolink link research docs/notes
# → created: docs/notes → symlink to ~/private-repo/research
# → added:   DB row in ~/private-repo/repo.db
# → added:   /docs/notes to .gitignore (managed block)

# On a fresh clone of my-project on another machine (after repolink setup):
repolink
# → every mapping materialized, no manual ln -s
```

## Install

macOS / Linux:

```sh
curl -sL https://raw.githubusercontent.com/thesatellite-ai/repolink-go/main/install.sh | sh
```

Or build from source: `git clone … && task install`.

## Why you'd use this

- **AI / coding context reuse** — have your prompt library, research notes,
  or skill definitions show up inside every project directory so your editor
  and AI agent see them in context.
- **Personal knowledge base that follows your code** — keep evergreen notes
  in one place, make them visible alongside the specific project they're
  about, without duplicating files.
- **Template injection across many repos** — skeleton docs, CI snippets,
  `.editorconfig`, any shared boilerplate managed in one source, reflected
  everywhere.
- **Team knowledge cross-pollination** — one "reference" repo feeds many
  service repos; updates in one place propagate to all consumers.
- **Zero-config on fresh clones** — commit a `.repolink.jsonc` pin and any
  machine running `repolink` gets every symlink back.

See [docs/USE-CASES.md](./docs/USE-CASES.md) for detailed scenarios, and
[docs/USAGE.md](./docs/USAGE.md) for the full command reference.

## Quickstart

```sh
# 1. Register your private-repo clone (once per machine):
cd /path/to/private-repo
repolink setup

# 2. Inside any consuming repo, add mappings:
cd /path/to/some-project
repolink link research docs/                 # symlinks entire research/ into docs/
repolink link templates/go-ci .github/       # injects CI templates
repolink link prompts/summarize ./SUMMARY.md # single file

# 3. On a fresh clone of the same project elsewhere:
repolink                                     # bare form = auto-detect + sync
```

## Everyday commands

```sh
repolink                        # sync the current repo (headline UX)
repolink status                 # read-only view of mappings + live fs state
repolink link <src> [dest]      # add one mapping + symlink
repolink unlink <id|name>       # soft-delete (no fs change)
repolink cleanup --yes          # remove fs symlinks for trashed mappings
repolink pause <name>           # active → paused (symlink gone, row kept)
repolink resume <name>          # paused → active (symlink back)
repolink map list               # list mappings
repolink verify                 # drift report (read-only)
repolink state --json           # full machine-state snapshot (for scripts / AI)
repolink config --list          # every profile + default
repolink reset --all --yes      # nuclear: drop every profile + repo.db
```

Every mutating command supports `--dry-run`. Every read command supports
`--json`. Full reference: [docs/USAGE.md](./docs/USAGE.md).

## Safety

- **Never deletes symlink targets.** `cleanup`, `unlink`, `map purge`, `reset`
  only touch the symlink file itself — your source folders in the private-repo
  are never traversed.
- **Soft-delete by default.** `unlink` flips a mapping to `trashed`; the fs
  removal is a separate opt-in step (`cleanup`). Always reversible via
  `map restore` until purged.
- **`--dry-run` on every mutator.** Preview before committing.

## For AI agents

There's a Claude Code–compatible skill at
[`skills/repolink/SKILL.md`](./skills/repolink/SKILL.md). Install:

```sh
SKILL_BASE_URL=https://github.com/thesatellite-ai/repolink-go/tree/main \
  npx skill skills/repolink
```

Or manually:

```sh
mkdir -p ~/.claude/skills/repolink
curl -sL https://raw.githubusercontent.com/thesatellite-ai/repolink-go/main/skills/repolink/SKILL.md \
  -o ~/.claude/skills/repolink/SKILL.md
```

Claude Code will then pick up repolink on the right prompts ("symlink this
into", "why is this folder missing", etc.) and run the CLI via its Bash tool.

## Learn more

- **[docs/USE-CASES.md](./docs/USE-CASES.md)** — detailed pain points +
  real-world scenarios + who this is for
- **[docs/USAGE.md](./docs/USAGE.md)** — comprehensive command reference
  with examples + workflows
- **[skills/repolink/SKILL.md](./skills/repolink/SKILL.md)** — Claude Code
  skill (user-facing CLI usage guide for AI agents)

## Status

v0.1.0 — all core commands shipped, 9 test packages green. See
[releases](https://github.com/thesatellite-ai/repolink-go/releases).

## License

TBD.
