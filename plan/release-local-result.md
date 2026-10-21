# v0.1.0 第一轮本地结果

日期：2026-10-19 更新（当前 mysqlfix 候选、两库/WSLc 验收、应用及 Alpine 本地材料；下文 dbfix、B 组与 A2/A3 历史记录继续保留）

当前结果：当前候选为 `build/release-local/mysqlfix-20261018-03/`；两种本地真实数据库各 35/35、应用源码/许可证与 LGPL 再链接材料、WSLc Linux amd64/SQLite 容器补验及镜像 tar 交付均已完成，分别见[数据库报告](release-local-database-acceptance-20261018.md)、[应用许可证报告](release-license-materials-20261019.md)和[Docker 报告](release-docker-delivery-20261019.md)。本轮另补齐[Alpine 本地源码/声明](release-alpine-materials-20261019.md)：18 包、11 origin、139 配方文件及 8 源码归档、126 项源码输入和实际附件离线核验通过。旧镜像的包名/架构清单错误由独立附件纠正，程序、镜像与应用源码保持冻结。

**交接结论：本地材料完成，下个会话可推进 R1 的 CI 准备。** 具体 workflow 修改仍按 AGENTS.md 审查补丁并取得对应授权。公开源码入口和真实下载、线上 Actions、GHCR/Release、其他平台实机及两个远端版本间升级按后续阶段待验；这些结果不作为进入 R1 本地准备前的总门禁。本轮未修改 CI、依赖或远端状态，未 commit。

历史 2026-10-17 dbfix 阶段：数据库本地修复、default/jsoniter、vet、相关 race、七目标构建和独立 SQLite 演练完成；当时候选为 `build/release-local/dbfix-20261017-01/`，998 项文件与 497 项冻结输入核对通过，独立 SQLite 34 项通过、行锁待真实两库。详见[历史本地修复报告](release-database-acceptance-fix-result-20261017.md)。该阶段未连接真实数据库的边界不代表后续 mysqlfix 验收状态。

以下为 2026-10-15 B 组历史范围与结果：按具体授权隔离安装固定许可证工具、生成真实报告、离线重建新候选及本地验证；详情见[B 组执行报告](release-license-tools-20261015.md)。A3 文档收口、关键 race 和生产更新/浏览器结果保留在[历史补充验收报告](release-acceptance-20261015.md)。

B 组阶段结论：当时的候选为 `build/release-local/b-final-20261015-181705-6ebd8c65/`，版本为 `v0.1.0-local.b-final-20261015-181705-6ebd8c65`。固定 `go-licenses v2.0.1` 已安装，真实 CSV 的 460 条记录、正文/NOTICE 和来源说明已进入七个 ZIP 与 Docker context，976 条文件哈希和 487 条源码输入均匹配。新包 PS 7/5.1 SQLite、普通/流式、取消/切换、日志/SSE、精确统计及重启持久化通过；新跑 vet、前端 lint/TypeScript/生产构建，普通/jsoniter/race 沿用相同源码证据。A3 生产更新/浏览器三终态和页面正文证据保留，该阶段未重跑。许可证分发条件、Docker、真实数据库、Unix 实机和远端升级仍有待办，L6.5 不自动判为严格通过。该阶段停止在 B 组范围内。

后续第 4 项更新（2026-10-15）：两个指定别名已按明确授权执行真实数据库验收，均失败。MySQL 在迁移前因证书链不受信任停止；PostgreSQL 18.6 在 migration 5 失败，留下 17 张表（含迁移表）和 7 条迁移记录，其余 16 张业务表均 0 行。只读定位确认旧 `channels.key` 前置条件与当前模型不符，SQLite 列检测误判掩盖了问题；完整业务、行锁、备份及云库程序/API/重启未执行。详见[环境验收报告](release-environment-acceptance-20261015.md)。下文 B 组的“未连接/未读 profile”是该阶段历史边界；不能替代这次结果。A2/A3/B 冻结文件均未改变。

## 边界与基线

