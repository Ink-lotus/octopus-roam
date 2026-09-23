# 阶段 1 交接文档：重试策略和手动上限

上一阶段：`plan/phase0-baseline.md`（基线与环境事实，**仍需先读**）。
权威计划：`plan/v1.0plan.md` §三（产品契约）、§四阶段 1、§五（迁移）、§六（验收）。
执行记录（含全部 Ruling 与自审结论）：`.superpowers/sdd/v1.0plan/progress.md`。

## 0. 一句话状态

阶段 1 功能与代码复审已完成，后端检查、前端 lint 和生产构建（含 TypeScript 检查）通过。阶段 1 初始提交为本地 `test` 上的 `4ed9cea`；随后独立清理了 3 个基线 lint 错误。下文 §1 至 §10 保留阶段 1 初始提交时的验收快照，最新收尾结果见 §11。阶段 2 尚未开始。
代码坐标：`internal/model/group.go`、`internal/relay/{retry.go,handler.go,state.go}`、`web/src/api/group.ts`、`web/src/components/modules/group/{Editor.tsx,Card.tsx}`、三份 locale。

## 1. 分支与 Git 现状（纪律约束）

- 分支 `test`，开发基线 commit `0711771`。本次阶段 1 提交包含 **13 个已改文件 + 5 个新增文件，共 18 个文件**。
- 已改：`internal/model/group.go`、`internal/relay/{handler.go,upstream.go,state.go,retry_test.go,testsupport_test.go,failover_test.go}`、`web/src/api/group.ts`、`web/src/components/modules/group/{Card.tsx,Editor.tsx}`、`web/src/locales/{en,zh_hans,zh_hant}.json`
- 新增：`internal/model/group_test.go`、`internal/relay/{retry.go,retrydelay_test.go,waitdelay_test.go}`、本文档。
- 用户已授权先提交阶段 1，再单独修复并审阅 3 个基线 lint 问题；本轮停留在阶段 1。提交范围不包含 `.pnpm-store/`、`web/node_modules/` 和临时构建产物。
- 最终代码与提交状态以 `git log`、`git show` 和 `git status` 为准；`.superpowers/sdd/v1.0plan/phase1.diff` 仅为开发过程中的补丁快照。

## 2. 环境事实（复述 + 阶段 1 新增）

- `-race` 不可用（无 C 工具链）→ 并发结论无法在本机验证。
- 用户授权后已执行 `pnpm install --frozen-lockfile`（pnpm 11.15.1、Node.js v24.18.0），依赖清单与锁文件未修改。前端生产构建和 TypeScript 检查通过；全量 lint 报 3 个基线错误，详见 §8。locale JSON 解析和三份 key 对齐检查此前已通过。
- 新增：测试夹具现在**会直接改库**（见 §3 的 `breakChannelBaseURL`）——因为"渠道地址为空"经 op 的写入路径产生不了。

## 3. 新增文件与夹具 API（阶段 2 可直接复用）

生产代码：

- `internal/relay/retry.go` — `RetryDelay(config model.GroupRelayConfig, retryCount int) time.Duration`：`retryCount` 即"第几次重试"（= 第 N 次尝试失败后等待的时间，`failures` 直接传入）。固定模式取 `member_retry_interval_seconds`，只对超出 `time.Duration` 范围的秒数饱和；指数模式取 `min(初始 * 2^(retryCount-1), 最大间隔)`，倍数固定 2。两种模式至少等待 1 秒，仅指数模式受 600 秒硬上限约束；无需依赖调用方先 Normalize。
- `internal/model/group.go` — `RetryMode`（`fixed` / `exponential`）、常量 `RetryInitialIntervalSecondsDefault = 1`、`RetryMaxIntervalSecondsLimit = 600`、4 个新字段（`retry_mode` / `retry_initial_interval_seconds` / `retry_max_interval_seconds` / `manual_max_attempts`）、`DefaultGroupRelayConfig` 与 `NormalizeGroupRelayConfig` 的回填。
- `internal/relay/state.go` — 抽出 `waitDuration(ctx, delay)`，`wait(ctx, seconds)` 改为它的薄包装，且 `wait` 的秒数换算走 `secondsToDuration`（与 `RetryDelay` 同一套饱和换算，避免超大固定间隔回绕成非正数后立即返回）。
- `internal/relay/upstream.go` — 新增 `trackingTransport` / `trackUpstreamRoundTrip`：包装 `http.RoundTripper`，在 `RoundTrip` 被调用时置标志；`sendPassthrough` / `sendConverted` 返回值增加 `sent bool`，向调用方**准确报告本轮是否真的发起了 HTTP 传输**（发送前的请求构造 / 参数解析 / 代理初始化失败均为 false）。只包一层传输层，连接池与 `closeIdle` 归还入口不受影响。

