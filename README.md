# Git Tide

> **Move with the flow.**

Git Tide (`tide`) is a command-line tool that sits on top of Git and makes common development workflows safer, faster, and more consistent.

Git remains the source of truth. Tide orchestrates Git operations, applies repository-specific policies, and provides a predictable interface for repetitive tasks.

## 1. Goals

- Reduce multi-step Git workflows to clear, high-level commands.
- Make branch workflows consistent across a team.
- Validate commit messages before commits are created.
- Detect likely secrets before they enter Git history.
- Enforce configurable local rules for protected branches.
- Prepare a local workspace for reviewing a Pull Request.
- Keep behaviour explicit, predictable, and easy to debug.

## 2. Non-goals

The first version is not intended to:

- Replace Git or hide Git's underlying model.
- Become a hosted Git platform.
- Run as a background service or require a server.
- Introduce a database or cloud dependency.
- Support every Git hosting provider from day one.
- Automatically rewrite repository history without explicit user intent.

## 3. Planned commands

| Command | Purpose |
|---|---|
| `tide init` | Initialise Tide configuration and local Git integration. |
| `tide update [branch]` | Update the configured base branch and integrate it into the current working branch. |
| `tide review <PR>` | Fetch and prepare a local workspace for a Pull Request review. |
| `tide pr` | Push the current branch and open a GitHub Pull Request against its base. |
| `tide doctor` | Diagnose repository, configuration, Git, and hook setup. |
| `tide config` | Inspect or validate Tide configuration. |

The command surface may evolve as workflows are implemented and tested.

## 4. Core workflows

### `tide update`

Conceptual flow:

1. Detect the current repository and branch.
2. Load Tide configuration.
3. Resolve the base branch for the current branch.
4. Fetch the required remote references.
5. Update the base branch according to the configured strategy.
6. Return to the original working branch.
7. Integrate the updated base branch.
8. Report the operations performed and any conflicts.

The initial implementation must protect uncommitted work. It should fail safely when the working tree state makes the operation ambiguous or risky.

### `tide review <PR>`

Conceptual flow:

1. Validate the repository and remote.
2. Resolve the Pull Request identifier.
3. Fetch the associated head branch or PR ref.
4. Create or update a predictable local review branch.
5. Check out the review branch.
6. Display useful review metadata and next steps.

GitHub is the first planned hosting integration. The design should avoid coupling core Git operations to GitHub-specific API types.

### `tide pr`

1. Resolve the GitHub repository from the configured remote and the base branch (`--base`, else the `branches.base` mapping, else `default_base`).
2. Refuse on a detached HEAD, on the base branch, on a protected branch, or when there are no commits ahead of the base.
3. Fetch the base, then show the title, base and commit count and ask for confirmation (skip with `--yes`).
4. Push the branch (the `pre-push` hook still applies) and create the Pull Request.

The title is the commit subject when the branch has one commit, otherwise the humanised branch name; the body lists the commits. Override with `--title` and `--body`; use `--draft` for a draft. Requires `GITHUB_TOKEN` with pull-request write access. If GitHub rejects the request after the push (for example because a PR already exists), the branch stays pushed and the error says so.

### Commit validation

Tide can install or manage a Git hook that validates commit messages against the repository's configured rules. Conventional Commits is the initial supported convention.

A rejected commit should include a concise explanation and a valid example.

### Secret scanning

Before a commit is accepted, Tide can inspect staged content for configured secret patterns. The initial implementation should include sensible built-in detectors and allow repository-specific rules.

Secret scanning is a preventive safeguard, not a guarantee that all secrets will be detected. It must avoid printing detected secret values in terminal output.

### Protected branches

Repository configuration can mark branches such as `main` and `stage` as protected. Local hooks can reject prohibited operations according to policy.

Local protection is a developer-side guardrail. It does not replace remote branch protection rules enforced by Git hosting providers.

## 5. Configuration

The following is a proposed configuration shape, not a final stable schema:

```yaml
version: 1

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
    - name: openai_api_key
      pattern: "sk-[A-Za-z0-9_-]{20,}"

hooks:
  manage: true
```

The file is `.tide.yml` at the repository root. Unknown keys are rejected and omitted keys take defaults; `tide config` prints the effective configuration and `tide config validate` checks it. An optional top-level `remote` (default `origin`) selects the remote used by `update` and `review`. See [`ARCHITECTURE.md`](ARCHITECTURE.md#10-decisions) for the resolved design decisions.

## 6. Engineering principles

- **Git first:** use Git as the source of truth.
- **Explicit behaviour:** commands should explain what they will do.
- **Safe by default:** fail rather than risk losing user work.
- **Pragmatic design:** avoid abstractions without a current use case.
- **Small surface area:** keep the CLI easy to learn.
- **Test real workflows:** use temporary Git repositories in integration tests.
- **No hidden network calls:** network operations must be part of an explicit workflow.
- **Useful errors:** report what failed, why, and what the user can do next.

## 7. Quick start

```bash
go build -o tide ./cmd/tide      # put the binary on PATH
cd your-repo
tide init                        # writes .tide.yml, installs hooks
tide doctor                      # verifies the setup
tide update                      # base branch → current branch
GITHUB_TOKEN=... tide review 123 # for private repositories
GITHUB_TOKEN=... tide pr         # push + open a Pull Request
```

Exit codes: `0` ok, `1` general, `2` usage, `3` configuration, `4` policy rejection, `5` external failure, `6` unsafe repository state.

## 8. Requirements

Initial development requirements:

- Go 1.23 or newer (to build).
- Git 2.30 or newer on `PATH`.
- A Git repository for repository-level commands.
- GitHub credentials only for operations that require GitHub API access.

Check the installed versions:

```bash
go version
git --version
```

## 9. Development

Module: `github.com/cellestias/git-tide`. Typical local commands (see also the `Makefile`):

```bash
gofmt -w .
go test ./...
go vet ./...
go build ./cmd/tide
```

## 10. Security considerations

- Never log secret values.
- Do not silently stage, discard, or overwrite user changes.
- Validate external input such as PR identifiers and branch names.
- Avoid shell-string construction; invoke Git with argument arrays.
- Treat repository configuration as untrusted input.
- Make hook installation and modification transparent.
- Preserve existing Git hooks when integrating Tide.
- Document the limits of local branch protection and secret scanning.

## 11. Documentation map

- [`ARCHITECTURE.md`](ARCHITECTURE.md) — system boundaries, modules, responsibilities, and workflows.
- [`TOOLS.md`](TOOLS.md) — language, dependencies, development tools, testing, CI, and release tooling.