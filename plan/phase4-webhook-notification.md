# 阶段 4 交接文档：通用 Webhook 通知

权威契约：[`v1.0plan.md`](v1.0plan.md)「阶段 4：通用 Webhook 通知」。

## 完成情况

- 新增 `webhook_endpoints` GORM 模型与认证 CRUD 接口：`GET /api/v1/webhook/list`、`POST /api/v1/webhook/create`、`POST /api/v1/webhook/update/:id`、`DELETE /api/v1/webhook/delete/:id` 和 `POST /api/v1/webhook/test/:id`。
- 端点默认方法为 POST、Content-Type 为 `application/json`、超时为 10 秒；超时可设为 1 至 120 秒。Header 以 JSON 对象持久化。
- 请求体使用 `text/template` 渲染，提供 `json` 函数。保存时解析模板并以样例事件执行；JSON Content-Type 会校验渲染结果。支持 Telegram、企业微信等自定义请求体。
- 新增手动连续失败阈值 10、故障转移失效成员阈值 3。手动告警按分组去重，成功请求后发送一次恢复；故障转移告警按冷却成员数去重，探测成功并低于阈值后发送一次恢复。
- 通知服务消费 Relay 请求和路由状态流，流缓冲关闭时会重连并读取当前快照。事件进入有界队列，由 4 个 worker 异步投递；每个端点独立超时，最多尝试 3 次。通知失败只记录脱敏错误，不改变 Relay 状态。测试发送走同一投递逻辑，不修改告警计数。
- 列表和写入响应会遮蔽 URL 用户信息、敏感查询参数与敏感 Header。更新时会保留未改变的遮蔽值。通知错误会清理分组渠道的凭据、代理地址和自定义 Header 值。
- 新增迁移版本 13，在设置表补入阈值默认值；数据库自动迁移会创建 Webhook 表。备份版本升到 6，导入与导出包含 Webhook Endpoint，同时保留版本 0、5 和 6 的导入支持。
- 设置页支持阈值调整、端点启停、创建、编辑、删除和测试；测试状态及失败原因会显示在列表中。简体中文、繁体中文和英文文案已同步。

## 验收结果

- `go test ./... -count=1`
- `go test ./internal/webhook -count=1`
- `go vet ./...`
- `go vet ./internal/webhook`
- `pnpm lint`
- `pnpm build --outDir <系统临时目录> --emptyOutDir false`（包含 TypeScript 检查）
- 三份 locale JSON 解析
- `git diff --check`

本轮未启动应用，也未对现有数据库执行迁移。迁移会在新版本首次启动时由既有数据库初始化流程应用。上游通知集成测试使用本地 `httptest` 服务。
