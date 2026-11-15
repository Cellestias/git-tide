# Git Tide

> **Move with the flow.**

Git Tide (`gtide`) is a command-line tool that sits on top of Git and makes everyday workflows safer and more consistent for a team:

- **Update** a working branch from its base branch in one command, without risking uncommitted work.
- **Review** a GitHub Pull Request or GitLab Merge Request on a predictable local branch.
- **See** your branches at a glance: base, upstream, staleness, protection, open PRs.
- **Switch** branches without losing work, with partial names and `-` for the previous branch.
- **Start** new branches from a fresh base.
- **Open** a Pull Request (GitHub) or Merge Request (GitLab) from your current branch, with the same commands on both and no `gh` or `glab` to install.
- **Guard** your repository with local hooks: Conventional Commits, secret scanning, and protected branches.

Git stays the source of truth. Tide runs the Git you already have, needs no server or database, and keeps working alongside plain `git` commands.

## Contents

- [Install](#install)
- [Quick start](#quick-start)
- [Commands](#commands)
- [Configuration](#configuration)
- [Security alerts](#security-alerts)
- [Authentication](#authentication)
- [What the hooks do](#what-the-hooks-do)
- [Exit codes](#exit-codes)
- [Machine-readable output](#machine-readable-output)
- [Security and limits](#security-and-limits)
- [Troubleshooting](#troubleshooting)
- [Development](#development)

## Install

Tide needs Git 2.30 or newer on your `PATH`. Nothing else: the release binaries are self-contained, so you do not need Go.

### The `gtide` command and the `tide` alias

The command is **`gtide`**, because another well-known tool already uses the name `tide`. Each release archive also contains a copy named `tide` (`tide.exe` on Windows) if you prefer the short name; install it only when nothing else on your machine is called `tide`. Everything in this README works the same with either name, and `gtide` is the one the Git hooks call.

### Quick install

One command downloads the latest release, verifies its checksum, and installs `gtide` and the `tide` alias. It uses `curl` / PowerShell, so macOS does not quarantine the binary and you do not hit the Gatekeeper warning.

**macOS and Linux**

```bash
curl -fsSL https://github.com/Cellestias/git-tide/releases/latest/download/install.sh | sh
```

**Windows (PowerShell)**

```powershell
irm https://github.com/Cellestias/git-tide/releases/latest/download/install.ps1 | iex
```

Options are environment variables: `GTIDE_VERSION=v1.1.0` to pick a release, `GTIDE_BIN_DIR` (`GTIDE_INSTALL_DIR` on Windows) to choose the folder, and `GTIDE_NO_ALIAS=1` to skip `tide`. An existing `tide` that is a different program is never overwritten. The scripts are [scripts/install.sh](scripts/install.sh) and [scripts/install.ps1](scripts/install.ps1); read them before piping anything to a shell.

### Download a release

The links below are for **v1.6.0**, the current release. Older and newer versions are on the [Releases page](https://github.com/Cellestias/git-tide/releases). Archive names are `gtide_<version>_<os>_<arch>`; verify a download against `checksums.txt` from the same release.

| System | Download |
|---|---|
| macOS, Apple Silicon (M1 and newer) | [gtide_v1.6.0_darwin_arm64.tar.gz](https://github.com/Cellestias/git-tide/releases/download/v1.6.0/gtide_v1.6.0_darwin_arm64.tar.gz) |
| macOS, Intel | [gtide_v1.6.0_darwin_amd64.tar.gz](https://github.com/Cellestias/git-tide/releases/download/v1.6.0/gtide_v1.6.0_darwin_amd64.tar.gz) |
| Linux (Ubuntu, Debian, Fedora, ...), x86-64 | [gtide_v1.6.0_linux_amd64.tar.gz](https://github.com/Cellestias/git-tide/releases/download/v1.6.0/gtide_v1.6.0_linux_amd64.tar.gz) |
| Linux, ARM64 (Raspberry Pi 4/5, AWS Graviton) | [gtide_v1.6.0_linux_arm64.tar.gz](https://github.com/Cellestias/git-tide/releases/download/v1.6.0/gtide_v1.6.0_linux_arm64.tar.gz) |
| Windows, x86-64 | [gtide_v1.6.0_windows_amd64.zip](https://github.com/Cellestias/git-tide/releases/download/v1.6.0/gtide_v1.6.0_windows_amd64.zip) |
| Windows, ARM64 | [gtide_v1.6.0_windows_arm64.zip](https://github.com/Cellestias/git-tide/releases/download/v1.6.0/gtide_v1.6.0_windows_arm64.zip) |
| Checksums for all of the above | [checksums.txt](https://github.com/Cellestias/git-tide/releases/download/v1.6.0/checksums.txt) |

**macOS and Linux**

This downloads the latest release for your system, verifies it against `checksums.txt`, and installs it:

```bash
repo=https://github.com/Cellestias/git-tide
version=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$repo/releases/latest" | sed 's#.*/##')
os=$(uname -s | tr '[:upper:]' '[:lower:]')                       # linux or darwin
case $(uname -m) in x86_64 | amd64) arch=amd64 ;; arm64 | aarch64) arch=arm64 ;; *) echo "unsupported CPU: $(uname -m)" >&2 ;; esac
name=gtide_${version}_${os}_${arch}

curl -fsSLO "$repo/releases/download/$version/$name.tar.gz"
curl -fsSLO "$repo/releases/download/$version/checksums.txt"
grep " $name.tar.gz\$" checksums.txt | { sha256sum -c - 2>/dev/null || shasum -a 256 -c -; }   # must print: OK
tar -xzf "$name.tar.gz"
sudo install -m 755 "$name/gtide" /usr/local/bin/gtide
# optional short alias, only if no other `tide` is installed:
# sudo install -m 755 "$name/tide" /usr/local/bin/tide
gtide --version
```

If you downloaded the archive with a browser on macOS, Gatekeeper may block the unsigned binary. Remove the quarantine flag once: `xattr -d com.apple.quarantine /usr/local/bin/gtide`. Downloading with `curl`, as above, does not set it.

**Windows (PowerShell)**

Set `$version` to the latest tag shown on the Releases page, and use `arm64` instead of `amd64` on an ARM PC:

```powershell
$repo = "https://github.com/Cellestias/git-tide"
$version = "v1.6.0"
$name = "gtide_${version}_windows_amd64"

Invoke-WebRequest "$repo/releases/download/$version/$name.zip" -OutFile "$name.zip"
Invoke-WebRequest "$repo/releases/download/$version/checksums.txt" -OutFile checksums.txt
$expected = ((Select-String -Path checksums.txt -Pattern "$name.zip").Line -split "\s+")[0]
if ((Get-FileHash "$name.zip" -Algorithm SHA256).Hash -ne $expected) { throw "checksum mismatch" }

Expand-Archive "$name.zip" -DestinationPath .
New-Item -ItemType Directory -Force "$env:LOCALAPPDATA\Programs\gtide" | Out-Null
Copy-Item ".\$name\gtide.exe" "$env:LOCALAPPDATA\Programs\gtide\"
[Environment]::SetEnvironmentVariable("Path", "$env:Path;$env:LOCALAPPDATA\Programs\gtide", "User")
# open a new terminal, then:
gtide --version
```

Windows SmartScreen may warn about an unsigned program the first time; choose "More info", then "Run anyway". Tide's hooks are POSIX shell scripts, which [Git for Windows](https://gitforwindows.org) runs with its bundled shell, so install Git that way. Windows binaries are built and started on every release, but the full test suite runs on Linux and macOS only; treat Windows as supported on a best-effort basis and report problems.

### Build from source

Requires Go 1.26.10 or newer (a Go 1.21+ install downloads the right toolchain by itself).

```bash
git clone https://github.com/Cellestias/git-tide.git && cd git-tide
make install                    # installs gtide, plus a tide symlink, to /usr/local/bin (PREFIX=~/.local to change)
gtide --version
```

The binary must be on the `PATH` seen by Git, because the installed hooks call `gtide`. If it is missing, hooks print a warning and skip their checks (see [Troubleshooting](#troubleshooting)).

## Quick start

An existing repository:

```bash
cd your-repo
gtide init        # creates .tide.yml and installs the Git hooks
gtide doctor      # checks that everything is set up
```

A new project, from an empty folder:

```bash
mkdir app && cd app
gtide init --remote git@github.com:me/app.git   # asks to create the repository, registers the remote
gtide save -m "chore: initial commit"            # the first commit is allowed on the protected base
gtide push                                       # the first publish of the base is allowed too
```

Someone else's project:

```bash
gtide clone git@github.com:me/app.git            # git clone, then gtide init in the copy
```

Then, in daily use:

```bash
gtide create feature/login            # new branch from the fresh base
gtide save -m "feat: add login form"  # stage and commit, with the checks
gtide status                          # where am I, what changed
gtide push                            # publish the branch
gtide pull                            # bring in what teammates pushed to it
gtide update                          # bring the base branch into feature/login
gtide pr                              # push and open a Pull Request
gtide pr status                       # where is it? review state, pending reviewers, URL
```

Commit `.tide.yml` so the whole team shares the same rules.

### Colours

Output uses a few colours when it goes to a terminal: `✓` green, `!` yellow, `✗` red, and in `gtide pr` the request state (open green, closed red, merged purple, draft dim) and the review outcome (approved green, changes requested red, otherwise yellow). Pipes, files and CI get plain text. Set `NO_COLOR=1` to turn colours off, or `CLICOLOR_FORCE=1` to keep them when piping (for example into `less -R`). There is no colour on Windows.

## Commands

Run `gtide <command> --help` for the built-in help.

### `gtide init`

Creates `.tide.yml` (shared settings), `secret.tide.yml` (local credentials reference and secret patterns), adds `secret.tide.yml` to `.gitignore`, and installs the Git hooks.

```bash
gtide init [--remote <url>] [--provider auto|github|gitlab] [--base <branch>] [--no-hooks] [--yes]
```

- Outside a Git repository, `init` offers to create one (`git init`) with `--base` as its first branch (default `main`); it asks first, and `--yes` skips the question. It refuses to do so in your home folder or the filesystem root.
- `--remote <url>` registers the remote (named after `remote:` in `.tide.yml`, else `origin`) before anything else, so the provider is detected from it and `secret.tide.yml` references the right token variable. It never replaces a remote that points somewhere else; use [`gtide remote set-url`](#gtide-remote).
- `--provider` sets `provider:` in the new `.tide.yml`. The default is `auto`, which detects GitHub or GitLab from the remote URL (see [Providers](#providers)).
- The default base branch is detected from `origin/HEAD`, then `main`, then `master`, then the branch an empty repository starts on. Override it with `--base`.
- An existing `.tide.yml` or `secret.tide.yml` is **never overwritten**. `secret.tide.yml` is created with owner-only permissions and references `${GITHUB_TOKEN}` or `${GITLAB_TOKEN}` (from `--provider`, or from the remote when `auto`).
- `.gitignore` is created if missing, otherwise `secret.tide.yml` is appended to it. Existing entries are kept and the rule is never duplicated.
- `init` reports, for each file, whether it was created, updated, or preserved.
- An existing Git hook is **preserved**: it is renamed to `<hook>.pre-tide` and still runs after Tide's checks pass. Tide refuses to proceed if a `.pre-tide` file already exists.
- Re-running `init` is safe; hooks that are already current are left alone.
- `--no-hooks` (or `hooks.manage: false`) skips hook installation.

### `gtide clone <url> [folder]`

`git clone`, then `gtide init` in the copy, so a project is ready to work on in one step.

```bash
gtide clone git@github.com:me/app.git             # into ./app
gtide clone https://gitlab.com/group/app.git my-app
gtide clone git@github.com:me/app.git --provider github --no-hooks
```

The folder defaults to the repository name. A folder that already holds files is refused before Git runs. A `.tide.yml` already committed in the repository is kept as it is. `--base`, `--provider` and `--no-hooks` work as in `init`. The URL is checked first (see [`gtide remote`](#gtide-remote)).

### `gtide remote`

Adds, changes or shows the remote Tide publishes to: the one named by `remote:` in `.tide.yml`, else `origin`. It works before `gtide init`.

```bash
gtide remote add git@github.com:me/app.git      # register it; says which provider the URL maps to
gtide remote set-url https://github.com/me/app.git
gtide remote                                    # list remotes with provider; `show` does the same
gtide remote add git@github.com:me/fork.git --name fork
```

`add` never replaces an existing remote: the same URL is a no-op, a different one is an error that points to `set-url`. The URL must be `https`, `http`, `ssh`, `git` or `file`, an scp-like address (`git@host:owner/repo.git`) or a local path. Tide refuses option-like input, remote helpers such as `ext::` (they make Git run a program), and a password or token written into the URL, which would end up in `.git/config`: use SSH or a Git credential helper.

### `gtide change <branch>`

Switches to another branch safely. Alias: `gtide switch`.

```bash
gtide change feature/two-fa
gtide change two-fa        # unique partial match
gtide change -             # previous branch
gtide change main --carry
```

- `<branch>` is an exact name, a case-insensitive part of a name that matches exactly one branch, or `-` for the previous branch. An exact name always wins over partial matches. If several branches match, Tide lists them and refuses.
- A branch that only exists on the remote (already fetched) is created locally with its upstream set.
- It refuses, without changing anything, while a merge or rebase is in progress, and when tracked files have uncommitted changes. Use `--carry` to let Git bring the changes along; Git still refuses if they would conflict, and nothing is lost either way. Tide never stashes for you.
- After switching it tells you what to do next: when the branch is behind its base (`gtide update`), behind its upstream (`git pull`), when its upstream was deleted, or when it is a protected branch.
- It makes no network call. Remote branches are the ones you already fetched; refresh them with `gtide branch --remote`.

### `gtide branch`

Lists branches with Tide's view of them. Alias: `gtide branches`.

```bash
gtide branch                        # local data only, works offline
gtide branch --remote --hosted      # + remote-only branches, GitHub protection and open PRs
gtide branch --mine --sort name
gtide branch --merged               # candidates for cleanup
gtide branch --stale 30             # no commits for more than 30 days
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
| `GITHUB` / `GITLAB` | Only with `--hosted`: `protected` on the provider and the open Pull Request (`PR #12`) or Merge Request (`MR !12`) for the branch. Requests from forks are not matched. |

| Flag | Effect |
|---|---|
| `--remote` | Fetches the remote (`git fetch --prune`) and also lists branches that exist only there. This is the only network call besides `--hosted`. |
| `--hosted` | Adds the `GITHUB` or `GITLAB` column through the provider API (`--github` still works as an alias). Public repositories work without a token; for private ones set the provider token. If the API cannot be read, the column shows `?` and a warning is printed; the command still succeeds. |
| `--mine` | Only branches whose last author is your `git config user.email`. |
| `--stale <days>` | Only branches with no commits for more than that many days. |
| `--merged` | Only branches that are fully merged into their base. |
| `--sort date\|name\|ahead` | Newest first (default), by name, or by commits ahead of the base. |

Filters combine (they all must match). Progress messages go to stderr, so the table on stdout is safe to pipe.

#### Delete and rename branches

```bash
gtide branch delete feature/old              # must be merged into its base
gtide branch delete --merged                 # every branch `gtide branch --merged` lists
gtide branch delete feature/old --remote     # also on the remote (asks first)
gtide branch delete feature/wip --force      # not merged: those commits are lost
gtide branch rename feature/old feature/new
```

`delete` never touches a protected branch or the checked-out one, and refuses a branch with commits its base does not have unless `--force`. It is all or nothing: if one name is refused, none is deleted. It lists what will go and asks once (`--yes` skips). `rename` accepts no protected name on either side and leaves the remote copy alone, telling you how to move it.

### `gtide create <branch>`

Creates a branch from an up-to-date base and switches to it.

```bash
gtide create feature/two-fa                  # from the configured base, fetched first
gtide create feature/two-fa --from release/1 # from another branch, tag, or commit
gtide create feature/two-fa --local          # no network: use the local base
gtide create feature/two-fa --track          # check out a branch that already exists on the remote
gtide create feature/two-fa --push           # also push it and set the upstream
```

By default Tide resolves the base for the new name from `branches.base` (falling back to `default_base`), fetches it, and starts the branch at `<remote>/<base>`. Your local copy of the base branch is not touched, and the new branch has **no upstream** until you push it, so a plain `git push` cannot target the base by accident.

| Flag | Effect |
|---|---|
| `--from <ref>` | Start from this branch, tag, or commit instead of the configured base. A remote branch is fetched fresh; a tag, commit, or local-only branch is used as it is. |
| `--local` | Start from the local branch and make no network call. If the local branch does not exist, the last fetched `<remote>/<branch>` is used and the output says it was not refreshed. |
| `--track` | The branch must already exist on the remote: create a local branch from it and set the upstream. Cannot be combined with `--from`, `--local`, or `--push`. |
| `--push` | Push the new branch and set `<remote>/<branch>` as its upstream. If the push fails, the branch still exists locally. |
| `--no-switch` | Create the branch without checking it out; also allowed with uncommitted changes. |

`gtide create` refuses (without changing anything) when the name is invalid or is a protected branch, the branch already exists locally, a merge or rebase is in progress, or, unless `--no-switch` is given, tracked files have uncommitted changes. Note that the base is resolved from the *new* branch name, so `feat/x` only picks up a `feature/*` mapping if you add a `feat/*` pattern.

### `gtide update [branch]`

Brings the base branch into your current branch.

```bash
gtide update            # base resolved from .tide.yml
gtide update main       # use this base for this run only
```

What it does, in order:

1. Checks that the repository is in a safe state (see below).
2. Fetches the base branch from the configured remote.
3. Fast-forwards the local base branch to match. It never creates merge commits or discards commits on the base.
4. Integrates the base into the current branch with `merge` (default) or `rebase`, per `update.strategy`.
5. Leaves you on your original branch and reports whether anything changed.

`gtide update` **stops before doing anything** when:

- HEAD is detached,
- tracked files have staged or unstaged changes (untracked files are fine; Git protects them itself),
- a merge, rebase, cherry-pick or revert is already in progress.

It never stashes, resets, or cleans for you. If the local base branch has diverged from the remote, or is checked out in another worktree, it stops and tells you to resolve that first.

On conflicts the repository is left in Git's normal conflict state. Resolve the files, `git add` them, then run [`gtide continue`](#gtide-continue-and-gtide-abort), or give up with `gtide abort`. Plain `git merge --continue` and `git rebase --continue` work too.

If you are already on the base branch, `gtide update` simply fast-forwards it to the remote.

### `gtide pull`

Brings the remote copy of the current branch into it, where `gtide update` brings in the base branch.

```bash
gtide pull
```

It fetches the branch the current branch follows (its upstream, else the same name on the configured remote) and integrates it: a fast-forward when possible, otherwise a merge or rebase per `update.strategy`. Nothing is done when you are ahead or up to date. Like `update`, it stops before touching uncommitted work and never stashes or resets; conflicts are left in Git's normal state for [`gtide continue`](#gtide-continue-and-gtide-abort). A branch that is not on the remote yet is a usage error that points to `gtide push`.

### `gtide continue` and `gtide abort`

When `gtide update` (or any merge, rebase, cherry-pick or revert) stops on conflicts, these finish or undo it without you having to remember which operation it was.

```bash
# ...edit the conflicted files, then:
git add src/auth/login.go
gtide continue          # finishes the merge/rebase/cherry-pick/revert
gtide abort             # or: give up and go back to before it; asks [y/N], --yes skips
```

`gtide continue` refuses while any file still has conflicts and lists them. Before continuing it runs the pre-commit checks, so a file staged with leftover `<<<<<<<` markers or a secret is rejected and the operation stays in progress. Git's prepared message is used as it is, with no editor. A rebase can stop again on the next commit: resolve it and run `gtide continue` again. `gtide abort` discards the conflict resolutions made so far, which is why it asks first.

### `gtide review <PR>`

Checks out a GitHub Pull Request or GitLab Merge Request on a local review branch named `tide/review/pr-<number>`.

```bash
gtide review 123
gtide review '#123'
gtide review https://github.com/acme/tool/pull/123
gtide review 123 --diff     # also print the whole patch
gtide review 123 --reset    # move the review branch back to the PR (asks first)
```

- Fetches `refs/pull/<n>/head` (`refs/merge-requests/<n>/head` on GitLab), so it works for requests from forks; the output marks them `(fork: owner/repo)`.
- Creates the review branch, or fast-forwards it if it already exists.
- Prints title, author, state, source and target branches, assignees, review status, and the URL.
- Fetches the base branch and prints a per-file summary of what the request changes (`git diff --stat <remote>/<base>...HEAD`). `--diff` prints the full patch too. External diff drivers and textconv filters are disabled, and control characters are stripped, because the content comes from a contributor.
- If the review branch has local commits that are not in the request, Tide refuses. `--reset` is the way out, and it never discards silently: it lists the commits that would be lost, prints the current tip (so you can keep it with `git branch <name> <sha>`), and asks `[y/N]`. `--yes` skips the question. When nothing would be lost it does not ask.
- Requires a clean tracked working tree. A full URL must point to the configured remote's repository.
- Reads the metadata from the provider API. Public repositories work without a token; private ones need the provider token. On GitLab, `<PR>` can also be `!123` or a `.../-/merge_requests/123` URL.

Return to your previous branch with `git switch <branch>`.

### `gtide status` and `gtide diff`

```bash
gtide status                  # branch, distance from upstream and base, what changed
gtide diff                    # work tree against the index: what `gtide save` would add
gtide diff --staged           # index against the last commit: what it would commit
gtide diff -f src/login.go --patch
```

`status` shows the branch, how far it is ahead of or behind its upstream and its base (as of the last fetch: it does no network call), any merge or rebase in progress, and the changed files in groups: conflicts, staged, not staged and untracked. `diff` prints a per-file summary with added and deleted lines; `--patch` prints the full diff, without colour, textconv or external diff drivers. Untracked files are not part of a diff; they show in `status`. Both take `--json` (see [Machine-readable output](#machine-readable-output)).

### `gtide stash`

Puts local changes aside so that `update`, `pull` and `change` can run, and brings them back.

```bash
gtide stash -m "half-done login form"   # same as `gtide stash push`
gtide stash --untracked                 # also untracked files
gtide stash list
gtide stash pop                         # newest, or `pop 2` / `pop stash@{2}`
gtide stash drop 1                      # asks first (--yes skips)
```

Untracked files stay in place unless `--untracked`, and ignored files such as `secret.tide.yml` are never touched. A stash that does not apply cleanly is kept, and Tide says so.

### `gtide commit` and `gtide save`

`gtide commit` commits what is staged. `gtide save` stages and commits in one step: the files you name with `-f`, or every change when you give none.

```bash
gtide commit -m "feat(auth): add login form"
gtide commit --type feat --scope auth -m "add login form"   # builds "feat(auth): add login form"
gtide commit -a -m "fix: handle empty token"                # also stages tracked changes

gtide save -m "feat: add login form"                       # every change, new files included
gtide save -m "feat(auth): add login form" -f src/auth/login_form.tsx
gtide save --type fix -m "handle empty token" -f api/token.go -f api/token_test.go
gtide save -m "docs: update readme" -f docs/                # a directory stages everything under it
```

| Flag | Effect |
|---|---|
| `-m, --message` | The commit message (required). |
| `--type`, `--scope` | Build `type(scope): message` from `-m`. `--scope` needs `--type`. |
| `--body` | Commit body, after a blank line. |
| `-a, --all` | `gtide commit` only: stage modifications and deletions of tracked files first. Untracked files are never added. |
| `-f, --file` | `gtide save` only, repeatable: stage only this file or directory. Without it, every change is staged. Paths are relative to where you run the command and must be inside the repository. |

Before anything is committed, Tide checks that the branch is not protected, that the message follows your Conventional Commits rules, and that the staged lines hold no likely secrets (the same checks as the hooks, with friendlier errors). Your Git hooks still run, and Tide never uses `--no-verify`.

`gtide save` without `-f` is like `git add --all`: modified, deleted, and new files that `.gitignore` does not exclude. Tide lists what it staged before committing, and the secret scan covers new files too. With `-f` it refuses when other files are already staged, so the commit holds exactly the files you named. If a check fails after staging, Tide restores the index and nothing stays staged. It also refuses during a merge or rebase, where plain `git commit` is the right tool.

The first commit of a repository is the one commit allowed on a protected branch: with no history yet there is nothing to protect, and the base branch has to start somewhere. The same goes for the `pre-commit` hook. Every later commit on a protected branch is refused.

### `gtide history`

Lists commits, newest first: hash, date, author and message. Without flags it shows the current branch, and only what the branch adds to its base (from `.tide.yml`, or `--base`), so inherited history does not drown the list. Merge commits are hidden unless you ask for them. Creating a commit is `gtide commit` or `gtide save`; `history` never changes the repository.

```bash
gtide history --me                                       # your commits on this branch
gtide history --branch feat/remember-login --member paulo
gtide history --remote                                   # the remote copy of the branch (fetches it first)
gtide history -f src/utils/rotation_keys.py              # who changed a file, following renames
gtide history -f src/utils/rotation_keys.py --patch      # ...and what each commit changed in it
gtide history --type fix --scope auth --since 2w
gtide history --search "login"
gtide history v1.3.2..v1.4.0                             # what a release added
gtide history --summary --since 1m                       # commits per author
gtide history --all-branches --merges                    # every branch at once
gtide history show 1a2b3c4                               # one commit, with the files it changed
gtide history show 1a2b3c4 --patch                       # ...and what it changed
```

| Flag | Effect |
|---|---|
| `--me` | Only commits made with your `git config user.email`. |
| `-M, --member` | Only commits whose author name or email contains the text (case-insensitive). Not combinable with `--me`. |
| `-b, --branch` | Another branch: local, or remote-tracking when it only exists on the remote. |
| `--remote` | Read the remote copy of the branch; fetches that branch first. |
| `-f, --file` | Only commits that touched this file or directory. Reads the whole history, not just what the branch adds. |
| `-s, --search` | Only commits whose message contains the text (plain text, case-insensitive). |
| `--type`, `--scope` | Only Conventional Commits of that type (`feat`, `fix`...) or scope (`auth`...). They read the subject's prefix, so `feat(auth)!:` counts as `feat`. |
| `<from>..<to>` | Commits in `<to>` that `<from>` does not have. Branches, tags or hashes. Not combinable with `--branch`, `--remote`, `--all` or `--base`. |
| `--since`, `--until` | `7d`, `2w`, `3m`, `12h`, or a date such as `2026-01-31`. |
| `--all`, `--base` | `--all` shows the whole history; `--base` picks the base to subtract. |
| `--merges` | Include merge commits. |
| `-n, --limit` | At most this many commits (default 30; `0` for all). With `--type`/`--scope` it counts after the filter. |
| `--summary` | Count commits per author instead of listing them. Ignores `--limit`. |
| `--patch` | Add what each commit changed. With `-f`, only that file's lines, following renames: a file's history as commits plus changes. Also in `--json`. Not combinable with `--summary` or `--compare`. |
| `--all-branches` | Read every local and remote-tracking branch and tag at once, not one branch. Not combinable with `--branch`, `--remote`, `--base` or a range. |
| `--graph` | With `--json`: add each commit's full hash, its parents and the branches and tags on it, with parents after their children, to draw a commit graph. |
| `--json` | Print JSON on stdout; each commit has a `files` array with the added and deleted lines. |

`gtide history --compare <from> <to>` shows how far `<from>` is ahead of and behind `<to>`, and whether each commit `<from>` has would cherry-pick cleanly onto `<to>`. Commits are tried oldest first, each on top of the ones that applied; the simulation runs in memory and changes nothing. It needs Git 2.40 or later, exits with code `6` when a commit conflicts, and does not combine with the filters above.

```bash
gtide history --compare feat/sign-in feat/staging
```

### `gtide undo` and `gtide amend`

Fix the last commit, while it is still only yours.

```bash
gtide undo                                   # take it back; its changes stay staged
gtide amend -f src/auth/login.go             # add a forgotten file to it
gtide amend -m "fix(auth): reject empty token"   # reword it
```

`undo` moves the branch to the parent of the last commit and keeps that commit's changes staged, so nothing is lost and `gtide save` can commit them again. `amend` folds the staged changes (and the files given with `-f`) into the last commit and, with `-m`, `--type`, `--scope` or `--body`, replaces its message. The message and the staged lines go through the same Conventional Commits and secret checks as `gtide save`, and a failed check restores the index.

Both refuse a commit that is already on a remote branch (rewriting it would diverge from everyone who fetched it; make a new commit or use `git revert`) and a merge commit. `undo` also refuses the first commit of a repository, and `amend` a protected branch.

### `gtide push`

Pushes the current branch to the configured remote under the same name and sets it as upstream.

```bash
gtide push            # publish the current branch
gtide push --force    # after a rebase or amend; asks [y/N] (skip with --yes)
```

Refuses on a detached HEAD and on protected branches (`branches.protected`), with one exception: a protected branch that is not on the remote yet can be published once, which is how a new project starts. Updating, deleting or force-pushing it stays refused (the `pre-push` hook follows the same rule). Uncommitted changes are not pushed, and Tide warns when there are some. `--force` is `--force-with-lease`: it replaces the remote branch only if it still points where your last fetch saw it, so a teammate's newer commits are never overwritten silently. Your `pre-push` hook still applies. A plain `git push` keeps working too.

### `gtide pr`

Pushes the current branch and opens a Pull Request (GitHub) or Merge Request (GitLab). The command is the same on both; Tide translates it for the provider.

```bash
export GITHUB_TOKEN=...          # or GITLAB_TOKEN; needs pull/merge request write access
gtide pr                          # shows a summary, asks [y/N]
gtide pr --draft --yes
gtide pr --base main --title "Add login" --body "Closes #12"
```

Flow:

1. Resolves the repository from the configured remote and the base branch (`--base`, else `branches.base`, else `default_base`).
2. Refuses on a detached HEAD, on the base branch, on a protected branch, or when the branch has no commits ahead of the base.
3. Fetches the base, then decides the title and body (see below).
4. Shows the title, base, and commit count, and asks for confirmation (skip with `--yes`). Declining pushes nothing and creates nothing (only the base branch is fetched).
5. Pushes the branch (your `pre-push` hook still applies) and creates the PR.

**Title and body.** In a terminal, Tide asks for each one; an empty answer (just Enter) keeps the default. The default title is the commit subject when the branch has exactly one commit, otherwise the humanised branch name (`feature/login-page` becomes "Login page"), and the default body lists the commit subjects. `--title` and `--body` skip their prompt. The body prompt reads one line; for longer text use `--body` or the template below.

**Template: `description.pr.md`.** If this file exists at the repository root, Tide shows a preview and asks `Build the pull request from description.pr.md? [y/N]` instead of the two prompts. Answer `n` and it falls back to the prompts. Commit it so the whole team shares the same template.

```markdown
# feat: {{branch}}

## Summary
{{commits}}

## Checklist
- [ ] Tests added
- [ ] Docs updated
```

- The first line, if it starts with `# `, is the title; everything after it is the body. Without that line the whole file is the body and the title falls back to the default.
- Placeholders, expanded as plain text in one pass: `{{commits}}` (commit subjects as a list), `{{branch}}`, `{{base}}`, `{{commit_count}}`. Anything else is left as written.
- `--title` and `--body` still win over the file for their own field.
- The file must be a regular file (no symlink) up to 64 KiB.

With `--yes`, or when stdin is not a terminal (a script, CI), Tide asks nothing: it uses the template when the file exists, otherwise the defaults. If the provider rejects the request after the push (for example, a request already exists for the branch), the branch stays pushed and the error says so.

### `gtide pr list`

Lists the repository's Pull Requests or Merge Requests, newest first: ID (`#12` on GitHub, `!12` on GitLab), title, author, state, source and target branch. The output has the same columns on both providers.

```bash
gtide pr list                          # open requests
gtide pr list --state merged           # open, closed, merged or all
gtide pr list --author me --limit 50   # `me` is the owner of the provider token
gtide pr list --assignee alice
```

`--limit` defaults to 20 (maximum 100). Open drafts show as `open (draft)`. Public repositories need no token; `--author me` and `--assignee me` do, because Tide has to ask the provider who you are. On GitHub the author and assignee filters are applied by Tide after fetching, so on a very large repository a narrow filter can scan up to 1000 requests before it fills the limit.

### `gtide pr view <id>`

Shows one request: title, description, author, assignees, state, source and target branches (with a fork marker), creation and update dates, review status, changed files with line counts, the commits, and the URL. `<id>` is a number, `#number`, `!number` (GitLab), or the request URL.

```bash
gtide pr view 42
```

Review status is one of `approved by ...`, `changes requested by ...`, or `review required`, followed by `waiting on ...` for reviewers still requested. GitLab's REST API has no "changes requested" state, so there the status is `approved` or `review required`, and GitLab reports no per-file line counts. The description, files and commits are capped (lists show "… and N more"). When one of the extra lookups fails, Tide prints a warning and shows the rest.

### `gtide pr status`

Shows the request opened from the current branch, if there is one: title, state, review status, branches, outstanding review requests, and URL. An open request wins over a closed or merged one for the same branch; requests from forks are ignored. Without one it says so and suggests `gtide pr`.

### `gtide tag`

Lists, creates, publishes and deletes version tags.

```bash
gtide tag                                         # newest version first (-n 0 for all)
gtide tag create v1.5.0 -m "Release 1.5.0"        # annotated, on HEAD
gtide tag create v1.5.0 --push                    # and publish it (asks first)
gtide tag push v1.5.0
gtide tag delete v1.5.0 --remote                  # locally and on the remote (asks first)
```

The name must be a version: `v1.4.0`, `1.4.0`, `v2.0.0-rc.1`. `create` only works on a protected branch (`--any-branch` lifts that), on a commit that is already on the remote, and when the branch is not behind its upstream; it never moves or replaces an existing tag. Publishing a version tag can start a release build (this repository's `release.yml` does), so `push` and `delete --remote` ask `[y/N]` first; `--yes` skips the question.

### `gtide doctor`

Diagnoses the setup without changing anything: Git version, repository, configuration, remote, base branch, each hook, and whether `gtide` is on the `PATH`. Problems are shown with a hint; it exits non-zero only on hard failures (no Git, not a repository, invalid configuration).

### `gtide config`

```bash
gtide config            # print the effective configuration (file merged over defaults)
gtide config validate   # validate .tide.yml and report every problem with its field
```

## Configuration

Tide reads `.tide.yml` (shared, committed) and `secret.tide.yml` (local, git-ignored; see [Authentication](#authentication)) from the repository root. Omitted keys use the defaults below; unknown keys are rejected so typos do not pass silently.

```yaml
version: 1
provider: auto # auto, github or gitlab
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

security:
  level: off # off, low, medium or high (see Security alerts)

hooks:
  manage: true
```

| Key | Default | Meaning |
|---|---|---|
| `version` | `1` | Schema version. Only `1` is supported. |
| `provider` | `auto` | Hosting provider: `auto` detects it from the remote URL, or force `github` / `gitlab`. |
| `remote` | `origin` | Remote used by `update`, `review`, and `pr`. |
| `branches.default_base` | `main` | Base for branches that match no pattern in `branches.base`. |
| `branches.protected` | `[main]` | Branches where local hooks reject direct commits and pushes. |
| `branches.base` | none | Map of branch pattern to base branch (see below). |
| `update.strategy` | `merge` | `merge` or `rebase`. |
| `commits.convention` | `conventional` | The only supported convention. |
| `commits.require_scope` | `false` | Require `type(scope):` instead of `type:`. |
| `secrets.enabled` | `true` | Scan staged changes for likely secrets. |
| `secrets.custom_patterns` | none | Extra detectors committed with the project: `name` (letters, digits, spaces, `.`, `_`, `-`) and `pattern` (Go regular expression). At most 500. Prefer `secrets.patterns` in `secret.tide.yml`, which is not committed. |
| `security.level` | `off` | How strict Tide is about the repository's security alerts: `off`, `low`, `medium` or `high`. See [Security alerts](#security-alerts). |
| `hooks.manage` | `true` | Whether `gtide init` installs hooks. |

### Providers

Tide talks to GitHub and GitLab through the same commands (`pr`, `review`, `branch --hosted`). `provider` in `.tide.yml` selects the adapter:

| Value | Behaviour |
|---|---|
| `auto` | Detects the provider from the `remote` URL: `github.com` is GitHub; `gitlab.com` and hosts starting with `gitlab.` are GitLab. HTTPS, `ssh://` and `git@host:path` URLs all work. If the host is not recognised the command stops and asks you to set `provider`. |
| `github` | Always GitHub. |
| `gitlab` | Always GitLab, on any host. Use it for self-hosted GitLab whose hostname does not start with `gitlab.`. Nested groups (`group/sub/project`) are supported. |

| Tide | GitHub | GitLab |
|---|---|---|
| `gtide pr` | Create Pull Request | Create Merge Request (`--draft` adds the `Draft:` title prefix) |
| `gtide pr list` | List Pull Requests | List Merge Requests |
| `gtide pr view <id>` | View Pull Request, files, commits, reviews | View Merge Request, diffs, commits, approvals |
| `gtide pr status` | Pull Request of the current branch | Merge Request of the current branch |
| `gtide review <n>` | View Pull Request, fetch `refs/pull/<n>/head` | View Merge Request, fetch `refs/merge-requests/<n>/head` |
| `gtide branch --hosted` | List protected branches and open Pull Requests | List protected branches and open Merge Requests |

Tide calls the REST APIs itself: you do not need `gh` or `glab`. A new provider is one more adapter behind the same interface; no command changes.

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

For a one-off exception, pass the base explicitly: `gtide update main` or `gtide pr --base main`.

## Security alerts

On GitHub, Tide can look at the repository's open **Dependabot** alerts (vulnerable dependencies) and **code scanning** alerts (findings in your code), warn you about them, and hold back a push. You choose how strict it is with `security.level` in `.tide.yml`:

| Level | At commit | At push |
|---|---|---|
| `off` (default) | nothing | nothing |
| `low` | nothing (no network call, commits stay fast) | lists the alerts; never blocks |
| `medium` | lists the alerts | lists them; **blocks while a critical alert is open** |
| `high` | lists the alerts | lists them; **blocks while any alert is open**, and when the alerts cannot be checked |

At every level the alerts are listed with their severity, the **file** each one is about (the `package.json`, `go.mod` or lockfile that holds the dependency, or the file and line of a code scanning finding), and the fixed version when there is one. A commit is never undone or refused because of an alert: the list only warns.

```text
$ gtide push
! 2 open security alerts on acme/tool (1 critical, 1 high)
    CRITICAL  package.json     npm:lodash: Prototype pollution (fixed in 4.17.21)
    HIGH      backend/go.mod   go:golang.org/x/net: HTTP/2 rapid reset
✗ push blocked by security level high: 2 open alerts (1 critical, 1 high)
  hint: fix them: a push that changes the file of an alert, such as backend/go.mod or package.json, is allowed so the fix can be sent. `gtide security` lists them
```

**The fix is never blocked.** Alerts belong to the repository, not to your commit, so a rule that stopped every push would stop the push that fixes the alert. Tide lets a push through when it changes the file an alert is about: every blocking alert on a file the push touches is treated as a fix in progress. Alerts on files the push does not touch still hold it back.

Where it applies:

- `gtide push` and `gtide pr` check before anything is pushed or opened.
- The pre-push hook checks a plain `git push` too, once (not again when `gtide push` just did).
- `gtide commit` and `gtide save` list the alerts after the commit, at `medium` and `high`.
- `git push --no-verify` skips the hook, like any hook. `gtide push` has no skip: to change the rule, change `security.level`, which is a reviewed change to a committed file.

When the alerts cannot be read (no network, no token, a token without rights), `low` and `medium` warn and carry on; `high` holds the push, because it asked for a guarantee. A feature that is simply **not enabled** on the repository (code scanning is not used everywhere) is not a failure and never blocks.

The alerts are read from the repository's default branch, so a fix shows up as resolved once it is merged there.

**Requirements.** GitHub only: GitLab has no alerts API Tide can read on every plan, so on a GitLab repository the level has no effect, and Tide says so instead of pretending to protect. Reading alerts needs `GITHUB_TOKEN` (or `provider_token` in `secret.tide.yml`) with the `security_events` scope, or access to the repository for a fine-grained token with *Dependabot alerts* and *Code scanning alerts* read permission. Without one, GitHub answers as if the alerts did not exist, and Tide reports that it could not look.

### `gtide security`

```bash
gtide security          # the open alerts, most severe first, with their files
gtide security --json   # the same as JSON (see docs/json.md)
```

It works at every level, even `off`: the level decides when Tide warns and what it blocks; this command only looks. It fails (exit 5) when none of the sources could be read, so a script can tell "no alerts" from "could not look". `gtide doctor` shows the configured level and whether it can work with your provider.

## Authentication

`gtide pr` needs a token with write access to pull / merge requests; `gtide pr list`, `pr view`, `pr status`, `gtide review` and `gtide branch --hosted` only need one for private repositories (and `--author me` / `--assignee me` need one to resolve who you are). Pushing uses Git's own authentication, not this token.

- **GitHub**: a fine-grained token with "Pull requests: Read and write" and "Contents: Read" on the repository, or a classic token with the `repo` scope.
- **GitLab**: a personal, project or group access token with the `api` scope (role Developer or higher).

### `secret.tide.yml`

`gtide init` creates this local file next to `.tide.yml` and adds it to `.gitignore`. It is never committed and never overwritten.

```yaml
provider_token: ${GITLAB_TOKEN}   # or ${GITHUB_TOKEN}

secrets:
  patterns:
    - name: AWS Access Key
      pattern: 'AKIA[0-9A-Z]{16}'
```

- `provider_token` must be a `${VARIABLE_NAME}` reference. A literal token is rejected (and never echoed). Tide resolves the variable at run time from the environment and, if it is empty there, from the same key in `.env`.
- `secrets.patterns` adds secret-scanning detectors on top of the built-in ones, with the same rules as `secrets.custom_patterns`. They are local, so a team can keep internal patterns out of the repository.
- A new file ships with a catalog of well-known key formats, all **commented out**: cloud (AWS, Azure, GCP, DigitalOcean, Cloudflare, Vault...), GitLab and other CI/registry tokens, observability tools, messaging and email, payments, AI providers, and generic detectors (JWT, database URLs, `Authorization` headers). To enable one, delete the `# ` at the start of its two lines. Existing files are never touched, so run `gtide init` in a fresh clone or copy the blocks you want from the repository's template. The generic group can flag test fixtures; review it before enabling.
- `gtide doctor` warns when the file is missing, not ignored, or tracked by Git, and when the referenced variable is not set. `gtide config` never prints these patterns or the token.

### Where the token comes from

Tide uses the first it finds:

1. The variable referenced by `provider_token` in `secret.tide.yml` (environment, then `.env`).
2. The provider's usual variable: `GITHUB_TOKEN`, then `GH_TOKEN` for GitHub; `GITLAB_TOKEN` for GitLab.
3. The same keys in the **`.env` file at the repository root**.

Because both files are per repository, each project can use the token of its own organisation:

```bash
# <repo>/.env  (git-ignored)
GITLAB_TOKEN=glpat-xxx
```

Only the keys above are read from `.env`; the rest of the file is ignored and nothing is exported to other processes. Quotes, `export` and `#` comments are understood; `$VARIABLE` is not expanded. The file must be a regular file of at most 64 KB.

If `.env` is **tracked by Git**, Tide refuses to use it, since the token is already exposed: revoke it and run `git rm --cached .env`. If `.env` is not covered by `.gitignore`, Tide uses it but warns, because a careless `git add` (including `gtide save` without `-f`) would commit it. A token is never written to the output or to error messages. You can also reuse your CLI login: `export GITHUB_TOKEN=$(gh auth token)` or `export GITLAB_TOKEN=$(glab auth token)`.

## What the hooks do

`gtide init` installs three hooks. They apply to every Git client on the machine, including IDEs, not only to the terminal.

| Hook | Rejects |
|---|---|
| `pre-commit` | A commit on a protected branch, staged changes containing a likely secret, or leftover merge conflict markers. |
| `commit-msg` | A message that is not a Conventional Commit. |
| `pre-push` | A push that updates or deletes a protected branch on the remote. |

**Commit messages** must look like `type(scope)!: description`, where the scope and `!` are optional. Accepted types: `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `build`, `ci`, `chore`, `revert`. Messages Git generates itself (`Merge ...`, `Revert "..."`, `fixup!`, `squash!`, `amend!`) are accepted. A rejection explains why and shows a valid example.

**Secret scanning** inspects only the lines you add. Built-in detectors cover AWS access key IDs, GitHub tokens, Slack tokens, Stripe live keys, Google API keys, and private key headers; add your own with `secrets.patterns` in `secret.tide.yml` (or `secrets.custom_patterns` in `.tide.yml`). Findings show the file, line, and rule name, never the matched value. For a false positive, put `tide:allow-secret` on that line.

**Conflict markers**: added lines that start with `<<<<<<<` or `>>>>>>>` (what Git writes into a file on a conflict) block the commit, so an unresolved conflict cannot slip into history. This check is always on, also when `secrets.enabled` is false. `=======` alone is not flagged, since it is a common heading underline. For content that really contains a marker, put `tide:allow-conflict-marker` on that line. `gtide commit` and `gtide save` apply the same check.

**Protected branches** stop direct commits and pushes, nudging the team to work on branches and merge through Pull Requests. The first commit of a new repository, and the first publish of a protected branch that is not on the remote yet, are allowed so a project can start. Commits made while HEAD is detached (for example during a rebase) are not blocked.

To bypass the hooks once, use Git's own flag: `git commit --no-verify` or `git push --no-verify`.

To remove Tide's hooks, delete the three files in the hooks directory (`git rev-parse --git-path hooks`) and, where a `<hook>.pre-tide` file exists, rename it back.

## Exit codes

| Code | Meaning |
|---:|---|
| `0` | Success. |
| `1` | General failure, including merge/rebase conflicts in `gtide update` and problems found by `gtide doctor`. |
| `2` | Invalid command usage or arguments. |
| `3` | Configuration invalid, or missing where required (including a missing provider token for `gtide pr`). |
| `4` | A policy rejected the operation (protected branch, commit message, secret, security level). |
| `5` | External failure: Git, the network, or the provider API. |
| `6` | Unsafe repository state; nothing was changed. |

Note that when a hook rejects a command, Git itself reports a failure; the Tide exit code applies when you run `gtide` directly.

## Machine-readable output

`status`, `diff`, `branch`, `history`, `history show`, `tag` and `stash list` take `--json`: stdout holds only the JSON document, notes and warnings stay on stderr, and an empty result is `[]` or an object, never prose. `history --patch` adds what each commit changed. Fields only get added in minor releases. The shape of each command is in [docs/json.md](docs/json.md).

```bash
gtide status --json | jq '.files[] | select(.conflict) | .path'
gtide history -f src/login.go --patch --json
```

## Security and limits

- **Local guardrails, not enforcement.** Anyone can bypass hooks with `--no-verify` or edit their local setup. Keep branch protection enabled on GitHub; it is the authoritative control.
- **Secret scanning is best-effort.** It reduces accidents; it does not guarantee that every secret is caught. If a secret does reach a commit, rotate it.
- **Secrets stay out of output.** Tide never prints matched values, tokens, or credentials embedded in remote URLs.
- **Tokens.** Tide reads the provider token from the environment or `.env` (see [Authentication](#authentication)) and never stores it. `secret.tide.yml` only references a variable, and a literal token there is rejected. Do not put tokens in `.tide.yml`.
- **Repository configuration is untrusted input.** Tide bounds file size and pattern count, uses Go's RE2 regular expressions (no catastrophic backtracking), and validates branch names before passing them to Git. Git is always run with an argument list, never through a shell.
- **No hidden network calls.** Only `branch` (`--remote` fetches, `--hosted` calls the API; `branch delete --remote` pushes), `history` (`--remote` fetches), `clone` (clones), `create` (fetch, and push with `--push`), `update` and `pull` (fetch), `push` and `tag push` (push; `tag delete --remote` too), `review` (API and fetch), and `pr` (fetch, push, and API; `pr list`, `pr view` and `pr status` only call the API) touch the network, and each says so as it runs.
- **GitHub and GitLab.** `review`, `pr` and `branch --hosted` support `github.com` and GitLab (gitlab.com or self-hosted). `TIDE_GITHUB_API_URL` and `TIDE_GITLAB_API_URL` point the API client elsewhere (HTTPS required, except loopback addresses), mainly for testing. GitHub Enterprise is not supported yet.
- No telemetry.

## Troubleshooting

**Hooks print `gtide: not found on PATH; skipping ... checks`.**
Git cannot find the binary. Put `gtide` in a directory on the `PATH` that your Git client sees. GUI clients and IDEs often do not inherit the terminal's `PATH`.

**Hooks do not run at all.**
Run `gtide doctor`. Check that `core.hooksPath` is not pointing elsewhere (`git config core.hooksPath`) and that the hook files are executable. Re-run `gtide init` after changing the hooks directory.

**`gtide update`: "uncommitted changes would be at risk".**
Commit them with `gtide save`, or put them aside with `gtide stash`, then re-run (`gtide stash pop` brings them back). Tide will not touch them.

**`gtide update`: "cannot fast-forward local <base>".**
The local base branch has commits the remote does not, or is checked out in another worktree. Fix that branch by hand (or work on a separate branch), then re-run.

**A commit is rejected for a secret that is not a secret.**
Add `tide:allow-secret` to that line, or narrow your custom pattern.

**`gtide pr`: "GITHUB_TOKEN is not set"** (or `GITLAB_TOKEN`).
Export a token with pull/merge request write access, or put it in `.env` at the repository root (see [Authentication](#authentication)). If a warning says `.env` is tracked by Git, Tide ignored it on purpose. `gtide review` works without a token only for public repositories.

**`gtide review` or `gtide pr`: "cannot detect the provider" or "not a supported ... remote".**
With `provider: auto` the remote must be on `github.com`, `gitlab.com`, or a host starting with `gitlab.`. For anything else (self-hosted GitLab) set `provider: gitlab` in `.tide.yml`. Also check that `remote` in `.tide.yml` is the remote that holds your project.

## Development

```bash
make build     # build ./gtide
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

Then point the download links in this README at the new version (the commands below the table already find the latest one by themselves):

```bash
make readme-links VERSION=v1.2.3
```

The `release` workflow runs vet and the tests, builds the six archives with `scripts/release.sh`, starts the Linux, macOS and Windows binaries as a smoke test, and publishes the GitHub Release with `checksums.txt` and generated notes. A tag with a suffix (`v1.2.3-rc1`) is published as a pre-release.
