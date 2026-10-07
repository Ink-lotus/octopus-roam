# M3 代码评审交接单（待修复）

交接对象：codex。评审人：Claude。日期：2026-10-06。

## 一、背景

- 分支 `test`，基线 `c451f6e`（M2 收尾）。评审对象是**工作区尚未提交的 M3 改动**：渠道自动更新（`channel_automation` / `task/channel_update`）+ 渠道并发上限（`relay/channel_concurrency`），含 `git diff HEAD` 的 20 个文件与 14 个新增文件。
- 评审方只读，**没有修改任何代码**。你拿到的工作区就是评审时的状态。
- 相关设计与执行记录见 `plan/m3-automation-execution.md`、`plan/m3-automation-result.md`。

## 二、评审已跑的验证（基线，修完要能全绿）

```
go build ./...                                   # 通过
go vet ./internal/...                            # 无输出
go test ./internal/op ./internal/relay ./internal/task ./internal/db ./internal/server/...   # 全绿（relay 34.8s）
```

**评审漏跑、要求你补上的：**

```
go test -race ./internal/relay ./internal/op ./internal/task
pnpm -C web lint
pnpm -C web build
```

M3 新增了两处并发原语（渠道额度 FIFO、自动化设置信号），`-race` 不能省。

## 三、待修问题（5 处，按严重度排序）

---

### 问题 1 — 故障转移分组可被单渠道额度打满而整组挂死【L3，最该修】

**位置**：`internal/relay/handler.go:207`（配合 `internal/relay/channel_concurrency.go:89`、`internal/relay/route.go:187`）

**现状**

`acquireChannelCapacity` 被放在**已经取得恢复探测令牌之后**调用。此时 `roundCtx` 还没创建（它在 `handler.go:243` 才建），传进去的是整个请求的 `ctx`，而 `acquireChannelCapacity` 的等待只在这四个条件上唤醒：

```go
select {
case <-ctx.Done():
case <-routeChanged:
case <-configChanged:
case <-changed:
}
```

即**额度排队没有任何超时上限**。

**为什么是问题（已逐行核实的完整链路）**

1. 优先级 1 成员失败进冷却时，`route.go:316-318` 把 `CurrentItemID` 清为 0。
2. 冷却到期后，`route.go:192` 的循环按成员顺序遍历，优先级 1 先命中 `route.go:206`，拿到探测令牌并返回；`CurrentItemID` **仍是 0**，优先级 2 的健康成员根本没被遍历到。
3. 该请求带着探测令牌走到 `handler.go:207`，若此刻该渠道的并发额度被**别的分组**的长流式请求占满，它就无限阻塞在额度队列里。
4. 同组其他请求重新选路时，`route.go:171` 因 `CurrentItemID == 0` 不短路，落到 `route.go:187` 返回 `routeSelection{Wait: true, ...}`——**没有 `WaitUntil`**。
5. `waitRoute`（`route.go:349`）在 `WaitUntil.IsZero()` 时只 `select` `ctxDone` 和 `selection.Changed`，于是同组所有请求无限阻塞。

结果：**整个分组挂住，直到客户端自己断开**，哪怕优先级 2 的健康成员完全空闲。原本"探测一次上游就出结果"的快路径被换成了可能无限长的额度排队。

**修复方向**（这是 L3，并发 + 共享选路逻辑，动手前先把你选的方案呈给用户确认）

- 推荐：**持有探测令牌时不排队**。给 `acquireChannelCapacity` 增加非阻塞/短超时模式，`selection.ProbeToken != 0` 时走该模式；拿不到额度就 `releaseRouteProbe` + `continue`，把探测机会交回下一轮，让别的请求或下次选路去试。理由：探测的语义是"快速试探恢复"，不该为它扣住整组的选路权。
- 备选：给额度等待加一个有界上下文（复用 `group.RelayConfig` 里已有的超时项，或新增配置项），超时则释放探测令牌后换成员。缺点是要引入/复用一个语义不完全贴合的超时值。
- 另一个独立于上面两条的加固：让 `route.go:187` 这条 Wait 分支带上一个有界的 `WaitUntil`，使等待者至少能周期性重新评估，而不是完全依赖探测持有者。可作为纵深防御，但**不要用它替代 1 或 2**——根因是探测持有者会无限阻塞。

**验收**

- 新增回归测试：两个分组共用同一渠道，渠道 `MaxConcurrency=1`，分组 A 的长请求占住额度；分组 B 的优先级 1 刚结束冷却、优先级 2 健康 → 分组 B 的请求必须在有界时间内落到优先级 2 并成功，不得挂死。
- `internal/relay/channel_concurrency_test.go`、`failover_test.go`、`route_state_test.go` 全部保持绿，且 `go test -race ./internal/relay` 绿。
- 按用户规则，L3 走完整 red-green：先写出会挂死（超时失败）的测试，再改实现。

---

### 问题 2 — 最热的管理接口被塞进整份刷新诊断【L3，涉及对外响应体，需用户拍板】

