# 阶段 0 交接文档：Relay 重构前测试基线

> 本文档供**后续会话（阶段 1 起）**阅读，替代上一会话的对话上下文。
> 上一会话只做了一件事：建立 `internal/relay` 的测试基线，**未改动任何生产代码**；6 个文件随后经用户授权提交到本地 `test`（见 §1）。
> 计划原文见同目录 `plan/v1.0plan.md`。

---

## 0. 一句话状态

`internal/relay` 从**零测试**变为 **6 个测试全绿**，锁定了固定间隔重试、手动模式持续重试、取消退出、基本故障转移、流式首帧前重试五类行为的**当前**语义；生产代码一行未改。

---

## 1. 分支与 Git 现状（纪律约束）

| 项 | 状态 |
|---|---|
| 当前分支 | `test` |
| `git branch -a` | 本地 `master`、`test`；远端 `origin/master` |
| `master` | **未提交、未推送、未写入**，HEAD 仍是 `4a54b59 build: :rocket: v0.13.6` |
| `beta` | **不存在**（未创建 / 未切换 / 未合并） |
| `test` 上的提交 | **1 个**：`3a4437e test: :white_check_mark: 建立 Relay 重构前测试基线`（6 files, +2395） |
| 工作区 | 提交后**干净** |

> 本节描述的是阶段 0 结束时的状态。**后续会话以 `git log --oneline -3` 和 `git status --short` 实测为准**，不要依赖本节记录。

`3a4437e` 的内容：4 个测试文件 + `plan/v1.0plan.md` + `plan/phase0-baseline.md`。**不含任何生产代码改动**（提交前 `git diff` 为空，新增文件全部为 `A`）。

**未授权动作（后续会话同样不得自行执行）**：merge、rebase、cherry-pick、push、pull、fetch、tag、PR/MR、远端状态变更、分支增删改、`git stash` / `reset` / `clean` / `checkout --`、改写历史、写任何东西到 `master` 或 `beta`。阶段内 commit 需**用户逐次授权**（阶段 0 的基线提交已获授权）。

---

## 2. 环境事实（本机，务必先读）

- OS：Windows 11；shell 默认 **PowerShell**（另有 Git Bash 可用，两者语法不同，勿混用）
- Go：**go1.27.0 windows/amd64**；`go.mod` 声明 `go 1.26.4`
- 数据库：SQLite（`github.com/glebarez/sqlite`，纯 Go / modernc，无需 CGO）
- **`-race` 在本机不可用**：`-race requires cgo`；置 `CGO_ENABLED=1` 后报 `cgo: C compiler "gcc" not found`。已确认 `gcc` / `clang` / `tcc` / `x86_64-w64-mingw32-gcc` **全部缺失**，`go env CC` = `gcc`。因此**一切竞态相关结论在本机无法用 race 检测器验证**。要跑 `-race` 需先装 C 工具链——属环境变更，**须单独授权**。
- 前端：`web/node_modules` **不存在**；`pnpm 11.15.1` 可用但**安装依赖被禁止**（不得改 `pnpm-lock.yaml`）。`web/package.json` 的 scripts 只有 `dev` / `build` / `preview` / `lint`，**没有 test 脚本**，故前端基线天然缺失。
- 网络检索/抓取走 `tinyfish`（`tinyfish search query "..."` / `tinyfish fetch content get "<url>"`）。

---

## 3. 新增文件与夹具 API

4 个新文件，共 556 行，**全部是测试代码**：

| 文件 | 行数 | 角色 |
|---|---|---|
| `internal/relay/testsupport_test.go` | 303 | `TestMain` + 全部夹具 |
| `internal/relay/retry_test.go` | 125 | 重试 3 个测试 |
| `internal/relay/failover_test.go` | 89 | 故障转移 2 个测试 |
| `internal/relay/stream_test.go` | 39 | 流式首帧前重试 1 个测试 |

### 夹具 API（阶段 1 扩测时直接复用，勿重造）

