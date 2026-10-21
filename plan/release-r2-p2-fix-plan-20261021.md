# R2 两项 P2 修复计划

日期：2026-10-21。状态：**待其他会话执行；本文件只记录方案，不代表修复或验收已完成。**

来源：[R2 本地差异审查](release-r2-local-review-20261021.md)。审查基线为 `test / 948de764ae9a034fd72899686d24dcc4ecad7fa5`，输入包含 31 个已跟踪改动、58 个新增文件；随后新增了审查报告。执行时必须重新核对工作区，不能把这个历史数量当作当前状态。

## 1. 目标、范围与交接约束

完成审查中的两项问题，并为修复后的实际源码补充回归证据：

| 编号 | 问题 | 完成后的行为 |
| --- | --- | --- |
| C1 · P2 | MySQL 自定义 CA 使无查询参数 DSN 丢失原有默认选项 | 启用 CA 前后保留相同的默认连接语义，同时继续严格校验 TLS |
| C2 · P2 | 页面确认目标与后台实际接受的更新版本未绑定 | 请求携带页面目标；后台检查并固定同一版本；目标变化时冲突刷新，由用户重新点击；成功回执驱动版本确认 |

执行约束：

- 先读用户当次指令及适用的 `AGENTS.md`，记录分支、HEAD、暂存区、工作区差异和待改文件哈希。在现有未提交改动上做局部修复，保留其他工作。
- 本次授权仅为新增此计划。计划不是后续批量修改、整文件重写、依赖或环境变更、CI 修改、commit、Git 历史或远端操作、真实数据库操作及真实在线升级的执行授权；需要时按当次授权门禁说明具体目标、命令和影响。
- 预期使用现有依赖、合成 CA、临时目录和 loopback 发布源。无需引入依赖或改动 CI、发布配置、数据库结构。
- 保留旧审查报告、旧证据及冻结候选。新测试输出使用全新目录，例如 `build/release-local/r2-p2-fix-<日期>-<序号>/`；创建前检查目录不存在。
- 若两个会话分别修复，C1 与 C2 可按下列文件边界拆分；合并两项结果后由一个会话完成共同验证，避免互相覆盖源码或共用可写测试产物。

## 2. C1：先保留原始 DSN 的默认参数，再注入 CA

### 2.1 已确认的问题

[ConnectionPath](../internal/conf/database.go) 在启用 `mysql_ca_file` 后解析 DSN 并添加 `tls` 查询参数。[initMySQL](../internal/db/db.go) 只有发现 DSN 不含 `?` 时，才追加 `charset=utf8mb4&parseTime=True&loc=Local`。两者串联后，原本没有查询参数的 DSN 跳过了默认值分支。

合成参数探针实测：`parseTime` 从 `true` 变为 `false`，`loc` 从 `Local` 变为 `UTC`，`charset=utf8mb4` 消失。已有刷新记录的数据库可能因时间字段扫描失败而无法完成启动；该影响依据驱动和启动调用链确认，尚未用真实 MySQL 复现。

证据：[mysql-defaults.log](../build/release-local/r2-review-20261021-01/mysql-defaults.log)、[原始参数探针](../build/release-local/r2-review-20261021-01/.probes/mysql_defaults.go)。

### 2.2 选定方案

在 `Database.ConnectionPath()` 的自定义 MySQL CA 分支做最小修复：

1. 保留无 CA 时直接返回 `d.Path` 的行为；保留数据库类型检查。
2. 将原始 `d.Path` 复制到局部变量。使用与当前 `initMySQL` 相同的判断：**只有原字符串不含 `?` 时**，先追加 `?charset=utf8mb4&parseTime=True&loc=Local`。
3. 解析这个已保留默认参数的 DSN，再执行现有 TCP/主机检查、CA 读取、TLS 注册和 `FormatDSN()`。
4. 保留 `ServerName`、`MinVersion: TLS 1.2`、受信 CA 和禁用明文回退等约束；错误信息继续脱敏。