**位置**：`internal/op/channel.go:60`、`internal/op/channel_automation.go:62-76`、`web/src/api/channel.ts`（`refetchInterval` 30000 → 5000）

**现状**

- `ChannelStatsList` 给**每个**渠道挂上 `Refresh: ChannelRefreshStatus(channel.ID)`，而 `ChannelRefreshStatus` 返回的 `Last` 是**完整的** `ChannelRefreshRecord`：全部 `added_models` / `removed_models` 模型名、逐 Key 的 `key_results`、`warnings`。
- 该函数还对每个渠道做一次 `json.Marshal` + `json.Unmarshal` 深拷贝（`channel_automation.go:71-73`），只为了防止调用方改到缓存。
- 前端同时把这条查询的轮询从 30s 压到 5s，且 `web/src/components/modules/home/rank.tsx:116` 的首页榜单与 `channel/index.tsx:58` 的渠道列表**共用**它。

**为什么是问题**

`internal/server/handlers/channel.go:106-107` 的注释明确写着这个接口"不带整份配置，界面按更短的间隔刷新它"。首刷 400+ 模型的渠道会让整份诊断**长期驻留**在最热的管理接口响应里（诊断记录是持久化的，不是刷新期间才有），加上每渠道一次 JSON 往返深拷贝、5s 一轮、两个页面共用——与该接口自己的契约相反。

**修复方向**

- 推荐：`ChannelStats.Refresh` 只带**轻量摘要**——`running` / `started_at` / `last.status` / `last.source` / `last.finished_at` / `before_count` / `after_count` / 新增增删条数计数。完整诊断（模型名单、`key_results`、`warnings`）挪到按渠道单取（扩展 `ChannelDetailGet`，或新增 `GET /channel/:id/refresh`）。摘要是值类型，顺带去掉 JSON 深拷贝。
- 轮询策略：列表查询回到 30s；"正在刷新"的快速反馈改由渠道详情/刷新对话框打开时的单渠道查询承担（2-5s）。
- **需要用户拍板的取舍**：列表上的"刷新中"指示要多快出现。如果用户坚持列表也要 5s 级反馈，那至少把诊断正文拆出去、只让 5s 的那条查询带摘要。先问，别自己定。

**验收**

- `internal/server/handlers/channel_automation_test.go:52` 已断言 `stats.Refresh != nil`，改字段形状时同步更新，并新增断言：列表响应里**不含**模型名单与 `key_results`。
- 前端类型 `ChannelRefreshStatus` 与消费它的组件同步改；`pnpm -C web lint` + `build` 通过。

---

### 问题 3 — 手动刷新的价格维护被硬编码 10s 超时套住【L2】

**位置**：`internal/op/channel_automation.go:141`

```go
maintenanceCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
defer cancel()
if maintenance != nil {
    result.Warnings = append(result.Warnings, maintenance(maintenanceCtx, result.AddedModels)...)
}
```

**为什么是问题**

`refreshChannelModelsWithClient` 是**手动与自动刷新共用**的路径（`channel_automation.go:154` 默认 `Source: "manual"`，`options.Automatic` 才改成 automatic）。原先这段维护（写价格行 + 清理 ghost）用的是请求上下文、没有上限；现在手动刷新也被 10s 硬超时覆盖。首刷大量模型 + 远端数据库时会超时，**价格行静默缺失，只回一条 warning**，用户在界面上看到的是"刷新成功"。

`context.WithoutCancel` 本身是对的——自动刷新的批次被取消时不该半途丢下价格维护。问题只在超时值和它覆盖到了手动路径。

**修复方向**

- 手动路径（`!options.Automatic`）恢复用请求上下文、不加额外超时。
- 自动路径保留 `WithoutCancel`，但超时要够：按 `len(result.AddedModels)` 伸缩，或直接提到分钟级。10s 对首刷来说明显不够。
- 超时发生时的 warning 文案要能让用户看出"价格行没写全"，而不是泛泛一句失败。

**验收**：补一条回归测试，maintenance 回调故意慢于 10s，断言手动刷新不会因此丢维护结果。L2 按用户规则补回归测试即可，不必走完整 red-green。

---

### 问题 4 — 排空额度队列是 O(N²) 次唤醒【L3】

**位置**：`internal/relay/channel_concurrency.go:100-106`

```go
func signalChannelCapacity(id int, entry *channelCapacity) {
    close(entry.changed)
    entry.changed = make(chan struct{})
    ...
}
```

**为什么是问题**

每次 acquire（`remove()` 在成功路径也会调用）、release、取消都 `close(entry.changed)` **唤醒全部排队者**；但 `channel_concurrency.go:69` 是严格 FIFO，只有 `entry.queue.Front() == position` 的队首能前进。N 个等待者排空需要 N² 次唤醒，而每次唤醒都要抢同一把全局 `channelCapacities.Mutex`——`listChannelStats` 现在每 5s 也要拿这把锁（见问题 2）。高并发渠道上这是可观的无谓竞争。

