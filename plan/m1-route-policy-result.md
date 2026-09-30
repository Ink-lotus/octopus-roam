# M1 路由策略与隐私保护验收记录

## 基线与范围

- 日期：2026-09-29。
- 状态：M1 实现与规定的自动检查已完成；浏览器人工交互验收未执行。
- 分支：`test`；开工基线：`1654a38403ac68f1236a8db87b408cf5e624456b`（S1 完成后的基线）。
- 任务定义：[v0.1.0-route-policy.md](versions/v0.1.0-route-policy.md) 的 V010-02、V010-03、V010-04，以及用户确认的手动等待配置变更通知。
- 用户已授权整个 M1 的后端、前端、三份 locale 和验收记录修改与验证。
- 开工前已有的 `plan/upstream-sync-v0.13.8-result.md` 工作区修改保留，不计入本轮实现。
- 未修改 `main.go` 版本、依赖、CI、迁移文件或备份版本；未执行 commit、push、标签或发布。

## 功能结果

### 手动模式等待与配置通知

未手动选择可用成员时，请求持续等待，直到人工选择、可用性变化或客户端取消；不增加自动选择、等待超时或尝试次数。配置通知唤醒请求重新读取最新分组；仍无可用选择时继续等待。

手动重试的固定间隔与指数退避也监听变更通知。请求发送期间若配置发生变化，旧失败结果不能按旧尝试上限结束请求。渠道启停、授权变化、成员选择、策略或模式修改均与路由通知衔接。

### V010-02：统一脱敏

新增 `internal/privacy`，在同一进程中对相同敏感值生成稳定的 `[redacted:…]` 指纹。使用进程随机 HMAC 密钥，避免公开凭据的无盐哈希。

覆盖 API Key、Authorization、Cookie、渠道和代理 URL 用户信息、敏感 query、自定义渠道 Header，以及结构化正文中的敏感字段。处理 URL 编码、JSON 转义、嵌套字段和重复字段，保留普通错误上下文与 JSON 数字精度。多次经过脱敏边界时结果保持幂等。

Relay 每轮记录实际使用的凭据，并用于请求错误、LastError、按需读取的请求体/响应体及已提交 SSE 的错误事件。历史诊断在渠道被修改或删除后仍可脱敏。普通成功请求和响应继续转发原始内容。

Webhook 事件、模型探测错误、通用错误响应、Gin 请求日志和 panic 诊断使用公共规则。debug SQL 日志保留语句结构、耗时和行数，使用绑定占位符，可能包含凭据的底层数据库错误仅输出稳定指纹。Webhook 编辑回显可以恢复当前指纹与旧 `***` 占位符；无法恢复的过期指纹会被拒绝，避免保存为真实凭据。实际 Webhook 投递继续使用真实配置。

**边界**：保护敏感字段及已知凭据，不自动识别自由对话中的全部个人信息。正文仍按需展示。指纹稳定范围为当前进程，重启后变化；原始配置、备份和实际转发内容不以掩码覆盖。

### V010-03：按模式整理配置

保留扁平 `GroupRelayConfig` JSON 和 `manual` / `failover` 枚举。公共参数由两种模式使用，`manual_max_attempts` 只作用于手动模式，`member_max_attempts` 和 `member_cooldown_seconds` 只作用于故障转移模式。

后端按当前模式校验和补默认值；保存当前模式参数时保留另一模式的历史参数。`member_affinity_seconds` 保留读取兼容，默认值、有效值与新保存值均为 0。路由指纹忽略不活跃模式字段，模式变更会清理旧冷却和探测状态。

前端使用可复用的策略字段表单，按模式显示参数；不显示亲和时间。新建分组读取全局默认策略后初始化，读取失败可重试。

### V010-04：全局默认路由策略

复用 `Setting` 表，键为 `group_route_defaults`，值为扁平 `GroupRelayConfig` 的 JSON 字符串。

| 接口 | 请求 | `data` 返回值 |
| --- | --- | --- |
| `GET /api/v1/group/route-defaults` | 无 | `GroupRelayConfig` |
| `POST /api/v1/group/route-defaults` | `{ "relay_config": GroupRelayConfig, "apply_to_existing": boolean }` | `{ "relay_config": GroupRelayConfig, "applied_groups": number }` |

