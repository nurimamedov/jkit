# jkit — a Jenkins CLI for developers

`jkit` is `gh` for Jenkins. Trigger builds, tail logs, inspect stages, and lint
`Jenkinsfile`s from your terminal — fast, scriptable, zero-config.

This is **not** a Jenkins admin tool. It's a developer productivity tool
that thinks in workflows, not endpoints.

```bash
jkit run my-app --wait --log     # trigger, wait, stream log
jkit status my-app               # recent builds
jkit log my-app -f               # tail live build log
jkit log my-app --slowest 10     # where the build's time actually went
jkit stages my-app               # list stages w/ IDs + paths, and the agent each ran on
jkit wait my-app --stage Deploy  # block until it finishes; exit code is the result
jkit diagnose my-app             # summarize the failure
jkit params my-app               # what params does it accept?
jkit inspect my-app              # which Jenkinsfile, which repo, which branches
jkit sources my-app 42           # which code that build actually ran
jkit scan team/svc               # what the last branch-indexing run did
jkit search backend              # find jobs by name
jkit history my-app              # success rate + duration trend
jkit running                     # what is building right now
jkit env my-app                  # a build's injected env vars
jkit input my-app --approve      # answer a paused "Promote to prod?" gate
jkit disable my-app              # stop it building, without touching its config
jkit lint                        # validate ./Jenkinsfile
jkit export html my-app 42       # offline HTML of a run, survives Jenkins cleanup
jkit open my-app                 # open in browser
jkit api /api/json               # raw authenticated API request, like gh api
```

## What this fork adds

This fork (`0.10.1-ext.4`) is the backend of [jx](https://github.com/nurimamedov/jenkins-control),
a wrapper that gives agents and scripts a stable JSON protocol for Jenkins. It keeps
upstream behaviour and adds safety and evidence features that wrapper relies on:

- strict same-origin authentication, private credential files and masked secrets in output;
- correct test filtering and build comparison (`diff` fails instead of returning an empty result);
- bounded queue receipts, artifact reads to stdout and file downloads with a 512 MiB limit;
- exact-byte `job-config get/update`, SHA-256-preconditioned `copy-job` and explicit-number `abort`;
- `job-config create-scm` for one standalone Pipeline from SCM with a verified configuration receipt;
- structured submission errors and stage start/completeness fields.

Two settings are read from the environment:

| Variable | Purpose |
| --- | --- |
| `JKIT_ARTIFACT_STATIC_HOST` | HTTPS host that Jenkins may redirect artifact downloads to; the redirected request carries no credentials. Without it such redirects are refused. |
| `JKIT_SCM_ROOTS` | JSON map from a top-level Jenkins folder to its CI repository and credential ID, used by `job-config create-scm`, e.g. `{"TEAM":{"url":"https://git.example.com/team/ci.git","credentials":"team-ci"}}`. |

Build notes: [docs/patch-build.md](docs/patch-build.md). Agents start with
[AGENTS.md](AGENTS.md) and [agent integration](agents/README.md).

## Install

From source (Go 1.26+):

```bash
go install github.com/ysmaoui/jkit@latest
```

Or build a checkout:

```bash
git clone https://github.com/ysmaoui/jkit && cd jkit
go build -o jkit .
sudo mv jkit /usr/local/bin/
```

Pre-built binaries for Linux / macOS / Windows (amd64 + arm64) are published
on the [Releases](https://github.com/ysmaoui/jkit/releases) page once a tag is cut.

## Authenticate

```bash
jkit auth login                                              # interactive
jkit auth login --host https://jenkins.example.com \
              --user me --token "$JENKINS_TOKEN"           # non-interactive
jkit auth status
jkit auth status --all                                       # check every configured host
jkit auth prune                                              # dry run: list redundant keys; --yes removes them
```

Credentials are stored in `~/.config/jkit/config.yml`. Multiple hosts are
supported via `--alias`.

## Zero-config job detection

Commands like `run`, `status`, `log`, and `open` resolve the job from:

1. `.jkit.yml` in the current or parent directory
2. Git remote `origin` (`org/repo`)
3. Current directory basename

```yaml
# .jkit.yml
job: team/backend/my-service
host: https://jenkins.example.com  # optional
```

Then `jkit run`, `jkit status`, `jkit log -f` Just Work.

## Documentation

- [Quickstart](docs/quickstart.md)
- [Command reference](docs/commands.md)
- [Configuration](docs/configuration.md)
- [Scripting / JSON output](docs/scripting.md)
- [Design notes](docs/DESIGN.md)
- [Contributing](CONTRIBUTING.md)

## Use with AI agents

Drop-in skill, slash command, and sub-agent that let an AI coding agent drive
`jkit` end-to-end (status, log streaming, failure triage). Authored for
[Claude Code](https://docs.claude.com/en/docs/claude-code/overview); the
prompts transfer to other agent runtimes. See [`agents/`](agents/README.md).

## Status

Early-stage. Expect breaking changes before `v1.0`. Issues and PRs welcome.

## License

[MIT](LICENSE)
