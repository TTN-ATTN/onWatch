# onWatch

**Free, open-source quota and cost monitoring for AI coding plans and APIs.**

onWatch polls your AI providers in the background, keeps the history in a local SQLite file, and serves a web dashboard so you see burn rate, resets and spend before you get throttled or over budget. Single binary, under 50 MB of RAM, zero telemetry - all data stays on your machine.

[![Version](https://img.shields.io/badge/Version-v2.14.17-0EA5E9?style=for-the-badge)](https://github.com/onllm-dev/onwatch/releases/tag/v2.14.17)
[![Stars](https://img.shields.io/github/stars/onllm-dev/onwatch?style=for-the-badge&logo=github&logoColor=white&label=Stars&color=181717)](https://github.com/onllm-dev/onwatch/stargazers)
[![Downloads](https://img.shields.io/github/downloads/onllm-dev/onwatch/total?style=for-the-badge&logo=github&logoColor=white&label=Downloads&color=181717)](https://github.com/onllm-dev/onwatch/releases)
[![Coverage](https://img.shields.io/codecov/c/github/onllm-dev/onwatch?style=for-the-badge&logo=codecov&logoColor=white&label=Coverage)](https://codecov.io/gh/onllm-dev/onwatch)
[![VS Code Marketplace](https://vsmarketplacebadges.dev/version-short/onllm-dev.onwatch.svg?style=for-the-badge&label=VS%20Code&logo=visualstudiocode&logoColor=white&color=007ACC)](https://marketplace.visualstudio.com/items?itemName=onllm-dev.onwatch)
[![License: GPL-3.0](https://img.shields.io/badge/License-GPL--3.0-brightgreen?style=for-the-badge&logo=gnu&logoColor=white)](LICENSE)

**Links:** [Website](https://onwatch.onllm.dev) | [Releases](https://github.com/onllm-dev/onwatch/releases) | [Buy Me a Coffee](https://buymeacoffee.com/prakersh)

![Anthropic Dashboard - Light Mode](./docs/screenshots/anthropic-light.png)

> **Beta:** onWatch is in active development; features and APIs may change.

---

## Quick Start

**macOS / Linux:**

```bash
curl -fsSL https://raw.githubusercontent.com/onllm-dev/onwatch/main/install.sh | bash
```

**Homebrew:**

```bash
brew install onllm-dev/tap/onwatch && onwatch setup
```

**Windows (PowerShell):**

```powershell
irm https://raw.githubusercontent.com/onllm-dev/onwatch/main/install.ps1 | iex
```

**Docker:**

```bash
docker run -d --name onwatch -p 9211:9211 -v onwatch-data:/data \
  -e ZAI_API_KEY=your_key ghcr.io/onllm-dev/onwatch:latest
```

The installers put the binary and a `.env` in `~/.onwatch/` (`%USERPROFILE%\.onwatch\` on Windows), add `onwatch` to your PATH, and set up auto-start (systemd on Linux, an optional launchd agent on macOS). Binaries for macOS, Linux and Windows (amd64/arm64) are on the [Releases](https://github.com/onllm-dev/onwatch/releases) page. See also the [Windows guide](docs/WINDOWS_SETUP.md) and [Docker](#docker).

Then open **http://localhost:9211** and log in (default `admin` / `changeme`; change it in Settings).

```bash
onwatch            # start in the background
onwatch --debug    # run in the foreground
onwatch status     # is it running?
onwatch stop
onwatch update     # self-update
```

### Configure

Put provider keys in `~/.onwatch/.env` ([`.env.example`](.env.example) lists every option):

```bash
ZAI_API_KEY=your_zai_key
SYNTHETIC_API_KEY=syn_your_key
COPILOT_TOKEN=ghp_your_token
ONWATCH_ADMIN_PASS=a-strong-password
```

Many providers need no configuration: Claude Code, Codex, Cursor, Gemini CLI, Grok, Kimi Code, Command Code and Antigravity credentials are detected from their local logins. You can also add any number of accounts from the dashboard instead of `.env` (see [Accounts & Costs](#accounts--costs)).

---

## Supported Providers

| Provider | What is tracked | Credentials | Guide |
|---|---|---|---|
| Anthropic (Claude Code) | 5-hour, 7-day, per-model and monthly windows | Auto-detected from Claude Code, or `ANTHROPIC_TOKEN` | - |
| Codex (ChatGPT) | 5-hour and weekly windows, review requests, reset credits | Auto-detected from `~/.codex/auth.json`, or `CODEX_TOKEN` | [Codex](docs/CODEX_SETUP.md) |
| Z.ai (GLM Coding Plan) | Tokens, time and tool-call limits | `ZAI_API_KEY` | - |
| Synthetic | Subscription, search and tool-call quotas | `SYNTHETIC_API_KEY` | - |
| GitHub Copilot (beta) | Premium interactions, chat, completions | `COPILOT_TOKEN` (classic PAT, `copilot` scope) | [Copilot](docs/COPILOT_SETUP.md) |
| MiniMax Coding Plan | Shared 5-hour model pool | `MINIMAX_API_KEY` | [MiniMax](docs/MINIMAX_SETUP.md) |
| Cursor | Plan usage and on-demand spend | Auto-detected from Cursor Desktop, or `CURSOR_TOKEN` | [Cursor](docs/CURSOR_SETUP.md) |
| Antigravity | Per-model Claude/Gemini/GPT quota pools | Auto-detected (IDE or `agy` CLI) | [Antigravity](docs/ANTIGRAVITY_SETUP.md) |
| Gemini CLI (legacy) | Per-model daily quotas | Auto-detected, or OAuth env vars | [Gemini](docs/GEMINI_SETUP.md) |
| Grok | Credits against plan limit | Auto-detected from `~/.grok/auth.json`, or `GROK_TOKEN` | [Grok](docs/GROK_SETUP.md) |
| Kimi Code | Weekly and window limits | Auto-detected from `~/.kimi-code`, or `KIMI_TOKEN` | [Kimi](docs/KIMI_SETUP.md) |
| OpenCode Go | 5-hour, weekly and monthly meters (USD) | `OPENCODE_GO_API_KEY` | [OpenCode](docs/OPENCODE_SETUP.md) |
| Ollama Cloud (beta) | Included monthly usage, per-model requests | `OLLAMA_API_KEY` | [Ollama](docs/OLLAMA_SETUP.md) |
| Mistral (beta) | API and Vibe allowances, pay-as-you-go charges | Browser cookie import or `MISTRAL_AUTH_COOKIE` | [Mistral](docs/MISTRAL_SETUP.md) |
| Muse (opt-in) | 5-hour prompts and weekly usage | `MUSE_ENABLED=true` (each poll spends one prompt) | [Muse](docs/MUSE_SETUP.md) |
| Command Code | Credits, 5-hour and weekly windows | Auto-detected from `cmd login`, or `COMMAND_CODE_API_KEY` | [Command Code](docs/COMMANDCODE_SETUP.md) |
| Zenmux | Subscription flow quotas, model usage | `ZENMUX_MANAGEMENT_API_KEY` | [Zenmux](docs/ZENMUX_SETUP.md) |
| Alibaba Token Plan | 5-hour, weekly and monthly windows | Browser session cookies | [Alibaba](docs/ALIBABA_SETUP.md) |
| OpenRouter | Credit usage and limits | `OPENROUTER_API_KEY` | - |
| Moonshot | Account balance and drop rate | `MOONSHOT_API_KEY` | [Moonshot](docs/MOONSHOT_SETUP.md) |
| DeepSeek | Account balance and drop rate | `DEEPSEEK_API_KEY` | [DeepSeek](docs/DEEPSEEK_SETUP.md) |
| Your own scripts | Per-call tokens, latency and cost | JSONL files | [API Integrations](docs/API_INTEGRATIONS_SETUP.md) |

onWatch tracks the provider, not the coding tool, so usage from Claude Code, Codex CLI, Cline, Roo Code, Kilo Code, Cursor and others is covered.

---

## Features

- **Dashboard** - Per-provider quota cards with live reset countdowns, status badges, usage rate and projections; charts from 1 hour to 30 days; dark/light mode.
- **History and insights** - Reset-cycle history, cross-quota peaks, usage sessions, burn-rate forecasts and trends.
- **Accounts & Costs** - Several accounts per provider and pay-as-you-go spend analysis (below).
- **Alerts** - Email (SMTP), browser push and [webhooks](docs/WEBHOOK_SETUP.md) when quotas cross warning/critical thresholds or reset.
- **Menubar / system tray** (beta) - Quota percentages in the macOS menubar or Linux/Windows tray, with a quick-view popover. See [tray notes](docs/TRAY_LINUX_WINDOWS.md) and the [GNOME extension](docs/GNOME_MENUBAR_EXTENSION.md).
- **VS Code extension** - Tightest quota in the status bar, hover breakdown, quick view in the sidebar. `code --install-extension onllm-dev.onwatch` ([details](docs/VSCODE_EXTENSION.md)).
- **Prometheus** - `/metrics` endpoint with optional bearer token ([details](docs/PROMETHEUS_METRICS.md)).
- **PWA** - Install the dashboard as an app from your browser.

### Accounts & Costs

The **Accounts & Costs** page (`/accounts`, card icon in the header) manages any number of accounts per provider - two Z.ai keys, a personal and a work OpenAI key, and so on.

- Each account has its own credentials, polling, history and pause switch. On a provider tab, an **Account** selector switches between them.
- Credentials are encrypted in the database and never shown again after saving. A key already tracked by one account cannot be added to another, so it is never polled twice.
- **Pay-as-you-go spend** comes from the provider's own billing API where one exists: OpenAI and Anthropic organization costs (needs an admin key), OpenRouter key usage, and OpenAI-compatible gateways with the legacy billing endpoint. Spend is shown with the provider's own scope (organization or key) and period.
- **Otherwise onWatch estimates the cost** from calls recorded through [API Integrations](docs/API_INTEGRATIONS_SETUP.md), using its built-in prices for common OpenAI, Anthropic, Gemini, Z.ai and DeepSeek models. Unknown models stay "Unknown", never $0. Add or override prices with `ONWATCH_PRICING_FILE`.
- **omp users:** if the omp coding agent is installed, its API keys and logins are offered as read-only accounts automatically, and the [omp extension](docs/API_INTEGRATIONS_SETUP.md#omp) reports every model call omp makes, so those accounts show real per-model token use and cost. onWatch never needs omp; set `ONWATCH_OMP_ENABLED=false` to skip discovery.

---

## Configuration Reference

| Variable | Default | Description |
|---|---|---|
| `ONWATCH_PORT` / `--port` | `9211` | Dashboard port |
| `ONWATCH_HOST` | `0.0.0.0` | Bind address; `127.0.0.1` keeps the dashboard local |
| `ONWATCH_POLL_INTERVAL` / `--interval` | `120` | Poll interval in seconds (10-3600) |
| `ONWATCH_DB_PATH` / `--db` | `~/.onwatch/data/onwatch.db` | SQLite database |
| `ONWATCH_ADMIN_USER` / `ONWATCH_ADMIN_PASS` | `admin` / `changeme` | Dashboard login; a password changed in Settings wins afterwards |
| `ONWATCH_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `ONWATCH_AUTH_MODE` | `local` | `trusted_proxy` for reverse-proxy SSO ([details](docs/TRUSTED_PROXY_AUTH.md)) |
| `ONWATCH_METRICS_TOKEN` | - | Bearer token for `/metrics` |
| `ONWATCH_PRICING_FILE` | - | JSON array of exact provider/model USD-per-million-token rates |
| `ONWATCH_OMP_ENABLED` / `ONWATCH_OMP_DIR` | `true` / `~/.omp/agent` | Optional read-only omp credential discovery |

Flags override environment variables. `--debug` runs in the foreground; `--test` isolates PID and log files from a production instance.

<details>
<summary>Provider variables</summary>

| Variable | Description |
|---|---|
| `SYNTHETIC_API_KEY` | Synthetic API key |
| `ZAI_API_KEY` | Z.ai API key |
| `ZAI_BASE_URL` | Z.ai base URL (default: `https://api.z.ai/api`) |
| `ZAI_REGION` | Z.ai region: `global` (default) or `cn` |
| `ANTHROPIC_TOKEN` | Anthropic OAuth token (auto-detected from Claude Code) |
| `CODEX_TOKEN` | Codex OAuth access token (recommended for Codex-only) |
| `COPILOT_TOKEN` | GitHub Copilot PAT with `copilot` scope (Beta) |
| `MINIMAX_API_KEY` | MiniMax Coding Plan API key |
| `MINIMAX_REGION` | MiniMax region: `global` (default) or `cn` |
| `OPENROUTER_API_KEY` | OpenRouter API key |
| `GEMINI_ENABLED` | Enable Gemini CLI quota tracking (Legacy, auto-detected) |
| `GEMINI_REFRESH_TOKEN` | Gemini OAuth refresh token (for Docker/headless) |
| `GEMINI_ACCESS_TOKEN` | Gemini OAuth access token (for Docker/headless) |
| `GEMINI_CLIENT_ID` | Custom OAuth client ID (optional, has defaults) |
| `GEMINI_CLIENT_SECRET` | Custom OAuth client secret (optional, has defaults) |
| `CURSOR_TOKEN` | Cursor access token (auto-detected from Cursor Desktop) |
| `GROK_TOKEN` | Grok bearer from `grok login` (or auto-detected from ~/.grok/auth.json) |
| `GROK_ENABLED` | Enable Grok provider (default: auto when auth present; set false to disable) |
| `GROK_HOME` | Custom Grok home dir (default ~/.grok; auth.json and sessions live here) |
| `KIMI_TOKEN` / `KIMI_CODE_TOKEN` | Optional static Kimi Code access token (prefer local OAuth credentials) |
| `KIMI_CODE_ENABLED` | Enable Kimi Code provider (default: auto when credentials present) |
| `KIMI_CODE_CREDENTIALS` | Path to kimi-code.json (default ~/.kimi-code/credentials/kimi-code.json) |
| `MOONSHOT_API_KEY` | Moonshot (Kimi) open-platform API key (enables balance tracking) |
| `MOONSHOT_BASE_URL` | Override the Moonshot API base URL (default `https://api.moonshot.ai`) |
| `DEEPSEEK_API_KEY` | DeepSeek platform API key (enables balance tracking) |
| `DEEPSEEK_BASE_URL` | Override the DeepSeek API base URL (default `https://api.deepseek.com`) |
| `OPENCODE_GO_API_KEY` | OpenCode console service-account key with usage read access (preferred) |
| `OPENCODE_GO_WORKSPACE_ID` | Session mode: OpenCode Go workspace ID (`wrk_...`) |
| `OPENCODE_GO_AUTH_COOKIE` | Session mode: `__Host-console_session` cookie value from opencode.ai |
| `OPENCODE_GO_BASE_URL` | Override the OpenCode console base URL (default `https://opencode.ai`) |
| `MISTRAL_ENABLED` | Enable Mistral subscription and pay-as-you-go tracking (default: false) |
| `MISTRAL_AUTH_COOKIE` | Manual Mistral Cookie header; keep private |
| `MISTRAL_BROWSER` | auto, chrome, firefox, safari (macOS), or edge |
| `MISTRAL_BROWSER_PROFILE` | Browser profile path/name; Firefox supports `::container=ID` |
| `MISTRAL_RETENTION` | How long Mistral rows are kept (default: `2160h` = 90 days, `0` disables pruning) |
| `OLLAMA_API_KEY` | Ollama Cloud API key from ollama.com/settings/keys |
| `OLLAMA_MONTHLY_LIMIT` | Ollama included usage cap in USD (0 = derive from plan) |
| `OLLAMA_RESET_DAY` | Ollama reset day of month, 1-31 (0 = learn from the first observed reset) |
| `META_API_KEY` | Meta Muse API key; setting it opts in to Muse tracking |
| `META_MUSE_MODEL` | Muse usage-probe model (default: Muse settings model, else `muse-spark-1.3`) |
| `MUSE_ENABLED` | `true` to opt in to Muse tracking, `false` to disable it (off by default: each poll spends a prompt) |
| `COMMAND_CODE_API_KEY` | Command Code API key (`user_...`) |
| `COMMANDCODE_ENABLED` | `false` disables Command Code even with credentials present |
| `COMMANDCODE_BASE_URL` | Override the Command Code API base URL (default `https://api.commandcode.ai`) |
| `COMMANDCODE_AUTH_PATH` | Override the Command Code auth-file path |
| `ZENMUX_MANAGEMENT_API_KEY` | Zenmux Management API key (not an inference key) |
| `ZENMUX_ENABLED` | `false` disables Zenmux polling even if a key is present |
| `ALIBABA_COOKIES` | Alibaba console cookies (Cookie header or Netscape cookies.txt text); keep private |
| `ALIBABA_COOKIES_FILE` | Path to an Alibaba cookies export, read at startup |
| `ALIBABA_REGION` | Alibaba console region (default `ap-southeast-1`) |
| `ALIBABA_ENABLED` | `true` to enable with dashboard accounts, `false` to disable even with cookies set |
| `ANTIGRAVITY_ENABLED` | Enable Antigravity provider (auto-detects local server) |
| `ANTIGRAVITY_SOURCE` | Data source: `both` (default), `cli` (agy), or `ide` |
| `ANTIGRAVITY_CLI_PATH` | Override path to the `agy` binary |
| `ANTIGRAVITY_BASE_URL` / `ANTIGRAVITY_CSRF_TOKEN` | Manual Antigravity connection (Docker) |
| `ONWATCH_API_INTEGRATIONS_ENABLED` | Enable or disable API Integrations ingestion (default: `true`) |
| `ONWATCH_API_INTEGRATIONS_DIR` | Directory onWatch tails for API Integrations JSONL events |
| `ONWATCH_API_INTEGRATIONS_RETENTION` | How long API Integrations rows are kept (default: `1440h` = 60 days, `0` disables pruning) |
| `ONWATCH_TRUSTED_PROXY_CIDRS` | CIDRs/IPs allowed to assert identity headers (required for `trusted_proxy`) |
| `ONWATCH_TRUSTED_USER_HEADER` | Identity header set by the proxy (default: `X-Forwarded-User`) |

</details>

<details>
<summary>HTTP API</summary>

All endpoints need a session cookie or Basic Auth. Add `?provider=<name>` to select a provider (`both` for all) and `&account=<id>` to select one account.

| Endpoint | Method | Description |
|---|---|---|
| `/api/current` | GET | Latest snapshot with summaries |
| `/api/history?range=6h` | GET | Chart history |
| `/api/cycles?type=<quota>` | GET | Reset cycle history |
| `/api/cycle-overview` | GET | Cross-quota values at peak usage |
| `/api/summary` | GET | Usage summaries |
| `/api/sessions` | GET | Usage sessions |
| `/api/insights` | GET | Usage insights |
| `/api/providers` | GET | Available providers |
| `/api/accounts` | GET/POST/PUT/DELETE | List, add, edit/pause, or remove accounts (credentials are write-only) |
| `/api/accounts/usage?account=<id>&range=30d` | GET | Account spend, daily and per-model usage |
| `/api/accounts/discover` | POST | Rescan optional omp credentials |
| `/api/api-integrations/current` | GET | API Integrations usage totals |
| `/api/api-integrations/history?range=` | GET | API Integrations chart history |
| `/api/api-integrations/health` | GET | API Integrations ingest health |
| `/api/settings` | GET/PUT | Settings (notifications, SMTP, providers, menubar) |
| `/api/password` | PUT | Change password |
| `/api/menubar/summary` | GET | Menubar snapshot (public on loopback) |
| `/api/update/check`, `/api/update/apply` | GET, POST | Check for and apply updates |
| `/metrics` | GET | Prometheus metrics |

</details>

---

## Docker

```bash
git clone https://github.com/onllm-dev/onwatch.git && cd onwatch
cp .env.docker.example .env      # add your keys
mkdir -p onwatch-data && sudo chown -R 65532:65532 onwatch-data
docker-compose up -d
```

- Images: `ghcr.io/onllm-dev/onwatch:latest` (distroless, ~10-12 MB) and `:alpine` (with a shell, needed for `docker exec`), for linux/amd64 and arm64.
- The container runs as UID 65532 and stores data in `/data`. Bind mounts must be owned by that UID, or use a named volume (`-v onwatch-data:/data`).
- Containers run in the foreground and log to stdout (`docker logs -f onwatch`).
- Local-login providers (Claude Code, Codex, Cursor, ...) need their credentials passed as environment variables, since the container cannot read your home directory.

---

## Operations

- **Data:** `~/.onwatch/data/` holds `onwatch.db` (SQLite, WAL) and logs (`.onwatch.log`, `menubar.log`; rotated at 50 MB, 3 backups).
- **Self-update:** `onwatch update` or the dashboard badge. Under systemd or launchd it restarts through the service manager. If a restart fails, the new binary is already installed; restart it with `sudo systemctl restart onwatch`, `launchctl kickstart -k gui/$UID/dev.onllm.onwatch`, or `onwatch stop && onwatch`.
- **Auto-start on macOS:** `onwatch service install | status | uninstall` manages a per-user LaunchAgent. On Linux the installer's systemd unit does this.
- **Anthropic 429 errors:** the usage API allows only a few requests per token. onWatch refreshes the OAuth token to get a fresh window and logs "Rate limit bypassed successfully" ([issue #16](https://github.com/onllm-dev/onWatch/issues/16)).

## Security

- API keys come from `.env` or the dashboard and are redacted in all logs.
- Dashboard credentials, SMTP passwords, webhook tokens and account credentials are protected at rest (password hash; AES-256-GCM with a key derived from the admin password).
- Session auth with Basic Auth fallback, constant-time credential comparison, parameterized SQL throughout.
- No telemetry: the only outbound requests go to the providers you configure.

## Development

```bash
./app.sh --build     # build ./onwatch
./app.sh --test      # tests with race detection
./app.sh --smoke     # vet, build check, short tests
nix develop          # optional dev shell (go, gopls, gofumpt)
```

See [DEVELOPMENT.md](docs/DEVELOPMENT.md) for cross-compilation and benchmarks. Contributions are welcome: write tests first, run `./app.sh --test`, and open a pull request with a conventional commit message.

<details>
<summary>Uninstall</summary>

```bash
# Homebrew
onwatch stop && brew uninstall onwatch && rm -rf ~/.onwatch

# macOS
onwatch service uninstall; onwatch stop; rm -rf ~/.onwatch
sed -i '' '/# onWatch/d; /\.onwatch/d' ~/.zshrc ~/.bash_profile 2>/dev/null

# Linux
onwatch stop; rm -rf ~/.onwatch
sed -i '/# onWatch/d; /\.onwatch/d' ~/.zshrc ~/.bashrc ~/.bash_profile 2>/dev/null
systemctl --user disable --now onwatch 2>/dev/null; rm -f ~/.config/systemd/user/onwatch.service
sudo systemctl disable --now onwatch 2>/dev/null; sudo rm -f /etc/systemd/system/onwatch.service
```

```powershell
# Windows
.\onwatch.exe stop
$p = [Environment]::GetEnvironmentVariable("Path","User"); [Environment]::SetEnvironmentVariable("Path",($p -split ";" | Where-Object {$_ -notlike "*\.onwatch*"}) -join ";","User")
Remove-Item -Recurse -Force $env:USERPROFILE\.onwatch
```

</details>

---

## License

GNU General Public License v3.0 - see [LICENSE](LICENSE). Powered by [onllm.dev](https://onllm.dev).

If onWatch saves you time, [buy me a coffee](https://buymeacoffee.com/prakersh) or star the repository.
