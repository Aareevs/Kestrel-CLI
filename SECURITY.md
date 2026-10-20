# Security Policy

## Supported versions

Security fixes go to the latest release.

| Version | Supported |
| ------- | :-------: |
| 1.0.x   | ✅ |
| < 1.0   | ❌ |

## Reporting a vulnerability

**Please don't report security issues in public issues, pull requests, or discussions.**

Email **aareevs@gmail.com** with the subject `[SECURITY] Kestrel: <short summary>`.

Helpful details:
1. **Type of issue**, e.g. a secret that escapes redaction, command execution from log content, a signal sent to a
   process Kestrel didn't start, path traversal, or unsafe handling of `kestrel.yaml`.
2. **Where**: the command, package, or MCP tool involved, and your Kestrel version (`kestrel version`) and OS.
3. **Steps to reproduce**: a minimal project or `fakesvc` invocation is ideal.
4. **Impact**: what an attacker could achieve.
5. **A suggested fix**, if you have one.

## What to expect

- Acknowledgment within a few days. This is a small, volunteer-run project, so there is no formal SLA.
- We'll confirm and assess the report, develop a fix, and agree on a disclosure timeline with you.
- Fixes ship in a new release, with credit if you want it.

## What counts

Kestrel's [security model](docs/security.md) lists what it defends against. Reports that break one of these
guarantees are in scope:

- Secrets reaching the terminal, the database or log segments, exports, JSON output, or MCP output despite
  redaction (redaction is best-effort and documented as such, so a secret format no pattern recognizes is a
  hardening request rather than a vulnerability, but please report it).
- Log content being executed, used to build commands, or steering the MCP server's tools. All MCP tools are
  read-only, so anything that makes one act is in scope.
- Kestrel signalling or killing a process it didn't start.
- A configured command running without `--allow-exec` or an explicit confirmation.
- Discovery reading outside the project root or executing project code.

Out of scope: denial of service by running Kestrel on a machine you already control, running a command you
explicitly allowed, and automated-scanner output with no demonstrated exploit.