夹具（`internal/relay/testsupport_test.go`）：

- `newTestChannel(t, name, baseURL, upstreamModel) int` 返回**授权主键**（阶段 0 已有）。
- `breakChannelBaseURL(t, grantID)` — 把该授权所属渠道的 `base_url` 清空并 `op.InitCache()`，用于构造"`buildOutbound` 失败"（即**没有真正发出上游请求**）的成员。它是唯一能造出这类失败的入口。
- `channelIDOfGrant(t, grantID) int` — 由授权主键反查渠道主键。
- `setChannelParamOverride(t, grantID, value)` — 改写测试数据库中渠道的 `param_override` 并刷新缓存，用非法 JSON 构造「发送层在构造上游请求时就失败（未发出请求）」的成员。此类非法 JSON 也能经现有 op 配置提交路径写入，解析失败发生在发送层。
- `failingStatuses(status, n)`、`newFakeUpstream`、`awaitRequests`、`serveWithTimeout`、`assertUpstreamFailure`、`testRelayConfig(maxAttempts)`、`testRetryConfigWithMode(mode)`、`newTestGroup` 同阶段 0 / 阶段 1 语义。

## 4. 契约落地情况（阶段 1 逐条对照）

| 契约 / 任务 | 落地位置 | 测试 |
|---|---|---|
| 总尝试次数语义、`retry_count = attempts - 1` | `RetryDelay` 形参即重试序号 | `retrydelay_test.go` 8 例 |
| 固定模式 | `RetryDelay` 非指数分支 | `TestRetryDelayFixedMode`、`TestFixedRetrySucceedsAfterSecondAttempt` |
| 指数模式：倍数 2、默认初始 1s、硬上限 600s | `exponentialRetryDelay`（循环递增，达上限即停，无溢出） | `TestRetryDelayExponentialSequence`（1,2,4,…,512,600,600）、`TestRetryDelayExponentialHandlesHugeRetryCount` |
| 手动模式遵守 `manual_max_attempts`，0 = 不限额 | `handler.go` 失败分支 | `TestManualRetryStopsAtAttemptLimitOne`、`TestManualRetryStopsAfterConfiguredAttempts`、`TestManualRetryContinuesWhenAttemptLimitIsZero` |
| 故障转移模式仍由 `member_max_attempts` 决定 | 同上（`else if recordRouteFailure`） | `failover_test.go` 3 例 |
| 换成员时重置该成员的尝试计数 | `failedItemID != item.ID` 分支 | 由 `failover_test.go` 的调用序列覆盖 |
| 只有真正发出的上游请求消耗尝试次数 | `trackingTransport` 上报的 `sent` + `!sent` 分支 | `TestFailoverSkipsMemberWhoseOutboundCannotBeBuilt`、`TestManualRetryDoesNotConsumeAttemptWhenSendFailsBeforeUpstream`（流式/非流式）；连接失败与响应超时仍计数由 `TestManualRetryCountsConnectionFailure` / `TestManualRetryCountsResponseTimeout` 护栏 |
| 取消立即退出 | `waitDuration` 的 `ctx.Done()` 分支 | `TestExponentialRetryBackoffStopsOnContextCancel`、`TestManualRetryStopsOnContextCancel`、`TestWaitSaturatesHugeIntervalAndStopsOnCancel` |
| 固定间隔秒数入口同样防回绕 | `wait` 走 `secondsToDuration` | `TestWaitSaturatesHugeIntervalAndStopsOnCancel`、`TestWaitBlocksForConfiguredSeconds` |
| 旧分组无需手动修改即可运行 | 后端 `RetryDelay` 自夹取 + 前端 `normalizeRelayConfig` | `TestNormalizeLegacyRelayConfigBackfillsNewFields`（端到端旧行测试不可达，见 §8） |
| 前端：模式选择、指数初始/最大、手动上限、隐藏亲和、边界校验 | `Editor.tsx` | TypeScript 检查和生产构建通过；阶段 1 修改文件无 lint 错误；未建立 UI 自动化测试 |

## 5. 阶段 0 基线断言的变化（**与阶段 0 文档的预判不同**）

