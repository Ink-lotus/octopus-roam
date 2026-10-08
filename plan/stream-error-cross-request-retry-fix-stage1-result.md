# 第一阶段实施记录

日期：2026-10-08。方案：[流式错误跨请求累计与重试保护修复方案](stream-error-cross-request-retry-fix.md)。

实际起点为 `test` 分支、`448907ec8fe581343a762bfcd3268017e28763bc`。开始时只有方案文档未跟踪，业务代码没有未提交改动。本次保留方案原文；代码与测试改动留在工作区。

## 行为与实现

- 新增 `internal/relay/health.go`。状态按分组成员保存于现有路由运行态，由 `routeMu` 保护；请求结束、请求历史裁剪和清空不影响它。共享快照、订阅注册和发布使用同一把锁，慢消费者关闭后可重新取得当前快照。
- 有效身份包含成员所属路由实例及上游配置摘要。仅保存摘要和递增版本，不在身份中保存凭据明文；摘要排除统计、并发上限及自动刷新偏好。配置替换或路由重置使旧尝试结果失效。纯通知及并发额度调整保留成员故障。
- 已发送的提交前失败、提交后协议错误、流读取失败及异常 EOF 均按尝试令牌结算一次。成员共享 `FailureStreak` 累计，截止时间取已有时间和 `now + RetryDelay(config, FailureStreak)` 的较晚值。
- `member_max_attempts` 新增跨请求含义：故障转移模式下，共享连续失败达到该值就进入冷却。故障未恢复时，后续探测失败继续累计并重新冷却。手动模式的 `manual_max_attempts` 仍只约束当前请求，`0` 仍表示不限额。
- 选路时检查共享等待，取得渠道容量后再次原子检查配置、冷却及恢复资格。等待不消耗尝试次数、不持有容量；实际允许时间取退避与冷却的较晚值，供等待遥测和 `Retry-After` 使用。
- 健康成员保持并发；故障成员恢复时只允许一个实际探测。首帧不确认恢复。取消、客户端写失败及未发送尝试归还资格，不增加上游失败。未发送错误保留固定等待，重新读取通知通道防止空转。
- 故障前已发出的成功请求不能清除之后的保护；同一未恢复周期内的并发失败分别累计。有效探测完整成功后推进周期，旧周期迟到失败只影响原请求历史。旧成员迟到失败延长自己的保护时，不重置正在使用的备用成员预算。
- `state.go` 以轮次身份对请求错误计数去重。不同轮次即使错误文案相同也分别计数，请求内成功不抹去已有错误。
- `upstream.go` 在转换前观察原始上游协议终态。客户端转换后的结束事件不足以单独确认上游成功；缺少合法终态的 EOF 记为失败。合法空输出终态可成功。用量聚合失败没有一律转换成上游失败。
- `webhook/service.go` 消费 Relay 共享健康快照。手动告警按共享失败数和故障周期去重；恢复通知要求有效成功使周期推进。`RetryCount` 保留关联请求的重试含义，不用共享失败数推算。
- 故障转移通知继续按未到期冷却成员数触发，并记录完整成功时的冷却数量。恢复要求告警之后的完整成功、成功当时及当前的冷却数量均低于阈值；冷却到期或开始探测不会借用先前仍超阈值时的成功冒充恢复。
- 错误通过现有请求 `privacy.Redactor` 写入共享快照，通知继续执行现有配置脱敏逻辑，遵循既有隐私开关。手动健康通知不会向路由 UI 发布成员 ID 为零的空路由状态。

## 验证覆盖

新增 `internal/relay/health_test.go`，并补充 Relay 流式、隐私、故障转移及 Webhook 测试。关键证据：

