# R0 本地数据库验收与 MySQL 修复结果

日期：2026-10-18。入口：[发布计划](release-plan-v0.1.0.md)。当前阶段：SQL 与原始程序自定义 CA 支持已修复，最终七平台候选及驱动已构建；经用户明确要求，第二轮本地 MySQL/PostgreSQL 各 35 项全部通过，数据库及空库恢复条件已收口。其他发布门禁继续保留。

## 首次本地执行

用户已明确授权按[执行清单](../build/release-local/r0-local-db-20261018-01/APPROVAL.md)初始化独立实例、生成可信 TLS 与私有 profile、创建两主库及两恢复库，并串行执行两引擎各一次完整验收。工具复用 `D:\tool\db-test\tools\` 中的 MySQL 8.4.11 与 PostgreSQL 18.6。数据和私有材料仅在 `D:\tool\db-test\instances\octopus-roam\r0-20261018-01\`、`D:\tool\db-test\profiles\octopus-roam\r0-20261018-01\` 新建；历史云库未连接。

候选为 `dbfix-20261017-01`。执行前再次核对 998 项文件、497 项冻结/工作区输入、181 项驱动链接输入、5 项驱动源码与 10 个数据库工具哈希；[离线预检](../build/release-local/r0-local-db-20261018-01/preflight.json)通过。[脚本验证](../build/release-local/r0-local-db-20261018-01/preparation-validation.json)包含语法、进程调用和六种不允许输出的拒绝检查。

| 引擎 | 实际结果 | 边界 |
| --- | --- | --- |
| MySQL 8.4.11 | 6 项通过、1 项失败；两库初始空库检查、主库 migration 3–13、缓存和默认设置通过；合成配置导入失败，分类为 `database_syntax_error` | 未执行后续行锁、刷新、备份、原始程序或恢复。主库已初始化，恢复库未迁移；没有清理或重跑 |
| PostgreSQL 18.6 | 35 项全部通过，包含真实行锁、刷新回滚/关联同步、快照、错误导入拒绝、加密备份、空库恢复、跨进程持久化和原始候选两次 API/正常启停 | 两主/恢复角色均记录 migration 3–13 success；此证据对应旧候选，修复后候选仍需对应验收 |

结果：[执行汇总](../build/release-local/r0-local-db-20261018-01/execution-summary.json)、[MySQL](../build/release-local/r0-local-db-20261018-01/roam-mysql-test-summary.json)、[PostgreSQL](../build/release-local/r0-local-db-20261018-01/roam-postgres-test-summary.json)、[逐库迁移记录](../build/release-local/r0-local-db-20261018-01/migration-records.json)。汇总在核对尚未执行的 MySQL 恢复库时标为失败；这不是初始化或 TLS 失败，实际故障以 MySQL `seed / explicit_disabled_json_configuration` 为准。

两引擎均通过严格身份校验 TLS 连接，并验证明文连接、错误 CA 被拒绝。MySQL 强制安全传输且关闭 mysqlx；PostgreSQL 使用 SCRAM、`verify-full` 与 `channel_binding=require`。CA 未导入系统信任根。

两个自建数据库进程均正常停止，13306 / 15432 均不再监听；原始候选的两次正常停止记录均为 `success=true`。另有仓库外既有产品运行实例，未操作该实例。四个测试库、合成数据、证书及私有 profile 保留；没有注册系统服务或修改系统 PATH、自启、防火墙、CI、依赖与远端状态。

## 故障定位与修复

`preparePrivacyImport` 使用原始条件 `key IN ?`，MySQL 中 `key` 是保留字，GORM 不会替原始 SQL 片段引用标识符。SQLite 接受该语句，因而此前本地测试未发现；PostgreSQL 本次也接受该原始片段。

局部修正 `internal/op/privacy_backup.go` 的读取和条件更新，以及 `internal/op/privacy.go` 的规则保存和开关更新，共四个条件，改用 GORM 的结构化 map 条件，让各方言引用 `settings.key` / `settings.value`。旧值仍作为参数绑定，空字符串旧值不被省略，更新行数不是 1 时仍拒绝并发冲突。

新增 `internal/op/privacy_mysql_test.go`，直接经过真实隐私导入读取/写入路径，使用 MySQL 与 PostgreSQL DryRun 方言检查生成 SQL 和绑定参数，不连接外部数据库。测试还核对空旧值与冲突拒绝。修复前实际失败日志为 [privacy-sql-before-fix.log](../build/release-local/r0-local-db-20261018-01/privacy-sql-before-fix.log)，修复后相关回归通过，见 [privacy-sql-after-fix.log](../build/release-local/r0-local-db-20261018-01/privacy-sql-after-fix.log)。

后续静态审查在 `internal/op/channel_refresh.go` 的两条快照查询发现同类问题：原始 JOIN 使用未引用的 MySQL 保留字 `keys` 作为别名。仅将别名和对应 SELECT 改为 `credentials`。新增回归检查通过真实快照校验函数捕获 MySQL 方言生成的查询，并验证渠道筛选参数保留。使用旧冻结源码 overlay 的实际失败见 [reserved-alias-before-fix-attempt2.log](../build/release-local/r0-local-db-20261018-01/reserved-alias-before-fix-attempt2.log)，修复后见 [reserved-alias-after-fix.log](../build/release-local/r0-local-db-20261018-01/reserved-alias-after-fix.log)。首次 overlay 调用因 PowerShell 未引用原生命令参数导致路径被错误解析，只是工具调用失败，其日志保留，不作为复现依据。

中间构建 `mysqlfix-20261018-01/` 在最后的工作区输入核对中检测到上述后续修复而停止，没有生成最终 `build-info.json` 或完整文件清单；不是可选候选。`mysqlfix-20261018-02/` 已完成两项 SQL 修复的七平台构建，但随后发现下述原始程序 TLS 跨进程问题，因此继续作为中间证据保留。最终构建使用全新 `mysqlfix-20261018-03/`，不回写中间快照或手工改清单来追认变化。

隐私修复后已通过完整显式产品包 Go default/jsoniter、`go vet` 以及 `internal/op`、`internal/server/handlers` 的 jsoniter race，日志为 `go-*-after-fix.log`。补入 JOIN 别名修复后，同组检查使用 `go-*-final.log`；这两组日志对应各自阶段，最终 SQL/TLS 代码的证据使用下述 `go-*-final-v3.log`。

## 原始程序的 MySQL 自定义 CA

启动链路审查发现：`Profile.BuildConnectionSpec` 向 MySQL 驱动注册的自定义 TLS 名称仅在验收 worker 内存在，无法通过 DSN 传给新的原始程序进程。新增驱动回归测试先注销合成父进程 TLS 注册，确认原 DSN 在新进程语义下无法解析，再验证交付配置使用内建 `tls=true` 并携带 CA 文件位置。这个问题由进程边界复现证据确认，首次失败验收尚未执行到原始 MySQL 程序阶段。

应用新增可选 `database.mysql_ca_file`（环境键 `OCTOPUS_ROAM_DATABASE_MYSQL_CA_FILE`），由原始程序在自身进程读取指定 PEM、注册可信根、绑定 DSN 主机名并强制 TLS 1.2 以上、禁止明文回退。未配置此选项时保持既有连接参数；配置给其他引擎、不可读文件、非法 PEM 或连接配置时拒绝，错误不包含私有路径/连接信息。PostgreSQL 继续使用已有 `sslrootcert` / `verify-full`。没有向系统或用户证书库导入信任根。

验收驱动仅在内存里的命名管道配置中传递内建 TLS 模式和 `mysql_ca_file`，原始程序负责在自身进程加载。覆盖文件是 `cmd/start.go`、`internal/conf/config.go`、新增 `internal/conf/database.go` 及相应测试，以及驱动 `suite.go` / `runtime_windows.go` / `main_test.go`。定向测试通过，见 [mysql-runtime-ca-tests.log](../build/release-local/r0-local-db-20261018-01/mysql-runtime-ca-tests.log)；包括可信 CA、主机名拒绝、禁用明文回退、DSN 参数保留、错误脱敏和环境键读取。

最终源码对应的完整 Go default/jsoniter、vet 与五个受影响包 race 记录使用 `go-*-final-v3.log`，此前日志保留其原始范围。新原始程序已在第二轮本地 MySQL 中通过实际 API、两次正常启停及持久化检查，验证自定义 CA 在原始程序进程内生效。

## 最终候选与驱动

当前候选为 [mysqlfix-20261018-03](../build/release-local/mysqlfix-20261018-03/)，版本 `v0.1.0-local.mysqlfix-20261018-03`，基线仍为 `test / 948de764ae9a034fd72899686d24dcc4ecad7fa5`，包含当前未提交修复。Windows amd64、Linux amd64/arm64/arm/386、macOS amd64/arm64 七个平台均完成构建；TypeScript/Vite 检查与前端生产构建通过。

候选包含 989 项文件清单和 500 项冻结输入，全部与当前工作区对应输入匹配；清单自身不自哈希，目录实际为 990 个文件。7 个 ZIP 的路径白名单、Unix 权限、解压内容与 SHA-256 均通过。七包及 Docker context 的真实许可证 CSV 哈希均为 `7282dfd529a5ab2904d5d084b61677a28bc63feeef14f4a8edf43e9944db8b16`。四 Linux 平台的 Docker context 已生成，daemon 未运行，不能称作已构建镜像。

绑定驱动位于 [mysqlfix-acceptance-20261018-03](../build/release-local/mysqlfix-acceptance-20261018-03/)，核对 500 项候选输入、184 项链接输入与 5 项驱动源码，驱动测试通过。独立 [SQLite 演练](../build/release-local/mysqlfix-acceptance-20261018-03/sqlite-rehearsal-summary.json) 35 项中 34 项通过、1 项真实行锁 pending；原始程序 API、两次正常启停和空库恢复通过。该 pending 随后由真实 MySQL/PostgreSQL 的行锁检查补齐，SQLite 记录保持原样。

| 绑定材料 | SHA-256 |
| --- | --- |
| 实际 Windows ZIP 解压程序 | `b270ae1f7374cf0840eee8ce5b3421797b2029f13eecc8d1bd91b3a3d76fd0f6` |
| 验收驱动 | `f224614d2ca5131c69ea44a6737ab16da99c5d65e1f9351b02638118a2f31f7a` |
| 第二轮执行脚本 | `c5b8131f5dc32023fc74782e6ebc5afca4026729634f5ea52aa6dd00beb622c4` |

最终 Go 检查均退出 0，覆盖显式产品包 `. ./cmd ./internal/... ./static` 的 default/jsoniter 与 vet；jsoniter race 覆盖 `cmd`、`internal/conf`、`internal/op`、`internal/server/handlers` 和 `scripts/acceptance/database`。精确日志在首轮证据目录：

- [go-default-final-v3.log](../build/release-local/r0-local-db-20261018-01/go-default-final-v3.log)、[go-jsoniter-final-v3.log](../build/release-local/r0-local-db-20261018-01/go-jsoniter-final-v3.log)、[go-vet-final-v3.log](../build/release-local/r0-local-db-20261018-01/go-vet-final-v3.log)、[go-race-final-v3.log](../build/release-local/r0-local-db-20261018-01/go-race-final-v3.log)。
- [候选构建日志](../build/release-local/r0-local-db-20261018-01/mysqlfix-candidate-build-v3.log)与[驱动准备日志](../build/release-local/r0-local-db-20261018-01/prepare-driver-v3.log)。

回归和真实数据库证据保存在冻结候选外，候选 `build-info.json` 的 `verification` 数组为空不表示没有运行上述回归。没有追改候选元数据或清单。新旧七平台逐目标比较 Go 模块及 replace 元数据一致，模块并集仍为 110 项；已有源码可用性证据继续沿用，但不等于完成源码附件和再链接义务。

## 第二轮本地执行

用户针对已准备的[第二轮清单](../build/release-local/r0-local-db-20261018-02/APPROVAL.md)明确要求“第二轮数据库验收也一起做了吧”，据此执行该目录的 `run-local-databases.ps1 -ExecuteAuthorized`。运行时间为 2026-10-18 22:31:09–22:31:44（UTC+08:00）。执行前核对脚本、驱动、实际 ZIP 解压程序哈希，新的实例、profile 和 runs 目录均不存在；脚本再次核对 989 项文件清单、500 项冻结及工作区输入、184 项驱动链接输入、5 项驱动源码与 10 项数据库工具。

仅新建 `D:\tool\db-test\instances\octopus-roam\r0-20261018-02\` 与 `D:\tool\db-test\profiles\octopus-roam\r0-20261018-02\`，使用新的两空主库及两空恢复库，未复用首轮已初始化库。严格 TLS、明文拒绝和错误 CA 拒绝全部通过，没有安装系统信任根。

| 引擎 | 当前候选结果 | 完成范围 |
| --- | --- | --- |
| MySQL 8.4.11 | 35/35 全部通过，套件用时 9.686 秒 | 原先失败的配置导入通过；迁移、业务、真实行锁、刷新事务/快照、错误导入回滚、加密备份与空库恢复、主库及恢复库跨进程持久化、原始程序两次 API/正常启停全部通过 |
| PostgreSQL 18.6 | 35/35 全部通过，套件用时 8.835 秒 | 同一完整范围通过，结果对应修复后当前候选；独立于首轮旧候选的通过记录 |

四个主/恢复角色各自有 migration 3–13 共 11 条 success 记录，无失败迁移。两引擎的原始程序各两份正常停止记录，共四份，均为 `success=true`。两个自建数据库也正常停止，13306/15432 无监听；仓库外既有产品运行实例保持运行。两轮数据、合成备份及私有证书/profile 保留，历史云库未连接。

当前证据：[执行汇总](../build/release-local/r0-local-db-20261018-02/execution-summary.json)、[MySQL 35 项](../build/release-local/r0-local-db-20261018-02/roam-mysql-test-summary.json)、[PostgreSQL 35 项](../build/release-local/r0-local-db-20261018-02/roam-postgres-test-summary.json)、[四库迁移记录](../build/release-local/r0-local-db-20261018-02/migration-records.json)、[授权执行记录](../build/release-local/r0-local-db-20261018-02/authorization-record.json)。准备阶段的 `preflight.json` / `preparation-validation.json` 保留原始准备状态，当前执行状态以这些新记录为准。

[收尾只读核对](../build/release-local/r0-local-db-20261018-02/final-handoff-checks.json)通过：当前 989 项文件清单、500 项冻结/工作区输入、184 项驱动链接输入与 5 项驱动源码一致，旧 dbfix 的 998 项文件清单未变；7 个归档和 8 份许可证哈希一致。逐平台 Go 依赖元数据与旧候选相同；本轮产品/驱动和数据库进程为 0，测试端口无监听。仅在内存读取两轮私有随机密码及私钥正文片段，在当前候选、驱动和两轮证据的 845 份公共文本中精确匹配，命中文件为 0，未输出私有值或其指纹。该核对没有再次连接数据库。

## 剩余条件

数据库与同引擎空库恢复条件已收口。两轮“各一次”执行授权均已消耗；后续重跑、清理或数据库变更仍需对应具体目标与操作授权，不将已初始化库当作空库，不改写迁移记录。

许可证源码交付/再链接、Docker 人工验收、其他平台实机、R1 CI 及真实远端升级继续保留对应门禁；`L6_5_strictly_passed=false`。本次未提交代码、修改依赖或 CI、操作远端状态。