**修复方向**

- 推荐：队列元素从 `struct{}{}` 换成每个等待者私有的 `chan struct{}`，释放额度时**只唤醒队首**。
- 配置变更仍需唤醒全部（上限可能被调高），所以保留一个独立的广播 channel 专门用于配置/渠道变更，与额度释放的定向唤醒分开。
- 注意别破坏现有已验证的性质：`active` 计数与 map 条目回收在各种交错下的自洽、空条目立即回收不留墓碑。

**验收**：`channel_concurrency_test.go` 全部保持绿；`go test -race ./internal/relay` 绿。L3，走完整 red-green。

---

### 问题 5 — 备份导入会误触发自动化调度器重置【L2】

**位置**：`internal/op/setting.go:140`

```go
for _, setting := range settings {
    settingCache.Set(setting.Key, setting.Value)
}
automationSettingsSignal.notify()   // ← 无条件
```

**为什么是问题**

`settingRefreshCache` 无条件广播。备份导入走 `op.InitCache()` 时，`task/channel_update.go:91` 的 `case <-changed:` 会把它当成"自动化设置变了"：

```go
case <-changed:
    changed = u.settingsChanged()
    config = u.config()
    if cancelRun != nil { cancelRun() }   // 取消正在跑的批次
    reset()                                // 定时器重置成完整周期
```

即**取消正在跑的刷新批次，并把定时器重置成完整周期（默认 1440 分钟）**，哪怕三个自动化键一个都没变。

同文件 `SettingSetString:56` 和 `SettingSetInt:79` 都用 `isChannelAutomationSetting(key)`（`setting.go:144`）正确收口了，只有这里漏了。

**修复方向**

在写缓存前记下三个自动化键（`SettingKeyChannelAutoUpdateEnabled` / `...Interval` / `...AutoAddGroups`）的旧值，写完后比对，**只有值真的变了才 notify**。注意首次 `InitCache`（缓存本来是空的）应当视为"变了"，让调度器拿到初始配置。

**验收**：补回归测试——自动化键不变的 `settingRefreshCache` 不触发信号；自动化键变化时触发；首次初始化触发。L2，补回归测试即可。

---

## 四、已核过、确认不是问题（别顺手改，别重复排查）

这些评审时都逐一看过并排除了，列出来避免你返工：

- `releaseCapacity` 在 `handler.go` 207→529 之间所有 `continue` / `return` 路径都已显式释放或由 defer 兜住，**没有额度泄漏**。
- 额度 FIFO 的 `active` 计数与 map 条目回收在各种交错下自洽，空条目立即回收，不留墓碑。
- `verifyChannelRefreshSnapshot` 用 `reflect.DeepEqual`，新加的 `*bool`（`FollowGlobalAutoUpdate`）归一化不会误判 stale。
- `refreshChangedChannelGroups` / `allGroupsByID` 仍被 update / enable / delete 三个处理器使用，**不是死代码**。
- `handler.go` 里移除 `groupMutationMu` 是**必须的**，否则新观察者会自锁。不要加回来。
- Go 1.26 的 Timer 语义使 `task/channel_update.go` 的 `timer.Stop()` + `Reset()` 不会收到残留 tick。
- `ChannelStats.Concurrency` 由 `internal/server/handlers/channel.go:111` 在 handler 层填充（`op` 不能 import `relay`），不是漏填。

## 五、约束（来自用户全局规则，优先级高于流程惯例）

- **风险分级与测试**：L1 写定向验证，L2 补回归测试，L3 走完整 red-green。本单已逐条标注：问题 1/2/4 是 L3，问题 3/5 是 L2。每条动手前把判定结论说出来。
- **L3 先呈设计**：问题 1、2、4 动手前把你选的方案和取舍呈给用户确认。问题 2 有个必须用户拍板的点（列表刷新指示的实时性要求）。问题 3、5 边界清晰，直接实现，不必先等点头。
- **不自动 commit**：修完跑全量验证后停下报告，commit 由用户决定。commit message 的 summary 用中文。
- **不建 worktree**：就地在当前工作区改。
- **不新增 / 升级 / 移除依赖**，不改 CI 与环境配置。
- **范围限定**：只修这 5 处 + 配套测试。不要顺手重构、不要动第四节里已排除的那些地方。超出范围的发现写进报告，别直接改。
- **不自行派发子代理**：遇到范围过大的工作，在自身上下文内尽力完成，并在报告里说明建议另行处理的部分；确有派发必要时停下向用户说明理由，获明确批准后再动。
- 默认中文回复；代码、命令、日志、专有名词保留原文。

## 六、交付时请说明

1. 改了哪些文件，每处对应本单的哪个问题编号。
2. 跑了什么验证、结果如何（含第二节要求补的 `-race` 与前端 `lint`/`build`）。
3. 该跑没跑的，为什么没跑。
4. 哪些问题因为取舍未定而搁置、在等什么决定。
5. 剩余风险与需要用户授权的动作（commit、合并、推送等）。