不要把此修复扩大为“为所有缺失参数补默认”。原始 DSN 已有 `?`（含空查询串）时，维持原行为；显式 `parseTime=false`、`loc=UTC`、其他 `charset` 必须保留。序列化可能改变参数顺序或布尔值拼写，回归应比较解析后的含义。

预期修改：[internal/conf/database.go](../internal/conf/database.go)、[internal/conf/database_test.go](../internal/conf/database_test.go)。`internal/db/db.go` 和 `cmd/start.go` 用于核对调用链，无需为此重构初始化流程。

### 2.3 回归要求

| 场景 | 必须断言 |
| --- | --- |
| 无查询参数的 MySQL TCP DSN + 自定义 CA | 解析结果 `ParseTime=true`、`Loc=Local`、`Params["charset"]="utf8mb4"`，TLS 配置仍严格 |
| 显式 `parseTime=false&loc=UTC&charset=latin1` + CA | 三项用户设置保持不变 |
| 已有查询参数但未设置上述选项 + CA | 不新增默认选项，保持原初始化语义 |
| 原字符串以 `?` 结尾 + CA | 按原有“包含 `?`”规则处理，不因查询串为空而补默认 |
| 无 CA | 返回原字符串，不改变已有及无查询参数 DSN |
| 既有 CA 正反例 | 非 MySQL、非 TCP、坏 DSN、缺失或非法 CA 继续失败；TLS 主机校验、最低版本、禁明文回退及错误脱敏不退化 |

使用合成 DSN 和测试临时目录中的 CA，不读取真实 profile、不连接数据库、不输出完整连接信息。现有 CA 测试显式带 `parseTime=true`，必须增加能在修复前失败的无查询参数用例。

## 3. C2：严格提交目标、冲突重试、回执确认

### 3.1 已确认的问题与决策

[前端更新 Hook](../web/src/api/update.ts) 接收 `targetVersion`，却以空 body 发出 POST；[后端更新器](../internal/update/core_apply.go) 重新选择最新发布版本，[handler](../internal/server/handlers/update.go) 只返回 `update_pending`。页面仍用点击前的旧目标等待版本相等。

已复现“页面 rc.2、发布源已变 rc.3”：后台选择 rc.3；前端即使查询到已安装 rc.3，也会在 30 秒后进入 `unconfirmed`。证据：[后端查询日志](../build/release-local/r2-review-20261021-01/update-backend.log)、[前端请求与状态日志](../build/release-local/r2-review-20261021-01/update-ui.log)。原复现没有执行程序替换。

本计划固定采用以下行为：**提交页面展示的目标；服务端发现目标已过期则返回冲突，刷新页面版本信息后由用户重新点击；成功时返回被接受的版本，前端以该回执建立确认记录。** 不在冲突后自动改目标再次 POST，也不把“当前版本不同于旧版本”视为完成。

### 3.2 HTTP 合同

保留现有认证中间件和响应 envelope，仅调整更新 POST 的请求及成功 `data`。

请求：`POST /api/v1/update`，`Content-Type: application/json`。

```json
{"target_version":"v0.1.0-rc.2"}
```

成功响应示例：

```json
{
  "code": 200,
  "message": "success",
  "data": {
    "status": "update_pending",
    "target_version": "v0.1.0-rc.2"
  }
}
```

`200` 表示更新流程已接受该目标，最终完成仍以后台实际运行版本确认。

| 条件 | HTTP 结果与行为 |
| --- | --- |
| 空 body、无效 JSON、缺失/空/类型错误的 `target_version`，或非法版本 | `400`；不能退回无目标的 latest 更新 |
| 目标格式合法，但锁内检查选中的可安装版本与请求不同 | `409`，使用可识别的 `ErrUpdateTargetChanged`；返回脱敏错误 |
| 目标一致且原有门禁全部通过，更新已接受 | `200`；回执目标与本次固定版本完全一致 |
| 已有容器、开发版、已是最新版、无发布、平台、并发或下载校验错误 | 保留原状态语义与错误映射；`429`、`503`、`502` 等不要被笼统改成目标冲突 |

版本必须符合现有 `parseVersion` 支持的正式版或 RC tag 格式，并拒绝首尾空白；与发布记录 `TagName` 精确比较。客户端不提供下载 URL 或其他发布源参数。错误响应沿用 `{code, message}`，无需改造全局 `resp` 协议。