接口使用现有 `{code, message, data}` 包装与分组管理鉴权。`applied_groups` 表示实际策略内容发生改变的分组数量。配置非法返回 400；通用 `/setting/set` 和 Setting setters 拒绝直接写此键。

新建分组省略或提交零值 `relay_config` 时使用全局默认策略；显式配置保持已有整份配置语义。未设置全局默认值的旧环境回退内建默认策略。

不勾选应用时只保存默认值，已有分组的持久化 JSON 不改写。勾选时，将默认值与分组修改放在同一事务中：更新公共及当前模式参数，保留另一模式参数、分组名称、模式、成员、顺序和 `active_item_id`。失败整批回滚；提交后刷新缓存，对有效策略变化重置路由，并发布完整分组事件。

分组修改和渠道修改在 handler 层串行完成写入、路由通知及事件发布，防止并发请求以旧配置覆盖新路由指纹。op 层串行分组事务及缓存更新。

设置页提供公共、手动、故障转移三类参数；“同时应用到已有分组”默认关闭，保存后反馈实际应用数量。全局默认策略不包含分组模式。

## 迁移与兼容

- 无 schema 迁移，无新增依赖，`dbDumpVersion = 6` 不变。
- 旧分组、旧扁平 JSON 和缺少默认策略键的旧备份可读取。缺键的增量导入不覆盖当前默认策略。
- 新备份通过现有 Settings 导出和导入全局策略。导入时验证对象结构、未知字段、参数取值及重复的默认策略 Setting 行。
- 非活跃模式的历史字段继续保留，切换到该模式时补合法默认值。
- 既有手动无限等待、故障转移尝试预算和冷却行为继续保留。

## 主要改动位置

| 范围 | 文件 |
| --- | --- |
| 策略模型与存储 | `internal/model/{group,setting}.go`、`internal/op/{group,group_route_defaults,setting,backup}.go` |
| 路由与诊断 | `internal/privacy/`、`internal/relay/{privacy,handler,route,state,channel,upstream}.go` |
| 管理接口与服务器日志 | `internal/server/handlers/{group,group_route_defaults,channel,setting}.go`、`internal/server/middleware/{logger,recovery}.go`、`internal/server/resp/resp.go`、`internal/server/server.go` |
| SQL 调试日志 | `internal/db/{db,logger}.go` |
| Webhook | `internal/webhook/{service,template}.go` |
| 前端 | `web/src/api/{group,queries,setting}.ts`、分组策略表单、新建分组、全局策略设置、日志正文提示、三份 locale |
| 验证 | 对应包的脱敏、路由等待、事务回滚、并发顺序、备份及接口测试 |

## 验证

Go 验证使用当前命令进程的 `GOCACHE = <workspace>/.tmp-go-cache`，避免共享缓存目录权限问题；未修改持久环境配置。数据库测试使用临时数据库。

| 检查 | 结果 |
| --- | --- |
| `go test ./...` | 通过 |
| `go vet ./...` | 通过 |
| `go test -race ./internal/privacy ./internal/op ./internal/relay ./internal/server/handlers ./internal/server/middleware ./internal/server/resp ./internal/webhook` | 通过 |
| `go test -race ./internal/db` | 通过 |
| `pnpm --dir web lint` | 通过 |
| `pnpm --dir web build --outDir "$env:TEMP/octopus-m1-build" --emptyOutDir false` | 通过；包括 `tsc --noEmit`，构建产物写入临时目录，仅有 `PLUGIN_TIMINGS` 性能提示 |
| 三份 locale 键一致性 | 通过，各 450 个键 |
| `git diff --check` | 通过 |

重点断言包括三种协议的错误/日志/诊断一致脱敏、原始成功转发不变、已提交流的错误脱敏、删除渠道后历史诊断、重复 JSON 敏感字段、Webhook 占位符编辑兼容、手动无限等待及及时重载、模式隔离、默认策略无覆盖时不改已有 JSON、事务失败整体回滚、并发写入与缓存顺序、新旧备份兼容。

浏览器人工交互验收尚未执行；前端验收以代码审阅、类型检查、lint 和构建为准。

## 2026-09-30 审阅问题修复

