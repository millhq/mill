# Security Policy

## Reporting a vulnerability

Please report security vulnerabilities through GitHub's private
vulnerability reporting, not a public issue:

**[Report a vulnerability](https://github.com/millhq/mill/security/advisories/new)**
(Security tab → "Report a vulnerability")

This opens a private advisory visible only to you and the maintainer, so
the issue isn't publicly disclosed before a fix ships. Please include
enough detail to reproduce it (steps, affected version/commit, expected
vs. actual behavior).

## Scope

Mill is a desktop app that executes guardrailed local commands and
integrations on the user's behalf. Things that are in scope for a security
report:

- A workflow, trigger, or MCP tool call bypassing the guardrail preview/
  approval step and taking an action without the user seeing it first.
- Secrets (connector credentials, API keys) stored anywhere other than the
  OS keychain, or leaking into logs, exported workflows, or the frontend.
- The MCP listener: Mill exposes a local, unauthenticated MCP server bound
  to `127.0.0.1` (loopback-only, by design — it never binds a non-loopback
  interface and is not reachable off the local machine). A report that it
  *is* reachable off-machine, or that a loopback process without the
  user's intent can drive it, is in scope. A report that "it has no auth"
  on its own is expected/by-design for a loopback listener and not itself
  a vulnerability, unless it demonstrates cross-boundary reachability.
- Arbitrary command/code execution reachable without going through the
  guardrail (i.e. a path that runs something the user never previewed or
  approved).

Out of scope: vulnerabilities requiring an already-compromised machine,
social engineering, or issues in third-party dependencies without a
demonstrated Mill-specific exploit path (report those upstream; Mill still
wants to know if it makes a dependency's issue reachable in a novel way).

## Supported versions

Mill is pre-1.0. There are no released version branches yet — the only
supported line is the latest commit on `main`. Fixes land there; there is
no backport policy until a 1.0 release establishes one.
