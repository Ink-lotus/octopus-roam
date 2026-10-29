# Octopus Roam v0.1.0 发布说明

这是 Octopus Roam 的首个独立正式版本。Release 使用 stable，程序与镜像使用准确版本 `v0.1.0`；GitHub 最新正式版本和 GHCR `latest` 随本次稳定发布更新。部署建议固定版本或已核对的镜像 digest。

## 相对已验收 rc.2 的差异

- 已验收基线为 `v0.1.0-rc.2 / 1a8729a11b122888a683fd1bd9491ce38ceb4e18`。本版更新正式版本标识、安装/升级说明、支持范围文档和发布记录；业务代码、数据库迁移、依赖、工具链及构建脚本保持相同。
- 支持范围采用已确认的 Windows amd64/SQLite 与 WSLc 3.0.3.0/Linux amd64 镜像/SQLite；其余完整构建继续作为实验性产物交付。
- 正式版重新绑定自身完整 SHA、源码及材料，并重建全部 11 个程序和四架构镜像。规定 CI 和实际编译版本/产物检查独立执行；未变范围的业务验收按影响复用。

## 来源与版本

Octopus Roam 从 `bestruirui/octopus` v0.13.6 分叉，选择性吸收 v0.13.8 的厂商预设修正、AxonHub 版本、reasoning effort 展示及后续 User-Agent 修复。产品版本线独立从 `v0.1.0` 起算，不把上游编号当作降级或迁移版本。在线“最新上游”差异不在本说明中声明。

发布构建固定 Go 1.27.3，包含 Go 官方 GO-2026-6809 / CVE-2026-111943 的标准库修复。实际程序编译元数据在 package、seal、verify 三阶段检查；最终程序的 TLS 与产品验收另行记录。

## 首发内容

- 支持 OpenAI Chat、OpenAI Responses 和 Anthropic 协议互转，以及渠道协议限制和模型授权。
- 提供手动/故障转移路由、全局分组默认策略、冷却重置、关键词/严格匹配、模型刷新和已有分组同步。
- 渠道自动刷新默认关闭；渠道可退出全局跟随；并发上限支持跨 Key、跨分组共享，流式请求直到结束才释放。
- 提供请求日志与 SSE、统计、Webhook 失败/恢复通知、隐私规则管理和预览。
- 提供当前版本口令加密备份、身份/格式校验及失败回滚保护；首发验收以新安装、当前版本恢复和重启持久化为准。
- Windows 提供 `octopus-roam.cmd` 管理菜单、自启动和正常停止支持；Linux/macOS 的单文件构建按实验性交付。
- 版本页只检查 Octopus Roam 独立发布源。稳定版和 RC 按独立通道比较，更新归档必须匹配同版本 `SHA256SUMS`；容器按镜像标签升级。
- MySQL 自定义 CA 配置与无查询参数 DSN 同时使用时保留字符集、时间解析和本地时区默认值；严格 TLS 校验继续生效。
- 更新操作提交页面确认的准确目标版本。空或无效目标返回 400；发布源目标变化返回 409，刷新页面数据后需再次点击。成功回执绑定接受的目标，完成状态以运行版本精确一致为准。

## 首发支持范围

正式 `v0.1.0` 采用方案 A：Windows amd64 原生程序 + SQLite，以及 Windows / WSLc 3.0.3.0 / Linux amd64 镜像 + SQLite。其余程序和镜像继续完整构建并标为实验性；MySQL/PostgreSQL、Docker Engine/Compose 和其他部署组合为首发范围外配置。见[完整支持矩阵](https://github.com/Ink-lotus/octopus-roam/blob/v0.1.0/docs/support-v0.1.0.md)和[工作包 8 升级与恢复记录](https://github.com/Ink-lotus/octopus-roam/blob/v0.1.0/plan/release-step-08-result-20261029-02.md)。

## 已完成的候选验收与适用边界

- rc.1 的[实际发布 run 40199841094](https://github.com/Ink-lotus/octopus-roam/actions/runs/40199841094)完成 prepare/test/build/publish；11 个程序均使用 Go 1.27.3，四架构镜像及材料映射通过。
- rc.1 的公开 Release 共 27 个附件，匿名下载的大小、SHA-256 与发布摘要一致；应用/四架构 Alpine 对应源码可以公开下载，GHCR 四平台 manifest 及匿名 Linux amd64 拉取通过。
- rc.1 候选 Windows/SQLite 包完成业务冒烟、当前加密备份、空库恢复、正常重启及数据持久化。真实 MySQL/PostgreSQL 的 TLS 正反例、迁移 3–13、备份恢复和重启通过；MySQL 自定义 CA 加无查询参数 DSN 的既有刷新记录启动通过。
- rc.1 的实际浏览器已覆盖目标确认、400/409、回执及成功/失败/超时终态；此前替换和恢复测试使用 loopback 发布源。工作包 8 随后完成两个公开 RC 的真实在线升级、数据保留和原始 helper 自动恢复；首个页面请求的 502 与后续成功 API 重试分别留有记录。
- rc.1 的 WSL Containers 3.0.3.0 / Linux amd64 已覆盖公开镜像启动、数据挂载、正常停止、重启及同数据目录换容器。工作包 8 进一步完成公开 rc.1 → rc.2 换镜像升级、空库加密恢复、重启及旧镜像/升级前数据回退；外部数据库容器与反向代理属于首发范围外组合。
- 11 目标构建包含 Android 四 ABI/NDK r28c。Linux arm64/arm/386、macOS amd64/arm64、Android 四 ABI 尚无产品原生实机验收；交叉构建和静态材料检查不代替这些结果。正式版支持范围按上述方案 A 限定；这些构建保持实验性。

## 已知限制与首发范围

- rc.2 公开 Release 的 27 个附件 digest 与统一校验清单一致，8 个关键附件已实际下载；完整 11 目标/四镜像 artifact 由发布 job 重校验。Windows 与 WSLc 的公开升级、备份恢复和失败恢复通过；未声明支持的部署组合继续保留验收边界。
- 流式响应已经提交数据后发生上游错误，仍可能结束本次响应；阶段一保护跨请求重试和健康状态，不承诺拦截所有上游错误。
- BUG-001（个别渠道刷新模型时出现 Anthropic 401 告警）保持低优先级、暂不处理；现有记录说明它不影响用户确认的实际 Anthropic 调用。
- 更名前测试目录、旧数据库迁移、降级兼容、真实供应商请求、生图、WebSocket 和 companion 不属于首发验收范围。

## 安装与对应源码

从 [v0.1.0 Release](https://github.com/Ink-lotus/octopus-roam/releases/tag/v0.1.0) 下载对应平台 ZIP 和 `SHA256SUMS`，校验后完整解压。Windows 保留压缩包中的 `windows` 文件夹和 `octopus-roam.cmd`。

正式支持的 WSLc 组合使用 `ghcr.io/ink-lotus/octopus-roam:v0.1.0-amd64`；范围外的 Compose 参考部署设置 `OCTOPUS_ROAM_VERSION=v0.1.0`。先保存现有数据和备份，再按安装/恢复流程运行。

同一 Release 提供 [octopus-roam-source-v0.1.0.zip](https://github.com/Ink-lotus/octopus-roam/releases/download/v0.1.0/octopus-roam-source-v0.1.0.zip)、四份 Alpine 源码、原始声明、重建说明和校验和。网络部署者须向交互用户提供显著、免费且可实际下载的对应源码入口，按 `SOURCE-OFFER.md` 和 `source-offer.html` 配置并验证。
