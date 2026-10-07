# M3 审阅问题修复与复验

日期：2026-10-06。对应 `plan/m3-automation-review.md`。用户在逐项核实后明确授权「按你的想法进行修复」。在原 M3 工作区上局部修改，未提交或推送。

## 修复结果

| 问题 | 实现与文件 |
| --- | --- |
| 1：恢复探测被渠道额度阻塞 | `internal/relay/handler.go`、`channel_concurrency.go`：探测只尝试立即取得额度，尊重已有 FIFO 等待者。拿不到则在 `route.go` 原子归还探测令牌，并推迟该成员 250 毫秒，下一轮扫描其他成员。所有探测候选都忙时按截止时间等待；不改失败冷却、不消耗上游尝试次数。 |
| 2：统计响应携带完整诊断 | `internal/model/channel{,_automation}.go`、`internal/op/channel{,_automation}.go`：列表只返回运行状态、来源、时间、前后模型数、增删数量及维护提醒数量；不包含模型名单、逐 Key 结果或 warning 正文。摘要按值构造；详情只复制可变切片，移除 JSON 深拷贝。 |
| 2：按需读取与前端展示 | `internal/server/handlers/channel.go` 新增受现有管理认证保护的 `GET /api/v1/channel/refresh-status/:id`。`web/src/api/channel.ts`、`web/src/components/modules/channel/Card.tsx` 保留列表 5 秒更新；展开原生可键盘操作的“刷新详情”才启用该渠道的 5 秒详情查询，收起即停止。手动刷新自动展开结果。三份 locale 同步。 |
| 3：维护上下文 | `internal/op/channel_automation.go`：手动维护使用原请求上下文，取消与原请求一致，无额外 10 秒限制；自动维护使用脱离批次取消的独立 2 分钟上下文。`internal/server/handlers/channel.go` 的 warning 明确说明价格未完整保存/载入或清理未完成。 |
| 4：额度广播唤醒 | `internal/relay/channel_concurrency.go`：每个等待者一个带缓冲通知通道；普通释放与队首移除只通知下一个等待者，成功取得额度后继续交接通知。沿用独立的配置变更广播，支持动态上限、禁用与删除；空条目即时回收。 |
| 5：缓存重载误重置 | `internal/op/setting.go`：写入缓存前比较三个自动化键，只有值变化或键首次进入缓存时广播。无变化或仅其他设置变化时不取消批次、不重置周期。 |

没有新增依赖、数据库结构或持久环境配置；已有测试只使用临时 SQLite。

## 回归与 red-green 证据

- 问题 1：`TestRelayBusyRecoveryProbeUsesHealthyFallback` 在修复前因 1 秒请求截止返回空响应失败；修复后另一分组的流仍占满第一渠道时，三个请求均从健康备用渠道成功返回，未遗留探测占用或额度等待者。
- 问题 4：`TestChannelCapacityFIFOHasLinearWakeups` 在修复前记录 16 个等待者共 185 次等待调用，超过线性上限；修复后通过，且验证 FIFO 顺序及排空后的占用数。
- 问题 2：扩展 `TestRefreshChannelModelsHandlerReturnsPerKeyResult`，修复前因列表包含 `added_models`、`removed_models`、`key_results` 且没有摘要计数失败；修复后摘要计数、详情完整结果以及非法/缺失渠道的 HTTP 状态均通过。
- 问题 3：`TestManualRefreshMaintenanceCanExceedTenSeconds` 在修复前的 10 秒处收到 deadline exceeded；修复后 10.1 秒维护完成。另验证手动取消继续传递、自动维护保留独立 2 分钟预算。
- 问题 5：`TestSettingReloadOnlySignalsAutomationChanges` 在修复前的“未变”和“无关设置”场景错误收到信号；修复后四个分支通过。
- `internal/relay/route_state_test.go` 额外覆盖所有探测候选忙时的有界等待、到期重新探测、冷却保持、迟到报告不得释放新令牌。
- `internal/op/channel_automation_test.go` 额外覆盖详情/摘要调用者修改结果不会污染共享缓存。

## 检查结果

Go 使用当前进程的工作区 `.tmp-go-cache`、已有模块缓存与 `GOPROXY=off`；没有修改持久环境。

| 检查 | 结果 |
| --- | --- |
| `go test ./... -count=1` | 通过 |
| `go vet ./...` | 通过 |
| `go test -race ./internal/relay ./internal/op ./internal/task ./internal/server/handlers -count=1` | 通过 |
| `pnpm --dir web lint` | 通过 |
| `pnpm --dir web build --outDir "$env:TEMP/octopus-m3-review-build" --emptyOutDir false` | 通过，含 TypeScript 检查 |
| locale 叶子键一致性 | 通过，三份各 507 键 |
| `git diff --check` | 通过 |

复用被忽略目录中的本地验收 harness，启动隔离临时 SQLite、受控模型上游、实际管理服务及当前 Vite 前端。浏览器 DOM 与请求记录确认：

- 两个渠道详情收起时，只请求统计摘要，不请求任一渠道的完整诊断。
- 手动刷新第一渠道自动展开详情，展示新增 2 个模型和逐 Key 结果；统计响应只有计数摘要。
- 展开期间只轮询第一渠道的诊断，第二渠道没有详情请求。
- 再次收起后，连续多个 5 秒周期只有统计请求；操作详情没有误打开渠道编辑弹窗。
- 控制台无应用错误。本次验收依据 DOM 和实际网络请求，不含像素级截图检查。

验收服务与浏览器标签在检查后关闭。

## 取舍与边界

- 列表保留 5 秒反馈；通过拆除正文降低额外响应与复制成本，未把轮询改为 30 秒。
- 250 毫秒只用于额度不足后的恢复探测重试。已经有健康当前成员的既有路由行为保持原样。
- 自动价格维护仍有 2 分钟上限；超出时保留已提交模型刷新，并明确展示维护提醒。此修复不新增价格补偿任务。
- 性能回归验证了唤醒次数的线性约束，未进行生产吞吐压测。
- 本次没有新增迁移；此前记录的 MySQL/PostgreSQL 实例迁移验收继续留在 R1。
