# repolink

Private-repo ↔ GitHub repo symlink manager. Keep research, plans, and snippets
in one central `private-repo/` and symlink folders into each consuming GitHub
repo. Mappings travel with `private-repo/` via git — every machine just needs
to know where its local clone lives.

## Install

macOS / Linux:

```sh
curl -sL https://raw.githubusercontent.com/thesatellite-ai/repolink-go/main/install.sh | sh
```

## Quickstart

```sh
# 1. Register your private-repo clone on this machine (once):
cd /path/to/private-repo
repolink setup

# 2. Inside any consuming repo, add a mapping:
cd /path/to/some-github-repo
repolink link research-folder docs
#   → symlinks private-repo/research-folder into docs/research-folder
#   → records the mapping in repo.db (lives inside private-repo/)

# 3. On a fresh clone of the consumer repo:
repolink          # auto-detects repo, creates every missing symlink
```

## Everyday commands

```sh
repolink                        # sync the current repo (bare alias)
repolink status                 # read-only view of mappings + live fs state
repolink link <src> [dest]      # add one mapping + symlink
repolink unlink <id|name>       # soft-delete (no fs change)
repolink cleanup --yes          # remove fs symlinks for trashed mappings
repolink pause <name>           # active → paused (symlink gone, row kept)
repolink resume <name>          # paused → active (symlink back)
repolink map list               # list mappings
repolink map mv <old> <new>     # rename a source (prefix match by default)
repolink verify                 # read-only drift report
repolink meta rename "<name>"   # edit the portable display_name
repolink config --list          # show every machine profile
repolink state --json           # full machine-state snapshot (for scripts / AI)
```

Every mutating command supports `--dry-run`; every read command supports `--json`.

## Docs

- Full spec: [docs/PROBLEM.md](./docs/PROBLEM.md)
- QA strategy: [docs/VERIFICATION.md](./docs/VERIFICATION.md)

## License

TBD.
