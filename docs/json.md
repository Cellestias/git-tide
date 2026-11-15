# Machine-readable output (`--json`)

Read-only commands can print JSON for programs: the VS Code extension, scripts, AI agents. This page is the contract.

## Rules

- **stdout is only the JSON document.** Progress, notes and warnings go to stderr, so `gtide status --json | jq` works.
- **Failure is the exit code**, not a JSON error object. The message and the `hint:` line are on stderr. Exit codes are listed in the [README](../README.md#exit-codes).
- **An empty result is valid JSON**: `[]` for lists, an object with empty arrays otherwise. Never a sentence.
- **Lists are arrays, never `null`.** A missing relation (no upstream, no base) is `null`.
- **Changes are additive.** New fields can appear in a minor release; existing fields keep their name, type and meaning until a major release. Ignore fields you do not know.
- Dates are RFC 3339 (`2026-11-15T12:00:00+01:00`) unless noted. Hashes in `history` are abbreviated, except where the field says full.
- Text values (subjects, paths, messages) come from the repository and are not sanitised: treat them as untrusted when you render them.

## Commands

| Command | Shape |
|---|---|
| `gtide status --json` | object |
| `gtide diff --json [--staged] [--patch] [-f path]` | object |
| `gtide branch --json [--remote] [--hosted] ...` | array |
| `gtide history --json [...] [--patch] [--graph] [--all-branches]` | array |
| `gtide history show <commit> --json [--patch]` | object |
| `gtide history --summary --json` | array |
| `gtide history --compare <from> <to> --json` | object |
| `gtide tag --json [-n limit]` | array |
| `gtide stash list --json` | array |

### status

```json
{
  "branch": "feature/x",
  "detached": false,
  "head": "",
  "upstream": { "name": "origin/feature/x", "ahead": 1, "behind": 0, "known": true },
  "base":     { "name": "main", "ahead": 2, "behind": 0, "known": true },
  "operation": "",
  "clean": false,
  "files": [
    { "path": "a.txt", "index": "M", "worktree": " ", "staged": true, "unstaged": false, "untracked": false, "conflict": false }
  ]
}
```

- `branch` is `""` when HEAD is detached; `head` (short hash) is then set. `head` is omitted otherwise.
- `upstream` is `null` when the branch has none; `base` is `null` on the base branch itself or when the base cannot be found. `known: false` means Git could not count the commits (`ahead` and `behind` are 0).
- Distances are as of the last fetch: `status` makes no network call.
- `operation` is `merge`, `rebase`, `cherry-pick`, `revert` or `""`.
- `index` and `worktree` are the two letters of `git status --short` (`" "` for none). `from` (the old path) appears on renames.
- A file can be both `staged` and `unstaged`. `conflict` files are neither.

### diff

```json
{
  "scope": "worktree",
  "files": [ { "path": "a.txt", "added": 1, "deleted": 0, "binary": false } ],
  "patch": "diff --git a/a.txt b/a.txt\n..."
}
```

`scope` is `worktree` (against the index) or `staged` (against the last commit). `patch` exists only with `--patch`. Untracked files are not in a diff: use `status`.

### branch

```json
[
  {
    "name": "feature/x", "current": true, "remote_only": false,
    "updated": "2026-11-15T12:00:00+01:00", "author": "Ana", "email": "ana@example.com",
    "upstream": { "name": "origin/feature/x", "ahead": 0, "behind": 0, "gone": false },
    "base": { "name": "main", "ahead": 2, "behind": 0 },
    "protected": false, "merged": false,
    "hosted": { "protected": false, "request": 12 }
  }
]
```

- `upstream` is `null` when there is none and for remote-only branches. `base` is `null` on the base branch.
- `protected` is Tide's own rule (`.tide.yml`). `hosted` exists only with `--hosted`: the provider's view, where `protected` and `request` (the open pull/merge request number) are `null` when the provider could not be asked, and `request` is also `null` when there is none.

### history

```json
[
  {
    "hash": "1a2b3c4", "author": "Ana", "email": "ana@example.com",
    "date": "2026-11-15T12:00:00+01:00", "subject": "feat: add login",
    "files": [ { "path": "src/login.go", "added": 10, "deleted": 2, "binary": false } ],
    "patch": "diff --git a/src/login.go b/src/login.go\n...",
    "patch_truncated": true
  }
]
```

- `patch` and `patch_truncated` appear only with `--patch`. With `-f` the patch holds only that file's lines, following renames, so a file's history reads as the commits and their changes.
- A patch is cut at 256 KiB per commit; `patch_truncated` is then `true` (and absent otherwise).
- `history show` adds `body` and a full-length `hash`; with `--patch` it adds `patch` and `patch_truncated` too (a merge commit shows its changes against the first parent).

With `--graph` (which needs `--json`) each commit also carries its topology, for drawing a commit graph:

```json
{
  "hash": "1a2b3c4",
  "full_hash": "1a2b3c4d5e6f...",
  "parents": ["9f8e7d6c5b4a..."],
  "refs": [
    { "name": "feature/x", "kind": "branch", "current": true },
    { "name": "origin/feature/x", "kind": "remote", "current": false },
    { "name": "v1.5.0", "kind": "tag", "current": false }
  ]
}
```

- `parents` are full hashes: empty for the first commit, two for a merge. `refs` are the branches and tags that point at the commit. `kind` is `branch`, `remote`, `tag`, or `head` for a detached HEAD. `current` marks the checked-out branch. A remote's `HEAD` pointer is left out.
- With `--graph` the commits come newest first **with every parent after its children**, which is what a lane layout needs. Without it the order is Git's default (by date).
- `--all-branches` reads every local branch, remote-tracking branch and tag at once instead of one branch; it does not combine with `--branch`, `--remote`, `--base` or a range. Add `--merges` to keep merge commits in the graph.
- The filters (`--me`, `--member`, `--search`, `--type`, `--scope`, `--since`, `--until`, `-f`) still apply, and they remove commits from the list. To draw the whole graph and only *highlight* the matches, ask twice: once without filters for the topology, once with them for the hashes that match.

### tag

```json
[ { "name": "v1.5.0", "annotated": true, "date": "2026-11-14", "commit": "<full hash>", "message": "Release 1.5.0" } ]
```

`commit` is the commit the tag points at; an annotated tag is looked through.

### stash list

```json
[ { "ref": "stash@{0}", "date": "2026-11-15T12:00:00+01:00", "message": "On feature/x: half-done login form" } ]
```

## Checking the version

`gtide --version` prints the version (`gtide version v1.6.0`). `--json` on the commands above and `history --patch` need **v1.6.0 or later**; `--graph`, `--all-branches` and `history show --patch` need **v1.7.0 or later**; `history --json` and `history show --json` exist since v1.4.0.