阶段 0 §4 预判"手动模式 4 次请求 / 上限相关的断言阶段 1 会变红"。**实际没有变红**，原因是 Ruling 1：`manual_max_attempts` 缺省补 0（不限额），旧分组与基线行为逐字保持。阶段 0 的 6 条基线断言全部仍绿。

## 6. 踩坑清单（阶段 2 必读）

1. **`op.GroupGetByName` 走缓存且不做 Normalize**（只有创建/更新/备份导入会 Normalize）。所以"后端已回填默认值"这个假设**不成立**：Relay 可能拿到零值新字段。任何新配置字段的读侧都要自带兜底，或在阶段 2 引入缓存装载时的统一 normalize。
2. **`!sent`（没真正发出请求的失败）不能让路由停滞**：改动期间踩过一次——只在 `!sent` 分支等待重轮，会让故障转移永久停在 `buildOutbound` 失败的成员上（见 Ruling 5）。今后在失败分支新增"提前 continue"时必须同时考虑是否要让出路由。
3. **失败分支的收尾顺序**：`cancelRound()` 必须在 `!sent` 分支之前调用，否则本轮 `roundCtx` 已登记在请求状态上却不取消（context 泄漏，实测过）。
4. **`pickGroupItem` 在手动模式下完全不看冷却与探测**，`RouteStateOf` 手动模式恒返回 `ProbeItemID: 0`，切模式时 `ResetRouteState` 清空进程内路由——所以手动模式不存在探测占用泄漏，别为此加多余的 `releaseRouteProbe`。
5. **`serveWithTimeout` 是 30 秒上限**：新测试若要快速暴露"路由停滞"，用 `upstream.awaitRequests(t, n, 5*time.Second)` 之类的短超时，别等满 30 秒。
6. 时间断言一律用**宽松下界**（700ms / 2s / 2.5s），不要改成精确毫秒（阶段 0 §3 的取舍）。
7. **测试里写超大 int 字面量要用 `platformInt`**（两个包的测试各有一份）：发布矩阵含 `linux/386` 与 ARMv7，`9223372037` 这类字面量在 32 位 `int` 上会导致测试编译失败，不影响忽略 `*_test.go` 的普通 `go build`。32 位收口值 `2147483647` 小于安全换算阈值 `9223372036` 秒，因此该平台覆盖大值等待和指数封顶，固定等待的饱和分支由 64 位测试覆盖。

## 7. 验证命令清单（可复制）

```bash
go build ./...
go vet ./...
go test ./internal/relay ./internal/model -count=1 -v
go test ./... -count=1
GOARCH=386 go test ./internal/model ./internal/relay -count=1                  # 32 位平台实测
GOOS=linux GOARCH=386 go vet ./internal/model ./internal/relay                 # 发布目标可构建性
GOOS=linux GOARCH=arm GOARM=7 go vet ./internal/model ./internal/relay         # 发布目标可构建性
git diff --check                      # 只有 LF→CRLF 警告, 无空白错误
node -e "..."                         # locale 解析与三份 key 对齐（脚本见 progress.md 记录）
```

复审实测：`go test ./... -count=1` 与 `go vet ./...` 通过；`go build ./...` 退出码为 0，但有 Go 模块缓存写入的 `Access is denied` 警告。Windows 386 的模型和 Relay 测试实际运行通过；Linux 386、ARMv7 的 `go vet` 通过。独立 overlay 回归用例验证了超大固定等待可取消、601 / 3600 / 86400 秒不被收窄，以及同协议和跨协议的发送计数边界。

前端实测：在 `web` 下执行 `pnpm lint --format json --output-file <临时报告>`，结果为 3 个错误、0 个警告；执行 `pnpm build --outDir <临时构建目录> --emptyOutDir false`，TypeScript 检查与 Vite 生产构建均通过。临时输出目录用于保留仓库内的 `static/out/README.md`。相关依赖清单、锁文件及 lint 配置均与开发基线一致。

## 8. 未验证 / 已阻塞项

- **全量前端 lint 尚未通过**：`web/src/components/modules/group/ItemList.tsx:68` 的 `t` 未使用；`web/src/components/ui/chart.tsx:367,369` 的 `ChartTooltip`、`ChartLegend` 导出触发 `react-refresh/only-export-components`。这两个源文件、lint 配置和依赖锁文件均与 `0711771` 一致，确认是基线问题；阶段 1 修改文件没有 lint 错误。用户已安排后续独立小修复。
- **`-race` 未跑**（无 C 工具链）。
- **旧分组的端到端验证不可达**：没有"按分组重载缓存"的入口，`op.InitCache()` 会重建全部缓存，无法在集成测试里构造"库里是旧 JSON + 转发链路读它"的最小场景。改为分别验证两端兜底（`RetryDelay` 单元测试 + `normalizeRelayConfig` 静态审阅）。
- **真实上游未接入**（全部用 `httptest` 假上游），上游真实超时/重试语义未验证。
- **32 位平台的测试只在 windows/386 上真跑过**（已跑，全绿）；`linux/386` 与 `linux/arm`(ARMv7) 只做了 `go vet` 的**类型检查**，没有交叉执行的测试结果（本机无 qemu 等模拟器）。

