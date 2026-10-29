# 工作包 8：rc.2 发布与公开回读结果

日期：2026-10-29。工作目录：`D:\tool\AI\octopus-roam`。目标仓库：`Ink-lotus/octopus-roam`。

## 当前结论

rc.2 的本地版本提交、材料、普通 CI、完整构建、四架构镜像、发布 workflow、公开 Release、公开附件和匿名 GHCR 身份回读均已完成。本报告将工作包 8 暂停在公开产物回读之后；工作包 8 尚未整体完成，不能据此进入正式 `v0.1.0` 发布。

已发布候选：`v0.1.0-rc.2`，完整提交 `1a8729a11b122888a683fd1bd9491ce38ceb4e18`。

## 已完成

| 项目 | 结果和证据 |
| --- | --- |
| 本地版本提交 | 本地 `main` 上的五文件提交 `1a8729a11b122888a683fd1bd9491ce38ceb4e18`，远端 `main` 已指向同一 SHA；提交范围为 `main.go`、`README.md`、`README_zh.md`、`docs/upgrade-r1.md`、`plan/release-notes-v0.1.0.md`。工作区中原有计划差异和临时文件保留，未纳入该提交。 |
| 材料 | `materials.json` SHA-256：`f976bcfed33c6b7467d53156d26026c5c9b455d3b6062ef15dbc753deff551ca`；bundle SHA-256：`d83b482e6ad976acf313a623be88dcfea1f264775b660d344939c2f7ae15851a`，大小 `588580328` bytes。生产、audit、pack、unpack、round-trip audit 全部通过，见 [production result](../build/release-local/step-08-rc2-20261028-01/evidence/production-01/result.json)。 |
| 材料入口 | draft inbox 资产已上传并回读，大小和 digest 与本地 bundle 一致；材料 workflow `40793792493` 成功。 |
| 完整发布 workflow | `40794165534` attempt 1 成功；`prepare`、普通 CI `test`、完整 11 目标构建及四架构离线镜像构建、`publish` 全部成功。publish job 的 artifact 重校验、tag、镜像推送、Release 创建均通过。 |
| GitHub Release | `v0.1.0-rc.2` tag 指向该 SHA，Release ID `427349579`，`draft=false`、`prerelease=true`，27 个附件均为 `uploaded`。 |
| 公开附件 | GitHub API 的 27 个附件 digest 与公开 `SHA256SUMS` 的 26 条记录全部一致。匿名下载并逐字节核对 8 个关键附件（`SHA256SUMS`、`BUILD.json`、`MATERIALS.json`、`IMAGE-MANIFEST.json`、`SOURCE-OFFER.md`、源码 ZIP、Windows amd64 ZIP、Linux amd64 ZIP）通过；源码 ZIP 内部 `47934` 个文件及其清单校验通过；两个公开二进制的 `go1.27.3`、VCS SHA、`vcs.modified=false` 和 build ID 通过。证据见 [downloaded verification](../build/release-local/step-08-rc2-20261028-01/evidence/public-rc2-02/downloaded-verification.json) 和 [release checks](../build/release-local/step-08-rc2-20261028-01/evidence/public-rc2-02/release-checks.json)。 |
| 公开源码入口 | 匿名下载的 `SOURCE-OFFER.md` 指向当前公开源码 ZIP，公开材料 SHA 与已审查材料一致。 |
| GHCR | 匿名 pull token 读取 `v0.1.0-rc.2` 组合 manifest 和 `-amd64`、`-arm64`、`-arm`、`-386` 四个架构 tag 均成功；四个 config digest 与 `IMAGE-MANIFEST.json` 的封存 image ID 一致，ARM variant 为 `v7`。rc.1 manifest 未变。证据见 [registry and publication](../build/release-local/step-08-rc2-20261028-01/evidence/public-rc2-02/registry-and-publication.json)。 |
| RC 通道隔离 | GitHub stable `latest` Release 和 GHCR `latest` 均为 HTTP 404；rc.2 没有污染稳定通道。 |

## 暂停时仍未完成的门禁

这些项目是工作包 8 的剩余实质内容，合计不属于一次短收尾：

1. 在隔离 Windows 实例上使用公开 `rc.1` 与 `rc.2`，实测版本查询、目标确认、真实 HTTPS 下载/校验、替换、重启、运行版本和 SQLite 数据保留。
2. 在同一隔离范围执行失败下载、校验、替换或重启场景的恢复检查；不得修改公开 RC 资产。
3. 在已声明的 WSL Containers 3.0.3.0/Linux amd64 范围，公开拉取 rc.2 镜像，完成换容器、挂载数据、备份/恢复、重启和版本确认。
4. 按需要补做通道规则、前后端版本一致性和缓存更新检查，并确认正式支持范围方案 A 是否获用户最终批准。方案 A 文档仍是待审核提案，尚未修改公开支持声明或 CI 合同。
5. 当前证据只下载了 8 个公开附件；其余附件已通过 GitHub digest 与 `SHA256SUMS` 核对，未逐个再次下载。完整 2.76 GB Actions artifact 已由 runner 的 publish job 重校验，但本地没有再次下载该 artifact 执行完整 `verify`。

工作包 8 的交接要求还明确：在真实 rc.1 → rc.2 升级、失败恢复、保留支持范围的容器升级和正式支持文档收口前，不能判断 R4 完成，也不能准备正式 `v0.1.0` 发布。

## 接续入口

- 发布 workflow：[40794165534](https://github.com/Ink-lotus/octopus-roam/actions/runs/40794165534)
- 公开 Release：[v0.1.0-rc.2](https://github.com/Ink-lotus/octopus-roam/releases/tag/v0.1.0-rc.2)
- 公开回读证据：[downloaded-verification.json](../build/release-local/step-08-rc2-20261028-01/evidence/public-rc2-02/downloaded-verification.json)、[registry-and-publication.json](../build/release-local/step-08-rc2-20261028-01/evidence/public-rc2-02/registry-and-publication.json)
- 公开回读使用的下载目录：[public-rc2-01/downloads](../build/release-local/step-08-rc2-20261028-01/evidence/public-rc2-01/downloads)
- 已审查材料输入：[inputs.json](../build/release-local/step-08-rc2-20261028-01/inputs-rc2-02/inputs.json)

本报告创建后不执行 commit、push、tag、Release、镜像写入、数据库操作或产品启动。
