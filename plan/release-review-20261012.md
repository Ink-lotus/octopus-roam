# v0.1.0 / L6.5 独立复审

日期：2026-10-12。基线：`test` / `948de764ae9a034fd72899686d24dcc4ecad7fa5`。

范围：`release-plan-v0.1.0.md`、L0–L6.5 的当前未提交改动和新增文件、相关调用链，以及 `build/release-local/l65-final-20261012-2f61c9b4/` 的候选和验收证据。

**结论：需要修订计划和修复代码，不能把当前状态理解为“本地缺陷已收口，只剩许可证和远端环境”。Windows 一键更新存在已复现的阻断；数据库入口也有实际缺陷。** 候选的文件、源码和归档哈希均匹配，既有测试记录有据可查；问题主要在测试覆盖缺口及由此遗漏的实现错误。

本次只新增本报告和独立复审材料，未修改原有产品源码、发布计划、结果书、CI、依赖或既有候选。没有读取真实数据库 profile、连接云数据库、访问真实发布源、提交或发布。需要授权的后续动作集中在第 5 节。

## 1. 代码问题

P1 表示应优先处理的更新阻断；P2 表示应在对应功能验收前修复；P3 为界面状态问题。以下分别说明运行复现与静态结论，不把未运行的平台写成实测通过或失败。

### C1 · P1 · Windows 更新永远无法完成准备握手（已复现）

位置：`internal/update/core_apply.go:218`、`:325`。

`scheduleWindowsUpdate` 固定启动 `powershell.exe`。Windows PowerShell 5.1 的 `Set-Content -Encoding UTF8` 会为 `.update-ready.json` 写入 BOM；Go 端直接 `json.Unmarshal(data, ...)`，未去掉 BOM。结果是 helper 已写出 `ready=true`，主进程仍等待 8 秒，随后终止 helper 并返回准备超时。Windows 的实际一键更新入口因此不可用。

复现直接调用生产 `scheduleWindowsUpdate`，旧进程始终存活，未执行程序替换。结果：`UTF8_BOM=true`，原始 JSON 解析失败，去 BOM 后 `ready=true`。helper 已由超时路径结束，复审结束时无该 helper 残留。

建议：统一 helper JSON 编码；读取准备文件时兼容 BOM，并补生产调度函数的握手测试。现有 `TestWindowsUpdateHelperReplacesAndRecoversRealProcesses` 直接启动脚本，只等待准备文件存在，绕过了 Go 端解析，因此会漏掉此问题。

证据：`build/release-local/review-20261012/regression-probes-run2.log`。

### C2 · P1 · Windows 重启参数和工作目录中的中文被错误解码（已复现）

位置：`internal/update/core_apply.go:216`，参数写入在同文件 `scheduleWindowsUpdate` 中。

Go 以无 BOM UTF-8 写入 `.update-arguments.json`，helper 却通过没有 `-Encoding UTF8` 的 `Get-Content` 读取。当前 Windows PowerShell 5.1 按系统 ANSI 编码读取，中文目录被读成乱码。修复 C1 后，此问题仍会使中文工作目录或配置路径无法用于重启；恢复旧程序又使用同一份错误解码的参数，可能连恢复启动也失败。

使用与 helper 相同的读取表达式复现：合成字符串 `review-配置-目录` 未能原样读回。现有 helper 测试使用带空格的英文路径，没有覆盖中文。

建议：显式按 UTF-8 读取，并给完整 helper 成功及失败恢复测试增加中文目录、中文配置路径；覆盖新旧程序的重启参数。

证据：`build/release-local/review-20261012/unicode-probe.log`。

### C3 · P2 · MySQL profile 无意关闭驱动默认认证能力（已复现）

位置：`internal/dbprofile/profile.go:258`。

代码用 `mysqlDriver.Config{...}` 的零值构造配置，没有使用驱动要求的 `mysqlDriver.NewConfig()`。生成再解析 DSN 后，`AllowNativePasswords=false`、`CheckConnLiveness=false`；前者会拒绝使用 `mysql_native_password` 的测试账号，即使其密码、TLS 和权限正确。本地驱动源码 `dsn.go` 明确要求通过 `NewConfig` 保留默认值，`FormatDSN` 会把这些 false 显式写入 DSN。

