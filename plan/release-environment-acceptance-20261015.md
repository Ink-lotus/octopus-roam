# 第 4 项：环境验收与数据库执行范围

日期：2026-10-15。关联：[发布计划](release-plan-v0.1.0.md)、[数据库凭据交接](cloud-database-test-handoff.md)、[Docker 人工验收](docker-local-test-handoff.md)。

用户已明确：沿用全部平台，缺少实机的保留待验；MySQL/PostgreSQL 由执行会话自动验收，Docker 由用户人工验收。该范围决定没有减少既有公开支持平台，也不表示缺少证据的平台已通过。

当前候选仍为 `build/release-local/b-final-20261015-181705-6ebd8c65/`，版本 `v0.1.0-local.b-final-20261015-181705-6ebd8c65`。本轮只新增独立验收入口和证据，不改产品源码、依赖、锁文件或 CI，不重建输入未变的候选。A2、A3、B 冻结目录保留。

## 当前环境与结果

- 本轮证据：`build/release-local/env-20261015-205423-4502ab5a/`。
- 通过原始 B 程序的离线 `db-check` 确认 `roam-mysql-test` 与 `roam-postgres-test` 都是有效 profile；两者均未配置 `restore_database`。没有回显或复制 profile。沙箱内首次只读检查因个人目录读取权限失败，沙箱外只读校验成功，前后记录分别保留，不能把前次结果认作档案格式错误。
- 用户已明确授权本文两条 `--connect` 命令及列明范围，已串行执行两个别名，驱动各退出 1。MySQL 在迁移前的 TLS 连接失败；PostgreSQL 通过两次空库检查，随后 migration 5 失败。**两库均未通过环境验收**，具体结果及留存数据见下一节。
- 用户表示可不需要恢复；当前方案不恢复原测试库、不清空它们。由于没有另一份空目标，**同引擎空库恢复保留待验**。加密导出、错误口令/损坏拒绝和实际失败写入回滚仍执行。不能把这些项目替代空库恢复。
- 最终准备目录为 `build/release-local/env-20261015-205423-4502ab5a/driver-06/`。7 项驱动测试通过，包含实际的 PostgreSQL SSLRequest/自签名 TLS 失败与凭据标记不外泄测试；完整 SQLite 演练的 35 条检查中 34 条通过，1 条为 SQLite 不适用的真实行锁待验，没有以 SQLite 代替 MySQL/PostgreSQL 行锁。原始 B Windows exe 两次实际启动/API/正常停止通过。
- 驱动 SHA-256：`26cf0d32d3f58d49bfc6650b194a0827dfdf3227470436137616350cec38b1c1`。冻结输入 487 条、实际链接输入 177 条均核对。准备元数据见 `driver-06/driver-build-info.json`，逐用例见 `driver-06/sqlite-rehearsal/result.json`；该目录只包含合成演练，不是云库通过证据。
- `driver-01` 是数据库函数演练；`driver-02`、`driver-03` 保留新增原始 exe 管道测试的失败，原因是 `DisconnectNamedPipe` 使普通配置读取收到错误而非 EOF；验收驱动改为 flush 后关闭服务端句柄，`driver-04` 起通过。`driver-05` 增加 TLS/解析泄露测试，`driver-06` 补齐非 Windows 编译分支并冻结最终入口。产品源码及原始程序均未为此修改。
- 收尾核对：A2/A3/B 的 969/973/976 条文件清单、各自冻结输入及 B 对应的 487 条工作区输入全部一致；新驱动 177 条链接输入、验收源码、原始 exe 和停止辅助脚本哈希一致。Linux amd64 的驱动测试包仅交叉编译通过，不算 Linux 产品运行证据。脚本/报告命令块解析、本地链接、gofmt、`git diff --check` 通过；依赖/锁文件/CI 无修改。详见本轮 `environment-preflight-audit.json`、`driver-linux-compile.json` 和 `environment-script-document-checks.json`。

## 已授权执行的实际结果

执行日期：2026-10-15。连接前重新核对了 B 的 487 条冻结/工作区输入、177 条链接输入、5 条验收源码，以及驱动、原始 exe、正常停止脚本和候选元数据的哈希，结果见本轮 `authorized-execution-inputs.json`。准备阶段的 `driver-build-info.json` 与历史演练结果没有追改成真实云库结果。

