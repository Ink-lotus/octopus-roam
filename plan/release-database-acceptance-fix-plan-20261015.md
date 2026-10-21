# 第 4 项真实数据库验收失败修复计划

日期：2026-10-15；复核与修复：2026-10-17。状态：已按本次三次明确授权完成本地修复、最终回归、七目标新候选和独立 SQLite 演练；真实数据库复验仍待新空目标及单独授权。

关联材料：[发布计划](release-plan-v0.1.0.md)、[环境验收报告](release-environment-acceptance-20261015.md)、[云数据库交接](cloud-database-test-handoff.md)。

## 1. 当前结论

当前冻结候选为 `build/release-local/dbfix-20261017-01/`，版本为 `v0.1.0-local.dbfix-20261017-01`。七目标构建、998 项文件及 497 项输入校验通过；独立 SQLite 演练位于 `build/release-local/dbfix-acceptance-20261017-01/`。完整结果见[本地修复报告](release-database-acceptance-fix-result-20261017.md)。A2、A3、B 候选和历史失败证据均保留，不追改。

2026-10-15 已按明确授权执行 `roam-mysql-test` 和 `roam-postgres-test`：

- MySQL 在空库前置检查阶段因 `tls_unknown_authority` 停止，没有应用写入。
- PostgreSQL 18.6 通过空库检查，但 `internal/db/migrate/005.go` 在 `schema_migration_and_cache` 阶段失败。当前库留下 17 张表（含 `migration_records`），迁移记录 7 行，其中版本 3、4、8、10、11、12 成功，版本 5 失败，其余 16 张业务表均为 0 行。
- SQLite 演练通过不能作为迁移兼容证据：只读复核确认 `channels` 没有 `key` 列，但 `github.com/glebarez/sqlite` 的 `Migrator.HasColumn("channels", "key")` 在该结构上返回 true，掩盖了 migration 5 的错误前置条件。
- 失败后只做了脱敏只读诊断，没有清库、删表、改写迁移记录、关闭 TLS、重跑迁移或启动产品。真实业务、行锁、刷新、备份、API/正常重启和恢复用例均未完成。

证据根目录为 `build/release-local/env-20261015-205423-4502ab5a/`，执行汇总为 `authorized-database-execution-summary.json`。完整失败原因和数据影响以环境验收报告为准。

### 2026-10-17 复核及修订依据

- **两项失败结论属实。** 工作区 HEAD 仍为 `948de764ae9a034fd72899686d24dcc4ecad7fa5`，已有未提交改动保留；修复前 B 候选 976 条文件哈希、487 条冻结输入和对应工作区输入全部匹配。历史 PostgreSQL 留存状态与 MySQL TLS 分类均与执行汇总一致，本次未重连或写入云数据库。
- **必须先修正迁移阶段依赖。** migration 11 注册在 `BeforeAutoMigrate`，migration 5 注册在 `AfterAutoMigrate`；数字版本并不构成跨阶段的执行顺序。旧多凭据表可能在 5 读取前被 11 清空。5 应在 11 之前完成旧地址/凭据搬运，11 必须在任何清空操作前检查源结构；只修改 5 的缺列分支不足以保护升级数据。
- **真正的完整旧结构不一定有 `base_url`/`key`。** Git 历史 `af2787d^:internal/model/channel.go` 的合法多地址/多凭据结构只有 `channels.base_urls` 与 `channel_keys.channel_key`，由当时的 AutoMigrate 补齐单值目标列。提前执行 5 后，必须依据完整旧来源显式补齐目标列；不能把这个合法版本当作损坏库。原有单值优先、首个地址/首条非空凭据的历史收敛规则保持不变，不声称保留全部旧地址和凭据。
- **列检测错误必须向上传递。** SQLite 驱动的 `HasColumn` 使用建表 SQL 的 LIKE，`PRIMARY KEY` 等文本即可干扰 `key` 判断；现有 `hasPhysicalColumn` 虽查实际列，却把查询错误转成 false。5/11 的结构判定需要一次读取指定表的真实列名，并把查询失败与列缺失分开处理。
- **补同进程重复初始化回归。** `BeforeAutoMigrate`/`AfterAutoMigrate` 成功后会清空全局注册列表，第二个数据库可能完全不跑迁移。保留注册列表，由每个库自己的迁移记录决定跳过和重试，避免测试因初始化顺序出现假通过。
- **完整旧库用例补证实 migration 8 的统计快照缺陷。** `snapshotStatsMetrics` 作为未导出嵌入字段被 GORM 忽略，导致 `InputToken` 加列报错，旧统计也无法正确扫描。已另获明确授权，将 `legacy.go` 与 `008.go` 的四处名称调整为导出的 `SnapshotStatsMetrics`；原有列名/类型不变，回归同时验证渠道与模型统计保留。
- **全量测试中的 SSE 失败来自既有夹具容量。** 共享测试库的分组数量可能超过测试订阅的 16 条缓冲，触发产品原有的慢订阅者关闭行为；B 源码 overlay 与单逻辑处理器下均已复现。经第三次明确授权，只在 `group_route_defaults_test.go` 中为策略事件契约用例按实际批次数量预留缓冲，另加独立用例验证默认缓冲满后关闭、移除慢订阅者并保留已缓冲事件；产品容量、实现、原断言和超时不变。
- **修正新目标的交付方式。** 现有数据库验收驱动只接受 `roam-mysql-test` 和 `roam-postgres-test`。使用新的仓库外 profile 文件，并在文件内用这两个固定别名指向用户准备的新空库；任意新别名目前会被拒绝。目标及写入命令仍需单独授权。
- **MySQL 目前没有产品 TLS 缺陷证据。** `BuildConnectionSpec` 已保留验证、默认驱动参数，并在自定义 CA 路径设置 `ServerName`。可以先用合成 CA 验证受信、未知 CA 和主机名不符三条路径；真实 CA/profile/服务端链是否正确仍需独立核对，不能根据 SQLite 或离线 TLS 测试宣告云库通过。

