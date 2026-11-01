# Git Tide — Architecture

## 1. Architectural direction

Git Tide is a local CLI application built as a **modular monolith**.

It runs on a developer's machine, operates inside an existing Git repository, and delegates Git object and reference operations to the Git executable. The application adds workflow orchestration and policy validation; it does not implement Git.

The initial architecture should optimise for:

- Clear separation between command parsing and application behaviour.
- Testable use cases.
- Explicit boundaries around external processes and network calls.
- Minimal dependencies.
- Safe handling of working-tree state.

Do not introduce microservices, a daemon, a database, or a plugin framework in the first release.

## 2. High-level structure

```text
┌─────────────────────────────────────────────┐
│                  CLI (tide)                 │
│       commands, flags, help, output         │
└──────────────────────┬──────────────────────┘
                       │
┌──────────────────────▼──────────────────────┐
│               Application layer              │
│        init · doctor · update · review       │
│          hook installation / checks          │
└──────────────┬──────────────────┬───────────┘
               │                  │
┌──────────────▼──────────┐  ┌────▼────────────┐
│      Domain / Policy    │  │  Integrations   │
│ branches, commits,      │  │ GitHub API      │
│ secrets, configuration  │  │ (when required) │
└──────────────┬──────────┘  └─────────────────┘
               │
┌──────────────▼──────────────────────────────┐
│                Infrastructure                │
│ Git process · filesystem · config · hooks    │
└─────────────────────────────────────────────┘
```

Dependencies should point inward: application use cases depend on domain concepts and small interfaces, while infrastructure provides concrete implementations.

## 3. Proposed project layout

```text
git-tide/
├── cmd/
│   └── tide/
│       └── main.go
├── internal/
│   ├── cli/               (Cobra commands)
│   ├── app/
│   │   ├── initrepo/
│   │   ├── doctor/
│   │   ├── update/
│   │   ├── review/
│   │   └── hooks/
│   ├── domain/
│   │   ├── config/
│   │   ├── branch/
│   │   ├── commit/
│   │   ├── secret/
│   │   └── policy/
│   ├── git/
│   │   ├── executor/
│   │   ├── repository/
│   │   └── hooks/
│   ├── config/
│   │   ├── loader/
│   │   └── schema/
│   ├── github/            (client, remote URL and PR reference parsing)
│   ├── secrets/
│   │   ├── detector/
│   │   └── rules/
│   └── output/
├── docs/
├── test/
├── .github/
│   └── workflows/
├── go.mod
├── README.md
├── ARCHITECTURE.md
└── TOOLS.md
```

This is a starting point, not a requirement to create every directory immediately. Add packages when there is real code to put in them. Avoid packages that contain only pass-through wrappers.

## 4. Layer responsibilities

### CLI layer

Responsibilities:

- Define commands, arguments, flags, and help text.
- Parse user input and perform basic syntactic validation.
- Construct application dependencies.
- Call one application use case.
- Render results and map errors to exit codes.

The CLI must not contain Git workflow logic or policy rules.

### Application layer

Responsibilities:

- Coordinate a complete user operation.
- Define the order of steps.
- Apply safety checks before destructive or state-changing operations.
- Translate infrastructure failures into useful application errors.
- Return structured results for the CLI to render.

Examples include `UpdateBranch`, `PrepareReview`, `InitialiseRepository`, and `RunDoctor`.

### Domain and policy layer

Responsibilities:

- Represent branch and commit policies.
- Validate commit messages.
- Evaluate protected-branch rules.
- Define secret-detection rules and findings.
- Validate configuration semantics.

This layer should not execute shell commands, read terminal input, or make network requests.

### Infrastructure layer

Responsibilities:

- Execute Git commands.
- Read and write configuration files.
- Inspect repository state and filesystem paths.
- Install or update hooks safely.
- Communicate with GitHub when a use case requires it.

## 5. Key boundaries and interfaces

Keep interfaces small and owned by the package that consumes them. The following are conceptual examples; refine them against actual use cases.

### Git executor

The Git executor should run Git with an executable and an argument list, never by interpolating user input into a shell command.

```go
type Command struct {
    Args []string
    Dir  string
}

type Result struct {
    Stdout string
    Stderr string
}

type Executor interface {
    Run(ctx context.Context, command Command) (Result, error)
}
```

In production, use `os/exec` with `exec.CommandContext`. In tests, provide a fake executor for unit tests and real temporary repositories for integration tests.

### Repository context

A repository service can expose operations needed by use cases:

- Find the repository root.
- Read the current branch.
- Determine whether the working tree is clean.
- Read configured remotes.
- Fetch refs.
- Switch branches.
- Merge or rebase according to an explicit strategy.
- Inspect staged changes.

Avoid creating a generic Git abstraction that attempts to model every Git command.

### Configuration loader

The configuration boundary should:

1. Locate the configuration file.
2. Parse YAML.
3. Apply documented defaults.
4. Validate schema and semantic constraints.
5. Return a typed configuration object.

Keep parsing errors distinct from invalid configuration values. Report the file path and field where possible.

### Policy engine

Policy evaluation should be deterministic and independent of Git process execution. It receives repository facts and configuration, then returns allow/deny decisions with reasons.

### Secret detector

The detector should accept staged content and a set of rules, then return findings containing safe metadata such as rule name, file path, and line number. Do not include the matched secret in normal output.

### Hook manager

The hook manager is responsible for installing and updating Tide-managed hooks without destroying existing user hooks. The first implementation must define a safe chaining strategy before modifying any existing hook.

## 6. Main workflows

### `tide update`

