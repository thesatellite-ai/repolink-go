# Use cases

Who repolink is for, the pain it kills, and concrete scenarios.

## The pain

You've accumulated content that you want **available inside multiple
project repos simultaneously**:

- Research notes on a topic multiple projects touch
- A prompt library you use across every AI-assisted repo
- Templates (CI config, editor settings, skeleton docs) that should live in
  every new repo you start
- Design docs that reference multiple service repos
- A "skills" or "snippets" folder your AI agent should see as it works on
  any of your projects

Three bad defaults you fall into without this tool:

### 1. Copy-paste duplication

You copy the folder into each project. A month later:

- You update one copy. The others silently diverge.
- You can't remember which copy is canonical.
- Your AI agent sees whichever copy is in the current repo, not the best one.
- Cleanup means hunting across every repo.

### 2. Git submodules

Heavyweight for what's usually a single folder. On every fresh clone:

- Teammate (or future-you) forgets `--recurse-submodules`, ends up with an
  empty directory.
- Pinning to specific submodule commits adds review overhead nobody wants.
- Submodules are repo-level; you wanted folder-level granularity.

### 3. Hand-rolled `ln -s`

Works once on your primary machine. Then:

- You clone on a new laptop. Where did the symlinks go? What paths did you
  use? Time to re-read three months of shell history.
- The symlinks aren't tracked. They're invisible to `git status` unless you
  remember to `.gitignore` them.
- Renaming the source folder breaks every link silently.
- No audit trail. No "which project uses this folder?" query.

## The repolink model

One **private-repo** — a regular git repo containing all your shared content
as folders (`research/`, `prompts/`, `templates/`, `codeskill/`, whatever).
Committed to a remote of your choice (GitHub private, self-hosted Gitea,
or local-only — all work).

A SQLite `repo.db` at the private-repo root records every mapping:
*"folder X inside this private-repo is symlinked into GitHub repo Y at
path Z under the filename W."* The DB travels with the private-repo via
git, so any machine that clones the private-repo has the full picture.

Each **consumer repo** (any GitHub project you want content visible in)
gets a tiny `.repolink.jsonc` pin file committed. That pin tells
repolink which private-repo this consumer is drawing from (matched by
display name or UUID — survives profile renames).

On a fresh machine:

1. Install `repolink` (one curl)
2. Clone your private-repo anywhere
3. `cd private-repo && repolink setup`
4. In any consumer repo: `repolink` — every symlink rematerializes

That's the whole flow. No memory. No YAML. No per-machine scripts.

## Real-world scenarios

### Scenario: solo developer with an AI prompt library

You've curated `~/private-repo/prompts/` over months — refactoring prompts,
bug-investigation prompts, docstring rewriters, etc. You want your AI
editor to see them inside every coding project.

```sh
cd ~/private-repo && repolink setup
cd ~/code/project-a && repolink link prompts .prompts
cd ~/code/project-b && repolink link prompts/refactor ./REFACTOR-PROMPT.md
# ... continue for each project that benefits from prompts in scope
```

Update a prompt once in `~/private-repo/prompts/` → every project
instantly sees the update. No staged commits, no per-repo sync.

### Scenario: research notes per topic, shared across implementation repos

You're working on three distributed-systems projects that all touch the
same literature. Your notes live in `~/private-repo/research/consensus/`.

```sh
cd ~/code/raft-impl && repolink link research/consensus docs/literature
cd ~/code/paxos-sim && repolink link research/consensus docs/literature
cd ~/code/blog       && repolink link research/consensus content/notes
```

Open any of the three in your editor → the literature is one folder away.
Update the notes once → all three projects see the revision.

### Scenario: boilerplate + CI templates

You maintain `~/private-repo/templates/ci/go/` with a battle-tested CI
pipeline. Every Go repo you start should have it:

```sh
cd ~/code/new-go-project && repolink link templates/ci/go .github/workflows/
```

Upgrading your CI template means editing it in one place and committing.
Every repo that linked it gets the new version on the next `sync`.

### Scenario: team knowledge-base cross-pollination

Your team has an internal `docs-repo` acting as the private-repo. Each
service repo links the relevant sub-folder:

- `services/billing` links `docs-repo/apis/billing/`
- `services/auth` links `docs-repo/apis/auth/`
- `services/notifications` links `docs-repo/apis/billing/` (+ auth)

Docs change in one place. Every service's repo has up-to-date API refs
inline. New team members clone a service, `repolink setup` the docs-repo
clone, run `repolink`, and every ref folder appears.

### Scenario: multi-machine dev workflow

You use a MacBook for morning work, a Linux workstation in the afternoon.
Both have the same private-repo cloned. Both have the same consumer repos
checked out. Neither has ever manually run `ln -s`.

Set up each machine once:

```sh
cd ~/private-repo && repolink setup
```

Then any consumer repo with a `.repolink.jsonc` pin works immediately:

```sh
cd ~/code/any-project && repolink
```

Mappings defined on one machine are visible on the other (via the
committed `repo.db`). Renames propagate automatically.

### Scenario: "I messed up, start over"

Experimenting went wrong. You want everything clean:

```sh
# In every consumer repo that had symlinks:
cd <consumer> && repolink unsync --all --yes   # remove symlinks, keep DB

# Then:
repolink reset --all --yes                     # drop all config + every repo.db
# Source folders in the private-repo — untouched.
```

Start fresh with `repolink setup`.

## Who this is for

Good fit if you:

- Keep a personal or team knowledge base in one git repo and want bits of
  it visible in many project repos
- Work across multiple machines with the same content setup
- Use an AI coding agent that benefits from having reference material in
  the project directory
- Want refactor-friendly shared content (rename the source, every
  consumer follows)
- Prefer one place to define + audit what's shared where

Bad fit if you:

- Want a general-purpose symlink manager for arbitrary paths (this is
  tightly scoped to the private-repo model)
- Need the shared content actually committed to each consumer (use
  submodules or subtree)
- Are managing package/library dependencies (use your language's package
  manager)
- Need multi-writer conflict resolution on the mappings DB (current
  design is single-writer per private-repo clone; cloud backend is on the
  roadmap but not v0.1)

## What repolink is not

- Not a dependency manager (npm / go-modules / pip do that)
- Not a submodule replacement in the strict sense (submodules track a
  specific commit; repolink symlinks the working-tree content)
- Not a content delivery system (the symlink always points at your local
  private-repo clone — nothing is fetched over the network)
- Not opinionated about private-repo layout — folders and files, whatever
  you like. repolink just manages the symlinks pointing into them.

## Going deeper

- [USAGE.md](./USAGE.md) — every command, detailed
- [skills/repolink/SKILL.md](../skills/repolink/SKILL.md) — installable
  Claude Code skill
