<div align="center">

<img src="web/public/logo.svg" alt="Octopus Roam Logo" width="120" height="120">

### Octopus Roam

**为个人打造的简单、美观、优雅的 LLM API 聚合服务**

简体中文 | [English](README.md)

</div>

Octopus Roam 从 [bestruirui/octopus](https://github.com/bestruirui/octopus) v0.13.6 分叉，已选择性吸收 v0.13.8。独立版本线从 **v0.1.0** 起算；独立更新路径已完成本地验证，尚未发布独立候选版或正式版。参见[变更日志](CHANGELOG.md)、[升级与备份说明](docs/upgrade-r1.md)和[发布计划](plan/release-plan-v0.1.0.md)。


## ✨ 特性

- 🔀 **多渠道聚合** - 支持接入多个 LLM 供应商渠道，统一管理
- 🔄 **协议互转** - 支持 OpenAI Chat / OpenAI Responses / Anthropic 三种 API 格式互相转换
- 💰 **价格同步** - 自动更新模型价格
- 🔃 **模型同步** - 自动与渠道同步可用模型列表，省心省力
- 🛡️ **自动故障转移** - 上游渠道发生故障时自动切换到可用渠道
- 🔍 **请求全链路实时可视化** - 客户端发起请求后，即可在前端实时查看完整请求链路
- 🛡️ **重试与健康保护** - 保护跨请求重试和渠道健康状态；流式响应已经发送数据后，上游故障仍可能结束本次响应
- 📊 **数据统计** - 全面的请求统计、Token 消耗、费用追踪
- 🎨 **优雅界面** - 简洁美观的 Web 管理面板
- 📦 **轻量单文件部署** - 单个二进制文件即可运行，无需额外运行时依赖
- 🗄️ **多数据库支持** - 支持 SQLite、MySQL、PostgreSQL


## 🚀 快速开始

### 🐳 Docker 运行

独立版本发布后使用其镜像和明确版本号，替换下方 `<version>`：

```bash
docker run -d --name octopus-roam -v /path/to/data:/app/data -p 8080:8080 ghcr.io/ink-lotus/octopus-roam:<version>
```

`docker-compose.yml` 使用同一独立镜像。首个正式版发布前须将 `OCTOPUS_ROAM_VERSION` 明确设置为已发布的 RC 标签，例如 `v0.1.0-rc.1`；候选版不会更新 `latest`，当前仓库尚未发布镜像。


### 📦 从 Release 下载

独立版本发布后，从 [Octopus Roam Releases](https://github.com/Ink-lotus/octopus-roam/releases) 下载对应平台的压缩包，并将整个压缩包解压到固定目录。

**Windows：**双击 `octopus-roam.cmd`，输入序号即可操作，无需输入命令：

1. 启动服务。
2. 启动服务并添加开机自启。
3. 关闭服务。
4. 关闭服务并关闭开机自启。

首次启动自动创建配置和数据目录，默认管理地址为 `http://127.0.0.1:8080`；已有配置会继续使用。开机自启在当前 Windows 用户登录后生效。菜单会显示运行及自启状态，关闭菜单窗口后服务仍在后台运行；停止服务请选择 `3` 或 `4`。请保留压缩包中的 `windows` 文件夹，里面是菜单所需的支持文件。

**Linux / macOS 等平台：**在解压目录运行：

```bash
./octopus-roam start
```

### 🛠️ 源码运行

**环境要求：**
- Go 1.26.4（以 `go.mod` 为准）
- Node.js 22.12+
- pnpm 11.10.0

```bash
# 在本仓库根目录构建前端
pnpm --dir web install --frozen-lockfile
pnpm --dir web build
# 启动后端服务
go run . start
```

> 💡 **提示**：前端构建产物会被嵌入到 Go 二进制文件中，所以必须先构建前端再启动后端。

离线准备本地发布候选版可使用 `powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File scripts/build-local.ps1 -Version v0.1.0-local.<编号>`。脚本只写入新的 `build/release-local/` 目录，使用已有缓存；缺少第三方许可证报告时会在构建信息中明确标记。

本地部署验收建议使用仓库外的独立运行目录，放置程序、配置、数据库及日志；见[本地部署说明](docs/local-deployment.md)。

**开发模式**

```bash
cd web && pnpm install && pnpm run dev
## 新建终端,启动后端服务
go run main.go start
## 访问前端地址
http://localhost:5173
```

### 🔐 默认账户

首次启动后，访问 http://localhost:8080 使用以下默认账户登录管理面板：

- **用户名**：`admin`
- **密码**：`admin`

> ⚠️ **安全提示**：请在首次登录后立即修改默认密码。

### 📝 配置文件

配置文件默认位于 `data/config.json`，首次启动时自动生成。

**完整配置示例：**

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

**配置项说明：**

| 配置项 | 说明 | 默认值 |
|--------|------|--------|
| `server.host` | 监听地址 | `0.0.0.0` |
| `server.port` | 服务端口 | `8080` |
| `database.type` | 数据库类型 | `sqlite` |
| `database.path` | 数据库连接地址 | `data/data.db` |
| `log.level` | 日志级别 | `info` |

**数据库配置：**

支持三种数据库：

| 类型 | `database.type` | `database.path` 格式 |
|------|-----------------|---------------------|
| SQLite | `sqlite` | `data/data.db` |
| MySQL | `mysql` | `user:password@tcp(host:port)/dbname` |
| PostgreSQL | `postgres` | `postgresql://user:password@host:port/dbname?sslmode=verify-full` |

**MySQL 配置示例：**

```json
{
  "database": {
    "type": "mysql",
    "path": "root:password@tcp(127.0.0.1:3306)/octopus-roam"
  }
}
```

**PostgreSQL 配置示例：**

```json
{
  "database": {
    "type": "postgres",
    "path": "postgresql://user:password@localhost:5432/octopus-roam?sslmode=verify-full"
  }
}
```

> 💡 **提示**：MySQL 和 PostgreSQL 需要先手动创建数据库，程序会自动创建表结构。

**环境变量：**

所有配置项均可通过环境变量覆盖，格式为 `OCTOPUS_ROAM_` + 配置路径（用 `_` 连接）：

| 环境变量 | 对应配置项 |
|----------|-----------|
| `OCTOPUS_ROAM_SERVER_PORT` | `server.port` |
| `OCTOPUS_ROAM_SERVER_HOST` | `server.host` |
| `OCTOPUS_ROAM_DATABASE_TYPE` | `database.type` |
| `OCTOPUS_ROAM_DATABASE_PATH` | `database.path` |
| `OCTOPUS_ROAM_LOG_LEVEL` | `log.level` |
| `OCTOPUS_ROAM_GITHUB_PAT` | 固定 Octopus Roam 发布 API 的可选速率限制令牌（不会发送给归档主机） |


## 📸 界面预览

### 🖥️ 桌面端

<div align="center">
<table>
<tr>
<td align="center"><b>首页</b></td>
<td align="center"><b>渠道</b></td>
<td align="center"><b>分组</b></td>
</tr>
<tr>
<td><img src="web/public/screenshot/desktop-home.png" alt="首页" width="400"></td>
<td><img src="web/public/screenshot/desktop-channel.png" alt="渠道" width="400"></td>
<td><img src="web/public/screenshot/desktop-group.png" alt="分组" width="400"></td>
</tr>
<tr>
<td align="center"><b>价格</b></td>
<td align="center"><b>日志</b></td>
<td align="center"><b>设置</b></td>
</tr>
<tr>
<td><img src="web/public/screenshot/desktop-price.png" alt="价格" width="400"></td>
<td><img src="web/public/screenshot/desktop-log.png" alt="日志" width="400"></td>
<td><img src="web/public/screenshot/desktop-setting.png" alt="设置" width="400"></td>
</tr>
</table>
</div>

### 📱 移动端

<div align="center">
<table>
<tr>
<td align="center"><b>首页</b></td>
<td align="center"><b>渠道</b></td>
<td align="center"><b>分组</b></td>
<td align="center"><b>价格</b></td>
<td align="center"><b>日志</b></td>
<td align="center"><b>设置</b></td>
</tr>
<tr>
<td><img src="web/public/screenshot/mobile-home.png" alt="移动端首页" width="140"></td>
<td><img src="web/public/screenshot/mobile-channel.png" alt="移动端渠道" width="140"></td>
<td><img src="web/public/screenshot/mobile-group.png" alt="移动端分组" width="140"></td>
<td><img src="web/public/screenshot/mobile-price.png" alt="移动端价格" width="140"></td>
<td><img src="web/public/screenshot/mobile-log.png" alt="移动端日志" width="140"></td>
<td><img src="web/public/screenshot/mobile-setting.png" alt="移动端设置" width="140"></td>
</tr>
</table>
</div>


## 📖 功能说明

### 📡 渠道管理

渠道是连接 LLM 供应商的基础配置单元。

**Base URL 说明：**

程序会根据渠道类型自动补全 API 版本和端点路径，您只需填写服务根地址即可：

| 渠道类型 | 自动补全路径 | 填写 URL | 完整请求地址示例 |
|----------|-------------|----------|-----------------|
| OpenAI Chat | `/v1/chat/completions` | `https://api.openai.com` | `https://api.openai.com/v1/chat/completions` |
| OpenAI Responses | `/v1/responses` | `https://api.openai.com` | `https://api.openai.com/v1/responses` |
| Anthropic | `/v1/messages` | `https://api.anthropic.com` | `https://api.anthropic.com/v1/messages` |

各协议路径可在渠道配置中修改。Gemini 服务商须使用其 OpenAI 兼容端点；本项目不提供 Gemini 原生协议。

### 独立版本的路由、自动化与备份

全局渠道自动更新默认关闭，周期为 1440 分钟，自动追加分组成员默认关闭。渠道默认跟随全局开关，可以单独退出。并发上限 `0` 表示不限，正数表示跨 Key、跨分组共享额度，流式请求持续占用至结束。

模型刷新须全部 Key 成功后才提交。成功空结果会清理模型和失效成员，保留渠道和分组实体。分组默认关键词匹配，严格匹配排除后缀变体。应用全局路由默认策略时保留每个分组的手动/故障转移模式、成员顺序与当前成员。

备份须设置至少 12 个字符的独立口令，下载为加密 JSON；口令无法找回，应单独保管。导出脚本改用已认证的 `POST /api/v1/setting/export`，在 JSON 请求体传入口令；旧 GET 返回 405。导入保留已审计的上游 0/5 格式，拒绝无标识预发布 6 号。升级前保存旧数据库和可执行文件，新备份不能交给旧程序恢复。

完整默认值、增量导入规则、日志状态、回滚步骤和 Key 失败/模型为空/并发排队/Webhook 排障见[升级说明](docs/upgrade-r1.md)。

设置页只检查 Octopus Roam 自己的发布源。稳定版只升级到更高稳定版；官方 RC 可升级到更高 RC 或对应稳定版。开发版和本地构建会显示状态，不会自行推断升级顺序。候选归档先下载到隔离目录，并校验同一版本的 `SHA256SUMS` 后才替换；程序、配置、数据库和备份目录保持分离。容器部署应替换镜像并重启容器，不能在容器内替换程序文件。

---

### 📁 分组管理

分组用于将多个渠道聚合为一个统一的对外模型名称。

**核心概念：**

- **分组名称** 即程序对外暴露的模型名称
- 调用 API 时，将请求中的 `model` 参数设置为分组名称即可

> 💡 **示例**：创建分组名称为 `gpt-4o`，将多个供应商的 GPT-4o 渠道加入该分组，即可通过统一的 `model: gpt-4o` 访问所有渠道。

---

### 💰 价格管理

管理系统中的模型价格信息。

**数据来源：**

- 系统会定期从 [models.dev](https://github.com/sst/models.dev) 同步更新模型价格数据
- 当创建渠道时，若渠道包含的模型不在 models.dev 中，系统会自动在此页面创建该模型的价格信息,所以此页面显示的是没有从上游获取到价格的模型，用户可以手动设置价格
- 也支持手动创建 models.dev 中已存在的模型，用于自定义价格

**价格优先级：**

| 优先级 | 来源 | 说明 |
|:------:|------|------|
| 🥇 高 | 本页面 | 用户在价格管理页面设置的价格 |
| 🥈 低 | models.dev | 自动同步的默认价格 |

> 💡 **提示**：如需覆盖某个模型的默认价格，只需在价格管理页面为其设置自定义价格即可。

---

### ⚙️ 设置

系统全局配置项。

**统计保存周期（分钟）：**

由于程序涉及大量统计项目，若每次请求都直接写入数据库会影响读写性能。因此程序采用以下策略：

- 统计数据先保存在 **内存** 中
- 按设定的周期 **定期批量写入** 数据库

> ⚠️ **重要提示**：退出程序时，请使用正常的关闭方式（如 `Ctrl+C` 或发送 `SIGTERM` 信号），以确保内存中的统计数据能正确写入数据库。**请勿使用 `kill -9` 等强制终止方式**，否则可能导致统计数据丢失。




## 🔌 客户端接入

### OpenAI SDK

```python
from openai import OpenAI
import os

client = OpenAI(   
    base_url="http://127.0.0.1:8080/v1",   
    api_key="sk-octopus-roam-P48ROljwJmWBYVARjwQM8Nkiezlg7WOrXXOWDYY8TI5p9Mzg",
)
completion = client.chat.completions.create(
    model="octopus-roam-openai",  // 填写正确的分组名称
    messages = [
        {"role": "user", "content": "Hello"},
    ],
)
print(completion.choices[0].message.content)
```

### Claude Code

编辑 `~/.claude/settings.json`

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:8080",
    "ANTHROPIC_AUTH_TOKEN": "sk-octopus-roam-",
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

编辑 `~/.codex/config.toml`

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
编辑 `~/.codex/auth.json`

```json
{
  "OPENAI_API_KEY": ""
}
```


---

## 🤝 致谢

- 🙏 [looplj/axonhub](https://github.com/looplj/axonhub) - 本项目的 LLM API 适配模块直接源自该仓库的实现
- 📊 [sst/models.dev](https://github.com/sst/models.dev) - AI 模型数据库，提供模型价格数据
- 🇨🇳 [上游 AtomGit](https://atomgit.com/bestruirui/octopus) - 原项目的国内代码托管
- 💬 [Linux.do](https://linux.do/)
