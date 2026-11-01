# Git Tide — Tools and Development Stack

## 1. Principles

The toolchain should keep Git Tide easy to build, test, audit, and distribute.

- Prefer Go's standard library when it is sufficient.
- Add dependencies only for clear user or developer value.
- Keep the binary self-contained where practical.
- Make CI reproducible.
- Do not require paid services for local development.
- Avoid adding infrastructure that a local CLI does not need.

## 2. Core stack

| Area | Proposed tool | Why |
|---|---|---|
| Language | Go | Static binary distribution, strong standard library, good CLI support. |
| CLI framework | Cobra | Mature command, flag, and help structure. |
| Configuration | `gopkg.in/yaml.v3` | YAML parsing for repository configuration. |
| Git integration | `os/exec` | Execute the installed Git binary without reimplementing Git. |
| HTTP | `net/http` initially | Standard-library HTTP client for provider API calls. |
| GitHub integration | GitHub REST API; evaluate `go-github` | PR metadata and repository operations. |
| Unit tests | `testing` | Standard Go test framework. |
| Assertions | Standard library initially | Avoid unnecessary testing dependencies. |
| Formatting | `gofmt` | Canonical Go formatting. |
| Static checks | `go vet` | Standard static analysis. |
| Linting | `golangci-lint` | Consolidated lint checks. |
| Vulnerability checks | `govulncheck` | Identify known Go dependency vulnerabilities. |
| Release builds | GoReleaser | Repeatable multi-platform builds and release packaging. |
| CI | GitHub Actions | Build, test, and validate pull requests. |

The table is a proposal. Pin tool versions in CI and document the minimum supported Go version once selected.

## 3. Local development setup

Install:

- A supported Go toolchain.
- Git.
- Make (optional, if a Makefile is used).
- golangci-lint.
- govulncheck.

Verify the environment:

```bash
go version
git --version
```

Typical development commands:

```bash
# Format source files
gofmt -w .

# Run all tests
go test ./...

# Run tests with race detection
go test -race ./...

# Static analysis
go vet ./...

# Build the CLI
go build -o tide ./cmd/tide

# Run the CLI
./tide --help
```

If a Makefile is introduced, it should wrap these commands rather than hide complex behaviour.

## 4. Dependency policy

Before adding a dependency, ask:

1. Is the functionality available in the standard library?
2. Does the dependency materially reduce complexity or risk?
3. Is it actively maintained?
4. Is its licence compatible with the project?
5. Does it introduce transitive dependencies disproportionate to its value?

Avoid, unless a concrete requirement emerges:

- Web frameworks.
- ORMs and database drivers.
- Redis clients.
- Dependency-injection frameworks.
- Large terminal UI frameworks.
- Telemetry SDKs enabled by default.
- Plugin systems.
- General-purpose workflow engines.

A CLI should not inherit the complexity of a web service without a real need.

## 5. Git execution rules

All Git subprocess execution should:

- Use `exec.CommandContext`.
- Pass arguments as separate values.
- Set the working directory explicitly.
- Capture stdout and stderr separately.
- Propagate cancellation and process errors.
- Avoid shell interpolation.
- Avoid logging credentials embedded in remote URLs.

Example:

```go
cmd := exec.CommandContext(ctx, "git", "status", "--short")
cmd.Dir = repositoryPath

output, err := cmd.CombinedOutput()
if err != nil {
    return fmt.Errorf("git status failed: %w: %s", err, output)
}
```

For production code, prefer structured output handling where possible. Do not parse human-oriented Git output if a stable porcelain format is available.

## 6. Testing tools and approach

Use the standard `testing` package.

### Unit tests

- Table-driven tests for validation and policy logic.
- Test edge cases and invalid input.
- Keep tests deterministic and independent of network access.

### Git integration tests

Use `t.TempDir()` to create disposable repositories. Initialise repositories and configure local test identity explicitly.

Example setup commands:

```bash
git init
git config user.name "Git Tide Test"
git config user.email "git-tide-test@example.invalid"
```

Test real repository behaviour for branch operations, hooks, staged changes, and conflict scenarios.

### Test safety

- Never mutate the developer's current repository.
- Never use real credentials.
- Do not make network calls in ordinary unit tests.
- Use fixtures for GitHub API responses.
- Ensure temporary repositories are cleaned up automatically.

## 7. Linting and quality checks

Recommended baseline:

```bash
gofmt -w .
go vet ./...
golangci-lint run
go test -race ./...
govulncheck ./...
```

Configure lint rules deliberately. Start with a manageable baseline; do not spend early development time fighting a huge collection of low-value stylistic rules.

## 8. Continuous integration

A pull-request workflow should:

1. Check out the repository.
2. Install the pinned Go version.
3. Run formatting verification.
4. Run `go vet`.
5. Run unit and integration tests.
6. Run the linter.
7. Run vulnerability checks.
8. Build the CLI for the host platform.

Release workflows can build supported OS/architecture combinations and attach checksums and release notes.

Do not publish release artifacts from untrusted pull-request contexts.

## 9. Distribution

GoReleaser is the proposed release tool.

Potential distribution channels:

- GitHub Releases with prebuilt binaries.
- Installation through `go install` for developers.
- Package-manager recipes later, once release cadence and naming are stable.

Initial release targets should be chosen explicitly. A practical starting set is Linux, macOS, and Windows on common architectures, subject to CI coverage.

The project should document checksum verification and the supported installation methods.

## 10. GitHub API integration

GitHub API access is needed for `tide review`, not for ordinary local Git workflows.

Requirements:

- Use HTTPS.
- Support a documented authentication mechanism.
- Never print or persist tokens in plaintext configuration.
- Respect API rate limits.
- Use request timeouts and context cancellation.
- Handle missing permissions and inaccessible repositories clearly.
- Keep provider-specific models behind the GitHub integration boundary.

Decide whether the first version uses an environment token, GitHub CLI authentication, or a dedicated OAuth/device flow. Do not implement multiple authentication strategies without a user need.

## 11. Configuration and secrets

- Store repository-level policy in the Tide configuration file.
- Do not store access tokens in that file.
- Validate configuration before performing state-changing operations.
- Keep secret patterns documented and testable.
- Ensure test fixtures use obviously fake values.
- Avoid logging staged content or matched secret values.

## 12. Repository conventions

Suggested conventions:

- `gofmt` for formatting.
- Clear package names, avoiding generic names such as `common` or `utils`.
- Small interfaces defined by consumers.
- Explicit context propagation for I/O and subprocess operations.
- Errors wrapped with operation context.
- No package-level mutable global state unless justified.
- Comments explain why, not merely what the code already says.
- Public APIs should be minimal until external consumers exist.

## 13. Initial toolchain checklist

- [x] Choose and document the minimum Go version (1.23; Git 2.30).
- [x] Initialise the Go module (`github.com/cellestias/git-tide`).
- [x] Add Cobra and YAML dependency only when the first commands/configuration need them.
- [x] Add Makefile targets if they simplify common workflows.
- [x] Configure GitHub Actions.
- [x] Configure golangci-lint.
- [x] Add unit and temporary-repository integration tests.
- [x] Add govulncheck to CI.
- [ ] Add GoReleaser when preparing the first release.

## 14. Explicitly deferred

Do not add these to the first version without a concrete requirement:

- Database or persistent local state store.
- Background daemon.
- Web dashboard.
- Cloud service.
- Mandatory telemetry.
- Third-party plugin marketplace.
- Complex interactive terminal UI.
- Support for multiple Git hosting providers before GitHub workflow is stable.