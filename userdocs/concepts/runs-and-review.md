---
kind: explanation
---

# Runs, review, and debugging

Mill crashes or restarts mid-workflow, and the run picks up where it
left off instead of vanishing. Open the workflow's **Runs** tab
afterward and every run is there step by step — inputs, outputs,
timing, and exactly where it stopped.

When a step fails after producing output, Mill keeps that output beside
the error. **Run from clipboard** shows the failed result and lets you
copy it manually with **Copy result**.

## When a run needs a person

**Review** is the one queue for everything waiting on you:

- **Guardrail asks** — an external-effect step parked for approval.
- **Human review steps** — a workflow deliberately pauses for your
  verdict, optionally collecting typed input that flows back into
  the run.
- **Agent writes** — changes an AI agent proposed over MCP, held
  until you approve.
- **Vault waits** — a step needed a stored secret while the vault was
  locked; the run waits here until you unlock the vault, then continues
  from that step.

Each entry shows exactly what will happen and how old the ask is.
When you're away, Mill escalates: an actionable notification, a dock
badge, and a floating approval prompt.

## When Mill relaunches during a paused run

A run paused for your approval survives an ordinary restart: Mill picks
it back up and it waits for you again, with its 24-hour window
restarting from the relaunch. Right after a restart there is a brief
moment where the run is still being picked back up — answering then
tells you to try again in a moment. An update that changes how workflows
run can't resume a paused run safely, so Mill stops it instead and the
run reads **Interrupted**: nobody answered, and nothing was applied. Run
it again when you're ready.

## Debugging a workflow

- **Test runs** from the editor execute the draft without counting
  as production activity.
- **Breakpoints** pause a run before a step; edit values, then
  resume — or run in step mode and walk the chain one step at a
  time.
- **New note…** in the Quick Panel opens a small note window away from
  the canvas; pick where it lands (the Scratchpad, a space, or the top
  level) and ⌘↩ saves it there. Plugins can add their own captures the
  same way.
- **Try this step** on any step runs just that step on an input you
  type, paste from the clipboard, or take from its last run — and
  shows the output right there, without running the workflow. A step
  that would need your approval tells you so instead of running.
- **Activity** shows trigger fires and run outcomes across all
  workflows, so a scheduled or watching workflow is never invisibly
  running. Its MCP calls section logs every call to or from Mill's
  agent connection — who called what, when, and what happened.

## The menu bar shows what Mill is doing

Mill's menu-bar icon is a live status surface, not just a launcher.
The icon itself means Mill is running; a count beside it means
something is waiting on you. Clicking it opens a small panel:

- **Needs you** — approvals, agent writes, and plugin asks waiting
  for a decision. Click a row to land on Review.
- **Running now** — what's executing, each with a Stop button.
- **Recent** — the last few runs that finished, with Done, Failed or
  Stopped and how long ago. Click one to see which steps ran, in the
  same small floating window.
- **Quit Mill…** — quitting tells you first what stops: your
  schedules, hotkeys and watchers pause until Mill runs again.

Right-clicking the icon keeps the plain menu: Open Mill, Quit.

## Run from the Quick Panel

Summon the Quick Panel with your hotkey, type a workflow's name, and
the footer shows what the highlighted row can do. Drag the panel by
its top edge; it stays where you leave it. To put it back in the
middle of the screen, search "Reset Quick Panel position" in the
command palette (⌘K).

- **↩** runs it. The panel stays open and the footer tells you the
  outcome: done and how long it took, failed and why, or waiting for
  your approval. Press Escape when you're done reading.
- **⌘↩** opens the workflow in Mill, read-only, with its Run and
  step-by-step controls. Right after a run, this opens that run, so
  you see which steps ran and where it stopped.
- **⌘⇧↩** runs the workflow and opens a small floating window with
  its canvas, so you watch the steps light up without leaving what
  you were doing. Close it when you're done, or press Open in Mill to
  continue in the full app on that run.
- **⌘⇧P** pins or unpins it.
- **⌘K** lists all of these with their shortcuts.

Anything you type that isn't a workflow's name can be kept: **Save as
note** files it into the Scratchpad card on the board, **Save as
task** adds it as a row to the Engagement tasks list, scheduled
for today. Both close the panel the moment they land.

The same panel is the fastest way to update Mill: type "update" and
press ↩ to check. The footer tells you whether you're up to date, or
names the next step — download and install, then restart to finish —
and typing "update" or "relaunch" again finds that step. A restart, or
quitting from the menu bar, first saves whatever you were still
typing: a note mid-edit, a cell being edited, a workflow draft.