| 目标 | 实际执行 | 结果 | 留存及未执行范围 |
| --- | --- | --- | --- |
| `roam-mysql-test` | `primary_empty_before_migration` 尝试连接，驱动退出 1 | `preflight` 失败，`tls_validation_failed`；只读诊断细分为 `tls_unknown_authority` | 未能确认空库，未执行迁移或任何应用写入；所有业务、行锁、备份、API/重启用例未执行 |
| `roam-postgres-test` | 两次空库检查通过；执行当前候选迁移，驱动退出 1 | `seed/schema_migration_and_cache` 失败，原分类 `test_failed`；只读迁移记录确认 version 5 为失败 | PostgreSQL 18.6；留下 17 张表（含迁移表），迁移表 7 行，其余 16 张业务表均 0 行；管理员/默认设置、合成业务数据、行锁、备份、API/重启均未执行 |

真实结果分别保存在 `driver-06/roam-mysql-test/`、`driver-06/roam-postgres-test/` 的 `preflight.json`、`result.json`；PostgreSQL 另有 `seed.json`。未产生真实数据库的备份或原始程序重启证据。失败路径的汇总 `result.json` 在延迟更新总耗时前落盘，故 `duration_ms=0` 不是执行耗时；逐检查和阶段 JSON 保留实际耗时，原文件不追改。

失败后只执行了独立的只读诊断，未再次调用 `db.InitDB`、迁移、缓存初始化或产品启动。诊断源码、离线构建/vet、输入及 exe 哈希、两库固定字段结果保存在 `diagnosis-01/`；MySQL 仅检查连接，PostgreSQL 使用 repeatable-read 的只读事务核对固定应用表和数字迁移记录，没有记录原始错误或连接参数。

PostgreSQL 的迁移记录为：3、4、8、10、11、12 成功，5 失败。源码 [migration 5](../internal/db/migrate/005.go) 在主 `AutoMigrate` 之后仍要求 `channels.key`，而当前 [Channel 模型](../internal/model/channel.go) 已将凭据拆至 `channel_keys`。保留的 SQLite 演练只读复核进一步确认：`channels` 实际没有 `key` 列，但 SQLite 驱动 `HasColumn("channels", "key")` 返回 true，migration 5 因此被误判为可通过。证据见 `diagnosis-01/sqlite-migration5-explanation.json` 及同名 Python 脚本；这说明既有 SQLite 通过不能证明该迁移兼容 PostgreSQL，不撤改其历史执行记录。

本轮没有关闭 TLS 校验、修改 profile/系统信任配置、修补产品或改写迁移状态，也没有清库、删表或重跑完整套件。PostgreSQL 已不满足本入口的空库前置条件；后续先修复 migration 5 并形成新候选，再由用户准备空目标，或针对准确对象另定重置范围。MySQL 需要在用户本地核对服务端证书链及可信 CA/profile 配置，不能用跳过校验解决。两者均无 `restore_database`，空库恢复继续待验。

## 独立入口及证据边界

源码为 [main.go](../scripts/acceptance/database/main.go)、[suite.go](../scripts/acceptance/database/suite.go)、[runtime_windows.go](../scripts/acceptance/database/runtime_windows.go)、[runtime_other.go](../scripts/acceptance/database/runtime_other.go) 和 [main_test.go](../scripts/acceptance/database/main_test.go)。[prepare-database-acceptance.ps1](../scripts/prepare-database-acceptance.ps1) 在新目录核对 B 的全部 487 条冻结输入，复制其中 177 条数据库/业务源码及模块输入，以已有缓存、`GOTOOLCHAIN=local`、`GOPROXY=off`、`-mod=readonly` 和 `-tags=jsoniter` 构建。没有安装或升级依赖，也没有关闭校验数据库。非 Windows 分支仅保持该验收包可编译，不承担其他 OS 的原始程序实测。

数据库集成驱动不是发布程序；迁移、业务操作、刷新事务、备份加密及导入使用冻结候选对应的原实现，凭据只在驱动内存及父子进程匿名管道中传递。原始 B Windows exe 的实际启动、API 和正常重启另外执行：其普通 `--config` 从一次性本地 Windows named pipe 读取，管道拒绝远程客户端并核对读取进程，连接串不落入文件、参数或环境。测试 HTTP 仅绑定 loopback，关闭启动价格更新，所有合成渠道明确不跟随后台自动刷新。