- 分支：`test`
- 基线 HEAD：`948de764ae9a034fd72899686d24dcc4ecad7fa5`
- 工作树：包含本轮未提交修改；没有执行 commit、分支切换、标签、push、PR/MR、Release、镜像发布或远程仓库 API 操作。
- L6.5 新增授权：`scripts/build-local.ps1`、`scripts/build.sh` 的局部补丁，以及全新独立目录中的本地候选离线重建。没有修改 GitHub Actions；`.github/workflows/build.yaml` 和 `.github/workflows/release.yaml` 保持原样。
- B 组具体授权：安装 `go-licenses v2.0.1` 及其固定工具依赖到本轮目录；生成报告、新建候选、隔离合成 SQLite/loopback 验收及指定交接文档局部更新。产品依赖、锁文件和构建脚本没有新增修改。
- 网络边界：B 工具获取使用公共 Go proxy 和校验数据库；补查许可证来源使用 TinyFish。业务验收仅使用 loopback，显式关闭启动价格任务；没有连接真实数据库或操作真实发布源，未读取真实 profile。

## L0：环境与隔离

| 项目 | 实际状态 |
| --- | --- |
| Go | `go1.27.0 windows/amd64`，使用仓库 `.tmp-go-cache` 与 `.tmp-go-mod` |
| Node.js / pnpm | `v24.18.0` / `11.15.1`，`web/node_modules` 已存在 |
| go-licenses | 固定 v2.0.1，隔离在 B 证据目录；工具哈希和模块依赖版本已核对，不修改系统 PATH |
| Git | 可用；当前为 `test` 分支 |
| Docker / Podman / BuildKit | PATH 中均不可用，未接触 daemon、未拉取镜像 |
| MySQL / PostgreSQL 客户端 | `mysql`、`psql` 均不可用；没有本地实例或可用云 profile/授权 |
| actionlint | 不可用；未联网安装，未声称 Actions 通过线上验证 |
| Go 离线限制 | 本轮保留合法 GOSUMDB 配置，使用 `GOTOOLCHAIN=local`、`GOPROXY=off`、`-mod=readonly` 和已有缓存；历史 A2 使用过 `GOSUMDB=off`，它关闭校验数据库查询，不等于关闭 go.sum 内容校验 |
| 启动联网路径 | 生产默认仍会执行价格任务；本地脚本显式设置 `OCTOPUS_ROAM_SKIP_STARTUP_PRICE_UPDATE=1` 后才启动隔离验收进程 |

## 2026-10-15 A3 与 B 补充结果

A3 七平台哈希、更新和浏览器逐用例结果与失败记录见[历史补充验收报告](release-acceptance-20261015.md)。B 当前候选的哈希、许可证正文校验、工具来源及新包验收见[B 组报告](release-license-tools-20261015.md)与独立 `b-licenses-20261015-181705-6ebd8c65/` 证据目录。三份候选均保持冻结；下文 A2 哈希表只作历史记录。

## L1–L3：实现与文档

已完成以下本地工作：

- 整理 fork 起点、选择性上游同步、独立版本线和首发范围，新增上游功能对照与 v0.1.0 Release Notes 草稿。
- 更新中英文 README、`CHANGELOG.md`、升级说明和本地部署说明；Compose 要求显式 `OCTOPUS_ROAM_VERSION`，不再把未发布的 `latest` 当作可用安装示例。
- 新增 `scripts/build-local.ps1` 离线构建入口，默认覆盖本轮可交叉编译的 Windows/Linux/macOS/ARM 目标，拒绝覆盖已有非空输出并恢复进程环境变量。
- 恢复独立自更新：发布源固定为 `Ink-lotus/octopus-roam`，稳定版/RC 版本比较独立，下载同版本归档和 `SHA256SUMS`，校验大小、路径、符号链接、重复文件、包内容和 SHA-256 后才替换。
- Unix 使用备份后原子替换；Windows 通过本地 PowerShell 辅助进程等待旧程序退出再备份、替换和重启。实际 helper 测试覆盖特殊参数/工作目录、支持文件备份、成功替换、启动失败回滚和旧程序恢复；失败状态可由重启后的 API/UI 展示。
- 更新 UI 在更新后短时间轮询当前版本和发布状态；容器、开发版、无独立 Release、无更新、不支持平台、更新忙和失败状态均有 API/UI 展示。
- 新增 `internal/dbprofile` 严格别名 profile 解析和 `db-check` 入口。默认只做离线校验；`--connect` 才会连接指定目标、执行迁移和读写冒烟，并且不打印密码、DSN、主机、账号、库名或原始错误。
- 新增 `scripts/test-runtime-sqlite.ps1` 隔离完整程序流程；使用临时目录和合成数据，不触碰现有运行实例。
- 仅新增 `plan/release-workflow-proposal.md` 作为 CI 提案；未修改任何 workflow 文件。

