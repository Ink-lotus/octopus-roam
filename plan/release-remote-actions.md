# v0.1.0 第二轮远端动作草案

状态：仅草案，第一轮不执行。当前用户只授权本地 L1–L6；GitHub Actions、Git 远端、Release、镜像仓库和真实在线升级仍需逐项明确授权。

## 进入条件

- 用户审查 `plan/release-local-result.md`、`plan/release-workflow-proposal.md` 和 `build/release-local/final-20261010-221847/`。
- 用户确认目标仓库仍为 `Ink-lotus/octopus-roam`，并明确本轮要启用的发布渠道（GHCR；Docker Hub 是否启用需单独决定）。
- 用户决定是否补齐 Android/macOS/ARM 构建、许可证报告、Docker 构建环境和 MySQL/PostgreSQL 自动验收。
- 所有远端写操作仍需要针对具体目标、命令和影响的明确授权；已有本地地址、凭据或本计划不构成授权。

## R0：只读核实（需要远端访问授权）

核对仓库当前默认分支、分支保护、已有标签/Release、Actions 启用状态、GHCR 包可见性和发布权限。若启用 Docker Hub，再核对账号、仓库和 secrets 是否存在。只记录状态，不创建、删除或修改对象；输出不得包含凭据值。

## R1：应用 CI 提案（需要单独 CI 文件修改授权）

目标文件：`.github/workflows/build.yaml`、`.github/workflows/release.yaml`。影响是改变测试门禁、版本注入、归档上传、tag/Release 创建顺序和镜像登录行为。

拟执行顺序：

1. 在本地审查提案与实际 workflow 差异，确认 `prepare → test → build → publish` 的依赖关系。
2. 获得授权后修改两个 workflow，运行可用的本地 YAML/脚本静态检查；本机没有 `actionlint`，不能声称线上通过。
3. 先以不发布的手动触发或分支验证测试门禁和 artifact 路径；线上 Actions 验证仍需用户授权。

不在第一轮执行，也不在没有授权时直接改写 `.github/workflows`。

## R2：main 集成与首次推送（需要 Git 远端状态授权）

审查工作树和提交边界后，准备中文 commit summary 草稿。若用户批准，再按指定顺序创建/更新 `main`、推送提交并核对首次推送不会误触发发布。分支切换、commit、push、默认分支切换和分支保护均是独立的远端状态变更。

## R3：RC 构建与发布（需要发布授权）

使用已审查的版本、commit 和 Release Notes，执行测试门禁、归档构建、SHA-256 校验、候选 tag、prerelease Release 和 GHCR 候选标签。候选版不更新 `latest`；Docker Hub 只有在用户明确启用且对应 secret/仓库核实后才执行。遇到已有 tag/Release/同名资产时先暂停核实，不覆盖或强推。

## R4：真实远端产物与在线升级验收（需要在线升级授权）

至少准备两个独立版本，按 `rc.1 → rc.2` 验证版本查询、归档下载、同版本校验、失败恢复、Windows/Unix 替换和重启。真实 GitHub API、Release 资产、公开下载和镜像拉取都属于远端操作；第一轮的本地 HTTP 夹具不能替代这项证据。

## R5：正式 v0.1.0（需要正式发布授权）

只有在候选版本、声明支持的数据库、Docker 人工验收、公开产物一致性和真实在线升级均有证据，且无未解决发布阻塞问题时，才准备正式 tag、Release、稳定镜像标签和发布公告。正式发布后补做下载/拉取和首次启动冒烟，并记录实际可用版本。

## 云数据库例外

MySQL/PostgreSQL 可以在第一轮作为单独例外，但需要用户先提供仓库外 profile 的别名/路径并授权具体测试范围。执行前必须准备按别名读取 profile 的入口、合成数据清理方案和空恢复库方案；不自动创建/删除数据库、清库、改密、调整云网络或处理其他远程资源。密码、完整 DSN、主机、账号、库名和原始错误不得进入工具输出、Git、日志或交付包。详见 `plan/cloud-database-test-handoff.md`。

## Docker 人工验收交接

用户在有 Docker 的云主机选择 `linux/amd64` 或实际 CPU 架构，使用本地构建目录或实际镜像 tar，按 C1–C9 检查启动、版本、页面/API、SQLite 挂载、重启持久化、备份/恢复、日志、网络暴露和停止行为。用户回传产物哈希或镜像 ID、架构、Docker/Compose 版本、数据库类型和脱敏结果；执行会话不把 Docker 人工结果写成自动通过。

在取得对应授权前，本文件只作为动作清单，不执行任何远端命令。
