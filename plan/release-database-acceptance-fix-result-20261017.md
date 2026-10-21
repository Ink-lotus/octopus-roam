# 数据库验收失败复核与本地修复

日期：2026-10-17。关联：[修订后的修复计划](release-database-acceptance-fix-plan-20261015.md)、[历史环境验收报告](release-environment-acceptance-20261015.md)。

状态：本轮已授权的本地源码修复、最终回归、七目标新候选与独立 SQLite 演练全部完成。真实 MySQL/PostgreSQL 本次未连接；`L6_5_strictly_passed=false`。

## 复核结论与实际修改

| 事项 | 复核结论与处理 |
| --- | --- |
| migration 5 要求 `channels.key` | 属实。当前模型已把凭据放入 `channel_keys`；现按实际列元数据识别完整当前结构并正常跳过旧字段搬运。 |
| SQLite `HasColumn` 误判 | 属实。驱动对建表 SQL 做 LIKE，`PRIMARY KEY` 可干扰 `key` 判断；5/11 的前置检查和删列路径改为实际元数据查询，错误向上传递。 |
| 5 与 11 的执行依赖 | 原计划遗漏。5 原在 AutoMigrate 后，11 在前；5 现移到 BeforeAutoMigrate，先处理旧地址/凭据，11 再展开授权。 |
| 合法早期旧结构 | 原计划的 fixture 不完整。历史 `af2787d^` 只有多值地址与旧凭据表，5 现使用历史快照补单值目标列。保持既有单值优先、首个地址和按主键排序首条非空凭据的收敛规则。 |
| 不完整/混合结构 | 返回固定的 `incomplete channel migration schema` 错误，5 通过正常流程记 failed。11 在清空产物前检查来源；分组项已经改写的中间态拒绝继续，防止级联删除成员。 |
| migration 8 统计快照 | 完整旧库回归补发现：未导出的嵌入类型被 GORM 忽略。经补充授权，将四处 `snapshotStatsMetrics` 名称改成 `SnapshotStatsMetrics`；列名及类型定义不变。 |
| 进程级迁移注册列表 | 原实现成功后清空列表，第二个库会漏跑。现在保留列表、对副本排序，由每个库的迁移记录控制成功跳过和失败重试。 |
| SSE 共享测试夹具 | 完整回归发现既有契约测试的 16 条缓冲可能被共享库的分组事件挤满。B 基线复现后经第三次授权，测试按实际分组数量分配缓冲，新增默认缓冲拥塞关闭、移除订阅者及保留已缓冲事件的独立回归。产品 SSE 实现未改。 |
| MySQL TLS | 历史 `tls_unknown_authority` 属实，尚无产品构造缺陷证据。新增真实 TLS 握手的合成 CA 测试：受信 CA 成功、未知 CA 拒绝、主机名不符拒绝；产品 TLS 代码保持原实现。 |
| 新库验收别名 | 驱动只接受两个固定别名。后续应提供新的仓库外 profile 文件，在其中以固定别名映射新空目标，不能直接使用任意新别名。 |

产品修改限定为 `internal/db/migrate/005.go`、`008.go`、`011.go`、`legacy.go`、`migrate.go`。新增测试为 `internal/db/migrate/005_test.go`、`migrate_test.go`、`internal/db/migration_test.go`、`internal/dbprofile/tls_test.go`；另修订已授权的 `internal/server/handlers/group_route_defaults_test.go`。已有未提交改动保留；没有新增依赖、改锁文件、改 CI、commit 或远端操作。

## 验证与证据

独立证据目录：[dbfix-review-20261017-01](../build/release-local/dbfix-review-20261017-01/)。修复前的失败、测试夹具校正过程及修复后检查均保留，未覆盖旧证据。完整升级 fixture 使用与 GORM 历史建表方式一致的命名外键；最初手写的无名外键触发 SQLite 驱动 DDL 解析错误，记录在 `regression-after-stats-fix.log`，没有把该夹具错误当作新的产品修复。

回归覆盖当前结构（包括改变 SQLite LIKE 大小写行为以消除误判）、旧多值与旧单值结构、不完整/混合结构、元数据查询失败、旧统计搬运、渠道/模型/授权/分组项关联、主键及顺序、外键开启与一致性、同进程不同新库、成功跳过和失败重试、迁移后正常重新初始化。

最终 Go 输入清单为 `regression-inputs-v2.json`，包含 211 个输入，SHA-256 为 `5b9fc7a7de6f70d213e3abba4b176116123c071619b49509fbc262bdff2cf881`。旧 `regression-inputs.json` 保留为修订 SSE 测试之前的证据。构建验证器会逐项对照工作区与冻结源码后再引用测试日志，不把引用日志描述成在冻结目录中重跑。

首次完整 default/jsoniter 检查通过，随后全量检查在既有 `TestRouteDefaultsAPIContractResetAndEvents` 的 3 秒事件等待处失败；串行 jsoniter 也复现，不能归因于并行检查的 CPU 争用。使用 B 候选五个产品文件的 overlay、单逻辑处理器运行整个 handlers 包同样失败（`group-events-baseline-review.log`），确认与本轮迁移修改无关。按实际事件批次分配测试缓冲的 overlay 验证通过（`group-events-overlay-review.log`），用户授权后已应用同一补丁，并增加独立慢订阅者回归。原断言、超时和产品容量均保留；失败日志、审阅差异及 overlay 均未覆盖。