现有 `internal/dbprofile`、`internal/db`、数据库验收驱动测试已离线通过；`internal/db/migrate` 原先没有测试。默认仓库外 Go cache 受沙箱限制，改用仓库已有 `.tmp-go-cache`/`.tmp-go-mod` 后通过；仅设置子进程环境，未改持久配置或依赖。

## 2. 修复目标与边界

目标是让当前产品在全新数据库上正确完成迁移和后续验收，并让测试能够区分真实旧结构、当前结构和损坏/不完整结构。修复必须保持 TLS 证书验证和现有迁移记录语义。

本计划包含：

1. 修复 migration 5 对当前 `channels`/`channel_keys` 结构的错误前置条件。
2. 消除 SQLite 列存在性误判，或让 migration 5 完全绕过不可靠的通用判断。
3. 为当前新库、旧库迁移和不完整结构增加回归测试，并在真实 PostgreSQL/MySQL 上重验。
4. 排查 MySQL 服务端证书链、主机名和仓库外 profile 的 `ca_file`；只有确认 profile/服务端配置正确后，才判断是否需要修改产品 TLS 代码。
5. 在源码确实变化时，以新目录重建候选并重新生成哈希、构建元数据和验收证据。

本计划不包含：清理当前部分初始化的 PostgreSQL 库、删除表或迁移记录、关闭 TLS 校验、复制真实 profile、修改 CI、Git/远端发布、安装新依赖或自动创建其他数据库。

## 3. 修复顺序

### A. 先完成只读基线

1. 阅读本计划、发布计划和环境验收报告，确认当前候选哈希及 `authorized-database-execution-summary.json`。
2. 检查工作树和 HEAD；保留已有未提交修改，不回滚、不提交、不切换分支。
3. 阅读 `internal/db/migrate/005.go`、`internal/db/migrate/011.go`、`internal/db/migrate/migrate.go`、`internal/model/channel.go` 和 SQLite/GORM 相关测试。
4. 保留当前 PostgreSQL 库的失败证据。不得把它当作新的空库，也不得在没有准确对象和单独授权时清理它。

### B. 修复 migration 5 和列检测

目标文件为 `internal/db/migrate/005.go`、`internal/db/migrate/011.go`、`internal/db/migrate/migrate.go`，以及补充授权的 `internal/db/migrate/008.go`、`internal/db/migrate/legacy.go` 和对应测试文件；列元数据读取与历史目标列使用小型辅助函数/结构快照，不新增依赖。