| 行为 | 测试 |
| --- | --- |
| 多个独立 Responses 请求累计故障、遵守截止时间、各有独立预算、空输出完整恢复 | `TestResponsesCrossRequestBackoffAndRecovery` |
| 首事件错误仍可重试，同请求两轮失败分别计数 | `TestIdenticalErrorsInDifferentRoundsCountSeparately`、`TestRequestFailureDeduplicatesByRound` |
| 故障后只有一个探测，首帧不解除保护，等待和读取取消释放容量 | `TestHealthSingleProbeHoldsThroughFirstFrame` |
| 健康请求并发、迟到成功不清故障、恢复后旧失败不重新打开故障 | `TestHealthEpochsAndExclusiveRecovery`、`TestHTTPResultsCannotCrossRecoveryEpoch` |
| 已排队请求发送前重查故障截止时间 | `TestCapacityQueuedRequestRechecksSharedDeadline` |
| 并发故障累计、指数封顶、清空历史和纯通知保留故障 | `TestHealthConcurrentFailuresAndReset` |
| 配置替换、分组删除、容量变化、慢订阅重连 | `TestHealthIdentityAndSlowSubscriber`，以及既有路由和 server handler 回归 |
| 等待使用退避与冷却的较晚值，503 的重试提示按跨请求指数增长 | `TestHealthWaitUsesLaterCooldownAndBackoff`、`TestManualRetryAfterUsesSharedExponentialStreak` |
| Chat、Responses、Anthropic 透传及 Chat→Responses 转换的成功、异常 EOF、错误 | `TestStreamEOFAndConvertedErrorsAffectSharedHealth` |
| 客户端写失败不计上游故障，已完整取得的非流式成功仍可恢复 | `TestClientWriteFailureDoesNotCountAsUpstreamFailure` |
| 提交后不重放，下一次请求使用备用成员 | `TestStreamingDoesNotFailoverAfterFirstEventCommit` |
| 真实 HTTP 跨请求失败告警、完整成功恢复、清空请求历史不影响通知 | `TestIndependentHTTPFailuresNotifyAndRecoverFromSharedHealth` |
| 真实提交后错误推动故障转移告警，恢复不由冷却到期冒充 | `TestCommittedHTTPFailuresDriveFailoverNotifications` |
| 快照重复消费、新周期和配置身份更换 | `TestHealthNotificationReconnectAndIdentityChange` |
| 错误事件和共享快照脱敏 | `TestCommittedStreamErrorIsRedacted` |

## 检查结果

| 命令 | 最终结果 |
| --- | --- |
| `go test ./internal/relay ./internal/webhook ./internal/server/handlers -count=1 -timeout=180s` | 通过，三个包分别耗时 47.179s、5.305s、2.740s |
| `go test -race ./internal/relay ./internal/webhook -count=1 -timeout=180s` | 通过，两个包分别耗时 51.052s、7.014s |
| `go vet ./internal/relay ./internal/webhook ./internal/server/handlers` | 通过 |
| `git diff --check` | 通过 |

额外执行 `go test ./... -count=1 -timeout=180s`，除 `internal/server/resp` 外均通过。失败项为已有的 `TestErrorRedactsCredentialEchoes`：测试没有启用默认关闭的诊断隐私开关，却要求响应自动脱敏。该测试、`resp.go` 和 `privacy/redact.go` 的工作区 Git blob 哈希均与起点提交一致；该包的依赖不包含 Relay 或 Webhook。本次没有修改这项无关的既有行为或测试。

默认沙箱最初拒绝读取现有 Go 构建缓存，后续通过工具权限审核运行检查；没有安装依赖、修改环境配置或使用真实上游。Windows 现有工具链可以执行 race 检查。

## 保留边界

第二阶段仍未实施，没有新增前导事件缓存。当前 HTTP 响应一旦提交，遇到错误仍结束响应；共享状态限制下一次上游发送，不能阻止外部客户端创建新的入站请求。

状态仅在单进程内保存，重启后清空。沿用现有取消和超时能力，没有新增长流空闲/总时限。协议矩阵验证了上述透传与 Chat→Responses 转换路径，没有宣称覆盖所有厂商扩展事件及全部协议转换组合。

没有依赖变更、数据库迁移、环境或 CI 修改、Git 提交或远程仓库操作。
