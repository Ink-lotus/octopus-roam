# Codex 模型目录

网关可为 Codex 提供独立的版本目录：

```text
GET /codex/models?client_version=<major.minor.patch>
```

目录使用现有网关 API Key 鉴权。推理仍通过现有 provider 的 `/v1/responses`，模型列表 `/v1/models` 保持 OpenAI / Anthropic 协议。

## 启用和接入

新部署和缺少该字段的旧配置默认启用目录，无需为此额外修改网关配置。若要显式设置或关闭，可在现有运行配置中增加以下对象，并保留其他配置：

```json
{
  "codex_catalog": {
    "enabled": true,
    "use_proxy": false
  }
}
```

`enabled` 默认 `true`，`use_proxy` 默认 `false`；显式配置或环境变量变更后重启生效。也可使用 `OCTOPUS_ROAM_CODEX_CATALOG_ENABLED` 和 `OCTOPUS_ROAM_CODEX_CATALOG_USE_PROXY` 环境变量覆盖。Windows 启动进程或容器须实际继承这些变量。

v0.1.5 将目录默认值改为开启：缺少 `enabled` 的旧配置在重启后采用新默认值；已有显式 `enabled: false` 保持关闭，也可使用 `OCTOPUS_ROAM_CODEX_CATALOG_ENABLED=false` 关闭。配置加载不改写原文件，客户端仍须自行配置 `model_catalog_url`。

`use_proxy=false` 显式直连，不读取 HTTP_PROXY 等环境代理；`true` 使用应用现有的 `proxy_url` 设置。所选代理缺失或无效时返回 503，不自动直连。取源不需要 OpenAI Key、ChatGPT 凭据或 GitHub Token。

在已有 Codex provider 中增加目录 URL，保留现有推理和网关 Key 配置，例如：

```toml
model_provider = "octopus-roam"

[model_providers.octopus-roam]
name = "octopus-roam"
base_url = "http://127.0.0.1:8080/v1"
model_catalog_url = "http://127.0.0.1:8080/codex/models"
wire_api = "responses"
supports_websockets = false
requires_openai_auth = true
# 保留已有的网关 API Key 配置
```

Codex 在请求目录时附加自己的 `client_version`；无需把示例中的版本固定在 URL 上。远程目录测试须确认没有生效的 `model_catalog_json`，包括 profile / managed 配置层。本地文件与远程 provider 目录不是自动合并关系。撤回此接入时移除 `model_catalog_url`；恢复本地目录须显式配置原文件。关闭服务端功能不会为客户端自动恢复旧目录。

## 来源与权限

合法版本原值映射到固定公开文件：

```text
https://raw.githubusercontent.com/openai/codex/rust-v<client_version>/codex-rs/models-manager/models.json
```

版本最多 32 个 ASCII 字符，三个点分隔的非负十进制整数，各段不能有前导零。参数必须恰好出现一次。运行时不维护版本白名单，也不回退 latest、main、相邻版本、历史路径或 chatgpt.com 在线接口。标签/文件不存在时返回 404。

快照代表对应客户端版本发布时的官方元数据，不代表账户当前权限或模型实时可用性。标签内容也可能改变；每次成功刷新记录实际原始字节的 SHA-256，旧摘要的验收结论不自动覆盖新内容。

网关只保留官方 `slug` 与当前客户端分组名精确匹配、且被当前 Key 的 `supported_models` 允许的条目。空允许列表沿用不限语义。分组改名、删除或权限收紧会反映到后续响应；临时冷却不改变目录。目录请求不产生推理费用统计。

完整原始 JSON、顶层未知字段和保留模型的原始条目得到保留。网关不改模型指令、上下文窗口、工具能力或 `auto_review_model_override` 的 null / 缺省 / 非 null 值，不添加 `origin`，不补入快照或权限范围中没有的 Luna。部署方须确保分组背后的路由具备相应能力；目录可见不保证推理成功。

## 缓存、并发和错误

上游为匿名 GET，不转发客户端 Key、Cookie、自定义 Header 或其他 query。TLS 校验保持，3xx 不跟随。GitHub Raw 的 `text/plain` 正文按实际 JSON 内容校验。

- 解压后目录和最终响应各限 1 MiB，超限返回错误，不截断。
- 进程内最多缓存 32 份完整目录，按固定来源与版本隔离，LRU 淘汰；重启或重新初始化清空。
- 每次响应重新读取权限和分组，不缓存按 Key 过滤的响应；外呼等待结束后再次鉴权。
- 同版本刷新合并，每版本最多 32 个等待者。最多 4 个实际外呼和 8 个排队版本，总等待者上界为 384；满额立即返回 503。
- 刷新从入队起总预算 5 秒，不自动重试。等待者可以取消；最后一个等待者离开会取消其刷新。
- 尊重上游 Cache-Control / Date / Age / Expires，剩余有效期上限 5 分钟，未指定有效期时为 5 分钟。no-store / private / Vary:* 不存储；no-cache / must-revalidate 等要求重验证的响应重新 GET。
- 首版采用无条件 GET，不实现上游或下游条件请求。过期刷新失败返回错误，不继续提供过期目录。客户端自身缓存行为由 Codex 决定。