驱动默认不联网，只有显式 `--connect` 才读取已选 profile 并连接。只接受两个固定别名及对应引擎；目标在一次执行中固定在内存，后续进程不会重新读取变更后的档案。父进程只接受固定结构、字段和值的 JSON。原始 stdout/stderr、驱动错误、主机、用户名、库名、连接串及密码不写入报告；错误仅为允许的分类。数据库版本仅保留数字版本，不保留服务端附带文本。

## 本次已执行的数据库命令

取得下节的明确授权后，已使用最终准备目录中的驱动串行执行两个别名；MySQL 失败后继续执行了独立的 PostgreSQL 验收。正式运行前核对了 `driver-build-info.json`、可执行文件及源码哈希。以下是本次命令记录：结果子目录已经存在，PostgreSQL 已留下应用表，不能直接复制重跑或清理后重试。

```powershell
$repo = 'D:\tool\AI\octopus-roam'
$driverRoot = Join-Path $repo 'build/release-local/env-20261015-205423-4502ab5a/driver-06'
$build = Get-Content -LiteralPath (Join-Path $driverRoot 'driver-build-info.json') -Raw | ConvertFrom-Json
$driver = Join-Path $driverRoot 'database-acceptance.exe'
if ((Get-FileHash -LiteralPath $driver -Algorithm SHA256).Hash.ToLowerInvariant() -ne $build.driver_sha256) {
    throw 'Driver hash mismatch.'
}
if ((Get-FileHash -LiteralPath $build.product_executable -Algorithm SHA256).Hash.ToLowerInvariant() -ne $build.product_sha256) {
    throw 'Product hash mismatch.'
}
if ((Get-FileHash -LiteralPath $build.stop_script -Algorithm SHA256).Hash.ToLowerInvariant() -ne $build.stop_script_sha256) {
    throw 'Normal-stop helper hash mismatch.'
}
$shared = @(
    '--profile', 'C:\Users\Ink-lotus\.config\octopus-roam\db-profiles.json',
    '--executable', $build.product_executable,
    '--executable-sha256', $build.product_sha256,
    '--stop-script', $build.stop_script,
    '--expected-version', $build.candidate_version,
    '--connect'
)
foreach ($alias in @('roam-mysql-test', 'roam-postgres-test')) {
    & $driver @shared --alias $alias --output (Join-Path $driverRoot $alias)
    # 非零退出表示该引擎失败；保留其 JSON，继续另一个独立目标。
}
```

此命令需要读取仓库外 profile；沙箱不能读取时，仅为该已审查的执行范围使用沙箱外运行，不更改目录 ACL 或系统配置。命令不接受完整 DSN；真实目标字段均由驱动在内存中读取。

## 具体授权的目标与影响

目标限定为该 profile 中 `roam-mysql-test`、`roam-postgres-test` 对应的两个专用测试逻辑库。两者在写入前都要通过只读目录查询确认没有表/视图；PostgreSQL 同时检查当前 schema 的序列等关系。任一目标非空，该目标停止，**不迁移、不清理、不以增量导入冒充空库初始化**；即使其名称带 test，也不自动重置。此时先回报，再另定准确对象和处理范围。

本次明确授权的范围如下，仅 PostgreSQL 通过空目标检查后实际开始了迁移，其余用例是否执行以结果表为准：

1. 执行当前版本迁移、建表/索引、初始化默认设置及合成管理员。范围为 `db.InitDB` 的应用模型和 `migration_records`；不创建/删除逻辑库、账号或云资源。
2. 创建和修改本轮合成渠道、凭据、模型、授权、分组/成员、API Key、禁用 Webhook、隐私规则与路由/自动刷新 JSON 配置；设置 `stats_save_interval` 为 11，经原始程序 API 改为 13，正常重启确认后改回 11。所有上游地址仅为 loopback fixture，Key 均为合成值。
3. 在真实引擎上运行两连接行锁串行化，以及刷新成功、成功空列表、上游失败和事务内实际外键写入失败。模型刷新会删除/替换本轮合成模型、授权和成员；失败用例必须回滚并核对写入前后内容一致，刷新诊断记录按产品行为单独持久化。
4. 为快照一致性创建并删除一条本轮专用的合成渠道及其从属数据；导出期间并发提交该删除，核对导出仍包含完整旧快照。这是准确限定的合成行删除，不是清空测试库。
5. 导出口令加密的当前版本合成备份；验证错误口令和损坏内容拒绝解密；导入含非法关联的合成组与成员，确认实际数据库写入失败、此前同事务写入回滚。合成备份保留在本轮独立证据目录，不是实际数据库连接材料。
6. 独立进程重新打开数据库、重建缓存，核对禁用状态/JSON/关联；启动原始 B exe 两次，以 loopback API 核对版本、登录、设置、渠道、分组、错误口令导入拒绝及正常停止后的持久化。只管理本轮创建的进程；正常停止失败则该用例失败，必要时仅结束自身创建的测试子进程并如实记录。
7. 本轮不自动清库、删表、删库、改密、删 profile 或改云设置。应用表、迁移记录和合成数据保留，由用户在测试后重置；入口不假称能用一个外层事务撤销迁移/重启/已提交刷新。没有 `restore_database` 时不连接任何其他库，也不执行空库恢复。