## 9. 决策摘要（完整版见 `.superpowers/sdd/v1.0plan/progress.md`）

- **R1**：`manual_max_attempts` 缺省 0（不限额），保阶段 0 基线。
- **R2**：手动预算耗尽 → **HTTP 503 + `upstream_error`**（与阶段 3 契约同形，阶段 3 只需补 Retry-After 与状态发布）。
- **R3/R5**：`!sent`（出站转换器构造失败）不消耗尝试次数，但**故障转移模式下立即让该成员进入冷却并重新选路**（否则请求永久停滞）。
- **R4**：非"上游失败重试"的等待（`:91` 分组缺失 / `:101` 无可用成员即全员冷却 / `:111` 授权缺失 / `:122` 渠道缺失）保持**固定间隔**，不指数化。**注意范围**：**不适用于重试等待**——`RetryDelay` 的 `retryCount` 来自"每请求内每成员"的连续失败计数，换成员即归 1，指数退避在成员之间不累积，每个成员都从 1 秒重新开始（手动模式例外，无成员切换，按 plan 第 1559 行顶到 600s 并保持）。上述四处走到时 `item.ID == 0`，**没有成员可换，故不存在计数重置**，指数化才会产生迟滞；且该分支在**阶段 2 任务 8** 会被"等待最早冷却截止时间"取代，R4 仅为阶段 1 占位。**代码注释中已标明"阶段 1 临时"**（`handler.go` 循环入口的分类说明 + 各处就地标注）。
- **R6（验收轮）**：固定间隔只防回绕、不按 600 收窄（饱和到最长可表示时长）。理由：plan 只说"固定模式沿用现有固定间隔字段"，按 600 收窄会静默改变 601 秒以上本来正常的既有配置。**第三轮补充**：这条现在对**所有**固定间隔的秒数入口成立——`RetryDelay` 与 `wait` 共用 `secondsToDuration`。
- **R7（验收轮）**：指数初始间隔按 600 收窄（Normalize + 前端 + `RetryDelay` 三处一致）。理由：首次等待不可能超过最大间隔，原"换算后封顶"已产生同一结果，属把隐含行为显式化，无语义变化。
- **R8（第三轮）**：`wait` 改用 `secondsToDuration`，不删 `waitDuration`、不改签名。理由：`waitDuration` 是"时长已定"的入口（重试路径从 `RetryDelay` 直接拿 `time.Duration`），`wait` 是"秒数入口"，只在秒数入口做换算即可，分工清晰。成本：无。

## 10. 阶段 2 开工前注意

- 用户本轮仅授权阶段 1 提交和基线 lint 清理；阶段 2 需等后续指令。
- 阶段 2 会重整 `route.go` 的状态机；届时注意坑 #2 与 #4。
- `web/src/api/group.ts` 里 `member_affinity_seconds` 已标注为"后端保留字段"，若阶段 2 决定彻底移除亲和，前后端要一起删（`MemberStatus.tsx` 与 `RouteState.AffinityUntil` 是仅存的使用点）。

## 11. 基线 lint 清理与最终复审

阶段 1 提交 `4ed9cea` 完成后，用户确认由 `gpt-6-luna` 以 `max` 推理强度执行小修复。源码仅修改两处：`ItemList.tsx` 移除 `MemberItem` 内未使用的翻译 hook；`chart.tsx` 从 Recharts 显式重导出 `Tooltip as ChartTooltip` 与 `Legend as ChartLegend`，保留原组件对象、公开名称和类型。

修复代理执行 `pnpm lint` 和 `pnpm build --outDir <系统临时目录> --emptyOutDir false`，退出码均为 0，生产构建包含的 TypeScript 检查通过。主代理已审阅完整差异，确认没有关闭 lint 规则、修改依赖或引入组件包装层；`git diff --check` 通过。此次小修复与本文档更新单独提交，阶段 1 原提交保留。

阶段 1 及其基线 lint 清理已完成，用户要求本轮不进入阶段 2。
