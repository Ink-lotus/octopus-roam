# 阶段 2 交接文档：故障转移状态机

权威契约：[`v1.0plan.md`](v1.0plan.md) §三故障转移、§阶段 2。

## 完成情况

故障转移选路现在固定使用健康的当前成员。当前成员耗尽尝试次数后进入冷却，路由从成员优先级列表开头重新扫描；冷却中的成员会跳过。全部可用成员都在冷却时，请求等待最早冷却截止时间，或者等待成员可用性和路由状态变化。请求取消会立即终止等待。

恢复探测由全局唯一令牌保护，同一分组同一时刻只允许一个请求占用探测。探测持有者在其尝试预算内继续使用原成员；取消时释放占用。成功和失败回报都要匹配路由代数，迟到结果不能覆盖已切换或已重置的状态。成员不可用、渠道或分组配置更新、分组删除和备份导入都会唤醒相关等待请求。

`member_affinity_seconds` 仍由后端读取以兼容旧配置，但不再参与选路或状态重置判定。运行状态 API 保留 `affinity_until` 字段并恒为零；前端已移除亲和倒计时，展示当前成员正常、成员冷却和等待恢复状态。

## 代码变更

- `internal/relay/route.go`：引入带等待条件、冷却截止时间、路由代数和探测令牌的内部选路结果；增加路由配置指纹、成员状态通知、等待通道、路由重置与旧快照拦截。
- `internal/relay/handler.go`、`internal/relay/state.go`：接入可取消的冷却与状态变更等待；在同一恢复探测中保留请求级尝试预算；请求退出时释放探测占用。
- `internal/op/group.go`：读取完整成员集合并统一补齐 Relay 配置默认值，让路由层可以判断成员不可用状态和配置版本。
- `internal/server/handlers/group.go`、`channel.go`、`setting.go`：分组模式、成员、Relay 策略变化时重置路由；渠道启停、更新或删除后通知受影响分组，并向所有已连接的 `/api/v1/group/events` 订阅者发布含 `items/available` 与 `runtime` 的完整 `changed` 快照；备份导入后清空旧运行状态并登记新分组配置。
- `web/src/api/group.ts`、分组组件与三份 locale：移除亲和倒计时展示，增加正常和等待恢复状态。
- 新增路由状态、故障转移和处理器测试，覆盖当前成员固定、优先级扫描、最早冷却等待与取消、探测令牌隔离、旧结果隔离、配置重置、恢复探测预算，以及渠道启停/更新/删除后两个 SSE 订阅者收到一致的分组成员与路由状态快照。

没有数据库结构、备份格式、依赖或锁文件变更。

## 验收结果

以下检查均通过：

- `go test ./internal/relay ./internal/op ./internal/server/handlers -count=1`
- `go test ./... -count=1`
- `go vet ./...`
- `pnpm lint`
- `pnpm build --outDir <系统临时目录> --emptyOutDir false`（包含 TypeScript 检查）
- `git diff --check`
- 三份 locale JSON 解析及路由状态文案存在性检查

`go test -race ./internal/relay` 未运行：当前 `CGO_ENABLED=0`，Go 报告 `-race requires cgo`。本轮 Go 测试使用系统临时目录下的独立 `GOCACHE`，前端构建输出也写入系统临时目录。

## 后续边界

本轮只完成阶段 2。阶段 3 的请求运行状态与错误可见性、Webhook、companion、告警通知等工作仍未开始。真实上游网络行为没有在本轮验证；转发测试使用本地测试上游。