实现要求：

1. **区分当前结构和旧结构，并保证 5 先于 11。** 全新当前模型没有 `channels.key`，凭据在 `channel_keys`；migration 5 对完整当前结构应安全跳过旧字段搬运并成功记录迁移。完整旧结构包括单值版，以及只有 `base_urls`/旧 `channel_keys.channel_key` 的多值版；后者需在搬运前按历史快照补齐单值目标列。migration 11 在任何清空/搬运前验证源列与凭据表形状。
2. **不依赖字符串模糊匹配判断列。** 对 `channels` 和 `channel_keys` 使用方言安全、针对指定表的实际列元数据；至少兼容 PostgreSQL、MySQL 和 SQLite。元数据查询失败必须返回错误，不能解释为列缺失。不能用其他表、索引或建表 SQL 中出现的 `key` 文本证明 `channels.key` 存在。
3. **处理不完整旧结构要显式失败。** 不能为了让新库通过而把缺列的真实旧库静默当作当前结构；为“完整当前结构、完整旧单值结构、完整旧多值结构、不完整/混合结构”分别定义可审查的判断和错误路径。无旧来源的目标缺列不能靠自动加空列掩盖，未知凭据表不能清空或删除。
4. **保留迁移记录语义。** 不删版本、不改现有 `migration_records` 状态，不把失败库伪造为成功。成功的全新库应由正常迁移流程写入 version 5 success。
5. **防止数据破坏。** 旧结构测试必须验证地址、凭据、模型、授权、分组项和主键/顺序的保留；SQLite 的表重建、外键和列删除路径必须单独检查。
6. **按库决定迁移状态。** 初始化成功后保留进程级注册列表，排序使用副本，每个库仍通过正常记录流程跳过成功版本、重试失败版本；不得手工追认失败库。
7. **让历史统计快照参与 GORM 映射。** 导出嵌入字段的类型名称，不改变数据库列定义；完整升级验证历史渠道统计的搬运和模型统计原值保留。已改写分组项来源的 migration 11 中间态显式失败，防止重试清空授权时级联删除现有成员。

回归测试至少覆盖：

- 当前模型全新 SQLite 数据库：migration 5 不应要求 `channels.key`，并能完成完整初始化。
- 两类明确的旧 fixture：单值版包含 `base_url`/旧 `key`，多值版初始只有 `base_urls`/旧凭据表；覆盖直接迁移和完整初始化，验证历史收敛规则、关联数据、主键/顺序及删除旧列行为。
- 不完整旧结构：缺少必要的旧列时返回固定错误，不写入错误迁移成功记录。
- SQLite 中另一个表或建表 SQL 含有 `key` 文本、但 `channels` 没有 `key` 的场景，确认列检测不误判。
- 元数据查询失败、同进程两个独立新库、成功迁移跳过及失败迁移正常重试；SQLite 开启外键并检查迁移后的外键一致性。
- PostgreSQL 和 MySQL 的真实新库迁移作为集成验收，不用 SQLite 结果替代。

### C. 排查并修复 MySQL TLS 阻塞

先做仓库外的配置核对，不修改产品源码：

1. 用户在本地确认服务端证书链完整、证书 SAN 与 profile 的 `host` 匹配、CA 文件路径可读且内容对应该服务端。
2. 确认 `roam-mysql-test` 保持 TLS 开启；需要额外 CA 时只在仓库外 profile 使用 `ca_file`。不得设为 `tls=false`，不得使用跳过证书验证的配置。
3. 使用脱敏只读诊断区分“profile/CA 配置错误”和“产品 TLS 构造错误”。不输出 CA 内容、主机、账号、DSN 或原始错误。
4. 如果可信 CA/profile 修正后连接成功，不需要因此重建产品候选；只需保存脱敏配置核对结果并在新一轮数据库验收中复测。
5. 如果 CA 正确仍失败，再检查 `internal/dbprofile/profile.go` 的 `BuildConnectionSpec`、MySQL 驱动默认配置和 `ServerName`，保留证书验证，并为可信 CA、未知 CA、主机名不匹配分别增加测试。

### D. 代码验证与候选重建

只有 B/C 的源码变更完成并通过本地测试后，才进入新候选：

