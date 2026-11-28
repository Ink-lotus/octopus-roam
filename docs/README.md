# 项目文档

公开参考文档统一放在随 Git 交付的 `reference/`，按用途导航：

| 分类 | 文档 |
| --- | --- |
| 部署与升级 | [本地部署](reference/local-deployment.md)、[升级与恢复](reference/upgrade-r1.md)、[支持矩阵](reference/support.md) |
| 备份与恢复 | [备份完整性](reference/backup-integrity.md)、[小时统计恢复](reference/hourly-statistics-recovery.md) |
| 功能说明 | [Codex 模型目录](reference/codex-catalog.md)、[Responses 历史清理](reference/responses-history-cleanup.md) |
| 发布维护 | [发布合同](reference/release-ci.md)、[材料入口](reference/release-materials.md)、[公开版本说明](releases/) |
| 历史参考 | [R1 加密设计](reference/backup-encryption-design.md)、[v0.1.0 支持记录](reference/support-v0.1.0.md)、[旧 Bug 链接兼容页](known-bugs.md) |

历史参考保留当时的设计、支持范围和验收事实；当前规则以对应现行文档为准。`README.md` 与 `known-bugs.md` 保留在 `docs/` 根目录，`releases/` 继续保存公开发布说明。

## 本地任务资料

四类任务目录全部仅本地保存并由 Git 忽略，不随源码分发：

| 目录 | 用途 |
| --- | --- |
| `docs/known-bugs/` | 已知问题的调查、修复方案与结果 |
| `docs/future-features/` | 未排期候选与选中后的独立功能任务 |
| `docs/release-plans/` | 当前选定版本及已发布版本的简短结果 |
| `docs/misc-changes/` | 文档、维护和其他改进任务 |

各分类的 `README.md` 只列 ID、标题、状态和任务相对链接。每个独立任务一个目录；Bug 保留稳定编号，功能保留 F 编号，版本用 `vX.Y.Z`，维护任务用 `YYYY-MM-DD-short-slug`。短候选可先留在总表，出现独立调查或方案后再建立任务目录。

同一任务跨会话持续更新原 `README.md`，记录目标、范围、当前方案、验收、结果与下一步；只在内容确有需要时拆出专题文档。不为重试创建新任务，不并行维护多份 CURRENT、STATE、交接稿或最终稿。采用候选、待处理、进行中、受阻、已完成、已取消等实际状态；只有完成约定验收才能写已完成。选入版本时由版本计划链接任务正文；完成后保留范围、结果、准确提交和必要证据，移出活动列表。旧授权和历史待办不自动生效。

本机各 worktree 共用主工作目录中的一套资料，可通过 `git worktree list --porcelain` 定位主工作目录；本机位置为 `D:/tool/AI/octopus-roam`。先读本页、本地版本索引和用户指定任务，不把其他 worktree 的旧交付稿作为第二份主计划。跨机器缺少本地资料时说明缺失；明确任务、准确 SHA 和验收入口足够时继续，不从旧档案推导任务。

## 工作材料

`build/<任务>/` 存放构建、下载、解包和临时测试数据；`evidence/<任务>/` 存放必要原始日志、报告、最小复现及清理账本。本机连接工具位于忽略的 `tools/local/`，凭据位置保持由工具管理。不要在仓库根新增 `.tmp*` 任务目录，也不要把产物放进任务正文目录。

最近一个已完成正式发布版本保留一套必要交付、应用及系统源码、正式镜像、许可/重建说明、哈希和最终验收证据。正式材料位于 Git 忽略的 `artifacts/releases/<version>/`，验收证据位于 `evidence/releases/<version>/`。最新版本身份以本地版本索引、发布记录、准确提交和实际产物核对；目录名称和修改时间不能替代验证。可再生缓存、重复包及最新版无用中间现场同样进入清理清单，不整套搬入长期归档。

新版本正式发布、公开交付核验及规定验收通过后，先封存并核验新保留集，再清理旧版大体积材料。源码、Git、在用 worktree、真实运行数据、连接凭据、共享依赖的一套安装及当前任务资料继续保护；新版本未完成时保留上一正式版本与必要任务现场。四类本地任务资料纳入现有本机备份安排，不自动安装备份软件或定时任务。

`build/`、`evidence/`、`artifacts/` 根是长期入口，清空后也保留。删除前逐项明确路径、用途、恢复来源、占用、链接与授权，不追随链接、不改权限、不把未读空间算作可回收。旧 worktree 由 Git 正常移除。迁移和删除后更新位置索引，区分现存、可恢复和永久删除；历史失败保持原事实，逻辑大小不当作实际回收量。

旧 `plan/` 已完成清理并退役，现行规划统一使用上述四类本地任务目录；不重建旧入口。历史记录中的旧路径通过本地清理索引查找，不直接重放旧命令。公开文档不依赖被忽略的本地任务文件；原始历史附件和发布事实保持不变。
