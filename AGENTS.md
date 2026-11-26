# Agent Instructions

Guidance for AI coding agents working on this repo.

## Before you start

1. Read [README.md](README.md) for the user-facing pitch and command surface.
2. Read [CONTRIBUTING.md](CONTRIBUTING.md) for build/test layout and the
   "Adding a Command" recipe.
3. Read [docs/DESIGN.md](docs/DESIGN.md) for architecture, error model, and
   output conventions.

## Using Jenkins tooling

Read [agents/README.md](agents/README.md) before operating Jenkins. Where the
`jx` wrapper is installed, use it first and read its agent guide.
This repository implements the lower-level `jkit` backend, with different JSON
and exit semantics. `jkit run` corresponds to `jx build`; `jkit test` to `jx tests`.
Direct backend writes do not inherit jx approvals or its durable journal.
For jx diagnose/history and log selection flags, consult the installed shared
guide; backend diagnose skips SUCCESS and backend history is a trend summary.

For this fork read [docs/patch-build.md](docs/patch-build.md).
jx expects this fork's version; upstream `@latest` lacks the extensions it uses.
Read `jkit COMMAND --help` and [docs/scripting.md](docs/scripting.md) for the
actual version's flags and exit codes. Repository help needs no authentication.

## Conventions

- `cmd/` is Cobra plumbing only. Business logic lives in `internal/`.
- HTTP lives in `internal/api/`. Domain types in `internal/jenkins/types.go`.
  Errors in `internal/jenkins/errors.go` (return typed errors — see DESIGN.md
  table).
- stderr is for progress/warnings, stdout is for data. Respect `--json`,
  `--format`, `NO_COLOR`.
- Don't add features beyond what the task requires. No premature abstraction.

## Quality gates before declaring work done

```bash
go vet ./...
go test ./... -count=1
gofmt -l .                 # must print nothing
make integration-test      # if behavior change
```

If you change command output format, also update `docs/commands.md`.

## Commit style

Short, imperative, no emoji. Focus on what changed, not how.
