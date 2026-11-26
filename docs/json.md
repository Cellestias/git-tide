# Machine-readable output (`--json`)

Every command that reads something, and every command that changes something, can print JSON for programs: the VS Code extension, scripts, AI agents. This page is the contract.

## Rules

- **stdout is only the JSON document.** Progress, notes and warnings go to stderr, so `gtide status --json | jq` works. For a command that changes something the whole human log (the progress lines and the success line) goes to stderr too.
- **Failure is the exit code**, not a JSON error object, and **stdout is then empty**: nothing half-written can be parsed. The message and the `hint:` line are on stderr. Exit codes are listed in the [README](../README.md#exit-codes). (Two exceptions print the document and still exit non-zero, because the document is the answer: `doctor --json` exits 1 when a check fails, and `config validate --json` exits 3 when the file is not valid.)
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
| `gtide security --json` | object |
| `gtide secrets scan --json [--history]` | object |
| `gtide doctor --json` | object |
| `gtide pr list --json [--state] [--author] [--assignee] [--reviewer] [--limit]` | array |
| `gtide pr view <id> --json` | object |
| `gtide pr status --json` | object |
| `gtide pr diff <id> --json` | object |
| `gtide pr comments <id> --json` | object |
| `gtide pr commit <id> <sha> --json` | object |
| `gtide pr options --json` | object |
| `gtide config show --json` (or `gtide config --json`) | object |
| `gtide config validate --json` | object |
| `gtide remote show --json` (or `gtide remote --json`) | object |

And, for the commands that change something, `--json` on: `commit`, `save`, `amend`, `undo`, `pick`, `revert`, `restore`, `fetch`, `push`, `pull`, `update`, `create`, `change`, `pr` (opening a request), `review`, `continue`, `abort`, `tag create`, `tag push`, `tag delete`, `branch delete`, `branch rename`, `stash`, `stash pop`, `stash drop`, `remote add`, `remote set-url`, `init` and `clone`. They share one envelope, described under [Actions](#actions).

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

### security

```json
{
  "provider": "github",
  "repo": "acme/tool",
  "level": "high",
  "alerts": [
    {
      "source": "dependabot",
      "number": 1,
      "severity": "critical",
      "title": "Prototype pollution in lodash",
      "package": "npm:lodash",
      "path": "package.json",
      "line": 0,
      "fixed_in": "4.17.21",
      "url": "https://github.com/acme/tool/security/dependabot/1"
    }
  ],
  "counts": { "critical": 1, "high": 0, "medium": 0, "low": 0 },
  "fixes": [
    { "path": "package.json", "package": "npm:lodash", "severity": "critical", "alerts": 5, "upgrade_to": "4.18.0", "unfixed": 0 }
  ],
  "sources": [
    { "source": "dependabot", "status": "ok", "message": "", "open": 1 },
    { "source": "code-scanning", "status": "not_enabled", "message": "", "open": 0 }
  ],
  "push": { "would_block": true, "blockers": 1, "unverified": false },
  "truncated": false
}
```

- `alerts` are the open ones, most severe first. `source` is `dependabot` or `code-scanning`; `severity` is `critical`, `high`, `medium` or `low` (a code scanning finding without a security severity is mapped from its rule: error is high, warning medium, note low). `path` is the file the alert is about, empty when the provider gives none; `line` is 0 when unknown; `package` and `fixed_in` are empty for code scanning.
- `fixes` is what to do about the Dependabot alerts, one entry per package and manifest, most severe first: `alerts` is how many it has, `upgrade_to` the lowest version that fixes all of those that have a patched version (empty when none has), and `unfixed` how many have no patched version yet. Code scanning findings are not in it, since they are not a dependency to upgrade.
- `level` is the configured `security.level` (`off`, `low`, `medium` or `high`). The command works at every level.
- `sources` says how each source went: `ok`, `not_enabled` (the repository does not use it: nothing to list, not a failure) or `error` (it could not be read; `message` says why); `open` is how many alerts that source has. `counts` always has the four severities.
- `push` says what the configured level would do to a push from here: `would_block` is true when alerts that the level blocks are open (`blockers` of them) or, at `high`, when a source could not be read (`unverified`). It does not know which files the push will change, so it is the verdict before the exemption for a fix. When every source is `not_enabled`, nothing is being checked: the document is valid and `alerts` is empty, but that is not a clean bill of health.
- If **no** source could be read the command fails (exit 5) instead of printing an empty document, so "no alerts" and "could not look" are never confused. A failure of only one source still prints the document, with that source as `error`.
- `truncated` is true when there are more alerts than Tide reads (1000 per source).
- GitHub only: on another provider the command fails with exit 3.

### secrets scan

```json
{
  "disabled": false,
  "scanned": 120,
  "skipped": 3,
  "files": [
    { "path": "src/config.js", "line": 12, "rule": "github_token", "tracked": true }
  ],
  "history": [
    { "commit": "1a2b3c...", "date": "2026-11-15T12:00:00+01:00", "path": "src/old.js", "line": 3, "rule": "aws_access_key_id", "in_files": false }
  ],
  "commits": 300,
  "truncated": false
}
```

- **No finding ever carries the matched text.** Only the file, the line and the rule name.
- `files` are the likely secrets in the files as they are now. `tracked` is false for a file that is not committed (and not ignored, or it would not be scanned).
- `history` is empty unless `--history` is given. `commit` is the full hash of the commit that added the line, `line` is its number in the file as that commit left it, and `in_files` is true when the same rule still matches in that file now, so the secret is in the work tree too and not only in the history. `commits` is how many commits were read.
- `disabled` is true when `secrets.enabled` is false in `.tide.yml`: nothing was scanned and the lists are empty.
- `skipped` counts files left out (binary, over 1 MiB, a line too long to read, symlinks). `truncated` is true when there are more files or findings than the scan reads (20 000 files, 500 findings).
- The exit code is 0 even with findings; with `--fail` it is 4 when there are any.

### doctor

```json
{
  "ok": false,
  "warnings": 1,
  "failures": 1,
  "checks": [
    { "level": "ok", "name": "git", "detail": "git version 2.45.0", "hint": "" },
    { "level": "warn", "name": ".env", "detail": "is not in .gitignore: `gtide save` or `git add -A` would commit it", "hint": "add .env to .gitignore" },
    { "level": "fail", "name": "secret.tide.yml", "detail": "is tracked by Git", "hint": "run `git rm --cached secret.tide.yml` and commit" }
  ]
}
```

- `level` is `ok`, `warn` or `fail`; `ok` at the top is true when no check failed. The exit code is 1 when one did, as in the text form, **and the document is still printed**: read stdout whatever the exit code.
- The check names are stable: `git`, `repository`, `config`, `remote`, `provider`, `secret.tide.yml`, `provider token`, `.env`, `secret scanning`, `security`, `base branch`, the hooks (`hook pre-commit`...), and `gtide on PATH`. Which ones are present depends on the repository.

### pr list, pr view, pr status

A request (a pull request on GitHub, a merge request on GitLab) is the same object in all three:

```json
{
  "number": 42,
  "title": "Add authentication",
  "state": "open",
  "draft": false,
  "url": "https://github.com/acme/tool/pull/42",
  "author": "ana",
  "assignees": ["bia"],
  "requested_reviewers": ["caio"],
  "head": "feature/login",
  "base": "main",
  "head_repo": "acme/tool",
  "created_at": "2026-11-01T10:00:00Z",
  "updated_at": "2026-11-02T11:30:00Z"
}
```

- `state` is `open`, `closed` or `merged`. `requested_reviewers` are the reviews still asked for. `head_repo` is empty when unknown or deleted.
- `pr list --json` is an array of these, newest first. A list cut at the provider's cap says so on stderr.
- `pr status --json` is `{ "provider", "noun", "repo", "branch", "request", "review", "warnings" }`: `request` is the request of the current branch, or `null`; `review` is `null` when there is no request or the reviews could not be read, and then `warnings` says why.
- `pr view <id> --json` is the request plus `provider`, `noun`, `repo`, `fork` (the source is another repository), `body` (the description, cut at 20 000 characters, with `body_truncated`), `review`, `files` (`path`, `old_path`, `status`, `additions`, `deletions`, `has_stats`), `commits` (`sha`, `subject`, `author`, `date`, oldest first), `viewer` (the login of the token's owner, empty without a token), `labels` (`name`, `color`, `description`; the request has them too in the list and the status), `files_truncated`, `commits_truncated` and `warnings`. What could not be read is `null` (the review) or `[]` (a list), and a warning names it.
- `pr diff <id> --json` is `{ "number", "head_sha", "truncated", "files": [...] }` with, for each file, `path`, `old_path` (a rename), `status` (`added`, `modified`, `deleted`, `renamed`), `binary` and `patch` (the hunks, from the first `@@` line; empty for a binary file). `head_sha` is the newest commit of the source branch. **GitHub only** for now.
- `pr comments <id> --json` is `{ "number", "truncated", "comments": [...] }`, oldest first. Each comment has `id`, `kind` (`conversation`, `inline` or `review`), `author`, `body` (cut at 20 000 characters, with `body_truncated`), `created_at`, `url`; inline comments also have `path`, `line`, `side` (`right` is the new version of the file, `left` the old), `diff_hunk`, `commit_sha`, `in_reply_to` (0 for the first of a thread) and `outdated` (the code changed since); a review has `review_state` (`approved`, `changes requested` or `commented`). **GitHub only** for now.
- `review` is `{ "state": "pending" | "approved" | "changes_requested", "approvals": [], "changes_requested": [] }`.

### config show, config validate, remote show

`config show --json` is `{ "found", "path", "secret_file_found", "local_patterns", "config": { ... } }`: the effective configuration, the file merged over the defaults, with the same keys as `.tide.yml` (`version`, `provider`, `remote`, `branches`, `update`, `commits`, `secrets`, `security`, `hooks`). The patterns of `secret.tide.yml` are local and never appear: only how many there are, in `local_patterns`. `found` is false when `.tide.yml` does not exist and the values are the defaults.

`config validate --json` is `{ "valid": bool, "path": "...", "errors": [] }`, with one string per problem, each starting with the field (`update.strategy: must be "merge" or "rebase", got "squash"`). The exit code is 3 when it is not valid, and the document is printed all the same.

`remote show --json` is `{ "configured": "origin", "configured_found": bool, "remotes": [ { "name", "url", "provider", "repo", "used" } ] }`. `provider` is `github`, `gitlab` or `unknown`; `repo` is `owner/name` on that provider, or empty; `used` marks the remote gtide publishes to (`remote` in `.tide.yml`). A password in a URL is hidden (`https://***@host/...`).

## Actions

A command that changes something prints, with `--json`, **one object with the same envelope**, after it has worked:

```json
{
  "ok": true,
  "action": "commit",
  "branch": "feature/x",
  "head": "1a2b3c4d5e6f...",
  "details": { "commit": "1a2b3c4d5e6f...", "subject": "feat: add login", "files": [ { "path": "a.go", "added": 10, "deleted": 0, "binary": false } ] }
}
```

- `ok` is always `true`: a failure prints nothing on stdout (see the rules above). Check the exit code first.
- `action` names the command: `commit`, `save`, `amend`, `undo`, `pick`, `revert`, `restore`, `fetch`, `push`, `pull`, `update`, `create`, `change`, `pr`, `review`, `continue`, `abort`, `tag create`, `tag push`, `tag delete`, `branch delete`, `branch rename`, `stash`, `stash pop`, `stash drop`, `remote add`, `remote set-url`, `init`, `clone`.
- `branch` and `head` are the branch checked out and the full hash of HEAD **after** the action (`branch` is `""` when HEAD is detached, `head` is `""` in a repository with no commit).
- `details` is read from the repository after the action, so it says what is true now. It is always an object. What it holds, per action:

| Action | `details` |
|---|---|
| `commit`, `save` | `commit` (full hash), `subject`, `files` (`path`, `added`, `deleted`, `binary`) |
| `amend` | as `commit`, plus `previous`: the hash it replaced |
| `undo` | `undone`: `{ commit, subject }` of the commit taken back, `staged`: its files, now staged |
| `pick` | `picked`: the commits copied (`commit` hash of the original, `subject`), `skipped`: those left alone (`commit`, `reason`) |
| `revert` | `reverted`: the commits taken back (`commit`, `subject`) |
| `restore` | `from` (what was asked), `staged`: the files now staged (`path`, `added`, `deleted`, `binary`) |
| `fetch` | `remote`, `upstream` (`origin/feature/x`, absent when the branch follows nothing), `ahead`, `behind` |
| `push` | `remote`, `upstream` (`origin/feature/x`), `forced` |
| `pull` | `upstream`, `strategy`, `before`, `after`, `changed` |
| `update` | `base`, `strategy`, `before`, `after`, `changed` |
| `create` | `branch`, `from`, `switched`, `pushed`, `tracking_remote` |
| `change` | `branch`, `previous`, `changed` |
| `pr` | `provider`, `noun`, `number`, `url`, `title`, `draft`, `head`, `base` |
| `review` | `branch` (`tide/review/pr-42`), `request`, `reset` |
| `continue` | `operation` (`merge`, `rebase`...), `in_progress` (a rebase can stop again on the next commit) |
| `abort` | `operation` |
| `tag create` | `name`, `commit`, `message`, `pushed` |
| `tag push` | `name`, `commit`, `remote` |
| `tag delete` | `name`, `remote` (also deleted there) |
| `branch delete` | `deleted` (the local branches that are really gone), `remote`, `forced` |
| `branch rename` | `old`, `new` |
| `stash` | `ref` (`stash@{0}`), `message`, `untracked`, `stashes` (how many there are now) |
| `stash pop`, `stash drop` | `ref`, `stashes` (how many are left) |
| `remote add`, `remote set-url` | `name`, `url` (a password is hidden), `provider` |
| `init` | `config` and `secret_file` (`path`, `created`), `gitignore` (`created`, `updated`), `provider`, `base`, `hook_changes`, `hooks_skipped`, `notes`, `remote` (`name`, `url`, `added`, or `null`) |
| `clone` | `path`, `url`, `branch`, `head`, `setup` (the same as `init`) |

- A prompt (`--force`, `tag push`, `branch delete`) is still asked on stderr unless `--yes` is given: a program should pass `--yes` for the ones it has the right to confirm, and nothing else.
- `--json` on `diff --patch`, `history show`, `review --diff` and the like prints its patch to stderr when the command is an action, because stdout is the document.

### pr commit, pr options

- `pr commit <id> <sha> --json` is `{ "number", "sha", "author", "name", "email", "date", "subject", "body", "body_truncated", "files_truncated", "files": [...] }`: one commit of a request with the hunks of each file (`path`, `old_path`, `status`, `additions`, `deletions`, `patch`, and `patch_missing` when the file changed but the provider gave no hunks). The commit does not have to be in the local repository. **GitHub only** for now.
- `pr options --json` is `{ "labels": [{ "name", "color", "description" }], "people": [...], "truncated" }`: what `pr edit` can use.

### pr edit, pr comment, pr approve, pr request-changes

`pr edit` has `details` `{ "provider", "number", "added_reviewers", "removed_reviewers", "added_assignees", "removed_assignees", "added_labels", "removed_labels" }` and `"action": "pr-edit"`. These take part in a review, so they print the [action](#actions) envelope: `pr comment` has `details` `{ "provider", "number", "comment_id", "url" }` and `"action": "pr-comment"`; `pr approve` and `pr request-changes` have `{ "provider", "number", "decision" }` (`approve` or `request_changes`) and `"action": "pr-review"`. They need the provider token with write access, and fail without printing anything on stdout when it is missing or the provider refuses (you cannot approve your own request on GitHub).

## Dry run: `--dry-run`

With `--dry-run` a command that changes something prints the same envelope, with a `dry_run` object, and changes nothing (see the [README](../README.md#dry-run---dry-run)):

```json
{
  "ok": true,
  "action": "save",
  "branch": "feature/x",
  "head": "1a2b3c4d5e6f...",
  "details": {},
  "dry_run": {
    "would": ["git add --all", "git commit -m feat: add login"],
    "stopped": ""
  }
}
```

- `dry_run` is **only present in a dry run**. Its presence is how a program knows that nothing happened.
- `would` is the list of steps that would run, in order, as text meant to be read: `git ...` for Git, `POST <address>` or `DELETE <address>` (no query string, no credentials) for the provider, `create <path>`, `add <entry> to <path>` and `hook <name>: installed` for files. It is `[]` when nothing would change.
- `stopped` is set (and omitted otherwise) when a step needs the result of an earlier one that was not made, so the list ends there. The exit code is still `0`.
- `branch` and `head` are the ones **now**, because nothing moved. `details` holds what the command can say without having run, and is often `{}`.
- A command that would be refused (protected branch, secret, wrong state) exits with its usual non-zero code, prints nothing on stdout and says why on stderr, exactly as without the flag.
- A reading command with `--dry-run` prints its usual document, with no `dry_run`.

## Checking the version

`gtide --version` prints the version (`gtide version v1.6.0`). `--json` on the commands above and `history --patch` need **v1.6.0 or later**; `--graph`, `--all-branches` and `history show --patch` need **v1.7.0 or later**; `gtide security` needs **v1.8.0 or later**; `gtide secrets scan` and `gtide doctor --json` need **v1.9.0 or later**; `--json` on `pr list`, `pr view`, `pr status`, `config`, `remote show` and on every command that changes something needs **v1.10.0 or later**; `pr diff`, `pr comments`, `pr comment`, `pr approve`, `pr request-changes`, `pr commit`, `pr edit`, `pr options`, `pr list --reviewer` and the `viewer`, `labels` and commit `author`/`date` of `pr view` need **v1.11.0 or later**; `pick`, `restore`, `revert` and `fetch` need **v1.12.0 or later**; `history --json` and `history show --json` exist since v1.4.0.
