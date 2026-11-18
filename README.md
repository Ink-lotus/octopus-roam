<div align="center">

<img src="web/public/logo.svg" alt="Octopus Roam Logo" width="120" height="120">

### Octopus Roam

**A Simple, Beautiful, and Elegant LLM API Aggregation Service for Individuals**

 English | [简体中文](README_zh.md)

</div>

Octopus Roam is a fork of [bestruirui/octopus](https://github.com/bestruirui/octopus) v0.13.6.

## ✨ Features

### Retained Upstream Features

- 🔀 **Multi-Channel Aggregation** - Connect multiple LLM provider channels with unified management
- 🔄 **Protocol Conversion** - Seamless conversion between OpenAI Chat / OpenAI Responses / Anthropic API formats
- 💰 **Price Sync** - Automatic model pricing updates
- 🔃 **Model Sync** - Automatic synchronization of available model lists with channels
- 🛡️ **Automatic Failover** - Automatically switches to an available channel when an upstream channel fails
- 🔍 **Real-Time End-to-End Request Visualization** - Watch the complete request path in the frontend from the moment the client sends it
- 📊 **Analytics** - Comprehensive request statistics, token consumption, and cost tracking
- 🎨 **Elegant UI** - Clean and beautiful web management panel
- 📦 **Lightweight Single-Binary Deployment** - Run as a single binary with no external runtime dependencies
- 🗄️ **Database Drivers** - SQLite, MySQL and PostgreSQL; official support targets use SQLite

### Added Features

- 🔃 **Shared Bulk Refresh** - One task across the channel and group pages, covering enabled channels that follow global updates regardless of display filters
- 🎛️ **Channel Compatibility Options** - Optionally remove explicit Responses history references while preserving client-provided history, or restrict custom headers to model discovery; both default to off
- ⏱️ **Custom Cooldown Rules** - Optional model or channel cooldowns based on upstream errors
- 📁 **Platform Categories** - Persisted, manually assigned categories with filtering and collapsible group lists
- 🛡️ **Retry and Health Protection** - Fixed intervals or exponential backoff and health tracking across requests; streaming requests are not replayed after the response has started
- 🚦 **Channel Concurrency Control** - Per-channel concurrency limits and a request queue shared across groups
- 🔔 **Webhook Notifications** - Group failure and recovery notifications with customizable templates
- 🔐 **Encrypted Backups** - Protect backups with a separate passphrase and export or restore channels, groups, cooldown rules, categories and other data
- 🪟 **Native Windows Management** - Menu-based start and stop, automatic startup at login and a native update helper

## Support scope

Supported deployments are the native Windows amd64 application with SQLite, and the Linux amd64 image with SQLite on a Linux amd64 host running Docker Engine, including Compose. The first Linux application acceptance run passed on the C02 development build from commit `9a7ed524bf763bc776ef291a0998aeb715700ad4`; each v0.1.4 candidate and publication run must still validate its own artifacts. See the [exact inputs and acceptance scope](docs/support.md). WSL2 / WSL Containers is expected to be compatible and is not a separate required release test environment.

Published versions retain their original support statements and acceptance records; see the [v0.1.2 release notes](docs/releases/v0.1.2.md). Native Linux, macOS and Android builds and other image architectures remain experimental. MySQL/PostgreSQL functionality and tests remain available outside the official support scope. All 11 program ZIPs, four image architectures and corresponding source remain available. See the [current support matrix and acceptance status](docs/support.md).

## 🚀 Quick Start

The instructions below describe the **v0.1.4** delivery layout. Before installation, confirm that this version is publicly available in this repository's Releases and verify its checksums. Candidate artifacts are for isolated acceptance. When upgrading from v0.1.0, follow the [current upgrade guide](docs/upgrade-r1.md). The first Windows migration from the old layout requires manually updating both the executable and cmd entry.

### 🐳 Linux amd64 / Docker Engine

Use the verified **v0.1.4** image after it is published. On a Linux amd64 host with Docker Engine installed, create a dedicated data directory on a Linux filesystem and start a new instance:

```bash
mkdir ./octopus-roam-data
docker run --detach --name octopus-roam \
  --publish 127.0.0.1:8080:8080 \
  --volume "$(pwd)/octopus-roam-data:/app/data" \
  --env PUID="$(id -u)" --env PGID="$(id -g)" \
  ghcr.io/ink-lotus/octopus-roam:v0.1.4-amd64
```

Data persists in the host directory. The example maps the application user to your Linux UID/GID and listens on the host's loopback address at `http://127.0.0.1:8080`; use SSH port forwarding to access a remote host. Change the default password after first login. For an existing deployment, follow the [upgrade and recovery steps](docs/upgrade-r1.md) and retain its data and permission settings.

### Docker Compose

In the repository's `docker-compose.yml`, replace `/path/to/data` with the absolute path to a dedicated Linux data directory. For local access only, set the port mapping to `127.0.0.1:8080:8080`. Set `PUID` and `PGID` in the service's `environment` when a specific application UID/GID is needed; the current image defaults both to `0` when omitted.

For a new instance, choose either Docker run or Compose. The Compose file requires an explicit published tag; from its directory in a Linux shell, run:

```bash
export OCTOPUS_ROAM_VERSION='v0.1.4-amd64'
docker compose config --quiet
docker compose up -d
```

The stable release updates `latest`; RC releases do not. Use an exact published tag or a verified digest and follow the [upgrade and backup instructions](docs/upgrade-r1.md) when replacing an existing container. WSL2 / WSL Containers remains an expected-compatible option; host mounts, permissions and networking may differ from Linux and are not separately required release tests.


### 📦 Download from Release

Download the [Windows amd64 ZIP](https://github.com/Ink-lotus/octopus-roam/releases/download/v0.1.4/octopus-roam-windows-amd64.zip), or an experimental ZIP for another platform, and [SHA256SUMS](https://github.com/Ink-lotus/octopus-roam/releases/download/v0.1.4/SHA256SUMS) from [v0.1.4](https://github.com/Ink-lotus/octopus-roam/releases/tag/v0.1.4), verify the checksum, and extract the entire archive into a permanent folder. The same release includes [corresponding source](https://github.com/Ink-lotus/octopus-roam/releases/download/v0.1.4/octopus-roam-source-v0.1.4.zip), original notices and rebuild instructions.

**Windows:** double-click `octopus-roam.cmd` and choose a numbered option:

1. Start the service.
2. Start the service and enable automatic startup.
3. Stop the service.
4. Stop the service and disable automatic startup.

The first start creates the configuration and data directories automatically. The default management address is `http://127.0.0.1:8080`; existing configuration is preserved. Closing the menu leaves the service running; choose `3` or `4` to stop it. v0.1.4 uses one cmd entry and native executable commands, with a per-user scheduled logon task. Published v0.1.0 packages still require their `windows` support folder. The first migration to the native layout requires manually updating both the executable and cmd entry; see the [migration instructions](docs/local-deployment.md#从旧脚本布局迁移).

**Experimental native Linux / macOS and other builds:** run in the extracted directory; native process upgrade and recovery remain unvalidated:

```bash
./octopus-roam start
```

### 🛠️ Build from Source

**Requirements:**
- Go 1.27.3
- Node.js 24.18.0
- pnpm 11.15.1

These are the reviewed release tool versions. See the release's `REBUILD.md` for source verification and offline reproduction.

```bash
# From this repository's root, build the frontend
pnpm --dir web install --frozen-lockfile
pnpm --dir web build
# Start the backend service
go run . start
```

> 💡 **Tip**: The frontend build artifacts are embedded into the Go binary, so you must build the frontend before starting the backend.

For an offline local build, use `pwsh -NoProfile -File scripts/build-local.ps1`. The default version is `v0.1.4-local.<timestamp>`. The helper writes only to a new `build/release-local/` directory, uses existing caches, and reports when the third-party license report is unavailable. Reviewed release materials use the separate [release contract](docs/release-ci.md).

For local deployment validation, keep the executable, configuration, database, and logs in a separate directory outside the repository. See the [local deployment guide (Chinese)](docs/local-deployment.md).

**Development Mode**

```bash
cd web && pnpm install && pnpm run dev
## Open a new terminal, start the backend service
go run main.go start
## Access the frontend at
http://localhost:5173
```

### 🔐 Default Credentials

After first launch, visit http://localhost:8080 and log in to the management panel with:

- **Username**: `admin`
- **Password**: `admin`

> ⚠️ **Security Notice**: Please change the default password immediately after first login.

### 📝 Configuration File

The configuration file is located at `data/config.json` by default and is automatically generated on first startup.

**Complete Configuration Example:**

```json
{
  "server": {
    "host": "0.0.0.0",
    "port": 8080
  },
  "database": {
    "type": "sqlite",
    "path": "data/data.db"
  },
  "log": {
    "level": "info"
  }
}
```

**Configuration Options:**

| Option | Description | Default |
|--------|-------------|---------|
| `server.host` | Listen address | `0.0.0.0` |
| `server.port` | Server port | `8080` |
| `database.type` | Database type | `sqlite` |
| `database.path` | Database connection string | `data/data.db` |
| `log.level` | Log level | `info` |

**Database Configuration:**

Three database drivers are available. Official support targets use SQLite; MySQL/PostgreSQL configurations remain outside that scope:

| Type | `database.type` | `database.path` Format |
|------|-----------------|-----------------------|
| SQLite | `sqlite` | `data/data.db` |
| MySQL | `mysql` | `user:password@tcp(host:port)/dbname` |
| PostgreSQL | `postgres` | `postgresql://user:password@host:port/dbname?sslmode=verify-full` |

**MySQL Configuration Example:**

```json
{
  "database": {
    "type": "mysql",
    "path": "root:password@tcp(127.0.0.1:3306)/octopus-roam"
  }
}
```

**PostgreSQL Configuration Example:**

```json
{
  "database": {
    "type": "postgres",
    "path": "postgresql://user:password@localhost:5432/octopus-roam?sslmode=verify-full"
  }
}
```

> 💡 **Tip**: MySQL and PostgreSQL require manual database creation. The application will automatically create the table structure.

### 🌐 Environment Variables

All configuration options can be overridden via environment variables using the format `OCTOPUS_ROAM_` + configuration path (joined with `_`):

| Environment Variable | Configuration Option |
|---------------------|---------------------|
| `OCTOPUS_ROAM_SERVER_PORT` | `server.port` |
| `OCTOPUS_ROAM_SERVER_HOST` | `server.host` |
| `OCTOPUS_ROAM_DATABASE_TYPE` | `database.type` |
| `OCTOPUS_ROAM_DATABASE_PATH` | `database.path` |
| `OCTOPUS_ROAM_LOG_LEVEL` | `log.level` |
| `OCTOPUS_ROAM_GITHUB_PAT` | Optional token for the fixed Octopus Roam release API (never sent to asset hosts) |

## 📸 Screenshots

### 🖥️ Desktop

<div align="center">
<table>
<tr>
<td align="center"><b>Dashboard</b></td>
<td align="center"><b>Channel Management</b></td>
<td align="center"><b>Group Management</b></td>
</tr>
<tr>
<td><img src="web/public/screenshot/desktop-home.png" alt="Dashboard" width="400"></td>
<td><img src="web/public/screenshot/desktop-channel.png" alt="Channel" width="400"></td>
<td><img src="web/public/screenshot/desktop-group.png" alt="Group" width="400"></td>
</tr>
<tr>
<td align="center"><b>Price Management</b></td>
<td align="center"><b>Logs</b></td>
<td align="center"><b>Settings</b></td>
</tr>
<tr>
<td><img src="web/public/screenshot/desktop-price.png" alt="Price Management" width="400"></td>
<td><img src="web/public/screenshot/desktop-log.png" alt="Logs" width="400"></td>
<td><img src="web/public/screenshot/desktop-setting.png" alt="Settings" width="400"></td>
</tr>
</table>
</div>

### 📱 Mobile

<div align="center">
<table>
<tr>
<td align="center"><b>Home</b></td>
<td align="center"><b>Channel</b></td>
<td align="center"><b>Group</b></td>
<td align="center"><b>Price</b></td>
<td align="center"><b>Logs</b></td>
<td align="center"><b>Settings</b></td>
</tr>
<tr>
<td><img src="web/public/screenshot/mobile-home.png" alt="Mobile Home" width="140"></td>
<td><img src="web/public/screenshot/mobile-channel.png" alt="Mobile Channel" width="140"></td>
<td><img src="web/public/screenshot/mobile-group.png" alt="Mobile Group" width="140"></td>
<td><img src="web/public/screenshot/mobile-price.png" alt="Mobile Price" width="140"></td>
<td><img src="web/public/screenshot/mobile-log.png" alt="Mobile Logs" width="140"></td>
<td><img src="web/public/screenshot/mobile-setting.png" alt="Mobile Settings" width="140"></td>
</tr>
</table>
</div>


## 📖 Documentation

### 📡 Channel Management

Channels are the basic configuration units for connecting to LLM providers.

**Base URL Guide:**

The program automatically appends the API version and endpoint path based on the channel type. You only need to provide the service root URL:

| Channel Type | Auto-appended Path | Base URL | Full Request URL Example |
|--------------|-------------------|----------|--------------------------|
| OpenAI Chat | `/v1/chat/completions` | `https://api.openai.com` | `https://api.openai.com/v1/chat/completions` |
| OpenAI Responses | `/v1/responses` | `https://api.openai.com` | `https://api.openai.com/v1/responses` |
| Anthropic | `/v1/messages` | `https://api.anthropic.com` | `https://api.anthropic.com/v1/messages` |

Protocol paths are editable per channel. Gemini providers must expose an OpenAI-compatible endpoint; native Gemini requests are not supported by this fork.

### Independent routing, automation, and backups

Global channel updates default to off; the interval defaults to 1440 minutes and automatic group additions to off. Channels follow global updates unless explicitly opted out; opted-out channels are excluded from automatic updates and bulk refresh. A concurrency limit of `0` is unlimited; positive limits share capacity across keys and groups, including the full lifetime of a stream.

All keys must refresh successfully before model changes commit. A successful empty response removes obsolete models and group members while preserving the channel and group. Keyword matching is the default; exact matching excludes suffix variants. Applying global routing defaults preserves each group's manual/failover mode, members, order, and active selection.

The channel and group pages share one bulk refresh task and progress display. It selects enabled channels that follow global updates, rechecks before probing, and normally skips channels disabled, deleted, opted out, or already being refreshed while waiting. Switching pages does not stop it and repeated clicks do not start another task. Group search and category filters do not narrow its targets. Explicit single-channel refresh keeps its existing behavior; requests already sent cannot be recalled, and reloading the browser does not resume the task.

In channel advanced settings, add cooldown rules only when needed. Each rule has an exact HTTP status/upstream code or type, a plain-text keyword, an integer duration in seconds (`1..2147483647`), and a model or channel scope. At least one matching condition is required; filling both uses AND. Keywords ignore English letter case and inspect the bounded upstream error message (up to 4096 UTF-8 bytes), without regex or scripts. Empty rules preserve existing retry and member backoff behavior.

Model scope covers the actual upstream model on that channel across keys, protocol grants and groups; channel scope covers the whole channel. Multiple cooldown sources use the latest deadline. Only actual Relay upstream failures trigger rules, including streamed errors within HTTP 200; normal output, prompts, client cancellation and model-list probes do not. Success and refresh do not clear a rule cooldown. Waiting releases channel capacity, and the existing member-cooldown release action does not clear shared rule cooldowns. Rules persist, while active timers reset on process restart and are not backed up. Rule or outbound connection/credential changes invalidate old rule state; display names and categories do not.

Channel advanced settings can remove only the top-level `previous_response_id` and first-level `input` items of type `item_reference` from outbound Responses requests, preserving all other client-provided history, IDs and encrypted content. The separate custom-header option limits all channel custom headers to model discovery and refresh. Both default to off and persist with the channel. See the [history contract](docs/responses-history-cleanup.md) and [v0.1.4 notes](docs/releases/v0.1.4.md).

Backups require a separate password of at least 12 characters and download as encrypted JSON. Keep the password separately: it cannot be recovered. Export scripts use authenticated `POST /api/v1/setting/export` with the password in the JSON body; the former GET returns 405. v0.1.4 exports `octopus-roam` content **1004**, including archived legacy hourly rows, rules, categories, group assignments and both channel compatibility options, in encrypted container **1**. Exports containing native MySQL/PostgreSQL hourly archives use content **1005**, requiring a compatible reader. It accepts product content 1000–1005 and audited unmarked upstream formats 0/5; unmarked 6 and unknown identities remain rejected. Incremental import accepts only compatible identities and references; conflicting configurations, credentials or unique keys reject the entire import with 409. Compatible replay retains the documented local state, including a group's category and active member. Use an empty instance for complete restoration; see the [integrity contract](docs/backup-integrity.md).

Migration 18 on SQLite, MySQL and PostgreSQL archives legacy hourly rows with `hour >= 24` before removing them from active statistics, fixing the v0.1.3 startup failure. It preserves original values without summing or assigning them to another hour. Archives are included in encrypted backups and empty-instance restoration. Old encrypted backups containing invalid hours require the explicit offline `backup-convert-hourly` command; see [hourly recovery](docs/hourly-statistics-recovery.md).

Save the old executable, configuration and complete database snapshot before upgrading. Content 1004 requires v0.1.4 or a later compatible release for restoration, and the old program is not promised to read the migrated database. Rollback restores the pre-upgrade snapshot and loses data added after that snapshot. The [upgrade guide](docs/upgrade-r1.md) explains the first Windows migration, incremental import, defaults and troubleshooting.

The Settings page checks only the Octopus Roam release source. Stable builds update only to a higher stable version; an official RC may update to a later RC or the corresponding stable version. Development and local builds show their status without inferring an upgrade order. A candidate is downloaded into an isolated directory and must match the same release's `SHA256SUMS` before replacement. The running program, configuration, database, and backup directories are kept separate. Container deployments are upgraded by replacing the image and restarting the container; the program is not replaced from inside the container.

---

### 📁 Group Management

Groups aggregate multiple channels into a unified external model name.

Current source supports one level of custom platform categories. Create, rename or delete categories, then assign each group manually to a category or Uncategorized. Deleting a category only removes assignments. Categories and assignments are stored on the server; category filters and collapsed sections are browser preferences. Name search, sorting, filtered counts, empty categories and virtualization remain available. Category changes preserve routing, member order, manual selection, failure counts and cooldowns.

**Core Concepts:**

- **Group name** is the model name exposed by the program
- When calling the API, set the `model` parameter to the group name

> 💡 **Example**: Create a group named `gpt-4o`, add multiple providers' GPT-4o channels to it, then access all channels via a unified `model: gpt-4o`.

---

### 💰 Price Management

Manage model pricing information in the system.

**Data Sources:**

- The system periodically syncs model pricing data from [models.dev](https://github.com/sst/models.dev)
- When creating a channel, if the channel contains models not in models.dev, the system automatically creates pricing information for those models on this page, so this page displays models that haven't had their prices fetched from upstream, allowing users to set prices manually
- Manual creation of models that exist in models.dev is also supported for custom pricing

**Price Priority:**

| Priority | Source | Description |
|:--------:|--------|-------------|
| 🥇 High | This Page | Prices set by user in price management page |
| 🥈 Low | models.dev | Auto-synced default prices |

> 💡 **Tip**: To override a model's default price, simply set a custom price for it in the price management page.

---

### ⚙️ Settings

Global system configuration.

**Statistics Save Interval (minutes):**

Since the program handles numerous statistics, writing to the database on every request would impact read/write performance. The program uses this strategy:

- Statistics are first stored in **memory**
- Periodically **batch-written** to the database at the configured interval

> ⚠️ **Important**: When exiting the program, use proper shutdown methods (like `Ctrl+C` or sending `SIGTERM` signal) to ensure in-memory statistics are correctly written to the database. **Do NOT use `kill -9` or other forced termination methods**, as this may result in statistics data loss.

---

## 🔌 Client Integration

### OpenAI SDK

```python
from openai import OpenAI
import os

client = OpenAI(   
    base_url="http://127.0.0.1:8080/v1",   
    api_key="sk-octopus-roam-P48ROljwJmWBYVARjwQM8Nkiezlg7WOrXXOWDYY8TI5p9Mzg",
)
completion = client.chat.completions.create(
    model="octopus-roam-openai",  # Use the correct group name
    messages = [
        {"role": "user", "content": "Hello"},
    ],
)
print(completion.choices[0].message.content)
```

### Claude Code

Edit `~/.claude/settings.json`

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:8080",
    "ANTHROPIC_AUTH_TOKEN": "sk-octopus-roam-P48ROljwJmWBYVARjwQM8Nkiezlg7WOrXXOWDYY8TI5p9Mzg",
    "API_TIMEOUT_MS": "3000000",
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
    "ANTHROPIC_MODEL": "octopus-roam-sonnet-4-5",
    "ANTHROPIC_SMALL_FAST_MODEL": "octopus-roam-haiku-4-5",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "octopus-roam-sonnet-4-5",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "octopus-roam-sonnet-4-5",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "octopus-roam-haiku-4-5"
  }
}
```

### Codex

The optional [Codex model catalog](docs/codex-catalog.md) serves the matching GitHub release snapshot with gateway permissions. Enable `codex_catalog.enabled` on the gateway before setting `model_catalog_url`; it is disabled by default.

Edit `~/.codex/config.toml`

```toml
model = "gpt-5.6-sol"
model_reasoning_effort = "xhigh"
model_provider = "octopus-roam"
preferred_auth_method = "apikey"

[model_providers.octopus-roam]
base_url = "http://127.0.0.1:8080/v1"
# Optional, after enabling the gateway catalog:
# model_catalog_url = "http://127.0.0.1:8080/codex/models"
name = "octopus-roam"
supports_websockets = false
requires_openai_auth = true
wire_api = "responses"
experimental_bearer_token = "sk-octopus-roam-"
```

Edit `~/.codex/auth.json`

```json
{
  "OPENAI_API_KEY": ""
}
```

---

## 📝 Changelog

[Read the detailed changelog](CHANGELOG.md)

## 🤝 Acknowledgments

- 🙏 [looplj/axonhub](https://github.com/looplj/axonhub) - The LLM API adaptation module in this project is directly derived from this repository
- 📊 [sst/models.dev](https://github.com/sst/models.dev) - AI model database providing model pricing data
- 💬 [Linux.do](https://linux.do/)