用户授权修复敏感字段后缀漏判、重复构建匹配器、按需读取正文重复脱敏三个问题。

### 识别规则与兼容边界

- `IsSensitiveName` 用于 Header、URL query 和非结构化错误，不再因 `id/name` 后缀跳过凭据词；`token` 恢复子串识别，覆盖 `X-Secret-Id`、`session_secret_id`、`X-Token-Value` 等旧 Webhook 规则保护过的字段。
- JSON 正文单独使用明确的元数据例外：`api_key_id`、`api_key_name`、`credential_name` 和常见协议用量/上限字段。例外不扩散到任意 `id/name` 后缀，也不用于 Header/query；元数据的字符串值仍按已知凭据检查。
- Webhook 回显、占位符恢复及过期指纹校验继续复用同一保守规则；原始投递配置保持可用。
- 正文后面的自由文本若新识别出凭据，会再次处理已经遍历过的正文节点，保护出现在前面的同值回显。

### 匹配器与正文缓存

- 每个 Redactor 缓存不可变的匹配器：长值排序与 `strings.Replacer`、短值正则只在凭据映射变化后按需重建。重复登记同值不会使缓存失效，实际替换在取得匹配器引用后释放 Redactor 锁。
- 增加内部使用的 `Redactor.Revision()`。每个请求分别缓存一份请求体和响应体结果，缓存键包含 Redactor 身份、凭据版本和输入正文。
- 请求开始后新增的上游凭据、终态后新增的凭据和响应正文变化都会使正文缓存失效。处理正文期间若发现新凭据或有并发登记，不把当前结果标为新版本的有效缓存。
- `RequestBody` / `ResponseBody` 只在 Relay 全局锁内取得正文与缓存引用；JSON 解析、匹配及等待同一正文的其他读取者均在全局锁外执行。缓存不影响运行中的无限等待请求读取诊断。

主要改动：`internal/privacy/redact.go`、`internal/relay/state.go`、新增 `internal/relay/diagnostic_body.go`，以及 privacy、relay、middleware、resp、webhook 的回归测试和两份 benchmark 文件。没有增加依赖、数据库迁移或对外接口字段；本次无前端修改。

### 性能对照

在相同 Windows amd64 / Intel i7-12650H 环境，修复前后分别执行以下命令，取三次结果的中位数；这是本地微基准，不代表端到端 Relay 延迟。

```powershell
go test ./internal/privacy ./internal/relay -run '^$' -bench 'Benchmark(ReplaceKnown|RedactorBody|DiagnosticBodyRead)$' -benchmem -benchtime=300ms -count=3
```

| 场景 | 修复前 | 修复后 | 每次分配次数（前 → 后） |
| --- | --- | --- | --- |
| 12 个长凭据，重复文本替换 | 5.37 μs | 1.13 μs | 44 → 5 |
| 加入一个短凭据的文本替换 | 61.45 μs | 51.53 μs | 98 → 23 |
| 100 条消息的正文脱敏 | 4.50 ms | 1.59 ms | 30,633 → 7,198 |
| 同一请求体重复读取 | 1.84 ms | 49.5 ns | 5,357 → 0 |
| 同一响应体重复读取 | 1.80 ms | 50.3 ns | 5,357 → 0 |

正文重复读取测量的是凭据和正文均不变时的缓存命中，不包含 HTTP 传输、首次读取或版本失效后的重新脱敏；命中路径直接返回已有字符串。

### 本轮验证

| 检查 | 结果 |
| --- | --- |
| `go test ./...` | 通过 |
| `go vet ./...` | 通过 |
| `go test -race ./internal/privacy ./internal/relay ./internal/server/middleware ./internal/server/resp ./internal/server/handlers ./internal/webhook` | 通过 |
| 上述 benchmark，修复前后各 3 次 | 通过 |
| `git diff --check` | 通过 |

回归覆盖 Header/query 后缀凭据、JSON 元数据可读性、已知凭据对元数据例外的覆盖、日志与 panic/错误响应、Webhook 回显与恢复/过期指纹、后续登记的长短凭据及编码别名、正文中的后置凭据回显、运行中和终态后的缓存失效、响应正文变化、请求删除和并发读取。
