# 发布准备工作包 6：最终 RC 产物公开前验收

日期：2026-10-25。工作目录：`D:\tool\AI\octopus-roam`。范围：交接计划 D 的产品测试部分及[第 5 节测试矩阵](release-rc-handoff-20261021.md#5-剩余测试矩阵)。

**结论：按用户本次调整后的门禁，RC 公开前强制检查通过。** 最终 RC 的 TLS、C1、C2、Windows/SQLite、数据库主库/恢复库、WSLc Linux amd64 容器持久化和静态四架构镜像检查都有证据；Linux arm64/arm/386、macOS、Android 原生平台明确不纳入本 RC 原生验收门禁，WSLc 结果按本次门禁规则等同 Docker Engine/Compose/GHCR 容器验收。没有发现需要退回源码冻结、材料或构建的问题。门禁通过不等于本报告获得 tag、Release、镜像推送或公开发布授权；真实公开 Release 下载及 RC 间升级仍按工作包 8 处理。

## 读取的计划和历史输入

本包读取并以实际证据为准的当前计划/合同包括：

- [RC 交接计划](release-rc-handoff-20261021.md)；
- [主发布计划](release-plan-v0.1.0.md)；
- [发布 CI 合同](../docs/release-ci.md)。

工作包 3–5 的实际报告为 [工作包 3](release-step-03-result-20261023-01.md)、[工作包 4](release-step-04-result-20261024-01.md)、[工作包 5](release-step-05-result-20261025-01.md)。C1/C2 输入和修复报告为 [R2 C1/C2 修复结果](release-r2-p2-fix-result-20261021.md)及 [R2 本地差异审查](release-r2-local-review-20261021.md)；数据库修复/历史验收为 [数据库验收修复结果](release-database-acceptance-fix-result-20261017.md)、[环境验收报告](release-environment-acceptance-20261015.md)、[R0 本地数据库验收](release-local-database-acceptance-20261018.md)、[Windows/SQLite 与历史 A3 验收](release-acceptance-20261015.md)、[Docker 交付报告](release-docker-delivery-20261019.md)。历史候选结果只作范围和缺陷背景，不冒充本 RC 的实测。

## 授权、命令和隔离边界

用户已明确批准本次一次性写入和测试范围：只在新的 `build/release-local/step-06-rc-public-20261025-01/` 派生并验收最终 artifact；只在仓库外新建 `D:\tool\db-test\instances\octopus-roam\step-06-20261025-01\` 和 `D:\tool\db-test\profiles\octopus-roam\step-06-20261025-01\`，使用 MySQL `127.0.0.1:13316`、PostgreSQL `127.0.0.1:15442`；写入合成数据库、SQLite、备份和重启文件；保留旧目录、旧数据库和原 artifact。

执行入口和影响如下，均绑定上述新路径：

```powershell
python -B scripts/release_acceptance.py prepare ...
python -B scripts/release_acceptance.py verify-layout ...
pwsh -NoProfile -File scripts/prepare-database-acceptance.ps1 ... -SkipRehearsal
node scripts/test-release-acceptance.mjs <新派生目录> <新运行输出目录> v0.1.0-rc.1 5ec4dc5905149db03159d9d02c8f76646300d477 22d306092e4874f8c2ed6cea35dddaaf9397ea22a44fe76473e9eb5e65afb8aa 65640fab073bc98826a2d4e49f9bad2536d8c5fb3ff6a8a6709627d639b6e2b7
```

数据库编排仅创建本包实例、私有 CA/服务端证书、测试账号、两个主库和两个恢复库；迁移、合成刷新记录、备份恢复和重启均写入这些新库。价格任务全程关闭；更新源、上游、浏览器和数据库验收使用 loopback 合成服务；未访问公开 Release，未做真实 RC 间升级，未修改系统信任根、Git、远端仓库、CI 或环境持久配置。C2 使用隔离的更新/重启程序；真实产品程序和测试夹具证据分别保存。会话结束只正常停止本包自建进程；既有仓库外 `D:\tool\AI\octopus-roam-runtime\octopus-roam.exe` 未由本包操作（早先证据记录 PID `3492`，本次只读观察到同路径的现有进程 PID `21980`）。

## RC 身份、完整性和实际编译版本

工作包 5 的原始下载目录 `build/release-local/step-05-publish-false-20261024-01/artifact-download-01/` 保留未改。本包派生布局位于 `build/release-local/step-06-rc-public-20261025-01/checkout-final/build/release-local/acceptance-layout/`，`build-info.json` 仍记录原始 artifact/material/source 绑定。

| 项目 | 实际值 |
| --- | --- |
| 版本 / commit | `v0.1.0-rc.1` / `5ec4dc5905149db03159d9d02c8f76646300d477` |
| source tree / SOURCE_DATE_EPOCH | `ddd0aab81a355bbcc96b395178d2de3bb108113c` / `1792752156` |
| 发布 artifact ID / 名称 | `12564820530` / `release-v0.1.0-rc.1-5ec4dc5905149db03159d9d02c8f76646300d477` |
| `artifact.json` SHA-256 | `22d306092e4874f8c2ed6cea35dddaaf9397ea22a44fe76473e9eb5e65afb8aa` |
| 原始 artifact seal 清单 SHA-256 | `5f7b1aedeb73a5084fbe356ba94f02018e3acddd593092359614a269cb7a9ad3` |
| `build.json` SHA-256 | `5582167e1d3e943d27c3fa5850fd091509902815687e1ccd0aa5fd7420ca2ac0` |
| `materials.json` SHA-256 | `65640fab073bc98826a2d4e49f9bad2536d8c5fb3ff6a8a6709627d639b6e2b7` |
| 应用源码 ZIP SHA-256 | `e25b26a4f20f086ade4fcec939db7d66219ffb3fe07b48b824cb650793710995` |
| Windows exe SHA-256 | `dba7242dafcf992794f1377ef49065fe7b9148e8b598ef23cd5c5bc06632017b` |
| Windows ZIP SHA-256 | `19739b687b0dd28a9dabf62d2b184f78c431027d0e4e319d6a10375fe4b4678f` |
| 实际编译版本 | `go1.27.3` |
| Windows `go version -m` | `vcs.revision=5ec4dc5905149db03159d9d02c8f76646300d477`，`vcs.modified=false` |

本包重新读取实际 Windows exe 的 `go version -m`，确认 `go1.27.3`、`GOOS=windows`、`GOARCH=amd64` 和冻结 commit。原始 artifact 的 11 个程序、11 个 ZIP、四架构镜像及 seal 仍以工作包 5 的下载实物和清单为准。

### 布局核验的失败与通过证据

原生 Windows `verify-layout` 在 `verify-layout-step06.log`、`verify-layout-step06-final.log` 中以退出码 2 报 `Published checksums differ`。该失败来自 Windows `Path`/POSIX 排序兼容，不是 artifact 字节损坏；交付文件实际哈希与发布清单匹配。工作包 5 在 Linux 的原始合同 verify 已退出 0，本包使用 POSIX 适配器重新执行并在 `verify-layout-step06-adapter.log` 以退出码 0 输出 `status: layout_verified`。这些日志、`.code` 文件和适配器源码均保留。报告同时保留原生失败证据，不把它改写为产品通过或删除失败材料。

## TLS、更新元数据和资产下载

`tls-update-acceptance-01/result.json` 与 `go-test.log` 是最终候选源码生产更新函数的独立 HTTPS 夹具证据。使用新生成的私有 CA 和服务端证书，合法自定义 CA 成功请求了：

- `/releases?per_page=30`；
- `/download/v0.1.0-rc.2/SHA256SUMS`；
- `/download/v0.1.0-rc.2/octopus-roam-linux-amd64.zip`。

错误 CA 和错误主机名均得到 `status=request_failed`，没有降级到明文。夹具观察到的请求路径和资产校验值保存在该 `result.json`。这是生产更新函数/最终候选源码的夹具证据；C2 的最终 Windows 程序证据另见下节，不能把两者混成一条证据。

普通 TLS 正反例只能证明应用路径兼容性，不能单独证明本次 CVE 修复。修复绑定证据为：`build/release-local/step-01-go1273-20261022-01/evidence/go-official-vulnerability.json`（官方 `GO-2026-6809`/`CVE-2026-111943`，影响 `crypto/x509`，修复版本含 Go 1.27.3）、`go-official-release.json`（官方 Go 1.27.3 发布索引及归档 SHA）、`go-toolchain.json`（实际工具、编译器、链接器为 go1.27.3；Windows 归档 SHA `f26081f438319757f8a1b46d7d0b2c5ec7583d4038e485e0aa725cfde7885fff`）和 `go-x509-source-observation.json`（官方归档中的拒绝链实现）。最终 exe 的实际 `go version -m` 与上述工具链和冻结 SHA 一致。

## C1：最终程序、MySQL/PostgreSQL 和无查询参数 DSN

数据库执行汇总为 `build/release-local/step-06-rc-public-20261025-01/database-execution-resume-08/execution-summary.json`；C1 细节在 `database-execution-resume-08/c1-noquery/result.json`、`observed.json`、`config-redacted.json` 和 `normal-stop.json`。真实执行使用新 MySQL 8.4.11、PostgreSQL 18.6 实例及新私有 CA，未复用历史库。

| 检查 | 实际结果 |
| --- | --- |
| MySQL/PostgreSQL 合法证书链、自定义 CA、TLS 身份 | 通过 |
| 明文、错误 CA、错误主机名 | 均拒绝 |
| 主库和恢复库 migration 3–13 | MySQL、PostgreSQL 均通过 |
| 备份、空库恢复、跨进程持久化、正常重启 | 通过 |
| 最终程序 C1 | 通过 |

C1 的最终程序连接 MySQL 主库，配置使用自定义 CA 和不含查询参数的 DSN。启动时读取了既有合成刷新记录：`channel_id=910002`、状态 `success`、`started_at=2026-10-25T13:52:51.0740000+08:00`、`finished_at=2026-10-25T13:52:51.0920000+08:00`，时间顺序有效；`stats_save_interval=11`；配置观察到 `dsn_has_query_parameters=false`、`database=mysql_primary`。这同时证明了时间字段读取和 C1 默认连接值。

中间失败证据 `database-execution-01/`、`database-execution-resume-01/` 至 `07/`（其中 resume-04 在最终程序阶段失败）全部保留；数据库业务检查随后由 resume-08 修正并通过。失败没有被覆盖，也没有清理旧实例或旧数据库。两个新实例结束时均正常停止，`13316` 和 `15442` 无监听。

## C2：最终 Windows 程序、浏览器和隔离更新/重启

最终 Windows 运行证据在 `windows-final-20261025-04/runtime-acceptance/`。使用派生布局中的原始 Windows exe/ZIP 完成解包哈希、普通和流式请求、失败切换、取消、日志/SSE、精确统计、正常停止、重启持久化以及隔离更新/重启；更新 helper、旧进程退出、替换、恢复和设置保留均通过。 `release-source-requests.json` 只记录 loopback 合成发布源的请求。

真实 Edge headless（隔离 CDP profile）证据为 `browser-acceptance-01/evidence-06/summary.json`，其中 `visible_document=true`。三个场景均通过：

| 场景 | 目标/请求 | 结果 |
| --- | --- | --- |
| success | 空 body `400`；过期目标 `409`；刷新后目标 `v0.1.0-rc.2`；POST `{"target_version":"v0.1.0-rc.2"}` | 回执、pending、成功和轮询停止通过 |
| rollback | 同上 | 失败回执、失败状态和轮询停止通过 |
| unconfirmed | 同上 | 30 秒未确认超时和轮询停止通过 |

每个场景均在刷新后再次确认目标；`phase_states` 分别为 6、5、5，最终状态不会继续轮询。 `v0.1.0-rc.2` 只是假设的 loopback 目标，未访问公开 Release，也不构成真实 RC 间升级证据。旧浏览器顺序脚本的失败保留在 `browser-acceptance-01/evidence/failure.json`、`evidence-02/failure.json`、`evidence-04/failure.json`；`windows-final-20261025-03/runtime-acceptance/` 的失败不能作为通过证据。

## Windows/SQLite 最终包

`windows-sqlite-final-20261025-02/` 使用最终 Windows exe、新 SQLite 数据目录和关闭价格任务完成初始化、登录、设置、渠道、分组、API Key、禁用状态、加密备份、正常重启持久化、错误口令/损坏导入回滚、空库恢复、禁用状态恢复和正常停止。当前备份 `primary/backup.encrypted.json` 的 SHA-256 为 `9a96c32fb43acc4ebf123e7c8d5306cb7d161acb01423410edb05cbae599635b`。 `windows-sqlite-final-20261025-01/` 因没有发布源而得到 `no_release`，作为失败证据保留；没有覆盖或清除它。

## 容器和平台矩阵

四个已构建镜像 tar 的静态核对通过，实际哈希为：

| tar | SHA-256 |
| --- | --- |
| `386.tar` | `ef23a4c8029ae4e04b9f3efa6574eda61c22516bc1957a6b89f8ee75e82b293c` |
| `amd64.tar` | `3c814bd1d8c134d1be6a345edc89711dd5b4563e20eb75119132bd48864c2428` |
| `arm.tar` | `2edb316f87be85eae991bd51120c817e4ace8b1acc8432346cea2b02bba2d8f6` |
| `arm64.tar` | `baeb3c2bc6961a20bdd3c4563969620bd164ea1b80f4587c414293aec9ca84ff` |

Compose 静态核对确认 `/path/to/data:/app/data` 持久化挂载和 `restart: unless-stopped`；entrypoint 为 `/app/octopus-roam start`。早先的 `container-platform-check-20261025-01/result.json` 仍保留其非管理员 shell 下 `blocked_no_container_runtime` 失败证据；该结果不再代表扩大权限后的 WSLc 能力。

本轮在管理员权限下使用 `C:\Program Files\WSL\wslc.exe` 3.0.3.0，实际加载并检查最终 RC amd64 镜像：镜像 ID `sha256:c3edfb7b8e426a136f888c94c13350673c4e8fd16344dbe312b677d723d43ad3`，标签 `ghcr.io/ink-lotus/octopus-roam:v0.1.0-rc.1-amd64`，对应 amd64 tar SHA-256 `3c814bd1d8c134d1be6a345edc89711dd5b4563e20eb75119132bd48864c2428`。在新 `/app/data` 目录和 `127.0.0.1:18106` 上启动主容器，价格任务以 `OCTOPUS_ROAM_SKIP_STARTUP_PRICE_UPDATE=1` 关闭；根页面、版本、合成设置/渠道/分组/API key 和加密备份均通过。主容器正常停止后用 `wslc start` 重启，数据仍在；再以同一数据目录和 `127.0.0.1:18107` 创建新容器，数据仍在。两个容器最终 `Exited (0)`，端口已释放，日志扫描没有 panic/fatal/exception/runtime error。完整证据见 `container-platform-check-20261025-02/container-acceptance-summary.json`、`image-inspect.json`、`primary-check.json`、`post-restart-check.json`、`post-recreate-check.json`、状态/日志和 `run-data/`；原容器、镜像和数据均保留。按本次用户门禁规则，该 WSLc 实测作为 Docker Engine/Compose/GHCR 容器验收的等价证据。

本轮实际 WSLc 操作均在管理员 PowerShell 7 中执行，核心命令为：

```powershell
wslc load --input D:\tool\AI\octopus-roam\build\release-local\step-05-publish-false-20261024-01\artifact-download-01\images\amd64.tar
wslc inspect --format json c3edfb7b8e42
wslc run --detach --pull never --name octopus-roam-step06-wslc-20261025-02 --publish 127.0.0.1:18106:8080 --env OCTOPUS_ROAM_SKIP_STARTUP_PRICE_UPDATE=1 --env PUID=65532 --env PGID=65532 --volume "D:\tool\AI\octopus-roam\build\release-local\step-06-rc-public-20261025-01\container-platform-check-20261025-02\run-data:/app/data" ghcr.io/ink-lotus/octopus-roam:v0.1.0-rc.1-amd64
wslc stop --time 30 octopus-roam-step06-wslc-20261025-02
wslc start octopus-roam-step06-wslc-20261025-02
wslc run --detach --pull never --name octopus-roam-step06-wslc-recreate-20261025-02 --publish 127.0.0.1:18107:8080 --env OCTOPUS_ROAM_SKIP_STARTUP_PRICE_UPDATE=1 --env PUID=65532 --env PGID=65532 --volume "D:\tool\AI\octopus-roam\build\release-local\step-06-rc-public-20261025-01\container-platform-check-20261025-02\run-data:/app/data" ghcr.io/ink-lotus/octopus-roam:v0.1.0-rc.1-amd64
wslc stop --time 30 octopus-roam-step06-wslc-recreate-20261025-02
```

两次 `--volume` 使用同一个新 `run-data` 绝对路径；没有使用旧数据目录或旧容器。

| 平台 | 实际结果 |
| --- | --- |
| Windows amd64 | 通过最终包和运行验收 |
| Linux amd64 | 通过 WSLc 3.0.3.0 管理员权限实测；同一数据目录的停止/重启/换容器持久化通过 |
| Linux arm64/arm/386 | 本 RC 不纳入原生平台验收门禁；保留交叉构建和静态材料结果 |
| macOS amd64/arm64 | 本 RC 不纳入原生平台验收门禁 |
| Android 四 ABI | 本 RC 不纳入原生平台验收门禁；保留四 ABI 构建/归档材料结果 |
| 容器启动/持久化/重启 | 通过 WSLc Linux amd64 实测，并按本次门禁规则等同 Docker Engine/Compose/GHCR 验收 |

## 第 5 节矩阵和强制门禁

| 第 5 节项目 | 结果 | 证据/剩余边界 |
| --- | --- | --- |
| TLS、元数据、资产下载 | 通过 | 自定义 CA 成功；错误 CA/主机名拒绝；CVE 另有官方说明、工具来源和实际编译版本绑定 |
| C1 真实 MySQL | 通过 | 最终程序、无查询参数 DSN、既有刷新记录、时间字段和默认值 |
| C2 页面与更新确认 | 通过 | Edge 真实文档、400/409、刷新重试、回执、pending/成功/失败/超时、停止轮询、隔离重启 |
| Windows/SQLite 最终包 | 通过 | 当前备份恢复、错误导入回滚、重启持久化和正常停止 |
| MySQL/PostgreSQL 主库/恢复库 | 通过 | TLS、migration 3–13、备份恢复、持久化和正常重启 |
| 四架构镜像静态材料 | 通过 | tar、程序、APK/源码映射和 Compose 静态项 |
| 容器启动/持久化 | 通过（WSLc 等价容器门禁） | 管理员权限下最终 RC 镜像启动、合成写入、正常停止、`wslc start` 重启、同数据目录换容器均通过；按用户规则作为 Docker Engine/Compose/GHCR 等价证据 |
| Linux/macOS/Android 原生平台 | 不纳入本 RC 门禁 | 用户明确本 RC 不进行这些原生平台验收；正式版或支持范围变更时另行决定 |
| 真实公开 Release 下载及 `rc.1 → rc.2` 升级 | 延后 | 明确留给工作包 8，不在本包提前执行 |

### 失败证据、明确排除项和处理决定

必须保留的失败/中间证据包括：原生 Windows verify 的两个退出码 2 日志、数据库 resume-01 至 07、无发布源的 SQLite `windows-sqlite-final-20261025-01/`、旧浏览器/运行顺序失败目录和早先非管理员 WSLc `container-platform-check-20261025-01/result.json`。这些失败没有改变原 artifact，也没有被删除或覆盖；管理员 WSLc 补验以新的 `container-platform-check-20261025-02/` 证据单独记录，并依据本次门禁调整判定通过。

本包没有发现源码缺陷、artifact 字节不完整、材料哈希不匹配或实际编译版本漂移，因此**不退回源码冻结、材料生产或工作包 5 构建**。本次明确的原生平台不验收项不构成本 RC 阻塞；若正式版或公开支持范围后来纳入这些平台，再另行建立验收任务并同步文档/材料门禁。若后续发现需要改源码或重建输入，必须重新退回冻结、材料和构建流程，不能直接修改本包候选。

**RC 公开前强制检查：通过（按本次门禁调整）。** 本报告不执行 tag、Release、镜像推送或其他远端写操作；真实公开 Release 下载及 `rc.1 → rc.2` 升级仍留给工作包 8。

本报告新建后只做文件存在、内容和 Git 状态只读核对；不 commit、不 push、不创建 tag/Release、不删除任何旧目录、数据库或失败证据。派生 checkout 中的 `internal/update/tls_acceptance_step06_test.go` 是本包验收夹具，不属于产品源码修复、`acceptance-layout` 或原 artifact；夹具和最终程序证据分别记录并保留。
