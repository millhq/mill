# Mill agent context

Read [CLAUDE.md](CLAUDE.md) first. It remains the shared project contract.
Then read every unconditional rule below before working:

- `.claude/rules/adopt-converged-patterns.md`
- `.claude/rules/architecture.md`
- `.claude/rules/comments.md`
- `.claude/rules/delivery-discipline.md`
- `.claude/rules/testing.md`
- `.claude/rules/ux-writing.md`

Before editing or reviewing relevant files, also read these scoped rules:

| File paths | Rule |
| --- | --- |
| `**/*.go` | `.claude/rules/backend.md` |
| `frontend/src/**/*.tsx`, `frontend/src/**/*.ts` | `.claude/rules/frontend.md` |
| `internal/domain/composition/**` | `.claude/rules/node-standard.md` |

Inspect `.claude/rules/` frontmatter for additions: no `paths:` means
unconditional; otherwise load when its existing path patterns match the work.
Read only relevant `docs/SPEC.md` sections, active goals/briefs, role bodies and
skills. Do not preload the full SPEC, backlog or historical transcripts.
After compaction, re-read the active goal and written contract before continuing.

## Skills and roles

`.agents/skills/` links to the canonical `.claude/skills/` folders. Read the
applicable `SKILL.md` there (or through its canonical path) when using a skill.
Native role profiles in `.codex/agents/` point to `.claude/agents/<role>.md`;
read that body before executing a role. Preserve its scope, never-list, docs
ownership/draft requirement, review format and owner PR verification duties.
For a primary orchestration task, also read `.codex/CONDUCTOR.md`.

## Codex translations

These translations replace Claude-specific transport, model and attribution
examples, not the shared working discipline:

- Native role model/effort defaults live in `.codex/agents/<role>.toml`.
  They are operational assignments, not claims of Claude cost/quality equivalence.
  Leave the orchestrator model unchanged. State the actual model on dispatch;
  use a permitted runtime choice and disclose unavailable configured models.
- When `agent_type` or named roles are exposed, use them and their runtime model
  assignments; otherwise pass the canonical role path, brief and boundaries
  explicitly. Neither mode implies a tool allowlist or enforced sandbox;
  inspect the active runtime and report unavailable capabilities.
- A reviewer starts fresh with `fork_turns=none`; never same-context review.
  Create each builder's git worktree explicitly. At most three concurrent
  children; honor any lower runtime limit and CLAUDE's heavy-gate lock.
- Track canonical `maxTurns` and brief token ceilings when metrics exist;
  disclose unavailable metrics, never invent usage or budget compliance.
- Continue foreground commands using `exec_command`'s `session_id` and
  `write_stdin`, with bounded waits. No `nohup` or detached gates. A tool wait
  returning a running session is foreground continuation, not abandoned work.
- Use current CLAUDE layout (`internal/services/<ctx>svc`) when the explorer's
  older root-service map differs. The command guard's rebase prohibition wins
  over older agent README guidance: merge or fix forward, never rewrite history.
- Do not fabricate Claude/Fable coauthors or Claude session URLs. Identify the
  actual tool/model only when attribution is needed and known.
- Native UI work uses available, permitted computer-use tools. Report permission
  denials; never route a denied action through a different control channel.
- Builders own bounded CI waits, fixes and merge-queue enqueue through verified
  `MERGED`; armed/open is not delivered. Verify actual queue state; never change
  branch protections. The orchestrator independently verifies and owns docs/closeout.
- Read `.codex/README.md` for native hook activation and lifecycle translations.
  At explicit task completion, run the canonical task-completion guard with the
  outcome subject and cwd. Perform canonical worktree cleanup only after removal
  of an owned worktree. Treat runtime denial feedback as authoritative.

Native config is prepared for a fresh session and user hook trust review;
its presence does not prove it is active in the current session.