| 符号 | 位置 | 用途 |
|---|---|---|
| `chatCompletionBody` | `testsupport_test.go:30` | 非流式上游成功响应，正文含 `"content":"pong"` 与 usage |
| `chatCompletionStreamBody` | `:33` | 两段 SSE：一个内容增量 + `data: [DONE]` |
| `TestMain` | `:37` | 建临时目录 → `db.InitDB("sqlite", <临时文件>, false)` → `op.InitCache()` → `m.Run()` → 清理 |
| `upstreamCallLog` | `:61` | `add` / `snapshot` / `count(member)`，记录上游到达顺序 |
| `fakeUpstream` | `:93` | 按脚本应答的测试上游 |
| `newFakeUpstream(t, name, log, statuses...)` | `:103` | `statuses` 依次给出**失败**状态码，用尽后一律成功；自动 `t.Cleanup` 关闭 |
| `fakeUpstream.handle` | `:118` | 记录 `"<成员名>/<上游模型名>"`；`payload.Stream` 为真时返回 SSE |
| `(*fakeUpstream).requestCount()` | `:155` | 已到达请求总数 |
| `(*fakeUpstream).awaitRequests(t, want, timeout)` | `:160` | **通道式**等待第 `want` 次请求到达，不轮询 |
| `failingStatuses(status, n)` | `:173` | 生成连续 n 次失败的脚本 |
| `testName(t, kind)` | `:185` | 进程内唯一名称（含 `atomic` 序号，兼容 `-count=N`） |
| `testRelayConfig(maxAttempts)` | `:192` | 测试用 `model.GroupRelayConfig`：间隔 **1s**、非流式超时 30s、首事件超时 30s、冷却 60s、**亲和 0** |
| `newTestChannel(t, name, baseURL, upstreamModel)` | `:205` | 经真实 `op.ChannelCreate` 建渠道+模型+凭据+授权，返回**授权主键** |
| `newTestGroup(t, mode, config, grantIDs...)` | `:239` | 经真实 `op.GroupCreate` 建分组，返回**分组名**（= 客户端请求的模型名） |
| `newRelayEngine()` | `:270` | `gin.New()` + `POST /v1/chat/completions → Forward(llm.APIFormatOpenAIChatCompletion)` |
| `serveWithTimeout(t, engine, recorder, req)` | `:277` | 30 秒兜底跑完一次转发，超时报"疑为重试循环未按预期退出" |
| `chatRequest(t, groupName, streaming)` | `:293` | 构造客户端请求；`streaming` 为真时带 `"stream":true` |

### 设计取舍（改夹具前先读）

- 用**真实 SQLite 临时文件 + 真实 op 层**，不用替身：转发链路经 `op` 读分组/渠道/凭据，替身会掩盖真实读取路径。**不要用 `:memory:`**——连接池下多连接各见一库。
- 夹具参数 `MemberRetryIntervalSeconds = 1`：`NormalizeGroupRelayConfig` 对 `<1` 回填默认 3，故 **1 是配置允许的最小非零值**，取它让重试等待确实发生又不至于久等。
- 参数 `MemberAffinitySeconds = 0`：`Normalize` 只对 `<0` 回填，故 **0 合法**，使故障转移选路只由冷却决定，排除亲和处理。
- 每轮测试的渠道/分组名由 `testName` 保证唯一，故**同包测试可共用一套库与缓存而不互相干扰**。
- `newRelayEngine` 只挂了 Chat Completions 一个路由；要测 Responses / Anthropic 协议需另加路由。
- 新用例只允许用标准库（`testing`、`net/http/httptest`、`context`、`sync`、`sync/atomic`、`time`、`slices`、`strings`）。**不得引入第三方测试依赖，不得连真实外部服务，不得使用真实凭据**，测试日志中不得出现凭据或敏感头。

---

## 4. 已锁定的基线行为（阶段 1 改代码后这些会变红，是**预期**的）