下游成功返回完整 JSON，不使用管理接口包装；设置 `Cache-Control: private, no-store`，不透传上游 ETag 或 Content-Length。合法空目录和过滤后的空集返回成功空数组，不能把上游失败解释为空目录。

| 状态 | 含义 |
| --- | --- |
| 400 | client_version 缺失、重复或格式不合法 |
| 401 | 现有网关鉴权失败，包括禁用、过期或超额 Key；入口拒绝时不访问上游，等待期间撤权则在取源后再次拒绝 |
| 404 | 对应 GitHub 标签或固定文件不存在 |
| 408 | 当前目录请求已取消 |
| 502 | GitHub 401/403、其他 HTTP 失败、网络失败、重定向、无实体的 304、非法 JSON / 条目 / 编码或超限 |
| 503 | 功能关闭、所选代理缺失/无效、本地并发满额、服务关闭，或 GitHub 429 |
| 504 | 目录总预算耗尽 |

目录错误使用 `error.code` / `error.message` 中的稳定分类，例如 `catalog_version_unavailable`、`catalog_upstream_access_failed`、`catalog_upstream_rate_limited`、`catalog_busy`、`catalog_timeout`。网关鉴权保持既有错误格式。错误不会包含上游正文、任意 Header、Key 或代理连接参数。

404 时核对客户端版本对应标签和固定路径；502/503 时区分网关鉴权、GitHub 访问和代理配置；504 时检查所选网络路径。不要用更换版本、扩大 Key 权限或设置 override 掩盖这些错误。

## 已验证范围

0.167.0 / 0.168.0 / 0.169.0 已取得 Windows 原生匿名 Raw HTTP 快照：每份 539696 字节、12 个模型，SHA-256 为 `1384625c0b6b3e67d3c60da29becea2b017f2c283fc1e2d05b8edafb4698eb10`。这些版本当前内容相同，测试另使用不同内容的版本 fixture，并核对实际请求路径。

C01 曾验证原样本地快照的 ExplicitConfig 路径。C02 随后以准确网关提交 `9a7ed524bf763bc776ef291a0998aeb715700ad4` 和原生 Codex CLI 0.169.0 完成真实 provider endpoint 八场景：实际生效配置没有 model_catalog_json，Sol/Astra 和重启后 Sol 均真实调用 `gpt-5.6-luna / low` 完成审核并得到 allow；包含五类负面条件及工具后续轮次。官方 override 原值保持。部分推理上游限制客户端类型，真实 Codex 仍须保留必要协议头。

Windows amd64 隔离开发程序已通过 91 项检查：默认关闭、直连和应用代理、三个版本实际来源路径、缓存和重启重取、无效/禁用/过期 Key、版本缺失、权限收紧和分组删除、原模型列表，以及 `gpt-6.1-sol` 的普通/流式推理。三模型可见时，各版本响应均为 180169 字节，SHA-256 为 `c075c83f9e0ecff971a20770e9f065d69a4a6de691c652774840f4fcd1a61187`；两个单模型权限范围和空集也已验证。模块测试另覆盖超额 Key、限流、并发、取消、解压上限和缓存控制。

开发验收入口为 [scripts/test-codex-catalog.py](../scripts/test-codex-catalog.py)，使用 Python 标准库。传入已构建 Windows 程序、准确程序 SHA-256、至少两个版本的原样快照和全新输出目录；`--help` 列出参数。脚本创建隔离配置、SQLite 和合成测试数据，使用 `localhost:8080` 的另行获准测试网关，Key 从交互式标准输入读取，每轮最多四次真实推理请求。它正常停止自建进程并保留证据；执行前应明确这些配置、数据和请求的授权范围。

C02 已完成同一 SHA 的 Windows/Linux/GitHub 完整 CI，Windows 实际开发 ZIP 的 91 项目录和 Linux 实际开发镜像的 116 项目录检查，以及两平台应用/升级/恢复、Linux 卷/Compose 和真实客户端验收；准确产物与范围见[支持证据](support.md)。目录原始快照摘要与网关源码/产物身份分别记录，同标签内容变化须重新核对。其他版本的 HTTP 取源成功不表示对应 CLI 已实测；这些 v0.0.0-ci 开发结果也不代替 v0.1.4 新候选的产物验收。