## A1：复审问题修复状态

| 复审项 | 修复/检查状态 | 证据与边界 |
| --- | --- | --- |
| C1/C2 Windows 握手、Unicode 参数 | 已修复；真实调度/helper 链路、中文与空格路径通过 | 本轮 runtime-acceptance 三场景实际替换/恢复、重启、PID/版本/哈希/设置确认；发布源及 RC 版本以 overlay 注入，真实远端待验 |
| C3 MySQL 驱动默认认证/TLS | 已修复并覆盖 DSN 默认值和 CA/TLS 名称 | `internal/dbprofile` 测试、A2 Go 回归；未连接真实 MySQL |
| C4 db-check 保留字写入 | 已改为 GORM 方言安全的无数据变化写入 | `cmd` 测试、A2 Go 回归；MySQL 真库语法/权限仍待环境 |
| C5 默认 profile 路径 | 已与交接统一为个人目录 `.config/octopus-roam/db-profiles.json`，保留 `--profile` 覆盖 | `cmd` 测试和云数据库交接说明；未读取真实 profile |
| C6 db-check 失败退出码 | 已使用 `RunE` 保持 JSON 脱敏输出并返回非零失败码 | `cmd/dbcheck_test.go`、A2 Go 回归 |
| C7 Unix exec 失败恢复 | 已增加备份恢复和非零退出路径 | Go 定向测试；本机未执行 Unix 实际进程更新 |
| C8 Target 子集 Docker context | 已按请求的 Linux 目标生成 context；无 Linux 目标时标记不适用 | A2 候选 context 含 4 个 Linux 目标；脚本语法通过 |
| C9 更新 UI 状态 | 已补 Zustand 订阅及目标版本确认，三种终态通过 | 本轮 visible Edge 的 pending/成功/回滚/未确认和停止短轮询记录；日志详情正文另由后续可见浏览器交互通过 |

## L4：最终回归

历史 L6/A2/A3 证据继续保留。B 新候选的全部 487 条源码/依赖/构建脚本输入与 A3 完全相同；普通/jsoniter/五包 race 按此复用并在独立 B 证据 `regression-provenance.json` 记录源日志和 SHA-256。B 重新运行发布条件 jsoniter vet、冻结前端 lint、TypeScript/Vite、PS 7/5.1 SQLite 及原始新包业务验收。B 的版本/时间和外部许可证输入改变已通过七平台构建、归档和最终包检查覆盖。根目录的无约束 `go test ./...` 会扫描历史 build 副本，不能作为当前产品回归入口。

| 检查 | 结果 |
| --- | --- |
| 普通产品包 Go 回归 | 复用 A3 通过日志，显式包集合 `. ./cmd ./internal/... ./static`；487 输入一致 |
| jsoniter 全产品包回归 | 复用 A3 通过日志及原来源说明；487 输入一致 |
| 五关键包 race + jsoniter | 复用 A3 通过日志；Go 输入一致 |
| `go vet -tags=jsoniter . ./cmd ./internal/... ./static` | B 冻结源码重新运行通过 |
| PS 7 / PS 5.1 最终包 SQLite | 本轮两者均通过 |
| 冻结前端 ESLint、TypeScript | 本轮通过，当前源码与冻结输入一致 |
| 冻结前端直接调用 Node/TypeScript、Vite 生产 build | 最终候选重新执行，通过 |
| Windows helper 解析及实际替换/回滚进程测试 | 通过 |
| Windows 菜单、自启动、路径引用、正常停止 | 完成（本地） | 菜单/自启复用既有测试；本轮最终包和更新实例使用正常 Ctrl+Break 退出，未改真实自启项 |
| PowerShell 脚本解析、locale 键一致性、文档链接检查 | 通过 |
| `git diff --check` | 通过 |