1. 新建 `build/release-local/dbfix-<唯一编号>/`，拒绝复用或覆盖 `b-final-20261015-181705-6ebd8c65`。新候选使用新的本地版本标识，例如 `v0.1.0-local.dbfix-<唯一编号>`。
2. 使用现有离线缓存、`GOTOOLCHAIN=local`、`GOPROXY=off`、`-mod=readonly`；不得新增或升级依赖。
3. 运行受影响 Go 测试、`go vet`，必要时运行相关 race 测试；至少覆盖 `internal/db/migrate`、`internal/dbprofile`、`internal/db` 及现有数据库验收驱动测试。
4. 重新生成七个目标的候选（如果构建入口要求全量），核对源码快照、候选输入、许可证报告副本、Unix 权限、归档清单、哈希和构建元数据。文档修订不单独触发重建，但代码/构建输入变化必须重建。
5. 保留旧候选和所有失败证据；不得在旧候选中追加修复文件或手工改哈希追认。

### E. 使用新库重跑数据库验收

1. 当前 PostgreSQL 测试库已部分初始化，不能直接重跑。优先让用户准备同引擎全新空逻辑库，使用新的仓库外 profile 文件，在其中沿用驱动支持的固定别名；先核对目标，再对具体写入命令取得授权。若必须重置现有库，先列出准确表/记录范围和命令，另取明确授权。
2. MySQL 只有在证书链/profile 核对通过后，才用新候选进行空库 preflight。preflight 失败立即停止，不迁移、不增量导入、不关闭 TLS。
3. PostgreSQL 和 MySQL 分别使用全新证据子目录；先运行空库检查，再运行迁移、缓存初始化、合成数据、行锁、刷新事务、备份/非法导入回滚和原始程序 API/正常重启。
4. 两库没有 `restore_database` 时，完整空库恢复继续记录为待验；不能用同一个已写入库的增量导入代替恢复证据。
5. 任一阶段失败都保留脱敏 JSON 和数据影响，停止该库后再决定是否执行另一独立目标；不自动清理、不自动重试。

## 4. 验收门槛

修复可以交回发布流程前，必须同时满足：

- 新 PostgreSQL 库迁移和 `InitCache` 成功，版本 5 记录 success，随后完整数据库套件通过；没有把 SQLite 演练当作替代。
- 新 MySQL 库在证书验证开启的情况下完成同一套流程；未知 CA/主机名错误仍被拒绝并脱敏分类。
- 迁移回归覆盖当前结构、完整旧结构和不完整旧结构，且不破坏旧数据关联。
- 新候选哈希与修复后的源码输入一致，A2/A3/B 旧候选和失败证据未变。
- 许可证、Docker、Unix/macOS/Android 实机、空恢复库和真实发布链路的既有门禁仍按发布计划记录，不能因数据库修复完成而自动判定 L6.5 严格通过。

## 5. 下一会话交接清单

下一会话开始时先读取本计划、[发布计划](release-plan-v0.1.0.md) 和[环境验收报告](release-environment-acceptance-20261015.md)，核对：

- 当前候选为 `dbfix-20261017-01`，A2/A3/B 与新候选均冻结；
- PostgreSQL 现有测试库为部分初始化失败目标，不能重跑或默认清理；
- MySQL `tls_unknown_authority` 先按 CA/profile/服务端证书排查；
- migration 5/11 的顺序与结构判定、列元数据错误、按库迁移记录及 migration 8 统计快照已完成本地修复和回归；
- default/jsoniter、vet、相关 race、七目标构建及 SQLite 演练已通过；SQLite 的行锁项保留为真实两库待验，不能据此追认历史云库失败；
- 本轮本地授权已完成；新数据库目标及具体写入命令仍需对应授权，源码/构建输入未变时不重复构建；
- 在完整数据库证据和其他剩余环境门禁完成前，保持 `L6_5_strictly_passed=false`。

2026-10-17 已取得并执行本轮本地源码/测试修改及新候选构建授权，另补充授权 `008.go`/`legacy.go` 的统计快照名称修复，以及 `group_route_defaults_test.go` 的夹具容量修订和慢订阅者回归；执行结果见[本地修复报告](release-database-acceptance-fix-result-20261017.md)。这些授权不包含真实数据库写入或重置、profile 修改、CI 或远端发布。
