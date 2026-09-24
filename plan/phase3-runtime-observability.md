# 阶段 3 交接文档：运行时状态和错误可见性

权威契约：[`v1.0plan.md`](v1.0plan.md)「阶段 3：运行时状态和错误可见性」。

## 完成情况

- `RequestState` 增加请求遥测字段，并随现有日志 SSE 发布；既有事件名和字段保持不变。
- `retry_count` 是当前成员已用的重试次数，`attempt` 是当前成员已发起的上游尝试数（含首次），`attempt_limit` 是总尝试上限；`attempt_limit=0` 表示不限次数。成员或路由恢复代数切换后，成员级尝试计数重置。
- `error_count` 累计请求中记录的渠道/上游错误；`last_error` 保留最近错误；`next_retry_at` 为 Unix 毫秒时间戳，无确定截止时间时为 0。`channel_status` 仅使用 `waiting`、`normal`、`error`。
- 新增聚合结构 `relay.GroupRuntime`、认证快照接口 `GET /api/v1/runtime` 和认证 SSE 接口 `GET /api/v1/runtime/stream`。SSE 事件名为 `runtime`，初始事件和更新事件都发送完整快照，快照按分组 ID 升序。
- 同一分组有多个请求时，活跃请求优先；相同活跃状态按较大的请求 ID 选择。没有活跃请求时保留该分组最新的终态请求。快照仅包含进程内请求记录当前保留的分组；从未收到请求或记录已清理的分组不出现在结果中。
- 最终上游失败返回 HTTP 503 和协议对应的 `upstream_error` JSON，并携带按当前重试退避向上取整的 `Retry-After` 秒数。输入格式错误仍返回 HTTP 400 和 `invalid_request_error`。
- 流式响应首个有效事件提交后发生上游流内错误时，请求直接终结，不再切换成员；新增回归测试验证第二成员未收到请求。
- 日志卡片显示尝试数/上限、已重试次数、错误次数、请求与渠道状态和重试倒计时；详情保留最近错误。三份 locale 已同步，旧后端缺少新增字段时仍可显示。

Webhook 与 companion 尚未作为消费者实现；本阶段提供了后续可复用的 Relay 聚合结构和 HTTP/SSE 数据源。

## 验收结果

- `go test ./... -count=1`
- `go vet ./...`
- `pnpm lint`
- `pnpm build --outDir <系统临时目录> --emptyOutDir false`（包含 TypeScript 检查）
- 三份 locale JSON 解析
- `git diff --check`

以上检查均通过。前端构建输出写入系统临时目录，没有覆盖 `static/out`。