历史 A2 使用过 `GOSUMDB=off`；本轮没有新增该设置，也没有将其写成非法的 `on`。当前构建使用已有缓存、`GOPROXY=off`、`GOTOOLCHAIN=local`、`-mod=readonly`。前端直接调用已有 node_modules 内的工具，不安装依赖。实际命令、执行目录、退出码与证据复用边界见新候选和本轮验收报告。

首次候选 `l65-final-20261011-2345-8c2b` 的默认并发 `jsoniter` 套件曾在 `TestRouteDefaultsAPIContractResetAndEvents` 超时失败，该候选未完成验收。单独重复该用例 25 次通过；整个 handlers 包以 `-count=5` 诊断时还暴露共享夹具的重名/路由注册问题，并再次出现事件超时。代码审查发现分组事件缓冲容量为 16，积累的分组及消费调度可能触发拥塞关闭；最终必需的 `-count=1` 套件改用 `-p=1` 后通过，没有跳过用例或修改产品代码。相关失败日志保留，测试稳定性仍是需要后续处理的风险。

## L5：产物与验收矩阵

### A2 历史产物（非当前交付）

历史 A2 目录：`build/release-local/a2-20261012-193213-9b75afef/`。下表保留其已纠正的实际哈希，不能用于核对当前候选。当前交付为 `build/release-local/dbfix-20261017-01/`；七平台、998 项文件和 497 项源码输入的核对见[本地修复报告](release-database-acceptance-fix-result-20261017.md)及新候选 `build-info.json`、`file-sha256sums.txt`、`source-input-sha256sums.txt`。B 的历史校验见[B 组报告](release-license-tools-20261015.md)，所有旧候选继续保留。

| 平台 | 二进制 SHA-256 | 归档 SHA-256 |
| --- | --- | --- |
| Windows amd64 | `f83e3248a5e3acb7023291f2394f401bd25f1aa4f7ca254b370e0f06995ab5e0` | `c8e796e86edb202b22f521117866c06b0751e26c69f3533d8313ca1df24e3301` |
| Linux amd64 | `16168144bba56a6ae68b6ce0f19020f25ec44508420c7c4ad83d598ae0ccc1bc` | `20bdd863098c5481e4db67635a269f0a9437343f45e26cb0014632326548fe49` |
| Linux arm64 | `e48e5311813cce661b674dd08e9c8e7e4acd90fa6344d37c9789a79d707279ae` | `763b537b87f57b9078d3588ba082412d429c16a82f64934894b887096dffc9be` |
| Linux arm/v7 | `4e6023f1632e26917cf7b90a4266a1e6136db83018af5c4dea579f514d7dba03` | `6a5ac9357c892f2530ae31cf91a65f7eb7e69910bf788c02503f95fb1914b372` |
| Linux 386 | `eb44247f2b8446d3c456516852f78da25f845d406704653e49145717af9debb8` | `4c01e7832e9b60f2b9052f37fef37a17409d82e3a13a3c7a6f3a02ab3137ffd2` |
| macOS amd64 | `413033174b4c33d1a303e4be287dfc95b3695a9de0d022793cb5f7d1c5694013` | `50de68e9332dbccd44c6015d9528ff26a42c94948e1051481870d94e6f6b7d01` |
| macOS arm64 | `6c97ebb99071b003ecfc9979f331011a489fa60c50c0333c9fe25728a9d8e9c2` | `b46a9da71235488265f7b5b6a47fc91ebe154784c4f039142c8a7c5743f6f56a` |

A2 历史 `build-info.json` 记录版本 `v0.1.0-local.a2-20261012-193213-9b75afef`、构建时间、HEAD、工作树状态、七个目标、源码输入清单、归档和验收结果。7 个 ZIP 的 creator 为 Unix，可执行文件 mode 为 `0100755`，其他文件为 `0100644`，没有符号链接；969 条候选文件哈希和 485 条源码输入哈希均为 0 个不匹配。没有许可证工具或非空报告，`license_report` 明确为 unavailable，未伪造空报告。Linux/macOS 仅证明交叉编译、目标元数据与归档合同，未在目标 OS 执行；Android/NDK 未构建。Docker context 包含 4 个 Linux 目标和本地构建材料，但缺许可证，未生成镜像或镜像 tar；云端交接还需按 [Docker 人工交接材料](docker-local-test-handoff.md)核对程序执行权限并完成 C1–C9。

