<p align="center">
  <img src="docs/assets/kestrel-banner.png" alt="Kestrel: Every failure has a trail." width="100%" />
</p>

<p align="center">
  <strong>Every dev stack failure has a trail. Stop guessing where it started.</strong><br>
  Zero-configuration incident intelligence and root-cause correlation for developers and autonomous AI agents.
</p>

<p align="center">
  <a href="https://github.com/Aareevs/Kestrel-CLI/releases"><img src="https://img.shields.io/github/v/release/Aareevs/Kestrel-CLI?style=flat-square&logo=github&logoColor=white&label=Release&labelColor=18181b&color=2563eb" alt="Release" /></a>
  <a href="https://github.com/Aareevs/homebrew-tap"><img src="https://img.shields.io/badge/Brew-Aareevs%2Ftap-27272a?style=flat-square&logo=homebrew&logoColor=f59e0b&labelColor=18181b" alt="Homebrew" /></a>
  <a href="cmd/kestrel/main.go"><img src="https://img.shields.io/badge/MCP-Protocol%20Ready-7c3aed?style=flat-square&logo=anthropic&logoColor=white&labelColor=18181b" alt="MCP Ready" /></a>
  <a href="https://github.com/Aareevs/Kestrel-CLI/releases"><img src="https://img.shields.io/badge/Platform-macOS%20%C2%B7%20Linux%20%C2%B7%20Windows-27272a?style=flat-square&labelColor=18181b" alt="Platform: macOS · Linux · Windows" /></a>
</p>

<p align="center">
  <a href="#installation">Installation</a>
  <span>&nbsp;&nbsp;•&nbsp;&nbsp;</span>
  <a href="#quickstart">30s Quickstart</a>
  <span>&nbsp;&nbsp;•&nbsp;&nbsp;</span>
  <a href="#command--flag-shortcuts">Shortcuts</a>
  <span>&nbsp;&nbsp;•&nbsp;&nbsp;</span>
  <a href="#ai-agent-companion-kestrel-mcp">AI Agent Companion</a>
  <span>&nbsp;&nbsp;•&nbsp;&nbsp;</span>
  <a href="#why-kestrel">Why Kestrel?</a>
  <span>&nbsp;&nbsp;•&nbsp;&nbsp;</span>
  <a href="#supported-ecosystems--capabilities">Ecosystems</a>
  <span>&nbsp;&nbsp;•&nbsp;&nbsp;</span>
  <a href="#documentation--architecture">Documentation</a>
</p>

---

## Installation

Choose your preferred installation method:

### 1. Homebrew (macOS & Linux)
Install prebuilt binaries without requiring Go:
```bash
brew tap Aareevs/tap
brew install kestrel

# Or in one command:
brew install aareevs/tap/kestrel
```

### 2. Curl Script (macOS & Linux)
Automatic OS and architecture detection, integrity verification via SHA-256 checksums, and installation to `~/.local/bin`:
```bash
curl -fsSL https://raw.githubusercontent.com/Aareevs/Kestrel-CLI/main/scripts/install.sh | sh
```

### 3. PowerShell One-Liner (Windows)
Automatic architecture detection, SHA-256 checksum verification, installation to `%LOCALAPPDATA%\Programs\kestrel`, and PATH registration:
```powershell
irm https://raw.githubusercontent.com/Aareevs/Kestrel-CLI/main/scripts/install.ps1 | iex
```

### 4. Go Install (Any Platform with Go 1.26+)
Build and install from source:
```bash
go install github.com/Aareevs/Kestrel-CLI/cmd/kestrel@latest
```

