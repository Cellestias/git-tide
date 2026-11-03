# Git Tide

> **Move with the flow.**

Git Tide (`tide`) is a command-line tool that sits on top of Git and makes everyday workflows safer and more consistent for a team:

- **Update** a working branch from its base branch in one command, without risking uncommitted work.
- **Review** a GitHub Pull Request on a predictable local branch.
- **See** your branches at a glance: base, upstream, staleness, protection, open PRs.
- **Switch** branches without losing work, with partial names and `-` for the previous branch.
- **Start** new branches from a fresh base.
- **Open** a Pull Request from your current branch.
- **Guard** your repository with local hooks: Conventional Commits, secret scanning, and protected branches.

Git stays the source of truth. Tide runs the Git you already have, needs no server or database, and keeps working alongside plain `git` commands.

## Contents

- [Install](#install)
- [Quick start](#quick-start)
- [Commands](#commands)
- [Configuration](#configuration)
- [Authentication](#authentication)
- [What the hooks do](#what-the-hooks-do)
- [Exit codes](#exit-codes)
- [Security and limits](#security-and-limits)
- [Troubleshooting](#troubleshooting)
- [Development](#development)

## Install

Tide needs Git 2.30 or newer on your `PATH`. Nothing else: the release binaries are self-contained, so you do not need Go.

### Download a release

Get the archive for your system from the [Releases page](https://github.com/Cellestias/git-tide/releases/latest). Names look like `tide_<version>_<os>_<arch>`:

| System | Archive |
|---|---|
| macOS, Apple Silicon (M1 and newer) | `tide_<version>_darwin_arm64.tar.gz` |
| macOS, Intel | `tide_<version>_darwin_amd64.tar.gz` |
| Linux (Ubuntu, Debian, Fedora, ...), x86-64 | `tide_<version>_linux_amd64.tar.gz` |
| Linux, ARM64 (Raspberry Pi 4/5, AWS Graviton) | `tide_<version>_linux_arm64.tar.gz` |
| Windows, x86-64 | `tide_<version>_windows_amd64.zip` |
| Windows, ARM64 | `tide_<version>_windows_arm64.zip` |

Check the download against `checksums.txt` from the same release, then put `tide` on your `PATH`.

**macOS and Linux**

```bash
sha256sum -c --ignore-missing checksums.txt   # on macOS: shasum -a 256 -c --ignore-missing checksums.txt
tar -xzf tide_<version>_<os>_<arch>.tar.gz
sudo install -m 755 tide_<version>_<os>_<arch>/tide /usr/local/bin/tide
tide --version
```

If you downloaded the archive with a browser on macOS, Gatekeeper may block the unsigned binary. Remove the quarantine flag once: `xattr -d com.apple.quarantine /usr/local/bin/tide`. Downloading with `curl` does not set it.

**Windows (PowerShell)**

```powershell
(Get-FileHash .\tide_<version>_windows_amd64.zip -Algorithm SHA256).Hash   # compare with checksums.txt
Expand-Archive .\tide_<version>_windows_amd64.zip -DestinationPath .
New-Item -ItemType Directory -Force "$env:LOCALAPPDATA\Programs\tide" | Out-Null
Copy-Item .\tide_<version>_windows_amd64\tide.exe "$env:LOCALAPPDATA\Programs\tide\"
[Environment]::SetEnvironmentVariable("Path", "$env:Path;$env:LOCALAPPDATA\Programs\tide", "User")
# open a new terminal, then:
tide --version
```

Windows SmartScreen may warn about an unsigned program the first time; choose "More info", then "Run anyway". Tide's hooks are POSIX shell scripts, which [Git for Windows](https://gitforwindows.org) runs with its bundled shell, so install Git that way. Windows binaries are built and started on every release, but the full test suite runs on Linux and macOS only; treat Windows as supported on a best-effort basis and report problems.

### Build from source

Requires Go 1.26.10 or newer (a Go 1.21+ install downloads the right toolchain by itself).

```bash
git clone https://github.com/Cellestias/git-tide.git && cd git-tide
make install                    # builds and installs to /usr/local/bin (PREFIX=~/.local to change)
tide --version
```

The binary must be on the `PATH` seen by Git, because the installed hooks call `tide`. If it is missing, hooks print a warning and skip their checks (see [Troubleshooting](#troubleshooting)).

## Quick start

```bash
cd your-repo
tide init        # creates .tide.yml and installs the Git hooks
tide doctor      # checks that everything is set up
```

Then, in daily use:

```bash
tide create feature/login            # new branch from the fresh base
git commit -m "feat: add login form" # hooks validate the message and scan for secrets
tide push                            # publish the branch
tide update                          # bring the base branch into feature/login
tide pr                              # push and open a Pull Request
```

Commit `.tide.yml` so the whole team shares the same rules.

## Commands

Run `tide <command> --help` for the built-in help.

### `tide init`

Creates `.tide.yml` and installs the Git hooks.

```bash
tide init [--base <branch>] [--no-hooks]
```

- The default base branch is detected from `origin/HEAD`, then `main`, then `master`. Override it with `--base`.
- An existing `.tide.yml` is **never overwritten**.
- An existing Git hook is **preserved**: it is renamed to `<hook>.pre-tide` and still runs after Tide's checks pass. Tide refuses to proceed if a `.pre-tide` file already exists.
- Re-running `init` is safe; hooks that are already current are left alone.
- `--no-hooks` (or `hooks.manage: false`) skips hook installation.

### `tide change <branch>`

Switches to another branch safely. Alias: `tide switch`.

```bash
tide change feature/two-fa
tide change two-fa        # unique partial match
tide change -             # previous branch
tide change main --carry
```

- `<branch>` is an exact name, a case-insensitive part of a name that matches exactly one branch, or `-` for the previous branch. An exact name always wins over partial matches. If several branches match, Tide lists them and refuses.
- A branch that only exists on the remote (already fetched) is created locally with its upstream set.
- It refuses, without changing anything, while a merge or rebase is in progress, and when tracked files have uncommitted changes. Use `--carry` to let Git bring the changes along; Git still refuses if they would conflict, and nothing is lost either way. Tide never stashes for you.
- After switching it tells you what to do next: when the branch is behind its base (`tide update`), behind its upstream (`git pull`), when its upstream was deleted, or when it is a protected branch.
- It makes no network call. Remote branches are the ones you already fetched; refresh them with `tide branch --remote`.

### `tide branch`

Lists branches with Tide's view of them. Alias: `tide branches`.

```bash
tide branch                        # local data only, works offline
tide branch --remote --github      # + remote-only branches, GitHub protection and open PRs
tide branch --mine --sort name
tide branch --merged               # candidates for cleanup
tide branch --stale 30             # no commits for more than 30 days
```

```text
BRANCH         UPDATED       LAST AUTHOR  UPSTREAM  BASE     STATUS
* feature/new  2 hours ago   Ana          in sync   main ↑2  -
  feature/old  3 months ago  Bruno        none      main ↑1  -
  fix/done     5 days ago    Ana          none      main ↓1  merged
  main         1 hour ago    Ana          in sync   -        protected
```

| Column | Meaning |
|---|---|
| `UPDATED` | Date of the last commit on the branch. |
| `LAST AUTHOR` | Author of the last commit. Git does not record who created a branch, so this is the closest honest answer. |
| `UPSTREAM` | Ahead (`↑`) and behind (`↓`) against the upstream, `in sync`, `none` when the branch has no upstream, or `gone` when the upstream was deleted on the remote. |
| `BASE` | The base branch from `.tide.yml` with ahead/behind against it. `-` for the base itself or when it cannot be found. |
| `STATUS` | `protected` (in `.tide.yml`), `merged` (nothing the base lacks, and the base has moved on), `remote-only`. |
| `GITHUB` | Only with `--github`: `protected` on GitHub and the open Pull Request (`PR #12`) for the branch. Pull requests from forks are not matched. |

| Flag | Effect |
|---|---|
| `--remote` | Fetches the remote (`git fetch --prune`) and also lists branches that exist only there. This is the only network call besides `--github`. |
| `--github` | Adds the `GITHUB` column through the API. Public repositories work without a token; for private ones set `GITHUB_TOKEN`. If the API cannot be read, the column shows `?` and a warning is printed; the command still succeeds. |
| `--mine` | Only branches whose last author is your `git config user.email`. |
| `--stale <days>` | Only branches with no commits for more than that many days. |
| `--merged` | Only branches that are fully merged into their base. |
| `--sort date\|name\|ahead` | Newest first (default), by name, or by commits ahead of the base. |

Filters combine (they all must match). Progress messages go to stderr, so the table on stdout is safe to pipe.

### `tide create <branch>`

Creates a branch from an up-to-date base and switches to it.

```bash
tide create feature/two-fa                  # from the configured base, fetched first
tide create feature/two-fa --from release/1 # from another branch, tag, or commit
tide create feature/two-fa --local          # no network: use the local base
tide create feature/two-fa --track          # check out a branch that already exists on the remote
tide create feature/two-fa --push           # also push it and set the upstream
```

By default Tide resolves the base for the new name from `branches.base` (falling back to `default_base`), fetches it, and starts the branch at `<remote>/<base>`. Your local copy of the base branch is not touched, and the new branch has **no upstream** until you push it, so a plain `git push` cannot target the base by accident.

| Flag | Effect |
|---|---|
| `--from <ref>` | Start from this branch, tag, or commit instead of the configured base. A remote branch is fetched fresh; a tag, commit, or local-only branch is used as it is. |
| `--local` | Start from the local branch and make no network call. If the local branch does not exist, the last fetched `<remote>/<branch>` is used and the output says it was not refreshed. |
| `--track` | The branch must already exist on the remote: create a local branch from it and set the upstream. Cannot be combined with `--from`, `--local`, or `--push`. |
| `--push` | Push the new branch and set `<remote>/<branch>` as its upstream. If the push fails, the branch still exists locally. |
| `--no-switch` | Create the branch without checking it out; also allowed with uncommitted changes. |

`tide create` refuses (without changing anything) when the name is invalid or is a protected branch, the branch already exists locally, a merge or rebase is in progress, or, unless `--no-switch` is given, tracked files have uncommitted changes. Note that the base is resolved from the *new* branch name, so `feat/x` only picks up a `feature/*` mapping if you add a `feat/*` pattern.

### `tide update [branch]`

Brings the base branch into your current branch.

```bash
tide update            # base resolved from .tide.yml
tide update main       # use this base for this run only
```

What it does, in order:

1. Checks that the repository is in a safe state (see below).
2. Fetches the base branch from the configured remote.
3. Fast-forwards the local base branch to match. It never creates merge commits or discards commits on the base.
4. Integrates the base into the current branch with `merge` (default) or `rebase`, per `update.strategy`.
5. Leaves you on your original branch and reports whether anything changed.

`tide update` **stops before doing anything** when:

- HEAD is detached,
- tracked files have staged or unstaged changes (untracked files are fine; Git protects them itself),
- a merge, rebase, cherry-pick or revert is already in progress.

It never stashes, resets, or cleans for you. If the local base branch has diverged from the remote, or is checked out in another worktree, it stops and tells you to resolve that first.

On conflicts the repository is left in Git's normal conflict state. Resolve the files, `git add` them, then run [`tide continue`](#tide-continue-and-tide-abort), or give up with `tide abort`. Plain `git merge --continue` and `git rebase --continue` work too.

If you are already on the base branch, `tide update` simply fast-forwards it to the remote.

### `tide continue` and `tide abort`

When `tide update` (or any merge, rebase, cherry-pick or revert) stops on conflicts, these finish or undo it without you having to remember which operation it was.

```bash
# ...edit the conflicted files, then:
git add src/auth/login.go
tide continue          # finishes the merge/rebase/cherry-pick/revert
tide abort             # or: give up and go back to before it; asks [y/N], --yes skips
```

`tide continue` refuses while any file still has conflicts and lists them. Before continuing it runs the pre-commit checks, so a file staged with leftover `<<<<<<<` markers or a secret is rejected and the operation stays in progress. Git's prepared message is used as it is, with no editor. A rebase can stop again on the next commit: resolve it and run `tide continue` again. `tide abort` discards the conflict resolutions made so far, which is why it asks first.

### `tide review <PR>`

Checks out a GitHub Pull Request on a local review branch named `tide/review/pr-<number>`.

```bash
tide review 123
tide review '#123'
tide review https://github.com/acme/tool/pull/123
```

- Fetches `refs/pull/<n>/head`, so it works for PRs from forks.
- Creates the review branch, or fast-forwards it if it already exists.
- If the review branch has local commits that are not in the PR, Tide refuses. Re-run with `--reset` to discard them.
- Prints title, author, state, source and target branches, and a suggested `git diff` command.
- Requires a clean tracked working tree. A full PR URL must point to the configured remote's repository.
- Reads PR metadata from the GitHub API. Public repositories work without a token; private ones need `GITHUB_TOKEN`.

Return to your previous branch with `git switch <branch>`.

### `tide commit` and `tide save`

`tide commit` commits what is staged. `tide save` stages and commits in one step: the files you name with `-f`, or every change when you give none.

```bash
tide commit -m "feat(auth): add login form"
tide commit --type feat --scope auth -m "add login form"   # builds "feat(auth): add login form"
tide commit -a -m "fix: handle empty token"                # also stages tracked changes

tide save -m "feat: add login form"                       # every change, new files included
tide save -m "feat(auth): add login form" -f src/auth/login_form.tsx
tide save --type fix -m "handle empty token" -f api/token.go -f api/token_test.go
tide save -m "docs: update readme" -f docs/                # a directory stages everything under it
```

| Flag | Effect |
|---|---|
| `-m, --message` | The commit message (required). |
| `--type`, `--scope` | Build `type(scope): message` from `-m`. `--scope` needs `--type`. |
| `--body` | Commit body, after a blank line. |
| `-a, --all` | `tide commit` only: stage modifications and deletions of tracked files first. Untracked files are never added. |
| `-f, --file` | `tide save` only, repeatable: stage only this file or directory. Without it, every change is staged. Paths are relative to where you run the command and must be inside the repository. |

Before anything is committed, Tide checks that the branch is not protected, that the message follows your Conventional Commits rules, and that the staged lines hold no likely secrets (the same checks as the hooks, with friendlier errors). Your Git hooks still run, and Tide never uses `--no-verify`.

`tide save` without `-f` is like `git add --all`: modified, deleted, and new files that `.gitignore` does not exclude. Tide lists what it staged before committing, and the secret scan covers new files too. With `-f` it refuses when other files are already staged, so the commit holds exactly the files you named. If a check fails after staging, Tide restores the index and nothing stays staged. It also refuses during a merge or rebase, where plain `git commit` is the right tool.

### `tide push`

Pushes the current branch to the configured remote under the same name and sets it as upstream.

```bash
tide push            # publish the current branch
tide push --force    # after a rebase or amend; asks [y/N] (skip with --yes)
```

Refuses on a detached HEAD and on protected branches (`branches.protected`). Uncommitted changes are not pushed, and Tide warns when there are some. `--force` is `--force-with-lease`: it replaces the remote branch only if it still points where your last fetch saw it, so a teammate's newer commits are never overwritten silently. Your `pre-push` hook still applies. A plain `git push` keeps working too.

### `tide pr`

Pushes the current branch and opens a GitHub Pull Request.

```bash
export GITHUB_TOKEN=...          # needs pull-request write access
tide pr                          # shows a summary, asks [y/N]
tide pr --draft --yes
tide pr --base main --title "Add login" --body "Closes #12"
```

Flow:

1. Resolves the repository from the configured remote and the base branch (`--base`, else `branches.base`, else `default_base`).
2. Refuses on a detached HEAD, on the base branch, on a protected branch, or when the branch has no commits ahead of the base.
3. Fetches the base and shows the title, base, and commit count.
4. Asks for confirmation (skip with `--yes`). Declining pushes nothing and creates nothing (only the base branch is fetched).
5. Pushes the branch (your `pre-push` hook still applies) and creates the PR.

Defaults: the title is the commit subject when the branch has exactly one commit, otherwise the humanised branch name (`feature/login-page` becomes "Login page"). The body lists the commit subjects. If GitHub rejects the request after the push (for example, a PR already exists), the branch stays pushed and the error says so.

### `tide doctor`

Diagnoses the setup without changing anything: Git version, repository, configuration, remote, base branch, each hook, and whether `tide` is on the `PATH`. Problems are shown with a hint; it exits non-zero only on hard failures (no Git, not a repository, invalid configuration).

### `tide config`

```bash
tide config            # print the effective configuration (file merged over defaults)
tide config validate   # validate .tide.yml and report every problem with its field
```

## Configuration

Tide reads `.tide.yml` from the repository root. Omitted keys use the defaults below; unknown keys are rejected so typos do not pass silently.

```yaml
version: 1
remote: origin

branches:
  default_base: stage
  protected:
    - main
    - stage
  base:
    feature/*: stage
    fix/*: stage
    release/*: main

update:
  strategy: merge

commits:
  convention: conventional
  require_scope: false

secrets:
  enabled: true
  custom_patterns:
    - name: internal_token
      pattern: "corp_[A-Za-z0-9]{32}"

hooks:
  manage: true
```

| Key | Default | Meaning |
|---|---|---|
| `version` | `1` | Schema version. Only `1` is supported. |
| `remote` | `origin` | Remote used by `update`, `review`, and `pr`. |
| `branches.default_base` | `main` | Base for branches that match no pattern in `branches.base`. |
| `branches.protected` | `[main]` | Branches where local hooks reject direct commits and pushes. |
| `branches.base` | none | Map of branch pattern to base branch (see below). |
| `update.strategy` | `merge` | `merge` or `rebase`. |
| `commits.convention` | `conventional` | The only supported convention. |
| `commits.require_scope` | `false` | Require `type(scope):` instead of `type:`. |
| `secrets.enabled` | `true` | Scan staged changes for likely secrets. |
| `secrets.custom_patterns` | none | Extra detectors: `name` (lowercase, digits, `_`, `-`) and `pattern` (Go regular expression). At most 100. |
| `hooks.manage` | `true` | Whether `tide init` installs hooks. |

### Choosing a base branch

`branches.base` maps a branch pattern to its base. In patterns, `*` matches within one path segment and `**` matches across segments. When several patterns match, the **most specific wins**, regardless of the order in the file: an exact name beats any pattern, and among patterns the one with more literal characters wins.

```yaml
branches:
  default_base: stage
  base:
    feature/*: stage
    feature/infra-*: main            # beats feature/*
    feature/hotfix-payments: main    # exact name beats both
```

For a one-off exception, pass the base explicitly: `tide update main` or `tide pr --base main`.

## Authentication

`tide pr` needs a GitHub token with pull-request write access (a fine-grained token with "Pull requests: Read and write" and "Contents: Read" on the repository, or a classic token with the `repo` scope). `tide review` and `tide branch --github` only need one for private repositories. Pushing uses Git's own authentication, not this token.

Tide looks for the token in this order and uses the first it finds:

1. The `GITHUB_TOKEN` environment variable.
2. The `GH_TOKEN` environment variable.
3. A `GITHUB_TOKEN=` or `GH_TOKEN=` line in the **`.env` file at the repository root**.

Because the `.env` is per repository, each project can use the token of its own organisation:

```bash
# <repo>/.env
GITHUB_TOKEN=ghp_xxx
```

```bash
echo ".env" >> .gitignore   # keep it out of Git
```

Only those two keys are read from `.env`; the rest of the file is ignored and nothing is exported to other processes. Quotes, `export` and `#` comments are understood; `$VARIABLE` is not expanded. The file must be a regular file of at most 64 KB.

If `.env` is **tracked by Git**, Tide refuses to use it, since the token is already exposed: revoke it and run `git rm --cached .env`. If `.env` is not covered by `.gitignore`, Tide uses it but warns, because a careless `git add` (including `tide save` without `-f`) would commit it. A token is never written to the output or to error messages. You can also use your `gh` login: `export GITHUB_TOKEN=$(gh auth token)`.

## What the hooks do

`tide init` installs three hooks. They apply to every Git client on the machine, including IDEs, not only to the terminal.

| Hook | Rejects |
|---|---|
| `pre-commit` | A commit on a protected branch, staged changes containing a likely secret, or leftover merge conflict markers. |
| `commit-msg` | A message that is not a Conventional Commit. |
| `pre-push` | A push that updates or deletes a protected branch on the remote. |

**Commit messages** must look like `type(scope)!: description`, where the scope and `!` are optional. Accepted types: `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `build`, `ci`, `chore`, `revert`. Messages Git generates itself (`Merge ...`, `Revert "..."`, `fixup!`, `squash!`, `amend!`) are accepted. A rejection explains why and shows a valid example.

**Secret scanning** inspects only the lines you add. Built-in detectors cover AWS access key IDs, GitHub tokens, Slack tokens, Stripe live keys, Google API keys, and private key headers; add your own with `secrets.custom_patterns`. Findings show the file, line, and rule name, never the matched value. For a false positive, put `tide:allow-secret` on that line.

**Conflict markers**: added lines that start with `<<<<<<<` or `>>>>>>>` (what Git writes into a file on a conflict) block the commit, so an unresolved conflict cannot slip into history. This check is always on, also when `secrets.enabled` is false. `=======` alone is not flagged, since it is a common heading underline. For content that really contains a marker, put `tide:allow-conflict-marker` on that line. `tide commit` and `tide save` apply the same check.

**Protected branches** stop direct commits and pushes, nudging the team to work on branches and merge through Pull Requests. Commits made while HEAD is detached (for example during a rebase) are not blocked.

To bypass the hooks once, use Git's own flag: `git commit --no-verify` or `git push --no-verify`.

To remove Tide's hooks, delete the three files in the hooks directory (`git rev-parse --git-path hooks`) and, where a `<hook>.pre-tide` file exists, rename it back.

## Exit codes

| Code | Meaning |
|---:|---|
| `0` | Success. |
| `1` | General failure, including merge/rebase conflicts in `tide update` and problems found by `tide doctor`. |
| `2` | Invalid command usage or arguments. |
| `3` | Configuration invalid, or missing where required (including a missing `GITHUB_TOKEN` for `tide pr`). |
| `4` | A policy rejected the operation (protected branch, commit message, secret). |
| `5` | External failure: Git, the network, or the GitHub API. |
| `6` | Unsafe repository state; nothing was changed. |

Note that when a hook rejects a command, Git itself reports a failure; the Tide exit code applies when you run `tide` directly.

## Security and limits

- **Local guardrails, not enforcement.** Anyone can bypass hooks with `--no-verify` or edit their local setup. Keep branch protection enabled on GitHub; it is the authoritative control.
- **Secret scanning is best-effort.** It reduces accidents; it does not guarantee that every secret is caught. If a secret does reach a commit, rotate it.
- **Secrets stay out of output.** Tide never prints matched values, tokens, or credentials embedded in remote URLs.
- **Tokens.** Tide reads `GITHUB_TOKEN` (or `GH_TOKEN`) from the environment, or from `.env` at the repository root (see [Authentication](#authentication)), and never stores it. Do not put tokens in `.tide.yml`.
- **Repository configuration is untrusted input.** Tide bounds file size and pattern count, uses Go's RE2 regular expressions (no catastrophic backtracking), and validates branch names before passing them to Git. Git is always run with an argument list, never through a shell.
- **No hidden network calls.** Only `branch` (`--remote` fetches, `--github` calls the API), `create` (fetch, and push with `--push`), `update` (fetch), `review` (API and fetch), and `pr` (fetch, push, and API) touch the network, and each says so as it runs.
- **GitHub only.** `review` and `pr` support `github.com` remotes. `TIDE_GITHUB_API_URL` can point the API client elsewhere (HTTPS required, except loopback addresses), mainly for testing.
- No telemetry.

## Troubleshooting

**Hooks print `tide: not found on PATH; skipping ... checks`.**
Git cannot find the binary. Put `tide` in a directory on the `PATH` that your Git client sees. GUI clients and IDEs often do not inherit the terminal's `PATH`.

**Hooks do not run at all.**
Run `tide doctor`. Check that `core.hooksPath` is not pointing elsewhere (`git config core.hooksPath`) and that the hook files are executable. Re-run `tide init` after changing the hooks directory.

**`tide update`: "uncommitted changes would be at risk".**
Commit or stash your tracked changes, then re-run. Tide will not touch them.

**`tide update`: "cannot fast-forward local <base>".**
The local base branch has commits the remote does not, or is checked out in another worktree. Fix that branch by hand (or work on a separate branch), then re-run.

**A commit is rejected for a secret that is not a secret.**
Add `tide:allow-secret` to that line, or narrow your custom pattern.

**`tide pr`: "GITHUB_TOKEN is not set".**
Export a token with pull-request write access, or put it in `.env` at the repository root (see [Authentication](#authentication)). If a warning says `.env` is tracked by Git, Tide ignored it on purpose. `tide review` works without a token only for public repositories.

**`tide review` or `tide pr`: "not a supported GitHub remote".**
The configured `remote` must be a `github.com` URL (HTTPS, `ssh://`, or `git@github.com:owner/repo.git`). Change `remote` in `.tide.yml` if your GitHub remote is not `origin`.

## Development

```bash
make build     # build ./tide
make install   # build and install to $(PREFIX)/bin (default /usr/local)
make release VERSION=v1.2.3   # archives and checksums for every platform in ./dist
make test      # go test -race ./...
make fmt       # gofmt -w .
make lint      # go vet + golangci-lint
make vuln      # govulncheck
```

Tests use disposable Git repositories and a fake GitHub API; they never touch your working repository or the network. Contribution guidelines:

- Use Conventional Commits, matching what the hooks enforce.
- Keep changes small and tested; prefer the standard library.
- Never log secrets, and invoke Git with argument arrays.

### Publishing a release

Releases are built by CI. Merge the work into the release branch, then tag it:

```bash
git tag v1.2.3 && git push origin v1.2.3
```

The `release` workflow runs vet and the tests, builds the six archives with `scripts/release.sh`, starts the Linux, macOS and Windows binaries as a smoke test, and publishes the GitHub Release with `checksums.txt` and generated notes. A tag with a suffix (`v1.2.3-rc1`) is published as a pre-release.
