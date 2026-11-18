# 当前支持范围与验收状态

后续开发和发布正式支持 Windows amd64 原生程序与 Linux amd64 Docker / Compose 镜像，数据库限定为 SQLite。首次 Linux 开发产物应用验收已在 C02 完成，准确输入见下文；v0.1.4 候选与正式发布 run 的实际产物仍分别验收。具体门禁见 [发布 CI 合同](release-ci.md)。

此调整适用于后续版本。已发布的 [v0.1.2](releases/v0.1.2.md)、[v0.1.1](releases/v0.1.1.md) 和 [v0.1.0 支持矩阵](support-v0.1.0.md)保留各自当时的支持范围、产物和验收事实，不改写历史发布记录。

## 部署矩阵

| 部署组合 | 当前定位 | 验收边界 |
| --- | --- | --- |
| Windows amd64 原生程序 + SQLite | 正式支持 | 在 Windows 上验收安装、菜单启停、原生更新助手、真实旧版升级、持久化及备份恢复；实际登录自启仅在该功能相关改动或异常时验收 |
| Linux amd64 宿主 + Docker Engine / Linux amd64 镜像 + SQLite | 正式支持；首次开发产物应用验收已完成 | 验证安装、正常启停、数据卷权限、真实旧版升级、容器重建、持久化及备份恢复；同时覆盖文档提供的 Compose 入口 |
| WSL2 / WSL Containers | 预期兼容 | 不再单列正式支持组合、固定 WSLc 版本或设置发布必验项；Windows 目录挂载、文件权限、网络和自启动等集成问题按实际环境调查 |
| Linux amd64 原生程序 | 实验性 | 容器内程序的运行结果不能替代原生安装、进程替换、自更新及恢复验收 |
| Linux arm64 / arm/v7 / 386 原生程序及镜像 | 实验性 | 保留构建和材料校验；缺少对应设备的完整运行及升级恢复验收 |
| macOS amd64 / arm64；Android 四 ABI | 实验性 | 保留现有构建与对应源码，不由 Linux 容器验收扩大支持范围 |
| MySQL / PostgreSQL、外部数据库 | 正式支持范围外 | 数据库驱动、升级迁移、备份恢复和数据保护继续维护；遗留小时迁移 18 覆盖三种数据库，运行结论须分别验收，不能由 SQLite 验收推导通过 |
| 其他容器运行时、反向代理、入站 TLS 终止及其他部署组合 | 正式支持范围外 | 按实际组合补充验证，不能从 Docker Engine 的结果推导全部环境等价 |

Docker Engine 与 Compose 的实际版本、宿主发行版、内核、架构和挂载方式随每次验收记录。Linux 测试可以在云机或其他 Linux amd64 主机上执行，云商不是产品支持条件，也不要求用户安装特定代理工具。

## 当前验证状态与发布要求