旧缓存前端仍发空 POST 时会得到 `400`，需要刷新页面加载新客户端；不保留无目标兼容旁路。后端与前端应作为同一修复结果构建和验收。

### 3.3 后端实现步骤

1. 在现有更新包中定义成功回执，例如 `UpdateReceipt{Status UpdateStatus, TargetVersion string}`，JSON 字段为 `status`、`target_version`；增加非法目标和目标变化的明确错误值。
2. 将生产入口统一调整为类似 `UpdateCore(targetVersion string) (*UpdateReceipt, error)` 的签名，同步内部 `updateCore`、handler 和所有测试调用方。具体类型名可局部调整，不能保留无参生产更新入口。
3. handler 绑定并检查请求结构，将目标传给更新包；更新包复用 `parseVersion` 校验格式，使直接调用也不能绕过。非法目标映射 `400`，目标变化映射 `409`。
4. 保留现有 `TryLock`、`updatePending` 及平台等检查。在同一更新锁内选取一次发布信息，检查其确实可安装，并将请求目标与 `info.TagName` 比较。
5. 目标不一致时，在资产下载、stage 创建、备份、程序替换、关服务、设置 pending 之前返回。回归要证明这些动作均未发生，而非只断言一个错误值。
6. 比较通过后固定本次选中的发布信息。ZIP、`SHA256SUMS`、解包/调度及成功回执始终使用该版本；后续发布源出现新版本也不能切换本次目标。
7. 仅在现有更新调度成功接受后返回回执；失败路径保留错误处理、清理和恢复行为。

安全边界保持：固定 GitHub 发布源、请求/重定向限制、版本升级规则、校验和、ZIP 路径及文件白名单、并发控制、Windows 握手/恢复、Unix 备份及恢复。测试注入只用于包内夹具，不新增可配置的生产下载源。

预期修改：

- [internal/update/core.go](../internal/update/core.go)、[internal/update/core_apply.go](../internal/update/core_apply.go)。
- [internal/update/release.go](../internal/update/release.go) 或 [internal/update/update.go](../internal/update/update.go)：沿现有组织放置错误值及回执类型。
- [internal/update/release_test.go](../internal/update/release_test.go)：同步签名，新增目标绑定回归。
- [internal/server/handlers/update.go](../internal/server/handlers/update.go)：请求解析、错误映射、回执；新增相应 handler 合同测试。

### 3.4 前端实现步骤

1. 保留调用处的 `UpdateRequest`（`targetVersion`、`previousVersion`），增加与成功 `data` 对应的回执类型。通过现有 `apiRequest` 提交 `body: { target_version: attempt.targetVersion }`。
2. 在 mutation 的成功处理之前校验实际回执：必须是 `status=update_pending`，且 `target_version` 非空、与请求目标严格相等。错误格式、旧字符串回执或不一致回执不能触发“已开始更新”的成功提示或建立有效 pending 记录；刷新版本查询并走错误提示。
3. 合法回执建立的 `UpdateAttempt.targetVersion` 取自回执，`previousVersion` 保留点击时的值，确认期限仍为接受成功后的 30 秒。
4. 收到 `ApiError.status === 409` 时刷新 `['update', 'latest']` 和 `['update', 'now-version']`，沿用现有失败提示；本次冲突不创建新的确认 attempt、不启动短轮询、不自动再次提交。无需新增业务错误码协议来区分所有 `409`。
5. 保留 `resolveUpdatePhase` 的优先级：无 attempt 为 idle；`update_failed` 优先为 failed；实际版本与被接受目标相等才为 completed；到期未确认则为 unconfirmed。终态停止 1 秒短轮询，恢复常规查询间隔。
6. 页面提交目标仍来自实际展示的最新 tag，当前版本来自后端查询。刷新后重新点击，才能接受新目标。

预期修改：[web/src/api/update.ts](../web/src/api/update.ts)；按交互需要局部调整 [Info.tsx](../web/src/components/modules/setting/Info.tsx)。[通用 API client](../web/src/api/client.ts) 已支持 JSON body 和带 status 的错误，无需重构。优先使用现有文案；确需新增提示时同步现有多语言资源。

