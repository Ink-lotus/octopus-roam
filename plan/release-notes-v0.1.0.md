# Octopus Roam v0.1.0-rc.1 发布说明

这是首个独立候选版本的发布说明。当前处于发布准备阶段；候选公开后用于安装、公开分发渠道和在线升级验收。发布时 Release 使用 prerelease，镜像使用准确候选标签。

## 来源与版本

Octopus Roam 从 `bestruirui/octopus` v0.13.6 分叉，选择性吸收 v0.13.8 的厂商预设修正、AxonHub 版本、reasoning effort 展示及后续 User-Agent 修复。产品版本线独立从 `v0.1.0` 起算，不把上游编号当作降级或迁移版本。在线“最新上游”差异不在本说明中声明。

发布构建固定 Go 1.27.3，包含 Go 官方 GO-2026-6809 / CVE-2026-111943 的标准库修复。实际程序编译元数据在 package、seal、verify 三阶段检查；最终程序的 TLS 与产品验收另行记录。

## 首发内容

- 支持 OpenAI Chat、OpenAI Responses 和 Anthropic 协议互转，以及渠道协议限制和模型授权。
- 提供手动/故障转移路由、全局分组默认策略、冷却重置、关键词/严格匹配、模型刷新和已有分组同步。
- 渠道自动刷新默认关闭；渠道可退出全局跟随；并发上限支持跨 Key、跨分组共享，流式请求直到结束才释放。
- 提供请求日志与 SSE、统计、Webhook 失败/恢复通知、隐私规则管理和预览。
- 提供当前版本口令加密备份、身份/格式校验及失败回滚保护；首发验收以新安装、当前版本恢复和重启持久化为准。
- Windows 提供 `octopus-roam.cmd` 管理菜单、自启动和正常停止支持；Linux/macOS 运行单文件程序。
- 版本页只检查 Octopus Roam 独立发布源。稳定版和 RC 按独立通道比较，更新归档必须匹配同版本 `SHA256SUMS`；容器按镜像标签升级。
- MySQL 自定义 CA 配置与无查询参数 DSN 同时使用时保留字符集、时间解析和本地时区默认值；严格 TLS 校验继续生效。
- 更新操作提交页面确认的准确目标版本。空或无效目标返回 400；发布源目标变化返回 409，刷新页面数据后需再次点击。成功回执绑定接受的目标，完成状态以运行版本精确一致为准。

## 已有验证与候选验收边界

- Go 1.27.3 基线 `f857486093a185669fa0996451949563ade37e9e` 的 [Ubuntu push CI run 39917087653](https://github.com/Ink-lotus/octopus-roam/actions/runs/39917087653) 已通过 Go default/jsoniter、vet、定向 race、前端检查、发布合同和 actionlint。最终 RC SHA 另行冻结并取得自身普通 CI；本条基线记录不代表最终 11 目标或四架构镜像构建已完成。
- 历史本地候选 `v0.1.0-local.mysqlfix-20261018-03` 在真实 MySQL/PostgreSQL 各通过 35 项验收，覆盖严格 TLS、迁移、业务、行锁、当前备份恢复和 Windows 原始程序启停。本 RC 使用 Go 1.27.3 重建后，仍需补两库 TLS 正反例，以及最新 DSN/CA 修复的无查询参数 DSN、既有刷新记录启动及时间字段读取。
- 历史 Windows/SQLite 业务和 loopback 发布源更新链路已有运行证据；最新目标确认/400/409/回执已通过后端和前端 VM 合同检查。本 RC 的实际浏览器、完整产品替换/重启、公开下载及两个真实 RC 间升级继续验收。
- WSL Containers 中 Linux amd64/SQLite 的历史镜像已完成启动、持久化、备份恢复和容器更新保护；其他架构、外部数据库容器、Docker Engine/Compose、反向代理及真实镜像升级尚需按支持范围补验。
- 七个标准目标有历史交叉构建记录。Android 四 ABI/NDK、其他 OS/架构的产品实机、运行中 Linux/macOS 的更新与失败恢复分别登记，构建成功不等于实机验收。
- 应用和 Alpine 对应源码、原始声明、实际包映射及公开获取入口随本版本材料逐项核验；旧候选的源码和旧 amd64 OS ZIP 不作为本 RC 或四架构镜像的替代证据。

## 已知限制与首发范围

- RC 发布后的真实下载、GHCR 匿名拉取及 `rc.1 → rc.2` 在线升级属于后续验收；每个新增 RC 仍需独立审查和批准。
- 流式响应已经提交数据后发生上游错误，仍可能结束本次响应；阶段一保护跨请求重试和健康状态，不承诺拦截所有上游错误。
- BUG-001（个别渠道刷新模型时出现 Anthropic 401 告警）保持低优先级、暂不处理；现有记录说明它不影响用户确认的实际 Anthropic 调用。
- 更名前测试目录、旧数据库迁移、降级兼容、真实供应商请求、生图、WebSocket 和 companion 不属于首发验收范围。

## 安装与对应源码

本候选公开后，从 [v0.1.0-rc.1 Release](https://github.com/Ink-lotus/octopus-roam/releases/tag/v0.1.0-rc.1) 下载对应平台 ZIP 和 `SHA256SUMS`，校验后完整解压。Windows 保留压缩包中的 `windows` 文件夹和 `octopus-roam.cmd`。

容器使用 `ghcr.io/ink-lotus/octopus-roam:v0.1.0-rc.1`；Compose 设置 `OCTOPUS_ROAM_VERSION=v0.1.0-rc.1`。先保存现有数据和备份，再按候选安装/恢复流程运行。

同一 Release 提供 `octopus-roam-source-v0.1.0-rc.1.zip`、四份 Alpine 源码、原始声明、重建说明和校验和。网络部署者须向交互用户提供显著、免费且可实际下载的对应源码入口，按 `SOURCE-OFFER.md` 和 `source-offer.html` 配置并验证。