建议：从 `NewConfig()` 开始赋值，保留现有 TLS 策略和时间解析参数；测试应解析生成的 DSN 并核对实际驱动配置，不能只检查 DSN 是否包含主机和库名。本次复现只用了合成值，没有建立网络连接。

证据：`build/release-local/review-20261012/regression-probes-run2.log`。

### C4 · P2 · MySQL 读写冒烟使用未引用的保留字（静态确认，待真库验证）

位置：`cmd/dbcheck.go:137`。

最后一次设置写入通过 raw SQL 引用 `key` 列，没有按方言引用标识符。`KEY` 是 MySQL 保留字，这条语句会在 MySQL 下产生语法错误；默认参数测试、SQLite 测试或 PostgreSQL 结果都不能覆盖此问题。

建议：通过 GORM 的模型、条件和列表达式生成该无数据变化的更新，保留原有“证明写权限、不改变设置值”的范围；补 MySQL/PostgreSQL 方言 SQL 生成检查，再在获准的测试库上确认。不要为测试此项扩大数据库写入范围。

### C5 · P2 · 默认 profile 路径与用户交接位置不一致（已复现）

位置：`cmd/dbcheck.go:81`；对照 `plan/cloud-database-test-handoff.md` 第 1 节及第 6 节。

交接指定 `~/.config/octopus-roam/db-profiles.json`，并明确 Windows 对应 `%USERPROFILE%\.config\...`；实现使用 `os.UserConfigDir()`，Windows 会指向 `%AppData%`。用户按交接创建文件后，省略 `--profile` 的命令仍找不到它；macOS 上同样存在默认位置差异。显式 `--profile` 是现有可用绕行方式。

建议：本次以用户已经确定的个人目录路径作为默认，保留显式覆盖；同步修正交接中又称“系统用户配置目录”的段落。测试只比较默认路径，没有读取用户的任何真实档案。

证据：`build/release-local/review-20261012/regression-probes-run2.log`。

### C6 · P2 · db-check 失败仍以退出码 0 结束（已复现）

位置：`cmd/dbcheck.go:39`、`:43`、`:63`。

Cobra 命令使用 `Run`，所有失败分支只打印 JSON 后正常返回。用最终候选检查一个明确不存在的合成 profile，输出为 `invalid_profile/profile_missing`，进程退出码仍为 0。连接、迁移和读写失败也走同一成功退出方式；依赖进程退出码的验收脚本会误判。

建议：结构化 JSON 保持脱敏；通过 `RunE` 或受控的命令结果让失败返回非零退出码，避免 Cobra 再打印原始数据库错误。有效的离线检查及实际通过的连接检查返回 0。现有 `Check-Profile` 验证器只检查 JSON，没有断言退出码。

证据：`build/release-local/review-20261012/dbcheck-exit-code.json`。

### C7 · P2 · Unix 替换成功后，exec 失败会留下已停止服务的存活进程（静态确认）

位置：`internal/update/core_apply.go:126`、`:142`；`internal/update/core.go:40`、`:56`。

替换函数创建旧程序备份并换入新程序，随后 `restartExecutable` 先执行 `shutdown.Shutdown()`，再调用 `syscall.Exec`。一旦 exec 因不可执行的发布文件、加载错误等原因返回，代码只记录日志：没有恢复备份、重新启动旧程序或非零退出。HTTP 服务和数据库已关闭，主 goroutine 仍在等待退出信号，监督进程也无法仅凭进程存活发现故障。

旧重启函数此前不可通过已停用的自更新入口到达；本轮恢复入口后，这成为实际更新路径。当前成功更新测试把 `restartAfterUpdate` 换成回调，并用文本充当二进制，不能验证 Unix 的真实重启。

建议：让重启流程保留备份位置和失败恢复责任；exec 失败时恢复并尝试旧程序，恢复失败时明确记录失败并以非零状态退出。补真实 Unix 进程用例；本机没有对应运行环境，本次不声称已做 Linux/macOS/Android 实测。

### C8 · P2 · 本地构建的 Target 子集参数依赖未请求的 linux/amd64（静态确认）

位置：`scripts/build-local.ps1:5`、`:284`。

`-Target` 接受平台子集，但所有目标编译完后无条件从 `packages/linux-amd64/octopus-roam` 生成 Docker context。选择仅 Windows 或仅 Linux arm64 时，前面的构建完成后会在这里因为文件不存在而失败，无法交付该子集的完整元数据。

