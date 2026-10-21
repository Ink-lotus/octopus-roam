# Octopus Roam v0.1.0 本地基线与上游功能对照

状态：第一轮本地整理。本文只依据仓库历史、现有源码和已提交验收记录，不代表已经在线核对“最新上游”。在线差异、远端分支和当前 Release 状态留到第二轮。

## 基线

| 分类 | 本地依据 | 结论 |
| --- | --- | --- |
| fork 起点 | `plan/upstream-sync-v0.13.8-result.md`、`main.go` 注释、`git log` | 从 `bestruirui/octopus` v0.13.6 分叉；产品版本线从 `v0.1.0` 独立起算 |
| 选择性同步 | `plan/upstream-sync-v0.13.8-result.md`、提交 `597b9a9`、`c3c8710`、`a208910` | 吸收 v0.13.8 的厂商预设修正、AxonHub 版本和 reasoning effort/日志展示；UA 修复在 `f9a20c8`、`1654a38` |
| 明确排除 | 同一同步记录 | 未带入上游重试错误历史改造和 LogMetrics 布局重排；没有声称追平最新上游 |
| 首发边界 | `plan/release-plan-v0.1.0.md`、`plan/r1-result.md` | 全新安装、当前版本备份恢复和重启持久化；旧版本迁移、降级兼容、远端发布与真实在线升级另行验收 |

## 功能对照

| 能力 | 上游基线/同步 | Octopus Roam 当前实现与证据 |
| --- | --- | --- |
| 协议和渠道 | 上游渠道/协议转换基线 | OpenAI Chat、OpenAI Responses、Anthropic 互转；协议路径和 `allowed_protocols` 由渠道控制，见 `internal/model/channel.go`、`internal/relay/handler.go` |
| 路由策略 | 上游默认故障转移路径 | `manual`/`failover` 分离、全局默认策略、手动切换和冷却重置，见 `internal/relay/route_actions.go`、`plan/failover-manual-switch-and-cooldown-reset-result.md` |
| 模型匹配 | 上游关键词路径 | 关键词和严格匹配；严格匹配不会把后缀变体加入分组，见 `internal/model/group.go`、`internal/op/channel_refresh.go` |
| 模型刷新 | 上游刷新接口 | 全部 Key 成功后原子提交；成功空列表清理模型/失效成员；刷新失败保留旧状态，并记录逐 Key 诊断，见 `internal/op/channel_refresh.go`、`internal/server/handlers/channel_refresh_test.go` |
| 分组同步 | 上游分组模型 | 可选择自动追加已有分组成员；不会自动创建分组；禁用渠道筛选只影响展示，见 `internal/op/channel_refresh.go` 与前端渠道列表 |
| 自动化与并发 | 上游没有本项目新增的全局约束 | 全局自动刷新默认关闭、渠道可退出跟随；`max_concurrency=0` 表示不限，正数跨 Key/分组共享，流式请求直到结束才释放，见 `internal/task/init.go`、`internal/relay/channel_concurrency.go`、`plan/m3-automation-result.md` |
| 重试和健康 | 同步时保留本项目阶段 2–5 的历史结构 | 跨请求重试和健康状态受到保护；流式响应提交后发生错误仍可能结束响应，第一阶段不承诺拦截所有上游错误，见 `plan/stream-error-cross-request-retry-fix-stage1-result.md` |
| 日志/SSE | 上游日志基线 | 请求状态、置顶/筛选、取消、运行时和日志 SSE；已吸收 reasoning effort 展示，见 `internal/server/handlers/log.go`、`web/src/components/modules/log/Item.tsx` |
| Webhook | 上游基础能力 | 自定义 URL、方法、Content-Type、认证头、模板和超时；失败/恢复通知有阈值与去重，见 `internal/model/webhook.go`、`internal/webhook/service.go` |
| 隐私规则 | 本项目新增 | 内置字段、正则和自定义规则的管理、预览、执行与诊断；敏感内容在日志/错误响应中脱敏，见 `internal/privacy/`、`plan/privacy-rule-management-implementation.md` |
| 备份 | 上游无本产品身份隔离 | 当前格式含 `product: octopus-roam`、内容版本 `1000`；加密容器版本 `1`；无标识上游 `0/5` 可审计导入，`6` 和外来身份拒绝，见 `internal/op/backup.go`、`docs/backup-encryption-design.md` |
| Windows | 上游启动方式基础 | `octopus-roam.cmd` 菜单、启动/停止、自启动、特殊路径和本目录锁；支持文件随完整包更新，见 `scripts/windows/` 与 `948de76` |
| 渠道模型列表刷新 | 上游渠道模型接口 | 模型获取访问各渠道配置的模型列表接口；全部 Key 成功后原子提交、空列表清理和失败保留旧状态，见 `internal/op/channel_refresh.go`。提交后的维护回调会使用本地价格预设补齐新增模型价格并清理无用价格，见 `internal/server/handlers/channel.go`；这不等于联网更新价格预设 |
| 模型价格更新 | 本项目独立维护功能 | 使用本地价格预设和价格更新任务，为已知模型更新价格；启动任务可能访问 `models.dev`，本地验收通过 `OCTOPUS_ROAM_SKIP_STARTUP_PRICE_UPDATE=1` 隔离，见发布计划 L0 |
| 程序版本更新 | 独立发布源 | 只检查 Octopus Roam 自己的发布源和版本归档；不访问渠道模型接口，也不使用价格预设。A2 的本地 HTTP 更新链证据见 `build/release-local/a2-20261012-193213-9b75afef/checks/logs/update-local-release.log` |

## 本轮更新行为

`internal/update/` 只构造固定的 `Ink-lotus/octopus-roam` API/Release 地址，不接受请求参数提供下载地址。稳定版只向更高稳定版升级；RC 可向更高 RC 或对应稳定版升级；`dev`、`unknown` 和 `v0.1.0-local.*` 只显示开发状态。下载同一 tag 的平台 zip 和 `SHA256SUMS`，在临时目录校验状态、大小、路径、压缩包内容和 SHA-256 后才替换。设置页保留手动 Release 链接，容器通过镜像更新。

本地测试通过 `internal/update/release_test.go` 的 `httptest` 夹具完成；没有访问真实 GitHub API、Release 资产或供应商服务。真实远端源、RC 到稳定版的在线重启、公开镜像和下载验收仍是第二轮待办。