历史 A2 的构建/验收命令保留在其 `checks/logs/`。下面仅示例使用已冻结 A2 verifier 新建独立候选；该 verifier 不含本轮新增 race、最终包业务及完整生产更新链路，不能据其通过宣布当前门禁全部满足。执行前审查输入、verifier 和本轮报告；示例包含合成 SQLite 写入，输出目录必须为全新目录：

```powershell
$batch = 'local-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '-' + [guid]::NewGuid().ToString('N').Substring(0, 8)
$output = Join-Path $PWD ('build/release-local/' + $batch)
$verifier = Join-Path $PWD 'build/release-local/a2-20261012-193213-9b75afef/checks/verification-tools/verify.ps1'
pwsh -NoProfile -File scripts/build-local.ps1 `
  -OutputRoot $output `
  -Version ('v0.1.0-local.' + $batch) `
  -VerificationScript $verifier
```

### 功能与数据库矩阵

| 项目 | 状态 | 证据/限制 |
| --- | --- | --- |
| SQLite 初始化、登录、CRUD、禁用状态和加密备份恢复 | 完成（本地） | 新候选 PS 7 / PS 5.1 两者通过；独立空库核对关联、设置和禁用状态 |
| SQLite 重启持久化、空库恢复和失败回滚 | 完成（本地） | 新候选两个 PowerShell 版本通过，错误口令/损坏导入被拒绝且原库保留 |
| Windows 菜单、自启动、路径引用、正常停止 | 完成（本地） | 菜单/自启复用既有测试；本轮最终包和更新实例使用正常 Ctrl+Break 退出，未改真实自启项 |
| Windows 隔离更新（Flat/Packaged） | 完成（本地） | `test-update-local.ps1` 与 Go helper 实际进程测试 |
| 独立更新发布源与归档安全校验 | 完成（本地） | 现有安全检查及本轮 loopback → 生产 API/调度/helper → 实际替换/恢复 → 新进程/版本确认；真实 Release 待验 |
| profile 别名离线检查 | 完成（本地） | `internal/dbprofile` 单元测试和 `db-check` 命令 |
| MySQL | 真实验收失败 | 已授权执行，迁移前 `tls_validation_failed`，只读细分为 `tls_unknown_authority`；无应用写入；见环境验收报告 |
| PostgreSQL | 真实验收失败 | 18.6 空库检查通过，migration 5 失败；保留应用表和迁移记录；后续业务及恢复未执行 |
| Docker C1–C9 | 待用户 Docker 验收 | B context 已含真实报告和四个 Linux binary；本地 staging/构建命令及模板已同步，未运行 daemon/构建镜像 |

## L6.5：最终候选收口

| 检查 | 状态 | 实际证据 |
| --- | --- | --- |
| 构建输入快照 | 完成（本地） | B 候选 487 条冻结源码输入与 A3/工作区一致；新增外部报告的哈希另由 8 份副本、全文件清单和独立校验记录覆盖；依赖复现仍需已有缓存 |
| 七目标重新构建 | 完成（本地） | `v0.1.0-local.b-final-20261015-181705-6ebd8c65`，目标元数据、jsoniter、CGO=0 和 ARMv7 均核对 |
| 归档路径/重复文件/符号链接检查 | 完成（本地） | 7 个 ZIP 均解压并与 package 清单比较；未发现路径穿越、重复路径或符号链接 |
| Unix creator/executable mode | 完成（本地） | 7 个 ZIP 均以结构化中央目录检查确认 Unix creator、`0100755` executable 和 `0100644` 文档/支持文件 |
| 哈希与证据路径 | 完成（本地） | B 976 文件、487 冻结/工作区输入均匹配；A2 969/485 与 A3 973/487 全部未变；新证据独立保存 |
| 受影响 Go/前端回归 | 完成（本地） | B 新跑 vet、lint、TypeScript、Vite；Go/race 的相同输入与复用来源已记录，不称作全部重新执行 |
| 最终候选 SQLite | 完成（本地） | 从新 ZIP 解压，在 PS 7 与 PS 5.1 完整验证初始化、CRUD、禁用/关联、持久化、加密备份、空库恢复及失败回滚，正常退出码 0 |
| 原始最终包业务与日志统计 | 完成（本地） | 普通/流式、按序切换、客户端/API 取消、SSE、精确统计及正常重启通过 |
| Windows 生产更新链路与浏览器终态 | 完成（A3 本地证据） | 三场景实际替换/恢复、中文参数、visible Edge 终态及轮询停止；B 源码完全一致，未重复执行更新或浏览器 |
| 浏览器页面交互 | 完成（A3 本地证据） | visible Paseo 页面及日志详情正文通过，旧失败记录保留；B 仅重验新包业务/API，不冒称新做浏览器交互 |
| profile 离线与脱敏 | 完成（本地） | A2 10 项合成 CLI 证据保留；本轮完整 Go 回归覆盖解析和 db-check，未读真实 profile |
| 许可证材料 | 报告及打包完成（本地） | 固定工具、460 条真实记录、518 条材料和 8 个副本校验通过；LGPL/MPL 源码交付/再链接材料及部分来源绑定限制仍保留发布门禁 |
| `scripts/build.sh` 显式参数门禁 | 完成（本地） | 既有 A1/A2 参数失败路径和语法证据保留；B 使用 PowerShell 离线入口重建，未执行 Bash/NDK 的完整发布构建 |
| CI workflow | 待授权 | 仅保留 `plan/release-workflow-proposal.md`，未修改 `.github/workflows` |

## L6：待办与交接

1. 用户审查[B 组报告](release-license-tools-20261015.md)、本结果和新候选。许可证清单及正文已补齐，A3 浏览器日志详情也已通过；下一步针对 LGPL/MPL 的源码交付、再链接材料及来源限制决定处理范围，再判断严格门禁。
2. 两个测试别名已完成本次获授权执行，当前失败分别为 MySQL 不可信证书链、PostgreSQL migration 5 的旧字段前置条件。先处理证书配置与迁移缺陷；PostgreSQL 已非空，重新验收前需准备空目标，不能绕过前置检查或默认清理。没有 `restore_database`，完整空库恢复继续待验。
3. 用户按 Docker 交接材料在有 Docker 的云主机人工验收；回传版本/哈希、CPU 架构、Docker/Compose 版本、C1–C9 结果和脱敏日志。
4. `go-licenses` 报告已完成；Android 构建、Docker 镜像 tar、真实 Release/registry/在线升级仍待各自环境和授权，完成前不能扩大公开支持声明。
5. 第二轮再决定是否修改 workflow、核实 GitHub 权限、集成 `main`、创建 RC/Release、发布 GHCR/Docker Hub、验证真实下载和在线升级。

本轮完成 B 组工具、报告、新候选和表内本地验收；未改产品源码、依赖锁文件或 CI。许可证分发材料、平台/数据库、Docker 和发布范围分别继续处理。本轮停止，不自动开展 CI/远端阶段。

临时资源按授权保留，均不作为发布附件：A2 候选的本地验收目录和临时资源清单见 `build/release-local/a2-20261012-193213-9b75afef/checks/temporary-resources.json`，其中 `a2-acceptance-*` 目录保留；旧 `l65-*` 验收、工具、profile 和非最终候选目录也继续作为历史证据保留，删除另行授权。历史 PS 5.1 的 BOM 配置兼容问题已由脚本的无 BOM UTF-8 写入处理；2026-10-15 使用 A2 归档和当前脚本在 PS 5.1 完整执行初始化、重启持久化、独立空库恢复及失败回滚，退出码 0，见 `build/release-local/a23-20261015-163351-cb4858db/logs/sqlite-ps51-remotesigned.log`。使用与生产 helper 一致的进程级 `-ExecutionPolicy RemoteSigned`，未改持久执行策略。

复审后状态：**B 已解除许可证工具/报告缺失阻碍，新候选及本地验收通过；许可证分发条件和真实环境门禁仍需处理，L6.5 不自动判为严格通过。** 本轮没有 commit、分支切换、标签、push、PR/MR、Release、镜像发布、真实数据库连接或远端状态变更。A2/A3、工具缓存、原始失败和合成测试数据均保留；已有仓库外实例 `D:\tool\AI\octopus-roam-runtime\octopus-roam.exe`（PID 2828）保留运行。本轮程序的收尾及最终核对见 B 报告。