未包含真实供应商调用、真实更新下载、Docker daemon/镜像操作、CI、Git 或远端仓库变更。若新增产品缺陷导致无法完成，保留失败证据并回报实际影响，不更改冻结候选。

## 全部平台的验收矩阵

| 平台 | 架构 | 已有证据 | 本轮/剩余状态 |
| --- | --- | --- | --- |
| Windows | amd64 | B 原始 ZIP 业务/SQLite、PS 7/5.1、归档及哈希；A3 更新/浏览器按相同源码复用 | 数据库入口及原始 exe 内存配置/正常重启的本地合成演练通过；真实 MySQL TLS、PostgreSQL migration 5 失败，云库业务/API/重启未执行 |
| Linux | amd64、arm64、arm、386 | B 交叉构建、真实许可证、ZIP 内容/权限与哈希 | 待各架构实机的启动、业务/SQLite、正常重启及真实 Unix 更新成功/失败恢复；本机 WSL 不可用 |
| macOS | amd64、arm64 | B 交叉构建、真实许可证、ZIP 内容/权限与哈希 | 待对应 macOS 实机，同样需要正常重启及 Unix 实际更新证据 |
| Android | amd64、arm64、arm、386 | 既有支持范围保留 | 未发现可用 NDK/SDK/adb 配置；构建、归档、运行及更新均待环境，未下载工具或扩大声明 |
| Docker | linux/amd64、linux/i386、linux/arm64、linux/arm/v7 | B context 的四个 Linux exe、真实许可证及本地交接 | 用户人工 C1–C9；缺少 daemon，不自动安装/拉取/构建 |

跨平台的实测必须绑定目标 OS/版本、CPU 架构、候选版本和归档/程序哈希，分别记录初始化、读写、备份、正常停止/重启、更新成功与 exec 失败恢复的结果。交叉编译、ZIP Unix 权限和 Windows 结果不替代这些项目。后续提供实机后在该机的新测试目录执行，不修改既有运行部署；没有实机继续标“待验”。

Docker 使用已有 [交接材料及 C1–C9](docker-local-test-handoff.md)，人工回传镜像 ID/版本/哈希、目标架构、Docker/Compose 版本及脱敏结果；暂未回传时保持待验。本轮不把 context 称作已生成镜像。

## 停止条件及发布状态

数据库入口完成离线验证、具体命令和影响可审查后，已按会话 AGENTS.md 的“数据库变更须先说明目标、命令、影响并获得明确授权”要求取得本次连接/迁移/合成行增删改授权，并完成两个别名的这次执行。该要求来自用户提供的会话规则；[数据库交接说明第 5 节](cloud-database-test-handoff.md#5-授权与第一轮边界)也按同一规则区分凭据和写入授权。本次因上述实际失败停止，已提交的应用表和迁移记录保留；新的清理、目标或配置变更不包含在本次范围内。

真实数据库失败、没有空恢复库、Docker 人工结果和其他平台实机时，不把第 4 项整体写成完成。许可证源码交付/再链接及真实发布链路门禁仍保留，`L6_5_strictly_passed=false`，不自动推进 CI 或远端发布。执行后重新核对 A2/A3/B 的 969/973/976 条文件哈希全部未变；本轮没有残留测试进程，原有仓库外 PID 2828 保留运行。脱敏执行汇总和最终证据索引见本轮 `authorized-database-execution-summary.json`。
