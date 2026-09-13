# Codex context bridge

`AGENTS.md` routes to the original `CLAUDE.md`, rules and role contracts.
`.agents/skills/` uses relative directory symlinks into `.claude/skills/`;
shared prose stays in one place. On Windows, enable real symlink support
(Developer Mode/admin and Git symlink support) or read the canonical skill
paths from AGENTS directly. AGENTS itself is an ordinary cross-platform file.

Native configuration follows the [AGENTS convention](https://learn.chatgpt.com/docs/agent-configuration/agents-md),
[skill discovery](https://learn.chatgpt.com/docs/build-skills),
[custom agents](https://learn.chatgpt.com/docs/agent-configuration/subagents)
and [hooks contract](https://learn.chatgpt.com/docs/hooks).

## Roles and activation

Primary orchestration sessions follow the [conductor startup
contract](CONDUCTOR.md) after loading AGENTS and its required project context.
Nine `.codex/agents/*.toml` profiles load their matching canonical role bodies.
Explorer uses Luna/medium, reviewer Luna/high, architect Astra/high, and all
other roles Sol/high. Generic children fall back to Sol/high through the
`agents.default_subagent_model` and `agents.default_subagent_reasoning_effort`
settings. These are operational defaults, not equivalents of Claude model
prices or quality. They do not change the primary session's model.
Explorer, reviewer, research and architect request native read-only sandboxes;
other roles inherit the parent sandbox so test caches and owned worktrees work.
The configured child concurrency cap is three, subject to lower runtime limits.
When the active transport exposes `agent_type` or named roles, use them and
their runtime model assignments. Otherwise pass the canonical role path, brief
and boundaries explicitly. Neither mode implies a per-agent tool allowlist or
an enforced sandbox; inspect the active runtime and report missing capability.

Start a fresh Codex session after updating this configuration; the running
session does not reload these defaults. Review/trust
the project configuration and hook definitions through the host's native
review flow. This repository never writes trust or bypasses it. Configuration
files alone do not prove activation. Confirm skill discovery, role loading and
hook behavior, fallback model selection and concurrency in that session before
relying on automatic enforcement.
The missing CLI executable and denied Codex app control observed during this
setup are this-session activation limitations, not permanent product limits.

The [native import flow](https://learn.chatgpt.com/docs/import) is preferred
for personal Claude settings, recent history and memory. Review its proposed
changes and check user/project hooks for duplicates: matching hook sources
combine, rather than replacing one another. Personal memories and histories
stay private, outside public git; they are not copied into this router.

## Every Claude hook and its Codex treatment

| Canonical event / matcher | Treatment |
| --- | --- |
| `UserPromptSubmit` | Native hook uses the same canonical backlog/research reminder. |
| `PreToolUse` / `Bash` | Native hook invokes both existing build and command guards with Bash and quoted git-root paths. Native `exec_command` normalizes to `Bash` with `tool_input.command`; policy scripts are unchanged. |
| `PreToolUse` / `Monitor` | No Monitor shim. Continue foreground work with bounded native session waits (`session_id` / `write_stdin`), never detach gates. |
| `TaskCompleted` | No exact native event. Manually run the canonical completion guard on explicit completion, with outcome subject and cwd as below; there is no replacement `Stop` hook. |
| `WorktreeRemove` | No exact native event. Only after removing an owned worktree, run the exact canonical cache cleanup below. |
| `SessionStart` | Native hook calls the existing stale-server sweep with unchanged behavior. |
| `PermissionDenied` | Use actual runtime denial feedback and report blocks without a workaround. The original local denial log is not automatically written. |

The manual lifecycle steps are obligations, not claims of exact automatic
parity. Both native Bash guards preserve their existing narrow matching and
fail-open behavior. Neither is a general shell security boundary. The startup
sweep retains the canonical script's existing process cleanup policy.

At explicit completion, substitute the real outcome subject and run in the
owned worktree. A nonzero result blocks the completion claim:

```sh
jq -cn --arg cwd "$PWD" --arg subject 'Delivered the context bridge' \
  '{cwd:$cwd,task_subject:$subject}' |
  bash "$(git rev-parse --show-toplevel)/scripts/hook-task-completed.sh"
```

After successfully removing an owned worktree, use the existing exact cleanup
(from `.claude/settings.json`'s `WorktreeRemove` command):

```sh
command -v golangci-lint >/dev/null 2>&1 && golangci-lint cache clean >/dev/null 2>&1; command -v "$HOME/go/bin/golangci-lint" >/dev/null 2>&1 && "$HOME/go/bin/golangci-lint" cache clean >/dev/null 2>&1; true
```

## Delivery ownership

The canonical builder body and Codex translation assign builders bounded CI
waits, fixes and repository merge-queue enqueue through verified `MERGED`.
Armed/open is not delivered. Use
`gh pr merge --auto` and verify actual queue state; an already-armed PR may
need native `enqueuePullRequest`. Never rebase, force-push or change branch
protections. The orchestrator independently verifies the merged result and
owns nested docs and closeout. Bounded waits remain foreground command
continuations; no detached or unlimited watch loop.

## Checks

`bash scripts/check-codex-context.sh` checks bridges against canonical sets,
requires reviewed canonical/native hook mappings, and feeds inert native-shaped
PreToolUse JSON to the configured guards. It never executes the command
inside `tool_input`. It also runs the existing command-guard probe suite.
Validation requires Bash, jq and Python 3.11+ (`tomllib`, standard library).
Canonical hook additions fail until classified and mapped; an inert fixture
proves this rejection. Native TOML checks parse semantics, allowing comments
and whitespace. Lefthook always runs this small gate; CI runs it in the required
`standing-context` job. `check-standing-context.sh` separately budgets AGENTS
at 120 lines / 1,500 words, preserving the existing CLAUDE/rule budgets.