截至 2026-11-16，C02 累计开发提交 [`9a7ed524bf763bc776ef291a0998aeb715700ad4`](https://github.com/Ink-lotus/octopus-roam/commit/9a7ed524bf763bc776ef291a0998aeb715700ad4) 已完成 Windows/Linux 原始完整 CI、额外 catalog race、三 workflow actionlint，以及 GitHub [首次完整 test](https://github.com/Ink-lotus/octopus-roam/actions/runs/43728302757)。Linux 应用验收于 2026-11-15 完成，Windows/原生 CLI 最终复验于 2026-11-16 完成。

| C02 开发证据 | 准确身份 / 结果 |
| --- | --- |
| Windows ZIP / 实际 EXE SHA-256 | `439499abde8875b71967025226701dcfd4deb3ace65f814f7cbeb030d5c74eef` / `d10a21fe4455d941effcc019d32aea16455693382468fd3ceb908d4b7459a9cd` |
| Linux 镜像 config digest | `sha256:30dbc472df969f7f799d5662f61498c211f6776e50d046ddb309b6f4020a81f2` |
| Linux 实际 image ID / image tar SHA-256 | `sha256:58ddac75f627d619f0495aede8ca011534d52cfb25021358d70d1bf01d3206b7` / `d1a929c4a2f28b9f518ab5be781333d0f538df5ac011bc18cb818c56835abb8e` |
| Windows 应用 | 91 项目录、9 组安装/真实 v0.1.2 升级/恢复、4 组菜单和 7 项更新；更新场景使用单独编译的回环 overlay，浏览器完成标记来自 API 回放 |
| Linux 应用 | 116 项目录、11 组安装/真实 v0.1.2 升级/正常启停/卷权限/同卷重建/恢复/Compose；Debian 13.7 x86_64、内核 6.18.60-instabox、Docker 29.8.2、Compose 5.6.0，Linux 文件系统 bind mount，应用 UID 10001/GID 1000 |
| 数据专项 | SQLite/MySQL/PostgreSQL 共 24 场景及 3×3 恢复、37 报告/305 检查点；不扩大正式数据库支持范围 |
| 原生客户端 | CLI 0.169.0 八场景，Sol/Astra 实际 Luna/low 审核、五类负面条件、重启与工具后续轮次；其他两个版本只验证快照取源 |
| 最终审计记录 SHA-256 | `C02-sse-final-audit-01.json`：`e1122c211bb83902e5a3b70dda26748b0a035f54056d7ac72a88dab8fb603eff`；原始报告及源码/产物映射保存在本机 C02 证据中 |

以上产物版本均为 `v0.0.0-ci`，证明首次开发验收已完成，不能改名当作 v0.1.4 正式版本产物。本版新 SHA、Notes、完整材料/候选及两平台实际产物结果独立记录；绿色 CI 不替代应用验收。历史 WSLc 结果保留原环境身份。

保留历史原因限制：B04 首次 Relay race 超时诱因、旧 helper 当时句柄持有者及 C02 首次进程记录占用者仍未完全查明；可复现的 helper 回滚缺陷与 SSE 长度缺陷已有修复，`desktop.WriteJSON` 未修改。新候选如复现异常须调查并补验。

后续版本按同一准确 commit 和实际交付产物留证：

1. Windows 使用本次 ZIP，Linux 使用本次 amd64 镜像，记录版本、完整 SHA、文件哈希或镜像摘要、环境、命令和退出码。
2. 验证新装、登录、主要 API 与流式功能、正常停止和重启。Windows 菜单及原生更新助手继续独立验收。实际退出并重新登录不作为每次发布的固定门禁，仅在登录自启功能相关改动或异常时执行；具体条件见 [发布门禁](release-ci.md)。
3. 使用真实前一正式版本和隔离数据副本验证升级、数据保留、空实例备份恢复，以及旧产物配合升级前完整副本的失败恢复。
4. Linux 使用 Linux 文件系统上的专用数据目录挂载到 `/app/data`，验证实际运行用户权限，以及正常停止后使用同一数据卷重建容器的数据一致性。Windows 共享目录的行为不属于这一结果的隐含保证。
5. 保留现有完整 CI、固定工具版本、锁文件、规定的 race 检查、来源与材料校验。构建成功或绿色 CI 不能代替实际应用验收。

首次 Linux 开发验收的准确输入已列于上表。每个后续版本仍须以自己的完整 SHA、Notes 和实际 ZIP/镜像完成上述门禁，不将旧产物结果转记为新产物通过。

## 安装、升级与发布材料

安装入口见 [中文 README](../README_zh.md)，数据备份、容器升级和失败恢复见 [升级说明](upgrade-r1.md)。部署固定已发布版本或已核对的镜像 digest；SQLite 使用完整数据目录，升级前正常停止并保留完整副本。

完整发布继续交付 11 个程序 ZIP、四架构镜像及对应源码、原始声明和哈希。提供构建产物不等于所有平台均正式支持。后续版本的 Release Notes、`INSTALL.md`、源码包 README 和双语 README 按本页与实际验收结果统一文案；具体输入要求见 [材料入口](release-materials.md)。已封存或公开的旧附件保持原样。