| # | 测试 | 锁定的当前语义 | 精确断言 |
|---|---|---|---|
| 1 | `retry_test.go:21` `TestFixedRetrySucceedsAfterSecondAttempt` | 手动模式，上游首败后按 `member_retry_interval_seconds` 等待再试，第二次成功即返回，首败不暴露给客户端 | 最终 200；正文含 `pong`；上游请求 **2** 次；调用序列 `["member/upstream-model" ×2]`；耗时 **≥ 700ms** |
| 2 | `retry_test.go:56` `TestManualRetryContinuesAfterRepeatedFailures` | **手动模式不受 `member_max_attempts` 限制**，持续重试直到成功 | 最终 200；上游请求 **4** 次（3 失败 + 1 成功），而配置 `maxAttempts=2`；耗时 **≥ 2s** |
| 3 | `retry_test.go:87` `TestManualRetryStopsOnContextCancel` | 取消后转发及时结束且**不再发起新的上游请求** | 取消后 **5s 内**结束；`requestCount()` 保持不变；`arrived` 通道无残留信号 |
| 4 | `failover_test.go:19` `TestFailoverSwitchesToNextMemberAfterAttemptsExhausted` | 成员耗尽 `member_max_attempts` 次尝试后让出路由，请求转由后续成员承载并成功 | 最终 200；顺序 `["first/model-first" ×2, "second/model-second"]`；`count(first)=2`、`count(second)=1` |
| 5 | `failover_test.go:54` `TestFailoverCallOrderAndCounts` | 4 成员：前两个各耗尽 2 次，第三个首次即成功，第四个**一次也不被调用** | 精确序列 `first×2, second×2, third×1`；`count(third)=1`；`unused.requestCount()==0` |
| 6 | `stream_test.go:17` `TestStreamingRetryBeforeFirstEventCommit` | 流式路径在**首帧提交前**失败仍可重试；首帧提交后不可重试（本测试只覆盖前一半） | 最终 200；`Content-Type: text/event-stream`；正文含 `pong`；上游请求 **2** 次 |

### 阶段 1 会**有意打破**的断言（别当成回归）

- **#2**：阶段 1 引入 `manual_max_attempts` 后，手动模式的重试次数将被新配置约束，"4 次请求"需按新语义**有意**改写。
- **#1 / #4 / #5**：若引入重试模式（固定/指数）或新选路顺序，间隔与冷却相关的时间下界与调用序列需按新配置调整。
- 时间断言全部是**宽松下界**（700ms / 2s），只用于区分"等待过"与"立即忙循环"，**不要**改成精确毫秒，也不要因此判定 flaky。

---

## 5. 转发链路关键坐标（实测确认，供阶段 1 定位）

### `internal/relay/handler.go` — `Forward` 主循环

- `:82` `for {}` 主循环开始；`:83` 每轮先查 `ctx.Err()`
- `:89` **每轮重新读分组**（配置与成员随时可改）；`:90-95` 分组不存在时等待 `DefaultGroupRelayConfig().MemberRetryIntervalSeconds`
- `:99` `item := pickGroupItem(group)`；`:100-105` **零值（无目标）时等待 `group.RelayConfig.MemberRetryIntervalSeconds` 后重选**
- `:109-115` 授权缺失/凭据停用 → 等待；`:120-126` 渠道被删 → 等待
- `:129` `sjson.SetBytes(raw.Body, "model", channelModel.Name)` —— **上游请求体里写入的是成员的真实模型名**，测试正是据此从上游侧确认命中成员
- `:136-143` 流式 + OpenAI Chat 时写 `stream_options.include_usage=true`
- `:147` `buildOutbound` 选出本轮协议并构造出站转换器；`:150-155` `context.WithCancelCause` + `startRound` 登记本轮目标
- `:163-168` 超时取值：非流式 `MemberNonStreamResponseTimeoutSeconds`，流式 `MemberStreamFirstEventTimeoutSeconds`
- `:170-192` `time.AfterFunc` 到点取消本轮；`passthrough` 走 `sendPassthrough`，否则 `sendConverted`
- `:195-231` **失败处理**：
  - `:199-203` 父 ctx 已结束 → 归还探测占用 + `markCanceled`，返回
  - `:205-208` `context.Cause(roundCtx) == context.Canceled`（**仅人工中止**）→ 不计失败、不等待，直接 `continue`
  - `:211-214` 真实失败才计渠道/模型/凭据统计
  - `:217-222` 同成员则 `failures++`，换成员则重置为 1
  - `:224-226` `recordRouteFailure(...)` 返回真（已冷却）→ **立即重选，不等待**
  - `:227-229` 否则等待 `MemberRetryIntervalSeconds`
- `:242` 成功后 `recordRouteSuccess`；`:249-262` 非流式一次性提交

### `internal/relay/route.go` — 路由语义

