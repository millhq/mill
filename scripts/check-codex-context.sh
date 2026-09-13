#!/usr/bin/env bash
# Checks source reachability and exercises configured guards without executing
# the tool-input command. Full hook-object equality rejects extra executable
# handlers before any command from configuration is used by the probes.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
root="$PWD"
fail() { echo "codex-context: $*" >&2; exit 1; }

for source in .claude/skills/*; do
  [[ -d "$source" && -f "$source/SKILL.md" ]] || fail "invalid canonical skill $source"
  name="${source##*/}"
  bridge=".agents/skills/$name"
  [[ -L "$bridge" && "$(readlink "$bridge")" == "../../$source" && -f "$bridge/SKILL.md" ]] || fail "broken skill bridge $bridge"
done
for bridge in .agents/skills/*; do
  [[ -d ".claude/skills/${bridge##*/}" ]] || fail "extra skill bridge $bridge"
done

# tomllib preserves semantic checks while allowing native TOML formatting.
python3 - <<'PYTHON'
from pathlib import Path
import tomllib

sources = {p.stem for p in Path('.claude/agents').glob('*.md') if p.stem != 'README'}
profiles = {p.stem for p in Path('.codex/agents').glob('*.toml')}
assert sources == profiles, 'canonical and native role sets differ'
assert all(p.suffix == '.toml' for p in Path('.codex/agents').iterdir()), 'extra native profile entry'
assignments = {
    'explorer': ('gpt-5.6-luna', 'medium'),
    'reviewer': ('gpt-5.6-luna', 'high'),
    'architect': ('gpt-6-astra', 'high'),
    **dict.fromkeys(('builder', 'closeout', 'pr-shepherd', 'research',
                     'test-investigator', 'verifier'), ('gpt-5.6-sol', 'high')),
}
assert sources == assignments.keys(), 'new role needs explicit native assignment'
for name in sources:
    path = Path('.codex/agents') / f'{name}.toml'
    profile = tomllib.loads(path.read_text())
    description = profile.get('description')
    assert isinstance(description, str) and description.strip(), f'{path}: missing description'
    model, effort = assignments[name]
    expected = dict(name=name, description=description, model=model,
                    model_reasoning_effort=effort,
                    developer_instructions=(
                        f'Read AGENTS.md first, then .claude/agents/{name}.md.\n'
                        'Execute that canonical role body with the Codex translations in AGENTS.md.\n'
                        'Honor the dispatched brief, role boundaries and available runtime limits.\n'))
    if name in ('architect', 'explorer', 'research', 'reviewer'):
        expected['sandbox_mode'] = 'read-only'
    assert profile == expected, f'{path}: native role contract drift'
config = tomllib.loads(Path('.codex/config.toml').read_text())
assert config == {'agents': {
    'max_concurrent_threads_per_session': 3,
    'default_subagent_model': 'gpt-5.6-sol',
    'default_subagent_reasoning_effort': 'high',
}}, 'native configuration drift'
PYTHON
for rule in .claude/rules/*.md; do
  grep -Fq "$rule" AGENTS.md || fail "router missing $rule"
done
grep -Fq 'CLAUDE.md' AGENTS.md || fail 'router missing canonical context'

# Unsupported lifecycle events stay explicitly classified. A source change
# must be reviewed before any corresponding native handler can be exercised.
validate_hooks() {
  local canonical="$1" native="$2"
  jq -e '
    (.hooks | keys) == ["PermissionDenied","PreToolUse","SessionStart","SubagentStop","TaskCompleted","UserPromptSubmit","WorktreeRemove"] and
    (.hooks.PreToolUse | length) == 2 and
    (.hooks.PreToolUse | map(.matcher) | sort) == ["Bash","Monitor"] and
    ([.hooks.PreToolUse[] | select(.matcher == "Bash")] == [{matcher:"Bash",hooks:[
      {type:"command",command:"${CLAUDE_PROJECT_DIR}/scripts/hook-build-guard.sh",args:[]},
      {type:"command",command:"${CLAUDE_PROJECT_DIR}/scripts/hook-command-guard.sh",args:[]}
    ]}]) and
    (.hooks.SessionStart == [{hooks:[{type:"command",command:"${CLAUDE_PROJECT_DIR}/scripts/sweep-stale-servers.sh",args:[]}]}]) and
    ([.hooks | to_entries[] | select(.key != "PreToolUse") | .value |
      (length == 1 and (.[0] | has("matcher") | not))] | all) and
    ([.hooks.PreToolUse[] | select(.matcher == "Monitor") | .hooks | length] == [1])
  ' "$canonical" >/dev/null || return 1
  jq -e --slurpfile canonical "$canonical" '
    . == {hooks: {
      UserPromptSubmit: $canonical[0].hooks.UserPromptSubmit,
      PreToolUse: [{matcher:"Bash", hooks:[
        {type:"command",command:"bash \"$(git rev-parse --show-toplevel)/scripts/hook-build-guard.sh\""},
        {type:"command",command:"bash \"$(git rev-parse --show-toplevel)/scripts/hook-command-guard.sh\""}
      ]}],
      SessionStart: [{hooks:[{type:"command",command:"bash \"$(git rev-parse --show-toplevel)/scripts/sweep-stale-servers.sh\""}]}]
    }}' "$native" >/dev/null
}
validate_hooks .claude/settings.json .codex/hooks.json || fail 'canonical/native hook registration drift needs review'

scratch=$(mktemp -d "${TMPDIR:-/tmp}/mill-codex-context.XXXXXX")
trap 'rm -rf "$scratch"' EXIT
jq '(.hooks.PreToolUse[] | select(.matcher == "Bash") | .hooks) +=
  [{type:"command",command:"inert-extra-handler"}]' .claude/settings.json >"$scratch/extra-hook.json"
if validate_hooks "$scratch/extra-hook.json" .codex/hooks.json; then
  fail 'canonical extra-handler regression probe was not rejected'
fi
source scripts/lib/git-fixture.sh
# Fixture environment changes stay in a subshell; a commit hook's exported
# git environment must never redirect these operations at the real index.
(
  git_fixture_init "$scratch/clean"
  git_fixture_init "$scratch/dirty"
  touch "$scratch/dirty/untracked"
  probe() {
    local index="$1" want="$2" cwd="$3" command_text="$4" hook input got
    hook=$(jq -r --argjson i "$index" '.hooks.PreToolUse[0].hooks[$i].command' "$root/.codex/hooks.json")
    input=$(jq -cn --arg cwd "$cwd" --arg command "$command_text" '{hook_event_name:"PreToolUse",cwd:$cwd,tool_name:"Bash",tool_input:{command:$command}}')
    got=0
    printf '%s\n' "$input" | (cd "$root/scripts" && bash -c "$hook") >"$scratch/probe.log" 2>&1 || got=$?
    [[ "$got" == "$want" ]] || fail "guard $index expected $want, got $got for inert input: $command_text"
  }
  probe 0 0 "$scratch/clean" 'task build'
  probe 0 2 "$scratch/dirty" 'task build'
  probe 0 0 "$scratch/dirty" 'git status'
  probe 1 2 "$scratch/clean" 'git push --force origin main'
  probe 1 2 "$scratch/clean" 'git rebase origin/main'
  probe 1 0 "$scratch/dirty" 'git status'
)
bash scripts/check-hook-command-guard.sh
echo 'codex-context: bridges and configured guard probes OK'