前端尚无独立测试框架或 `test` script。使用现有 TypeScript 与 Node 内建测试/VM，新增持久化回归脚本；可参考 [原前端探针](../build/release-local/r2-review-20261021-01/.probes/update_ui.cjs) 的加载方式，执行实际 `update.ts`，不要在测试里复制状态算法。脚本新增路径在结果报告中给出；本修复不需要安装测试依赖或修改 CI。

检索并核对 `UpdateCore(`、更新 POST 和旧字符串响应的调用方。已有验收脚本若依赖旧合同，按实际使用同步；只调用 GET 的检查无需机械改写。

### 3.5 回归矩阵

| 层次/场景 | 必须断言 |
| --- | --- |
| 请求与 handler | 实际前端 mutation 发出正确 JSON body；handler 接收同一目标；错误输入为 `400` 且不调用安装动作；认证保持 |
| rc.2 页面、rc.3 发布源 | 更新包返回目标变化错误；handler 为 `409`；资产下载、stage、备份/替换、关服务与 pending 均未发生 |
| rc.2 请求、rc.2 发布源 | 同一选中版本用于 ZIP、校验文件、调度和成功回执；响应对象及 JSON 字段符合合同 |
| 校验目标后发布源出现 rc.3 | 本次继续使用已选中的 rc.2，不二次选 latest、不改变回执 |
| 完整前端流程 | 首次 rc.2 提交冲突 → 两项查询刷新 → 页面展示 rc.3 → 用户再点击 → 收到 rc.3 回执 → pending → 实际版本 rc.3 时 completed；期间无自动第二次 POST |
| 异常回执 | 缺字段、错误状态、旧字符串或不同目标均不触发有效 pending/成功提示，并刷新查询 |
| 版本确认与终态 | 被接受 rc.2、实际仍旧版或变成其他版本时不能 completed；30 秒后 unconfirmed；`update_failed` 优先于版本相等；终态停止短轮询 |
| 既有更新保证 | 并发/已 pending、容器/平台/本地构建、校验失败和恢复等现有测试继续通过 |

后端使用包内 loopback 发布夹具和现有可注入动作记录调用；不替换当前工作中的程序。前端测试覆盖真实 mutation、查询失效和状态路径，不能仅对手写模拟算法或类型声明断言。原探针中的“空 POST、错误目标超时”是缺陷断言，需要反转为正确行为并补足请求到确认的链路。

## 4. 执行顺序与验证

1. **核对输入。** 读取本计划和审查报告，记录当前差异与待改文件哈希；若实现已被其他会话修改，基于当前内容重新定位，不套用历史补丁。
2. **完成 C1。** 在授权范围内局部修改，增加参数回归，运行 conf 定向测试。
3. **完成 C2。** 先固定请求/回执及错误合同，再同步后端、handler、前端和调用方；运行更新包、handler 和前端合同回归。
4. **合并验证。** 两项定向回归通过后，对合并后的同一份源码执行一次适当的完整受影响验证。只在新变更、失败或未解疑点出现时扩大或重复检查。
5. **形成新证据与结果报告。** 记录源码哈希、命令、退出码、测试结果、未验证项及适用平台；再次审查 C1/C2 的最终差异。

### 4.1 环境与命令约定

本地 shell 默认 PowerShell 7。先核对已安装工具、现有缓存和 R1 的环境记录，使用进程级离线设置：

```powershell
$env:GOTOOLCHAIN = 'local'
$env:GOPROXY = 'off'
$env:GOFLAGS = '-mod=readonly'
$env:pnpm_config_verify_deps_before_run = 'error'

go test ./internal/conf
go test ./internal/update ./internal/server/handlers
```

这些是后续执行会话的定向验证入口，并未在写计划时执行。每条命令单独检查退出码；不能仅以最后一条命令成功代表全部成功。可使用现有 `.tmp-go-cache`、`.tmp-go-mod`，先验证路径与依赖完整性；不因缓存缺项自行联网升级、改锁文件或修改持久环境配置。