建议：按实际构建的 Linux 目标生成对应 context；没有 Linux 目标时将 Docker context 标记为不适用并正常收口。若有必选平台，应在编译前明确检查，不能隐式依赖。无需为复现这一确定的文件依赖而重新生成整套候选。

### C9 · P3 · 更新 UI 没有结束“准备重启”状态（静态确认）

位置：`web/src/components/modules/setting/Info.tsx:112`、`:126`；`web/src/api/update.ts:82`。

POST 成功后 mutation 的 `isSuccess` 持续为 true。即使后续轮询已得到新版本和 `latest`，页面仍显示准备重启；30 秒后如果版本未改变，也没有转换为“未确认完成”。按钮禁用状态同样依赖这个不会随后台版本改变而清除的标志。用户刷新页面可以退出该状态，但这没有实现完整的确认流程。

建议：记录本次目标版本，用当前版本确认完成；分别处理完成、明确失败、确认超时，终止短周期轮询。不能把 POST 成功当作安装成功。本次为状态逻辑审阅，没有冒称完成浏览器交互验收。

## 2. 计划与验收状态需要修订的内容

### P1 · 更新 L6.5 状态，按用例补齐证据

计划开头、L6.5 执行状态、第 7 节交接及结果书应补充本次复审结论。建议状态为：**“既有候选与证据已保存且哈希一致；复审发现本地实现缺陷和验收缺口，待修复、受影响回归及候选重建；许可证和真实环境验收继续待补。”**

至少需要补以下对应关系：

| 计划要求 | 当前证据实际覆盖 | 后续动作 |
| --- | --- | --- |
| L3：本地模拟源驱动真实进程更新 | Unix 路径用文本替身和重启回调；Windows helper 直接调用测试绕过 Go 调度握手 | 增加真实入口到替换、重启、版本确认的完整链路，优先覆盖 Windows |
| L3：中文路径、失败恢复 | 英文空格路径及引号参数；直接 helper 的即时退出回滚 | 增加中文路径和实际调度；按平台记录恢复证据 |
| L5.2：最终包普通/流式请求、取消/失败切换、日志统计、页面交互 | 最终包脚本主要执行初始化及 CRUD；已有单元测试可作底层证据 | 对未执行的最终包流程明确标待验，补本地模拟服务与必要交互记录 |
| L5.3：SQLite 空库完整恢复、关联/禁用状态、新配置、失败回滚 | 最终运行脚本在原数据库里重新导入，仅断言一个设置恢复；创建过的渠道、分组、API Key 仍在原库 | 在另一份空 SQLite 库恢复并核对业务内容，补拒绝/回滚用例；现有单元测试不能冒充最终包验收 |
| L5.3：MySQL/PostgreSQL 完整自动验收 | `db-check --connect` 只有迁移、缓存读取、查询和设置无变化写入；`restore_database` 只报告是否配置 | 保持冒烟命令边界，另补完整集成入口，取得具体授权后执行 |

结果书已经如实说明只核对设置、云库待验等限制；需要纠正的是上层“本地可执行工作已完成”的概括，不能只保留许可证一个本地收口门槛。

### P2 · Docker 人工交接仍缺可离线准备的材料

L5.4 要求完整构建命令、本次测试镜像的 Compose 模板、数据库占位配置及操作说明。当前 context 只有 6 个文件，Compose 仍指向尚未发布的 GHCR 标签，没有本次本地测试镜像模板、明确构建命令或数据库示例。许可证缺失阻止实际镜像构建，但不阻止准备这些文档和模板。

还需在交接步骤落实 Linux 二进制执行权限：候选 ZIP 的 0755 已验证，Windows 目录中的普通文件经上传后不能据此保证保留 0755；现有 Dockerfile 只对 entrypoint 加执行权限。应明确使用保留权限的归档，或在具体构建步骤设置程序权限并验收。

建议保留“镜像未生成、C1–C9 待用户验收”，把本地交接材料的准备与 Docker daemon/许可证依赖分别列项。`scripts/build.sh --output-dir` 产出的目录也需要在 R1 明确映射到 Dockerfile 固定的 `build/docker` 和 `build/THIRD_PARTY_LICENSES.csv` 路径；当前脚本并不独立生成完整 Docker 构建上下文。

### P3 · 清理过期交接文字和重复构建指令

