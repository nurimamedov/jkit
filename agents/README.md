# AI agent integration

Drop-in artifacts that let an AI coding agent drive `jkit` competently — a
skill, a slash command, and a sub-agent. Authored for
[Claude Code](https://docs.claude.com/en/docs/claude-code/overview); the
prompts and workflows transfer cleanly to other agent runtimes that accept
free-form system prompts.

## Codex and Claude Code quickstart

When working in this checkout, Codex reads root `AGENTS.md`; root `CLAUDE.md`
imports those same instructions for Claude Code. Read this file and
[the command reference](../docs/commands.md) for usage. These Markdown files
need no skill installation. Optional copied skills are not automatically
kept in sync with this checkout.

For product operations use the workspace **jx** wrapper first, and read its
the jx agent guide. PATH `jx` is that
launcher, not Jenkins X / JayeX; `jkit 0.10.1-ext.3` is its pinned Go backend.
The [extended build notes](../docs/patch-build.md) describe the extensions.
Upstream `go install ...@latest` does not provide that measured artifact.

For direct jkit usage outside that workspace, start with local discovery:

```sh
jkit --version
jkit --help
jkit status --help
jkit wait --help
```

Then use explicit job/build selection and configured authentication for reads:

```sh
jkit status team/example 42 --json
jkit stages team/example 42 --json
jkit sources team/example 42 --json
```

These targets are examples. Read `building` before `result`: Jenkins can report
SUCCESS on an in-progress build. Direct `jkit` JSON is command-specific, not a
jx protocol envelope; `jkit log` emits text. `jkit wait` / `run --wait` encode
build outcomes in exit codes, unlike a successful jx read of a failed build.
See [scripting and exit codes](../docs/scripting.md).

| Task | jx | jkit |
| --- | --- | --- |
| Submit build | `build` | `run` |
| Test report | `tests` | `test` |
| Failure diagnosis | `diagnose` (sampled leads, includes SUCCESS) / `triage` (baseline comparison) | `diagnose` |
| Stage history across recent builds | `history --build-limit 5` | `history --limit 5` (trend summary) |
| Completion check | `wait --timeout 120` | `wait --max-wait 2m` |
| Recover uncertain write | saved operation ID / idempotency key | no jx journal |

Jenkins writes require authorization for that concrete action. Do not translate
jx recovery into a fresh `jkit run`, config update or copy: a lost response can
already have changed Jenkins. Do not expose auth config, tokens, raw XML or
unbounded logs as diagnostics. A tool's installed skill is not permission to
mutate Jenkins or delegate work.

The jx guide documents bounded `log --head`/`--tail`, exact `--stage-id`, literal
OR search (`--any-of`) and `--ignore-case`. Those jx options are not a command
translation table for jkit. In jx, diagnose signature categories remain tentative;
history has explicit absent/unavailable cells and does not sum overlapping stages.

## What's here

| Path | Type | Purpose |
|---|---|---|
| [`skills/jenkins/SKILL.md`](skills/jenkins/SKILL.md) | Skill | Optional command reference; available after installing the skill in the agent runtime. |
| [`commands/jenkins-monitor.md`](commands/jenkins-monitor.md) | Slash command | `/jenkins-monitor URL` — wait for a build, then summarize the result (with `jkit diagnose` on failure). |
| [`subagents/jenkins-build-analyzer.md`](subagents/jenkins-build-analyzer.md) | Sub-agent | Specialized failure-triage agent. Delegated to from the main thread when the user asks "why did the build fail?". |

All three assume `jkit` is on `$PATH` and `jkit auth login` has been run.

## Install (Claude Code)

User-wide (recommended):

```bash
git clone https://github.com/ysmaoui/jkit
cp -r jkit/agents/skills/jenkins                ~/.claude/skills/
cp    jkit/agents/commands/jenkins-monitor.md   ~/.claude/commands/
cp    jkit/agents/subagents/jenkins-build-analyzer.md ~/.claude/agents/
```

Per-project (commit alongside your repo):

```bash
mkdir -p .claude/{skills,commands,agents}
cp -r jkit/agents/skills/jenkins                .claude/skills/
cp    jkit/agents/commands/jenkins-monitor.md   .claude/commands/
cp    jkit/agents/subagents/jenkins-build-analyzer.md .claude/agents/
```

Restart Claude Code (or `/reload`) to pick them up.

## Use with other agent runtimes

The `SKILL.md` body is plain Markdown and works as a system prompt or a
retrieved context chunk for any agent (Cursor, Aider, custom OpenAI/Claude
SDK loops, etc.). Strip the YAML frontmatter and inline the body where your
runtime expects guidance.

The `jenkins-build-analyzer` prompt is similarly portable — feed it as a
sub-task system prompt with a `Bash`-equivalent tool.

## Usage examples

```
> /jenkins-monitor https://jenkins.example.com/job/team/job/svc/42/

> Why did the last build of my-service fail?
  (main thread delegates to jenkins-build-analyzer)

> Trigger my-service with FOO=bar and tail the log
  (skill kicks in, agent runs `jkit run my-service -p FOO=bar --wait --log`)
```

## Customizing

- **Restrict hosts** — change `allowed-tools: Bash(command:jkit*)` in `SKILL.md`
  to e.g. `Bash(command:jkit status*, command:jkit log*)` for a read-only profile.
- **Swap the model** — `jenkins-build-analyzer` uses `haiku` for cost; bump to
  a larger model for trickier failure analysis.
- **Add your own** — see the [Claude Code skill docs](https://docs.claude.com/en/docs/claude-code/skills)
  and the existing files as templates.

## Contributing

PRs welcome. Keep skills CLI-flag-accurate against the current `jkit` release —
if a flag changes, update `SKILL.md` in the same PR.