完整受影响验证参考现有 [scripts/ci-test.sh](../scripts/ci-test.sh)，在支持相应工具的平台对新源码执行：

- Go default/jsoniter 全包测试、两组 vet，以及现有 conf/db/op/update/handler 范围的 race；race 所需 CGO/编译器不可用时如实列缺项。
- 前端新增合同回归、ESLint、TypeScript/Vite 构建；构建使用明确记录的版本与 commit 输入。
- 现有 release-contract 合成回归（审查时为 27 项），并检查 Go 及前端锁文件没有意外变化。
- `git diff --check` 和暂存区差异检查；新增文件另行直接检查，不能只依赖默认 diff。

**不要直接重跑** `build/release-local/r1-local-closeout-20261020-01/run-ci.py`：它固定历史 snapshot、baseline、overrides 和输出文件，会测到旧源码或写入旧证据。可参考其环境准备方式，但新运行器必须明确指向本次修复后的完整输入，并写入新目录；运行前核对该绑定。

前端验证使用新的独立源码和独立依赖副本，避免共享可写 `node_modules` junction 配合 pnpm 自动整理。保留 `pnpm_config_verify_deps_before_run=error`，必要时直接调用现有 Node CLI；不关闭依赖检查来让构建通过。

### 4.2 证据适用范围

- 旧 R1 完整测试是对应旧输入的历史证据；旧 R2 参数和更新日志是缺陷证据，均不能替代修复后的回归。
- 代码修改后，旧冻结候选不包含这两项修复。若另行构建新 Windows 候选或进行 loopback 实际更新验收，应使用新目录，记录版本、源码和产物哈希，正常停止自建进程并保留失败证据。
- 合成 DSN 回归不等于真实 MySQL 启动验收；包内调度模拟和前端状态回归不等于真实程序替换验收。报告必须区分这些结果。
- Ubuntu Actions、Android/NDK、多架构真实镜像、公开发布及真实 Release 间升级仍属于原发布计划的后续验收，不能因本地 P2 回归通过而标记完成。

## 5. 完成标准与交付

- [ ] C1 无查询参数 + CA 用例在修复前失败、修复后通过，默认选项、显式选项和 TLS 行为均符合本计划。
- [ ] C2 请求、锁内校验、下载目标、成功回执和前端确认共同绑定同一版本；目标变化返回冲突且没有安装副作用。
- [ ] 前端冲突刷新后必须重新点击；完成仍要求实际版本严格相等；失败与超时终态及轮询行为有回归覆盖。
- [ ] 所有生产调用方和相关验收调用方已核对，定向及合并验证结果可追溯到同一份修复后源码。
- [ ] 新结果报告列明修改文件、验证命令/退出码、证据路径、缺项和残余风险；旧材料保持原件，没有将模拟验收表述为真实升级。
- [ ] 交付当前工作区差异供审阅；后续暂存、commit、集成和远端操作继续按用户当次授权执行。

提交分组仍参考 [原提交边界](../build/release-local/r2-review-20261021-01/commit-boundaries.json)：C1 属数据库组，C2 属更新组，验收脚本属工具组，计划和结果报告属文档组。这里只给归属，不执行提交。

建议新结果报告命名为 `plan/release-r2-p2-fix-result-<日期>.md`，其中分别列出 C1/C2 的最终实现、回归结果和未验证事项；状态未达标时如实标记，不能只更新结论为“已修复”。

## 6. 可复制的交接指令

> 请先读取 `plan/release-r2-p2-fix-plan-20261021.md` 和 `plan/release-r2-local-review-20261021.md`，核对当前工作区及适用的 AGENTS 指令，按本计划处理 C1/C2。采用 C1 的原始 DSN 默认值保留方案，以及 C2 的严格提交目标、409 冲突刷新后重新点击、成功回执确认方案。先明确局部修改范围并遵守当次授权门禁；本计划本身不替代额外授权。保留现有未提交改动和历史证据，新增能覆盖缺陷的回归，对修复后的实际源码验证，并输出独立结果报告。完成后交付差异、证据和未验证项，供下一步审阅。