- `:36-55` `RouteStateOf`：**手动模式不走进程内路由**，`CurrentItemID` 直接取 `group.ActiveItemID`，冷却/亲和均为空
- `:59-64` `ResetRouteState`
- `:68-118` `pickGroupItem`：
  - `:69-76` **手动模式只在 `item.ID == group.ActiveItemID` 时返回该成员，否则返回零值**（→ 主循环持续等待重试）
  - `:83-90` 亲和期内沿用当前成员，不提前探测已恢复的高优先级成员
  - `:92-113` 按 Priority 升序扫描，跳过冷却未到期的；冷却已到期的成员**只放行一个探测请求**（`ProbeItemID`）
- `:121-157` `recordRouteSuccess`：探测成功解除冷却并可能立即切回；`:147-153` 亲和只在**故障切换后的首次成功**时开始
- `:161-191` `recordRouteFailure`：
  - `:162-164` **手动模式直接 `return false`** → 不冷却、不限制次数（对应基线 #2）
  - `:174-176` 常规成员 `failures < MemberMaxAttempts` 时不冷却
  - `:179-188` 到期时间 = `now + MemberCooldownSeconds*1000`；当前路由失败才清 `CurrentItemID` 并置 `affinityArmed = true`
- `:194-202` `releaseRouteProbe`（人工中止/客户端断开时归还探测占用）
- `:205-229` `groupRouteLocked`：清理已删除成员的冷却/探测/当前成员残留

### `internal/relay/state.go`

- `:178-186` `wait(ctx, seconds)`：`ctx.Done()` 先于计时器时 `markCanceled` 并返回 **false**（调用方随即 return）
- `:120-128` `finishRound`；`:189-202` `markCommitted`；`:257-298` `finishLocked` 写终态、统计、裁剪历史

---

## 6. 踩坑清单（下一会话必读，避免重复踩）

1. **手动模式必须显式指定当前成员**，否则 `pickGroupItem` 恒返回零值，主循环只等待重试，测试表现为"转发未在 30 秒内结束"或"等待上游第 1 次请求到达超时"。
   `op.GroupCreate` **不会**设置 `ActiveItemID`；夹具 `newTestGroup` 因此在手动模式下额外调用 `op.GroupUpdate(group.ID, &model.GroupUpdateRequest{ActiveItemID: &itemID})`。
   **这是既有设计语义，不是缺陷**：手动模式没有进程内路由，当前成员只能由分组配置人工指定，未指定时不产生目标并持续等待，直到成员被选入——对应界面"先选成员再使用"。**用户已明确确认此为特性**。
2. **`GroupCreate` 会把 `Priority` 设为 `i+1`**，`group.Items` 已按 Priority 升序，故夹具传入的 `grantIDs` 顺序即选路顺序。
3. `GroupGetByName` **会过滤掉 `!item.Available` 的成员**；`groupSnapshot` 的 `Available = channel.Enabled && channelKey.Enabled`。渠道或凭据被停用 == 成员直接消失，而非报错。
4. 上游**任何 status ≥ 400** 都被 `httpclient` 转成 `*httpclient.Error`，因此都会走重试分支（流式与非流式一致）。`data: [DONE]` 是 OpenAI 流的结束标记。
5. **仓库既有文件的 gofmt 偏差**：`gofmt -l internal/relay/` 会列出 `channel.go`、`handler.go`、`protocol.go`、`route.go`、`state.go`、`upstream.go`，原因是工作区 `route.go` 等为 **CRLF 行尾**（`file(1)` 已验证）。这是**既有状况，不是本次引入**，不要去"修"。
   新增的 4 个测试文件是 LF 且 gofmt 合规。
6. 分类器/写操作偶发不可用时会返回 `... is temporarily unavailable, so auto mode cannot determine the safety of ...`；此时重试即可，**不要**用 `dangerouslyDisableSandbox` 绕过，也不要在 `master` 的工作区里落文件。
7. 测试耗时：`-count=1` 约 **9~14s**，`-count=10` 约 **91s**（含真实 1 秒粒度等待，属正常）。

---

## 7. 验证命令清单（可复制）