### 5. Manual Release Binaries
Precompiled standalone archives (`.tar.gz` for macOS/Linux, `.zip` for Windows) and `checksums.txt` are available on the [GitHub Releases page](https://github.com/Aareevs/Kestrel-CLI/releases/latest) for macOS (Apple Silicon & Intel), Linux (x86_64 & ARM64), and Windows (x64).

---

## Quickstart

```bash
cd your-project

# 1. Inspect: Discover repository facts, dependencies, ports & health endpoints
kestrel inspect   # or: kestrel ins

# 2. Watch: Supervise processes in dependency order, auto-probe readiness, and isolate process trees
kestrel watch     # or: kestrel w

# 3. Diagnose: Pinpoint what broke first, rank root-cause hypotheses, and view next steps
kestrel diagnose  # or: kestrel diag

# 4. Verify: Continuously prove the fix holds across a 30s stability window
kestrel verify    # or: kestrel v
```

*(No configuration file? You can run any command immediately with `kestrel run -- <command>`, or generate a `kestrel.yaml` via `kestrel init`.)*

### Command & Flag Shortcuts

| Quick Command | Full Command | Description |
| :--- | :--- | :--- |
| `kestrel w` / `kestrel start` | `kestrel watch` | Supervise stack in dependency order with live, classified logs |
| `kestrel diag` | `kestrel diagnose` | Isolate root-cause hypotheses backed by immutable event IDs |
| `kestrel tl` / `kestrel time` | `kestrel timeline` | Chronological multi-service failure timeline |
| `kestrel v` / `kestrel check` | `kestrel verify` | Verify runtime fix across continuous stability window |
| `kestrel ins` / `kestrel info` | `kestrel inspect` | Detect technologies, service candidates, ports, and health routes |
| `kestrel r` | `kestrel run` | Wrap any command with log classification and zero config |
| `kestrel s` / `kestrel stat` | `kestrel status` | Current liveness vs. probe readiness per service |
| `kestrel sess` | `kestrel sessions` | Browse, show, or export recorded incident sessions |
| `kestrel doc` | `kestrel doctor` | Check platform, permissions, SQLite storage, and tools |
| `kestrel mcp` | `kestrel mcp` | Launch read-only stdio MCP server for AI coding agents |

**Everyday Flag Shortcuts:**
- `-j` / `--json`: Universal shorthand for JSON output across all commands (`kestrel diag -j`, `kestrel ins -j`, `kestrel s -j`, `kestrel tl -j`).
- `-v` / `--verbose`: Diagnostic logging to stderr.
- `-q` / `--quiet`: Suppress non-essential output.
- `--project <dir>`: Target a specific project directory.
- `--allow-exec`: Authorize custom test verification command checks without interactive prompt.
- `--session last`: Target the latest recorded session.
- `--no-color`: Disable ANSI styling (honors `NO_COLOR`).

---

## AI Agent Companion (`kestrel mcp`)

When AI coding agents (Claude Code, Cursor, Windsurf, VS Code) debug through raw terminal commands, they burn large shares of their context window grepping logs, parsing noise, and guessing at cascading failures.

`kestrel mcp` provides a **read-only** Model Context Protocol (MCP) server over standard stdio. Agents query pre-correlated causal graphs, ranked root-cause hypotheses, and error timelines directly instead of guessing:

```bash
# Add to Claude Code
claude mcp add kestrel -- kestrel mcp --project /path/to/your/project
```

### Supported MCP Tools

| Tool | Description |
| :--- | :--- |
| `get_diagnosis` | Ranked hypotheses per incident with supporting/contradicting event IDs and actionable next steps |
| `get_failure_timeline` | Structured incident timeline, dependency graph, and historical verification results |
| `get_recent_errors` | Most recent error and fatal events with secret redaction applied |
| `get_session_summary` | Session state, event counts, probe results, and notable lifecycle events |
| `get_verification_results` | Past and active `kestrel verify` reports |
| `get_service_status` | Current liveness and readiness state across supervised services |
| `list_services` | Configured service manifests with secrets masked |
| `inspect_project` | Static technology discovery findings and inferred dependencies |

- **Strictly Read-Only**: The MCP server never starts, stops, or signals processes.
- **Zero-Trust Log Handling**: Log text is delivered as data, never instructions.
- **Pre-Redacted**: All tokens, API keys, JWTs, and database URLs are redacted before reaching the agent.

---

## Why Kestrel?

| Capability | Kestrel | Standard Shell / Logs | Docker Compose Logs | LLM Reading Raw Logs |
| :--- | :---: | :---: | :---: | :---: |
| **Zero-Config Execution** | **Yes** | Yes | No (requires compose) | Yes |
| **Causal Root-Cause Attribution** | **Yes (Graph + Ports)** | No | No | No (guesses on timing) |
| **Clean Process Containment** | **Yes (Zero Orphans)** | No (leaves orphans) | Yes (containers only) | No |
| **Multi-Sample Stability Window** | **Yes (Continuous)** | No | No | No |
| **Automatic Secret Redaction** | **Yes (Pre-storage)** | No | No | No |
| **Bounded Memory Under Floods** | **Yes (Ring Buffers)** | No (hangs terminal) | No (fills disk) | No (token overflow) |
| **Read-Only MCP Server** | **Yes (Native stdio)** | No | No | No |
| **Offline & Private** | **Yes (100% Local)** | Yes | Yes | No |

---

## Supported Ecosystems & Capabilities

<p align="center">
  <img src="https://skillicons.dev/icons?i=nodejs,nextjs,vite,express,go,python,django,fastapi,flask,java,spring,ruby,rails,php,laravel,rust,docker&theme=dark" alt="Supported technologies" />
</p>

| Stack | Detected From | Support Level |
| :--- | :--- | :---: |
| **Node.js**: Vite, Next.js, Nuxt, Astro, SvelteKit, Express, NestJS… | `package.json` scripts, lockfiles, `vite.config.*`, proxies | Full |
| **Go**: gin, echo, fiber, chi, net/http | `go.mod`, `main.go`, health routes in code | Full |
| **Docker Compose** | services, published ports, `depends_on`, build contexts | Full |
| **Python**: Django, FastAPI, Flask (pip, uv, Poetry, Pipenv) | `manage.py`, `pyproject.toml`, `requirements.txt` | Standard |
| **Java**: Spring Boot (Maven, Gradle) | `pom.xml`, `build.gradle(.kts)`, `application.properties` | Standard |
| **Ruby**: Rails, Sinatra | `Gemfile` | Standard |
| **PHP**: Laravel | `composer.json`, `artisan` | Standard |
| **Rust**: axum, actix-web, rocket, warp | `Cargo.toml`, `src/main.rs` | Standard |

---

## Documentation & Architecture

- **[Architecture & Engine Design](docs/architecture.md)** — Package structure, supervision pipelines, and storage lifecycle.
- **[Diagnosis Rules Catalog](docs/diagnosis-rules.md)** — Comprehensive catalog of incident correlation rules and evidence requirements.
- **[Verification Specification](docs/verification.md)** — Continuous stability window algorithm and verification verdicts.
- **[MCP Server Reference](docs/mcp.md)** — Tool schemas and agent configuration guides for Cursor, Claude, and VS Code.
- **[Configuration Guide](docs/configuration.md)** — Complete schema documentation for `kestrel.yaml`.
- **[Security & Privacy Model](docs/security.md)** — Threat model, redaction rules, and process execution safeguards.
- **[Releasing Guide](docs/releasing.md)** — GoReleaser workflows, checksums, and Homebrew tap management.

---

## Contributing

Contributions are welcome: error patterns, diagnosis rules, ecosystems for `inspect`, and bug reports. Read
[CONTRIBUTING.md](CONTRIBUTING.md) first (it has the design rules and how to add each kind of thing), and follow
the [Code of Conduct](CODE_OF_CONDUCT.md). Report security issues privately, as described in
[SECURITY.md](SECURITY.md).

## License

MIT License. Copyright (c) 2026 Aareevs. See [LICENSE](LICENSE) for details.
