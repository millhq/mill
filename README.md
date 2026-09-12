<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset=".github/assets/banner-dark.svg">
  <source media="(prefers-color-scheme: light)" srcset=".github/assets/banner-light.svg">
  <img alt="Mill — guardrailed agentic workflows, on your desktop" src=".github/assets/banner-light.svg" width="720">
</picture>

</div>

![An Atlas board in Mill: a diagram, a table, and cards side by side, with the sidebar's Workflows, Configure, Atlas, Activity, Review, Secrets, and Extensions pages](.github/assets/readme-hero.png)

# Mill

A guardrailed desktop workbench for automations, a knowledge board and connectors — what you see is what your AI sees.

[![CI](https://github.com/millhq/mill/actions/workflows/ci.yml/badge.svg)](https://github.com/millhq/mill/actions/workflows/ci.yml)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/alicoding/mill/badge)](https://scorecard.dev/viewer/?uri=github.com/alicoding/mill)
[![License](https://img.shields.io/github/license/alicoding/mill)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go&logoColor=white)](go.mod)
[![Platform](https://img.shields.io/badge/platform-macOS-111111?logo=apple)](#quick-start)

An AI acting on a system it can't verify has to guess at the real state,
and the gap between the guess and reality is where hallucination and
silent failure live. Mill closes that gap: the human and the agent work
from the same verified view, and every action that reaches outside your
machine is previewed and gated before it happens. Mill is one binary,
keeps your data in local files, never calls an AI API of its own, and
never phones home.

## What Mill does today

<!-- mill:inventory:start -->

- **Home** — What ran, what is waiting for your approval, and the workflows you use most.
- **[Workflows](userdocs/concepts/workflows-and-steps.md)** — Copy something from a web page and the seeded Clipboard → Markdown workflow reads it, converts the HTML, writes the Markdown back to your clipboard, and notifies you — one trigger, three steps, connected in order on a canvas.
- **[Configure](userdocs/concepts/configure.md)** — Point two workflows at one Integration entry instead of pasting the base URL and auth into each step, and changing the endpoint later is a single edit that both workflows pick up.
- **[Atlas](userdocs/concepts/atlas.md)** — Drop a folder of markdown files onto the board and every file becomes a card — edit one outside Mill and the card updates itself, no re-import.
- **Activity** — Every run, guardrail decision, and agent call in one searchable log.
- **[Review](userdocs/concepts/runs-and-review.md)** — Mill crashes or restarts mid-workflow, and the run picks up where it left off instead of vanishing.
- **[Secrets](userdocs/concepts/secrets.md)** — Every field in Mill that needs a password, a token, or a key takes a pick from Secrets, never a typed value — a workflow carries the name of a secret, and Mill fills in the value only at the moment a step runs.
- **[Extensions](userdocs/concepts/extensions.md)** — An extension adds what Mill can do — a board object, a workflow step, a command, a secret source, a view — as a folder of two files you install, and it runs only with your say-so.

54 step types · 8 board object kinds · 13 extension contribution families · 49 MCP tools

- **[Automate with agents](userdocs/agents/connect-mcp.md)** — Connecting over MCP and what agents can do.
- **[The browser extension](userdocs/reference/browser-extension.md)** — Pairing a browser so Mill can replay recorded steps in your own signed-in session.

<!-- mill:inventory:end -->

Everything above is reachable by a human in the app and by an agent
over MCP, under the same guardrails.

## Quick start

Mill installs by cloning and building — no hosted service, no account.
You need Go, Node, the [Task](https://taskfile.dev) CLI, and the Wails
v3 CLI (`go install github.com/wailsapp/wails/v3/cmd/wails3@latest`).

```sh
git clone https://github.com/millhq/mill.git
cd mill
task setup:hooks    # once: installs the pre-commit hooks that mirror CI
task install:app    # builds Mill and installs it in /Applications
```

`task dev` runs a hot-reloading development copy instead of installing.
Prebuilt macOS releases are on the
[releases page](https://github.com/millhq/mill/releases); the
[Install](userdocs/start-here/install.md) page covers both routes and
where your data lives.

Then, in ten minutes:

1. Run the seeded example workflow and rebuild it yourself —
   [Your first workflow](userdocs/start-here/first-workflow.md).
2. Put a card, a table, and a diagram on a board —
   [Your first board](userdocs/start-here/first-board.md).
3. Point an agent at Mill over MCP —
   [Automate with agents](userdocs/agents/connect-mcp.md).

## Documentation

The same documentation is in three places: the **Docs** view inside the
app (the footer link, or "Open docs" in the command palette),
[`userdocs/`](userdocs/) here on GitHub, and
[`userdocs/llms.txt`](userdocs/llms.txt) for agents, with
[`userdocs/llms-full.txt`](userdocs/llms-full.txt) carrying every page in
one file.

## Contributing

File bugs and feature requests as
[issues](https://github.com/millhq/mill/issues); the bug template asks
for the build badge the app shows, which tells us exactly which build you
ran. Pull requests are welcome through the process in
[CONTRIBUTING.md](CONTRIBUTING.md). Report a vulnerability privately, as
[SECURITY.md](SECURITY.md) describes, never as a public issue.

## License

Apache License 2.0 (`Apache-2.0`) — see [LICENSE](LICENSE).