1. Resolve repository root and current branch.
2. Load and validate configuration.
3. Resolve the current branch's configured base branch.
4. Inspect working-tree and index state.
5. Stop if the operation could overwrite or obscure user work.
6. Fetch the required remote refs.
7. Update the base branch using the configured strategy.
8. Return to the original branch.
9. Integrate the updated base branch.
10. Report success, conflicts, or recovery guidance.

The operation should not automatically stash, reset, clean, or discard changes in the initial version. If conflict resolution is needed, leave the repository in a normal Git state and explain what happened.

### `tide review <PR>`

1. Validate the PR identifier.
2. Resolve the GitHub repository from the configured remote.
3. Request the PR metadata.
4. Fetch the associated PR ref or head branch.
5. Create or update a local review branch.
6. Check it out only after validating local branch/worktree state.
7. Print the PR title, source branch, target branch, and review branch.

The GitHub API client should be isolated from the core Git workflow. Fetching and checking out the branch should remain Git operations.

### Commit validation

1. A Git hook invokes Tide at commit time.
2. Tide reads the proposed commit message and staged file list.
3. Commit policy validates the message.
4. Secret detection inspects staged content.
5. Tide returns success or a non-zero exit code with actionable findings.

Hooks must be fast and deterministic. Expensive network checks do not belong in local commit hooks.

### Protected branch enforcement

1. Hook receives the Git operation context.
2. Tide identifies the current or target branch where possible.
3. Policy evaluates the operation against protected branch configuration.
4. The operation is allowed or rejected with a clear explanation.

Local hooks cannot guarantee protection against users bypassing hooks or modifying local configuration. Remote protection remains authoritative.

## 7. Error handling and exit codes

Use typed or sentinel errors where they improve handling, but avoid building a large custom error framework.

Suggested CLI exit-code categories:

| Code | Meaning |
|---:|---|
| `0` | Operation completed successfully. |
| `1` | General or unexpected failure. |
| `2` | Invalid command usage or arguments. |
| `3` | Configuration invalid or missing where required. |
| `4` | Policy rejected the operation. |
| `5` | External dependency failure (Git, network, or provider API). |
| `6` | Unsafe repository state; operation was not started. |

Keep error messages concise and actionable. Preserve useful Git stderr without exposing secrets.

## 8. Testing strategy

### Unit tests

Test domain behaviour without invoking Git:

- Conventional Commit parsing and validation.
- Branch-to-base resolution.
- Protected branch policy.
- Configuration defaults and validation.
- Secret rule matching and safe finding output.

### Integration tests

Create temporary Git repositories and run the real Git executable:

- Repository discovery.
- Branch switching and update workflows.
- Merge/rebase outcomes.
- Hook invocation.
- Staged-file secret scanning.
- Existing hook preservation.

Tests must never run against the developer's actual working repository.

### End-to-end tests

Run the compiled `tide` binary against disposable repositories. Cover the most important user-facing command paths and exit codes.

## 9. Security and reliability

- Use `exec.CommandContext` and argument arrays; do not invoke a shell for Git commands.
- Validate branch names and external identifiers.
- Never print secret matches.
- Treat configuration and repository content as untrusted input.
- Avoid destructive Git commands unless explicitly requested and fully specified.
- Do not silently overwrite existing hooks.
- Use context cancellation for external processes and HTTP requests.
- Make network-dependent operations explicit.
- Ensure temporary files are created safely and cleaned up.

## 10. Decisions

Resolved in the first implementation:

1. **Configuration filename:** `.tide.yml` at the repository root. Unknown keys are rejected; missing keys take documented defaults.
2. **`tide update` strategy:** `merge` by default; `update.strategy: rebase` is opt-in. The local base branch is only ever fast-forwarded.
3. **Explicit target:** `tide update [branch]` overrides the configured base.
4. **Branch mapping:** `*` matches within one path segment, `**` across segments. The most specific matching pattern wins (exact name, then most literal characters), independent of file order.
5. **PR reference:** `refs/pull/<n>/head`, fetched into the hidden ref `refs/tide/pr/<n>` and checked out as `tide/review/pr-<n>`. Accepted identifiers: `123`, `#123`, or a `github.com` PR URL for the configured remote. Remote URLs: `https://`, `ssh://`, and `git@github.com:o/r.git`. PRs from forks use the same ref; the PR URL must match the configured remote.
6. **Hook chaining:** an existing hook is renamed to `<hook>.pre-tide` and invoked after Tide's checks pass. Tide never overwrites an existing `.pre-tide` file. If `tide` is not on `PATH` the hook warns and skips its checks (fail-open); `tide doctor` reports it.
7. **Secret rules:** built-ins for AWS, GitHub, Slack, Stripe, Google, and private-key headers, plus `secrets.custom_patterns` (Go RE2 syntax, so no catastrophic backtracking). Only added staged lines are scanned. A line containing `tide:allow-secret` is skipped.
8. **Minimum versions:** Go 1.23, Git 2.30.
9. **First-release operating systems:** Linux and macOS (the hooks are POSIX shell scripts). Windows is untested.
10. **GitHub authentication:** `GITHUB_TOKEN` or `GH_TOKEN` from the environment, optional for public repositories. `TIDE_GITHUB_API_URL` overrides the API root (HTTPS required, except loopback).
11. **JSON output:** not in v1.
12. **Telemetry:** none.

Hook enforcement summary: `pre-commit` (protected branch, secrets), `commit-msg` (Conventional Commits; Git-generated merge/revert/fixup messages are accepted), `pre-push` (updates or deletions of protected remote branches).

Exit code 1 is also used when `tide update` stops on merge/rebase conflicts; the repository is left in Git's normal conflict state.