- L6.5 的“当前候选的已知收口事项”仍称脚本使用 `Compress-Archive`。实际已改为 GNU tar + libarchive，应标记为历史问题已解决；本次也验证了候选哈希，不能据旧条目再改一遍归档实现。
- 第 7 节前一段要求输入未变时不重建，后一段又无条件要求完成候选重建。改为：先核对证据，只在源码/构建输入变化或存在明确产物问题时重建；本次实施产品修复后确实需要新候选。
- 保留旧候选和原始验收记录，追加复审状态；不得用改写历史证据或手工追改哈希的方式宣布修复已通过。

### P4 · 明确 R0/R1 准备与发布门禁的依赖

计划要求 L6.5 通过才进入 R0/R1，但许可证工具又需要到 R0 补齐。建议明确：R0 的范围决定、工具/环境准备，以及获准的 R1 本地 CI 准备可以用于解除阻碍；这不代表候选通过，更不代表允许 R2/R3 的远端变更或发布。发布门禁继续包含许可证、产品缺陷、完整候选证据及所声明平台/数据库的实际验收。

CI proposal 的 `prepare → test → build → publish`、固定 commit、构建前不推 tag、RC 不覆盖 latest 的方向正确，应保留。旧 workflow 与新 `build.sh` 参数不匹配也已明确记录；当前不能直接推送 main 后发布。

### P5 · 修正回归命令的执行位置和离线说明

本次在仓库根目录执行计划列出的 `go test ./...`，会扫描历史 `build/prelaunch-retry-20261009-215944/before/` 中不完整的源码副本，造成 3 个历史包编译失败；当前产品包的测试全部通过。此前 L6.5 在冻结源码目录执行，和本次根目录失败不矛盾。

建议命令前写明进入哪个冻结源码目录；若在仓库根目录检查产品代码，则显式使用 `. ./cmd ./internal/... ./static`。不要删除历史材料来让测试变绿，也不要把此次历史目录失败报成当前产品回归。

第 3 节说不关闭校验，L6.5 示例却设置 `GOSUMDB=off`，需要统一。可优先使用已有缓存、`GOTOOLCHAIN=local`、`GOPROXY=off`、`-mod=readonly`；本次检查没有新增关闭校验数据库的设置。若采用 `GOSUMDB=off`，应准确说明它关闭的是校验数据库查询，不能宣称所有校验均未改变，也不能把它误写成所有 go.sum 校验都关闭。

### P6 · 修正功能对照中的价格与模型刷新表述

`release-upstream-feature-comparison.md` 的“渠道价格刷新”一行把渠道模型刷新描述成使用本地价格预设，混淆了三条路径。应分别写清：渠道模型列表刷新访问渠道模型接口；模型价格更新使用价格预设/价格更新任务；程序版本更新访问独立发布源。这也影响本地运行验收应隔离哪些出站路径。

## 3. 本次验证结果

所有新证据位于 `build/release-local/review-20261012/`。用于复现的 Go 测试通过 `-overlay` 加载，原有源码未改；证据目录有独立 module 边界，不进入根模块的测试包列表。

| 检查 | 结果 | 证据 |
| --- | --- | --- |
| 最终候选完整文件哈希 | 994 条匹配，0 不匹配 | `hash-audit.json` |
| 冻结源码及当前工作区输入 | 各 482 条匹配，0 不匹配 | `hash-audit.json` |
| 归档 SHA-256 | 7 个全部匹配 | `hash-audit.json` |
| 现有 update/dbprofile 测试 | 通过 | `existing-focused-tests.log` |
| 普通 `go test ./... -count=1 -timeout=180s` | 整体失败：历史 build 副本 3 包编译失败；当前产品包全部通过 | `go-full.log` |
| `go test -tags=jsoniter . ./cmd ./internal/... ./static -count=1 -timeout=180s` | 通过 | `go-jsoniter.log` |
| 前端 ESLint / TypeScript | 两者退出码 0 | `frontend-checks.json` |
| PowerShell 解析 / Bash 语法 / git diff --check | 通过 | `static-checks.json` |
| C1、C3、C5 的期望行为回归探针 | 3 项失败，确认缺陷 | `regression-probes-run2.log` |
| C2 Unicode 探针 | 失败，确认缺陷 | `unicode-probe.log` |
| C6 最终候选 CLI 错误退出 | JSON 报错但退出码 0 | `dbcheck-exit-code.json` |
| 本次 Windows 调度 helper 残留 | 0 | `process-check.json` |