最终源码的四项检查全部通过，记录均绑定上述 v2 清单，实际退出码均为 0：

| 检查 | 命令范围 | 耗时 | 证据 |
| --- | --- | --- | --- |
| Go 全量 default | `go test -mod=readonly -p=1 . ./cmd ./internal/... ./static ./scripts/acceptance/database -count=1` | 162.23 秒 | `go-test-default-v2.json` / `.log` |
| Go 全量 jsoniter | 同上并加 `-tags=jsoniter` | 164.01 秒 | `go-test-jsoniter-v2.json` / `.log` |
| vet | `go vet -mod=readonly ./cmd ./internal/... ./static ./scripts/acceptance/database` | 1.26 秒 | `go-vet-v2.json` / `.log` |
| race | `go test -mod=readonly -race -p=1 ./internal/db/migrate ./internal/db ./internal/dbprofile ./scripts/acceptance/database ./internal/server/handlers -count=1` | 71.50 秒 | `go-test-race-v2.json` / `.log` |

离线命令使用 `GOTOOLCHAIN=local`、`GOPROXY=off`、`-mod=readonly`，缓存指向仓库已有 `.tmp-go-cache`/`.tmp-go-mod`；仅设置子进程变量，没有修改持久环境配置。

## 新候选与后续真实数据库门禁

新候选：[dbfix-20261017-01](../build/release-local/dbfix-20261017-01/)，版本 `v0.1.0-local.dbfix-20261017-01`。使用现有 `scripts/build-local.ps1`、B 组真实许可证报告和本轮哈希验证器生成，构建耗时 371.35 秒，退出码 0；前端 TypeScript 检查和 Vite 生产构建通过。七个目标为 Windows amd64、Linux amd64/arm64/arm/386、Darwin amd64/arm64；交叉编译不代表其他系统实机验收。

`candidate-integrity-final.json` 核对了 998 项候选文件、497 项冻结输入及对应工作区输入、七个 ZIP 的清单/内容/Unix 权限/哈希、七个包与 Docker context 的八份许可证副本。比 B 多出的十个冻结输入是本轮四个新增测试，以及先前已存在的五个数据库验收驱动 Go 文件和 `prepare-database-acceptance.ps1`；后六个文件本轮未修改。首次额外核对把 metadata 的八进制权限字符串误按整数比较，记录为 `candidate-integrity-inspection-01.json`，按构建脚本的实际字段格式修正核对后通过，未改归档或冻结候选。

| 证据 | SHA-256 |
| --- | --- |
| `build-info.json` | `7585e58eba560b3a35d235de6f541a6cc301b9a9bfec7265fea172bb1025afa3` |
| `source-input-sha256sums.txt` | `28210f50ed8138490853db2f7aaaf2b349073346bd5ea665b12b1aa226a888f2` |
| `file-sha256sums.txt` | `88fe35d3baf11fabd671522589eea870b3358d241d42af5cbda2861e3dd8a3ba` |

独立验收：[dbfix-acceptance-20261017-01](../build/release-local/dbfix-acceptance-20261017-01/)。现有 `prepare-database-acceptance.ps1` 以新候选运行驱动测试、构建和 SQLite 3.53.3 演练，退出码 0。`sqlite-rehearsal-summary.json` 共 35 项：34 项通过，`database_row_lock_serialization` 在 SQLite 下记录为 pending，仍须在真实 MySQL/PostgreSQL 验证。覆盖空库前置检查、迁移/缓存、设置与禁用状态、刷新事务及回滚、备份一致性、非法导入回滚、加密备份、空库恢复、新进程持久化，以及原始候选 Windows 程序的 API 和两次启动/正常停止；没有浏览器或 Unix 实机结论。

`sqlite-schema-inspection.json` 用只读连接核对本轮合成的 `primary.db` 与 `restore.db`：两库的 migration 3–13 共 11 条记录均为 success，包含 version 5；`channels.key/type/base_urls` 均不存在，外键违规为 0。`acceptance-integrity-final.json` 确认 497 项冻结输入一致，并核对驱动链接的 181 项数据库/业务输入及 5 个相同驱动源码文件；本轮启动的验收进程均已退出。新证据全部写入独立目录，冻结候选生成后没有追加文件或改写哈希。

修复后再次核对 A2/A3/B 的 969/973/976 条文件哈希、B 的 487 条冻结输入，均一致；记录为 `historical-candidate-integrity-final.json`。B 对应工作区的 481 个原有输入未变，差异严格限定为本轮五个产品文件及一个既有 SSE 测试文件；四个新增测试另列入新候选。依赖未变，继续使用既有真实许可证报告，SHA-256 为 `7282dfd529a5ab2904d5d084b61677a28bc63feeef14f4a8edf43e9944db8b16`，不将报告存在等同于许可证分发条件全部完成。

真实 PostgreSQL 按历史证据为部分初始化失败目标；本次未重连，不能当作空库直接重跑。MySQL 仍待可信 CA、服务端链与主机名核对。用户准备新空目标及新的仓库外 profile 后，再按新驱动、具体目标和写入命令取得对应授权。没有 `restore_database` 时，空库恢复仍待验。

本次本地证据不能替代真实两库迁移、行锁、业务、备份、API/正常重启与恢复证据；Docker、其他平台实机、许可证分发材料和真实发布链路继续按发布计划保留门禁。
