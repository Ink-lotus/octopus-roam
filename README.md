<div align="center">

<img src="web/public/logo.svg" alt="Octopus Roam Logo" width="120" height="120">

### Octopus Roam

**A Simple, Beautiful, and Elegant LLM API Aggregation Service for Individuals**

 English | [简体中文](README_zh.md)

</div>

Octopus Roam is a fork of [bestruirui/octopus](https://github.com/bestruirui/octopus), based on v0.13.6 with selected v0.13.8 changes. Its independent version line starts at **v0.1.0**. R1 is under validation; no independent release has been published yet. See the [changelog](CHANGELOG.md), [upgrade and backup guide](docs/upgrade-r1.md), and [R1 results](plan/r1-result.md).


## ✨ Features

- 🔀 **Multi-Channel Aggregation** - Connect multiple LLM provider channels with unified management
- 🔄 **Protocol Conversion** - Seamless conversion between OpenAI Chat / OpenAI Responses / Anthropic API formats
- 💰 **Price Sync** - Automatic model pricing updates
- 🔃 **Model Sync** - Automatic synchronization of available model lists with channels
- 🛡️ **Automatic Failover** - Automatically switches to an available channel when an upstream channel fails
- 🔍 **Real-Time End-to-End Request Visualization** - Watch the complete request path in the frontend from the moment the client sends it
- 🚧 **Upstream Error Shielding** - Intercept all upstream errors to keep agent tasks running without interruption
- 📊 **Analytics** - Comprehensive request statistics, token consumption, and cost tracking
- 🎨 **Elegant UI** - Clean and beautiful web management panel
- 📦 **Lightweight Single-Binary Deployment** - Run as a single binary with no external runtime dependencies
- 🗄️ **Multi-Database Support** - Support for SQLite, MySQL, PostgreSQL


## 🚀 Quick Start

### 🐳 Docker

After the independent release is published, use its image and explicit version tag (replace `<version>`):

```bash
docker run -d --name octopus-roam -v /path/to/data:/app/data -p 8080:8080 ghcr.io/ink-lotus/octopus-roam:<version>
```

`docker-compose.yml` uses the same independent image with the `latest` tag by default. Set `OCTOPUS_ROAM_VERSION` to a published tag such as `v0.1.0-rc.1` to select a specific version. Candidate releases do not update `latest`; use a candidate tag until the first stable release. The image has not been published yet.


### 📦 Download from Release

When available, download the archive for your platform from [Octopus Roam Releases](https://github.com/Ink-lotus/octopus-roam/releases) and extract the entire archive into a permanent folder.

**Windows:** double-click `octopus-roam.cmd` and choose a numbered option:

1. Start the service.
2. Start the service and enable automatic startup.
3. Stop the service.
4. Stop the service and disable automatic startup.

The first start creates the configuration and data directories automatically. The default management address is `http://127.0.0.1:8080`; existing configuration is preserved. Automatic startup runs when the current Windows user signs in. The menu shows service and startup status. Closing the menu leaves the service running; choose `3` or `4` to stop it. Keep the included `windows` folder, which contains the menu's support files.

**Linux / macOS and other platforms:** run from the extracted folder:

```bash
./octopus-roam start
```

### 🛠️ Build from Source

**Requirements:**
- Go 1.26.4 (see `go.mod`)
- Node.js 22.12+
- pnpm 11.10.0

```bash
# From this repository's root, build the frontend
pnpm --dir web install --frozen-lockfile
pnpm --dir web build
# Start the backend service
go run . start
```

> 💡 **Tip**: The frontend build artifacts are embedded into the Go binary, so you must build the frontend before starting the backend.

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

Three database types are supported:

| Type | `database.type` | `database.path` Format |
|------|-----------------|-----------------------|
| SQLite | `sqlite` | `data/data.db` |
| MySQL | `mysql` | `user:password@tcp(host:port)/dbname` |
| PostgreSQL | `postgres` | `postgresql://user:password@host:port/dbname?sslmode=disable` |

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
    "path": "postgresql://user:password@localhost:5432/octopus-roam?sslmode=disable"
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
| `OCTOPUS_ROAM_GITHUB_PAT` | For rate limiting when getting the latest version (optional) |

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

### Solo routing, automation, and backups

Global channel updates default to off; the interval defaults to 1440 minutes and automatic group additions to off. Channels follow the global switch unless explicitly opted out. A concurrency limit of `0` is unlimited; positive limits share capacity across keys and groups, including the full lifetime of a stream.

All keys must refresh successfully before model changes commit. A successful empty response removes obsolete models and group members while preserving the channel and group. Keyword matching is the default; exact matching excludes suffix variants. Applying global routing defaults preserves each group's manual/failover mode, members, order, and active selection.

Backups require a separate password of at least 12 characters and download as encrypted JSON. Keep the password separately: it cannot be recovered. Export scripts must use authenticated `POST /api/v1/setting/export` with a password in the JSON body; the former GET returns 405. Import supports encrypted backups and audited upstream formats 0/5; unmarked pre-release format 6 is rejected. Save the old database and executable before upgrading—new backups cannot be restored by old builds.

The [upgrade guide](docs/upgrade-r1.md) lists defaults, incremental import rules, status definitions, rollback steps, and troubleshooting for failed keys, empty models, queued requests, and Webhooks.

---

### 📁 Group Management

Groups aggregate multiple channels into a unified external model name.

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

Edit `~/.codex/config.toml`

```toml
model = "gpt-5.6-sol"
model_reasoning_effort = "xhigh"
model_provider = "octopus-roam"
preferred_auth_method = "apikey"

[model_providers.octopus-roam]
base_url = "http://127.0.0.1:8080/v1"
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

## 🤝 Acknowledgments

- 🙏 [looplj/axonhub](https://github.com/looplj/axonhub) - The LLM API adaptation module in this project is directly derived from this repository
- 📊 [sst/models.dev](https://github.com/sst/models.dev) - AI model database providing model pricing data
- 🇨🇳 [Upstream AtomGit](https://atomgit.com/bestruirui/octopus) - China-based hosting for the original project
- 💬 [Linux.do](https://linux.do/)