第一次 overlay 命令的参数被 PowerShell 解析截断，未加载测试；已改为分开的 `-overlay` 和带引号路径重跑，结论以 `regression-probes-run2.log` 为准。没有把命令调用错误算作产品缺陷。

本次没有重新生成生产前端、七平台归档或镜像，没有重跑 race；原回归证据及来源由候选哈希核对支持。本次 `jsoniter` 使用默认包并行度通过；结果书中曾发生的事件测试超时仍保留为既有稳定性风险，单次通过不能证明已修复。

## 4. 继续保留的环境与发布待办

1. 真实第三方许可证报告尚缺；当前候选不能作为公开 Release/Docker 发布输入。
2. MySQL/PostgreSQL 的完整功能、刷新事务、重启、空库恢复及回滚待专用环境和针对性授权；本次不接收或读取真实连接材料。
3. Docker 镜像及 C1–C9 待用户在有 Docker 的环境验收；先补本地交接材料。
4. Unix 实际启动/更新、Android/NDK、公开平台矩阵仍需按最终支持范围完成验证。
5. R1 CI 实施/actionlint、远端条件核实、RC、真实下载/镜像及两版本在线升级、GA 全部保留原有独立授权边界。

## 5. 集中待用户回复的具体范围

本轮审阅已完成。建议下一轮按以下 A/B/C 顺序处理；这里列出目标、操作形式和影响，尚未执行这些修改。

| 组 | 目标与文件 | 拟执行操作 | 影响 |
| --- | --- | --- | --- |
| A1：代码局部修复 | `internal/update/core_apply.go`、`core.go`、必要的 `release.go` 及 `release_test.go`；`internal/dbprofile/profile.go`、`profile_test.go`；`cmd/dbcheck.go` 及新增命令测试；`scripts/build-local.ps1`；`web/src/api/update.ts`、设置页 `Info.tsx` 和必要的三语文案 | 用 `apply_patch` 逐项应用 C1–C9 的局部补丁；运行受影响的 Go、前端和脚本检查 | 改善更新握手/恢复、数据库检查、构建子集和界面状态；改变当前源码输入，既有候选继续作为历史证据 |
| A2：本地验收补齐 | `scripts/test-runtime-sqlite.ps1`，必要的独立测试脚本；新的唯一 `build/release-local/<批次>/` | 对测试脚本做局部补丁；使用本地模拟发布源及合成数据，运行真实更新链路；新建两份一次性 SQLite 测试库，初始化/写入/空库恢复/失败回滚；受影响检查通过后，调用 `pwsh -NoProfile -File scripts/build-local.ps1 -OutputRoot <全新目录> -Version <新的本地版本> -VerificationScript <已审查入口>` | 只操作本轮测试目录/进程和合成数据；明确包含测试库内写入；保留既有候选及真实运行实例，不删除旧资源 |
| A3：计划与交接修订 | 本发布计划、`release-local-result.md`、`cloud-database-test-handoff.md`、`release-upstream-feature-comparison.md`、必要的 workflow proposal 和 Docker 测试材料 | 用 `apply_patch` 更新第 2 节列出的状态、用例矩阵、命令和交接；生成指向本地测试镜像的新模板，不修改现有部署环境 | 把复审发现转为明确待办；区分本地通过、缺证据、待环境与待授权 |
| B：许可证与缺失工具/平台 | 先列出固定版本工具、来源、用途及获取命令 | 另行提出具体联网/安装范围 | 解除许可证和平台环境阻碍；本次未申请执行 |
| C：云数据库、CI 与远端发布 | 沿用原计划 R0–R5 和云库交接 | 每个具体目标和命令另行授权 | 不随 A 组修复自动开始 |

需要回复的是：是否授权 A1–A3 的多文件局部修复、一次性本地 SQLite 验收及新候选重建。没有新增依赖、整文件覆盖重写、删除既有文件、commit、Git 历史操作、CI 实改、真实数据库连接或远端状态变更的申请。

暂停实施的依据是用户本会话提供的 AGENTS.md：“必须先说明目标、命令、影响，并获得针对该操作的明确授权：……批量修改……数据库变更……”。本次跨文件修复和新增实际 SQLite 验收属于该范围；只读审阅、独立复现和证据核查已经完成，不需要等回复才能继续的审阅工作已处理。
