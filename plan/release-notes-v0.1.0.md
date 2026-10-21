# Octopus Roam v0.1.0 发布说明草稿

状态：草稿，尚未发布。版本、Release、镜像和公开下载地址仍需第二轮授权与验收。

## 来源与版本

Octopus Roam 从 `bestruirui/octopus` v0.13.6 分叉，选择性吸收 v0.13.8 的厂商预设修正、AxonHub 版本、reasoning effort 展示及后续 User-Agent 修复。产品版本线独立从 `v0.1.0` 起算，不把上游编号当作降级或迁移版本。在线“最新上游”差异不在本说明中声明。

## 首发内容

- 支持 OpenAI Chat、OpenAI Responses 和 Anthropic 协议互转，以及渠道协议限制和模型授权。
- 提供手动/故障转移路由、全局分组默认策略、冷却重置、关键词/严格匹配、模型刷新和已有分组同步。
- 渠道自动刷新默认关闭；渠道可退出全局跟随；并发上限支持跨 Key、跨分组共享，流式请求直到结束才释放。
- 提供请求日志与 SSE、统计、Webhook 失败/恢复通知、隐私规则管理和预览。
- 提供当前版本口令加密备份、身份/格式校验及失败回滚保护；首发验收以新安装、当前版本恢复和重启持久化为准。
- Windows 提供 `octopus-roam.cmd` 管理菜单、自启动和正常停止支持；Linux/macOS 运行单文件程序。
- 版本页只检查 Octopus Roam 独立发布源。稳定版和 RC 按独立通道比较，更新归档必须匹配同版本 `SHA256SUMS`；容器按镜像标签升级。

## 已知限制与首发范围

- 当前草稿对应本地工作区和本地模拟发布源，不能证明真实 GitHub Release、公开镜像或线上自更新已经可用。
- MySQL/PostgreSQL 全新安装、读写、刷新、重启和当前版本备份恢复需要专用测试库自动验收；没有证据的引擎仍为待验。
- Docker 由用户按第一轮交付包人工验收；当前环境未确认有可用 Docker daemon 或离线基础镜像。
- 流式响应已经提交数据后发生上游错误，仍可能结束本次响应；阶段一保护跨请求重试和健康状态，不承诺拦截所有上游错误。
- BUG-001（个别渠道刷新模型时出现 Anthropic 401 告警）保持低优先级、暂不处理；现有记录说明它不影响用户确认的实际 Anthropic 调用。
- 更名前测试目录、旧数据库迁移、降级兼容、真实供应商请求、生图、WebSocket 和 companion 不属于首发验收范围。

## 安装提示（待发布时补齐）

稳定版发布后从 [Octopus Roam Releases](https://github.com/Ink-lotus/octopus-roam/releases) 下载对应平台归档，校验 `SHA256SUMS` 后完整解压。首个稳定镜像发布前，Compose 必须设置明确的 RC/稳定标签，不把 `latest` 当作可用安装示例。Windows 保留压缩包中的 `windows` 文件夹和 `octopus-roam.cmd`。