```bash
go test ./internal/relay -count=1        # ok 9.4s
go test ./internal/relay -count=1 -v     # 6 个测试逐一 PASS
go test ./internal/relay -count=10       # ok 91.0s，60 次执行无 flaky
go test ./... -count=1                   # exit 0（除 relay 外均 no test files）
go vet ./...                             # exit 0，无输出
gofmt -l internal/relay/                 # 见 §6.5：既有 CRLF 文件会被列出
git diff --check                         # 无空白错误
git diff --stat ; git diff               # 阶段 0 验证时均为空（当时改动全为未跟踪文件）
git status --short ; git branch --show-current ; git log -1 --oneline
```

`go test -race ./internal/relay -count=1` **在本机无法执行**（见 §2），不要把它当成"跑过了"。

---

## 8. 未验证 / 已阻塞项

1. **`-race` 未执行**，原因见 §2。测试涉及的共享状态（`upstreamCallLog` 互斥锁、`atomic.Int64`、`arrived` 通道）已按并发安全方式编写，但**未经竞态检测器验证**。
2. **前端基线未取得**：`web/node_modules` 不存在 + 无 test 脚本；安装依赖被禁止。
3. 未做真实上游联调、未用真实凭据、未连任何外部服务（任务书要求）。
4. 未做高并发压测；`-count=10` 只覆盖顺序执行。

---

## 9. 前后端 Relay 默认值差异（**仅记录，未修复**，阶段 1 需自行决策是否统一）

| 字段 | 后端 `DefaultGroupRelayConfig()`<br>`internal/model/group.go:22` | 前端 `defaultRelayConfig`<br>`web/src/components/modules/group/Editor.tsx:28` | 差异 |
|---|---|---|---|
| `member_max_attempts` | 2 | 2 | 一致 |
| `member_retry_interval_seconds` | **3** | **1** | **不一致** |
| `member_non_stream_response_timeout_seconds` | 120 | 120 | 一致 |
| `member_stream_first_event_timeout_seconds` | 30 | 30 | 一致 |
| `member_cooldown_seconds` | 60 | 60 | 一致 |
| `member_affinity_seconds` | **300** | **0** | **不一致** |

连带事实（阶段 1 改默认值前必读）：

- `NormalizeGroupRelayConfig`（`internal/model/group.go:34-58`）：**零值结构体整体替换为默认值**；否则逐字段回填——`<1` 回填的字段是前 5 个，`member_affinity_seconds` 只在 `<0` 时回填，故 **0 是合法值**。
- 前端 `Editor.tsx:336-339` 用 `{...defaultRelayConfig, ...initial?.relay_config}` 展开：**只要后端回传了非零值就会覆盖前端默认**；两个差异字段恰好是前端"看起来更短/更省"的值，实际以哪个为准取决于后端是否回传，值得阶段 1 单独核对。
- 相关文件：`internal/model/group.go`、`web/src/api/group.ts`、`web/src/components/modules/group/Editor.tsx`。上一会话**未修改**其中任何一个。

---

## 10. 越界清单：阶段 1 之前**不得**提前引入

上一会话已核对**未**引入以下任何一项，后续会话在阶段 1 正式开工前也应保持：

`RetryMode` / `RetryModeFixed` / `RetryModeExponential`、任何 `retry_*` 新配置字段、`retry_mode`、`retry_initial_interval_seconds`、`retry_max_interval_seconds`、`manual_max_attempts`、`RetryDelay`、指数退避、`attempts` / `retry_count` 新语义、新故障转移扫描顺序、最早冷却等待、路由世代/版本、并发探测、新运行态字段、`RequestState` 扩展、快照接口、新 SSE 接口、Webhook、通知阈值、伴侣、状态令牌、过滤示例 UI、正则复制按钮、亲和隐藏、DB 迁移、备份版本变更、前端表单改动。

> 注：`testsupport_test.go` 里的 `testRelayConfig` 是**测试专用**的 `model.GroupRelayConfig` 字面量，不是新增字段，也未改动生产默认值。

---

## 11. 阶段 1 开工前需要用户确认的动作

- 是否安装 C 工具链以启用 `-race`（环境变更，须单独授权）
- 是否统一前后端 Relay 默认值（§9 的两个差异）
- 阶段 1 的具体实现范围与验收标准

**权威约束仍以用户全局规则（授权门禁）与 `plan/v1.0plan.md` 为准。**
