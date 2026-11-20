# v0.1.6 验收入口与参数合同

本入口为真实 v0.1.5 → v0.1.6 和 1006 场景补充验收，不改动 `../backup/product.py` 固定的 v0.1.2/v0.1.3 遗留专项。源码阶段只生成、核验合成 fixture 和入口合同；实际候选、Windows 登录自启和 Linux 镜像/卷/Compose 的通过证据必须来自候选阶段，分别交给会话 05/06。

真实旧版固定为已发布 `v0.1.5`、commit `1856fd5fcfa52e5d27798dc2aeea549d58dd0fa6`。原正式 artifact 清单 SHA-256 为 `832e5a010eb5ce0466bd44f9d302d58578f0b4a38212086a5a7ef171393bdbbb`，程序 ZIP 为 `2eb863a13c9d4582854f94307d9b0faeed07e2780f98a0ac4b7eb9262618143f`。不能换旧版参数、改备份版本号、重新计算未经审查的清单来代替真实旧版。

所有命令中的 `<FINAL_SHA>`、`<ARTIFACT_SHA256>`、`<MATERIALS_SHA256>`、路径和镜像 ID 由本轮准确回执填写；它们不是默认值。参数文件、机密、数据库和证据继续本地保存。Python 入口不输出 DSN 或生产凭据，产品测试中的上游 Key/管理登录/备份密码都是固定合成输入。

## 准确源码检查总控

`source.py` 随准确 SHA 进入 Git，Windows/Linux 从各自无改动的隔离 checkout 执行同一个入口。参数 JSON 只放在本地测试目录，允许 gomodcache、gocache、pnpm_store，以及对应平台的 bash、tool_path、既有 Windows python3 adapter 的目录/摘要、既有 actionlint 路径；拒绝 DSN、任意环境注入或未知字段。Linux 使用真实 Python 3.14.6，拒绝 adapter 参数。

```powershell
python -B scripts/acceptance/v016/source.py --source <EXACT_SOURCE_CHECKOUT> --commit <FINAL_SHA> --output <NEW_SOURCE_EVIDENCE> --settings <LOCAL_NONSECRET_SETTINGS_JSON>
```

Linux 用 `python3 -B` 执行相同命令。总控核对全部 Git blob 的原字节，断言固定工具，按锁文件准备已有离线缓存，执行原 `scripts/ci-test.sh` 和原规定 race，追加 14 项 Node/Webhook race，Windows 追加 desktop race 和五项 Windows runner，Linux 追加 actionlint v1.7.7；然后从准确源码生成/核验 fixture，再核对完整源码、锁文件和暂存区。它只报告源码检查通过；GitHub 与 `server.py` 的结果必须另外绑定这个准确 SHA。

## 纯 fixture 验证

固定 Go 1.27.3，使用已有锁定依赖缓存；`GOTOOLCHAIN=local`、`GOPROXY=off`、`GOFLAGS=-mod=readonly`。生成器不打开数据库、不启动产品，输出目录必须新建且父目录已存在。

```powershell
go run ./scripts/acceptance/v016 --output <NEW_FIXTURE_DIRECTORY>
go run ./scripts/acceptance/v016 --check --output <FIXTURE_DIRECTORY>
go test -count=1 ./scripts/acceptance/v016
go test -tags=jsoniter -count=1 ./scripts/acceptance/v016
python -B -m unittest discover -s scripts/tests -p test_release_v016.py -v
```

Linux 使用相同 Go/Python 命令。manifest 包含十一份 JSON 的准确摘要和预期正则/模型列表；两种小时归档均通过产品的 `NewHourlyArchive` 构造，保留 NULL、负零、MySQL DOUBLE 与 PostgreSQL REAL 的原始位。生成器 `--check` 比较完整原字节，拒绝覆盖旧输出。

| Fixture | 功能边界及真实产品预期 |
| --- | --- |
| dump-1000 | 不含分类、冷却规则、两个后续渠道开关或归档；导入后导出 1004 |
| dump-1001 | 分类及原模型级冷却；缺失 notify_webhook；导出 1004 |
| dump-1002 | 增加 strip_responses_history；导出 1004 |
| dump-1003 | 增加 custom_header_model_only；导出 1004 |
| dump-1004 | 原冷却及格式 1 归档；导出 1004 |
| dump-1005 | 格式 1、MySQL 格式 2、PostgreSQL 格式 2 共存；无扩展冷却；导出 1005 |
| dump-1006 | Key 作用域、通知及上述三份归档；导出 1006 |
| no-archives-1004 | 无归档、无扩展冷却；导出 1004 |
| format1-1006 | 只有格式 1 归档，但有扩展冷却；导出 1006 |
| key-only-1006 | Key 作用域、通知关闭、两种归档；单独由 Key 作用域触发导出 1006 |
| notify-only-1006 | 原模型作用域、通知开启、两种归档；单独由通知开关触发导出 1006 |

这些文件用于候选中的真实 HTTP 导入/重复导入与重启。生成或 Go round-trip 通过不代表真实升级、恢复或旧程序拒绝已经通过。

## 产品入口

先由现有 `scripts/release_acceptance.py prepare` 对完整 sealed artifact 进行审核并派生布局。产品入口强制重新执行 `verify-layout`，核对执行脚本/旧 helper 与准确 Git blob/派生源码逐字节相同，核对旧正式清单、build 和 ZIP/tar；fixture 还须通过准确源码的 Go 生成器核验。使用 `--validate-only` 时不创建输出或启动应用，结果只写 `actual_product_acceptance=not_run`。

Windows PowerShell 7：

```powershell
python -B scripts/acceptance/backup/product_v016.py --repository <EXACT_SOURCE_CHECKOUT> --candidate <VERIFIED_CANDIDATE_LAYOUT> --commit <FINAL_SHA> --artifact-sha <ARTIFACT_SHA256> --materials-sha <MATERIALS_SHA256> --previous-artifact <ORIGINAL_V015_ARTIFACT> --previous-manifest-sha 832e5a010eb5ce0466bd44f9d302d58578f0b4a38212086a5a7ef171393bdbbb --platform windows --fixtures <FIXTURE_DIRECTORY> --output <NEW_WINDOWS_EVIDENCE> --hold-for-browser --allow-isolated-write
```

Linux Bash/Python 原生执行，不使用 PowerShell：

```bash
python3 -B scripts/acceptance/backup/product_v016.py --repository <EXACT_SOURCE_CHECKOUT> --candidate <VERIFIED_CANDIDATE_LAYOUT> --commit <FINAL_SHA> --artifact-sha <ARTIFACT_SHA256> --materials-sha <MATERIALS_SHA256> --previous-artifact <ORIGINAL_V015_ARTIFACT> --previous-manifest-sha 832e5a010eb5ce0466bd44f9d302d58578f0b4a38212086a5a7ef171393bdbbb --platform docker --image <VERIFIED_CURRENT_IMMUTABLE_IMAGE_ID> --previous-image <VERIFIED_V015_IMMUTABLE_IMAGE_ID> --fixtures <FIXTURE_DIRECTORY> --output <NEW_LINUX_EVIDENCE> --allow-isolated-write
```

Linux 在 Docker Engine 宿主使用 host network，但服务、fixture 和 Webhook 都绑定 `127.0.0.1` 的本轮空闲端口。没有外部 Webhook 参数。数据仅挂载新证据目录的 `/app/data`，应用 UID=10001、GID=宿主核验用户的 GID、目录模式 0770。Compose 保留准确候选源码中的原部署文件，以本轮 override 改为不可变镜像、本地端口/卷和本轮标签；实际 `compose config`、恢复、停止、同卷重建均核对。清理仅停止并移除本轮成功创建且标签吻合的容器，不删除卷数据、镜像或证据。

产品按顺序执行：真实 v0.1.5 HTTP 创建旧数据与普通/SSE 请求 → 正常停止 → 复制完整旧运行目录（包括配置/数据库/原程序）并留原字节 manifest → 候选从副本升级 → 原正则、旧规则默认关闭通知、旧模板创建/编辑/禁用/恢复/测试 → 可视化表达式与真实模型刷新 → 十一份 fixture 各在空库导入两次并重启 → 两个真实已进入上游的请求触发 Key 冷却及活动期延长 → 同 Key 的另一个模型受冷却、其他 Key 实际请求成功 → 延长不重复通知、真实到期后新一轮通知 → 1006 加密导出/空库恢复/重放/重启 → 错密码 400、冲突 409 保持数据 → 真实 v0.1.5 拒绝同一 1006 密文且数据不变 → 原旧程序+完整升级前副本恢复。Linux 最后执行卷权限、同卷重建和 Compose。

导出始终调用真实产品的密码 POST 接口。每份原始密文先保存在对应 runtime 的 `export-NNN.encrypted.json`，然后由准确源码编译的 `inspect-backup` 辅助程序复用 `op.DecryptBackup` 验证并读取内层版本、实体和归档；不会调用明文 GET 或把密文容器版本 1 当作内容版本。密码和密文经 stdin 传入，解密结果只经捕获的 stdout 返回父验收进程；证据记录密文/明文摘要、命令与退出码，不记录密码、认证头或登录令牌。该辅助程序不打开数据库、不充当候选产品。

`http.jsonl` 保存实际 API 方法、路径、状态、响应长度/摘要及起止时间，`exports.jsonl` 和 `backup-inspections.jsonl` 关联密文与解密结果，`checks.json` 保存场景起止时间。旧版准备失败时，在原 cleanup 正常停止成功后补存 `failure-preserved-v015-preupgrade` 完整运行目录与清单；这是故障保全，不代表原场景或升级通过。任何验收脚本修正都必须进入新准确 SHA 并重新绑定材料/候选，旧失败证据保留。

Windows `--hold-for-browser` 强制在升级后保留真实原正则等待 UI 操作。`browser-ready.json` 绑定准确 commit/artifact，并列出五个必需动作。使用冻结的 `../backup/browser.mjs` 在新 browser profile 中操作当前页面：全局/渠道手写表达式分别打开、清空草稿、取消并核对原值；再分别以“保留 starts v016-model-、排除 contains preview/retired”应用、保存、重开并核对条件行；记录一行和超过 144px 时的预览/滚动、桌面和手机截图。截图/命令记录放在产品输出内；`browser-done.json` 填准确 commit/artifact、实际动作和每份文件摘要。入口继续通过真实 API/模型刷新核对保存结果，不能只提供 passed=true。

Linux 的默认产品运行只记录 API/后端正则场景，浏览器标为 not_run。双平台最终报告须关联同一候选前端的 Windows 实际 UI 证据与 Linux 实际 API/镜像证据，不把生成器或开发浏览器结果称为 Linux 现场 UI 通过。

## Windows 菜单、真实原生助手和状态写入

产品阶段完成后，用其真实升级前完整副本执行独立原生场景：

```powershell
python -B scripts/acceptance/v016/native.py --repository <EXACT_SOURCE_CHECKOUT> --candidate <VERIFIED_CANDIDATE_LAYOUT> --commit <FINAL_SHA> --artifact-sha <ARTIFACT_SHA256> --materials-sha <MATERIALS_SHA256> --previous-artifact <ORIGINAL_V015_ARTIFACT> --previous-manifest-sha 832e5a010eb5ce0466bd44f9d302d58578f0b4a38212086a5a7ef171393bdbbb --platform windows --fixtures <FIXTURE_DIRECTORY> --product-evidence <PASSED_WINDOWS_PRODUCT_EVIDENCE> --output <NEW_NATIVE_EVIDENCE> --allow-isolated-write
```

此入口使用真实候选 ZIP 的 `.cmd`，选择 1/3/0；用真实 v0.1.5 父进程及准确候选 stage 执行旧助手成功、本次候选助手成功和候选启动失败后恢复真实 v0.1.5。失败由旧进程停止后占用本轮 loopback 端口触发，在助手开始回滚时释放；候选程序/stage 没有替换成合成 RC、假程序或修改版本常量。状态写入另覆盖初始状态短暂读句柄、实际初始→Ready 期间读句柄、持续拒绝保留完整旧状态、释放后正常启动。没有捕捉到真实 Ready 阶段就失败，不默认为通过。进程以路径、PID、创建时间校验，并合作停止；原生结果不包含实际登录自启通过。

真实登录自启因为本版修改了桌面状态写入而成为本轮门禁。会话 05 必须先批准以下具体方案：

1. 在本机当前用户的可交互 Windows 会话中，使用测试别名 `windows-v016-login-01` 和新目录 `build/release-local/v016-login-<FINAL_SHA前12位>-01`；准确候选 EXE/.cmd、配置及旧数据副本绑定同一 SHA/ZIP。该目录为唯一任务目标。实际运行目录不在测试范围内。
2. 审核空闲端口、任务归属与 Task Scheduler Operational 日志状态；仅为此目录注册当前用户 `desktop boot` 登录任务。会话 02 于 2026-11-20 只读确认该日志当前未启用，因此执行前须另外明确批准只启用 `Microsoft-Windows-TaskScheduler/Operational` 及结束后恢复未启用状态；不擅自改日志或系统配置。任务名由部署路径与当前用户 SID 的产品哈希规则生成，原 SID/账户不写入报告。
3. 从实际菜单选择 2（启动并添加登录自启），核对任务的目标 EXE/arguments/working directory/当前用户归属；选择 3 正常停止但保留该任务，记录退出码、快照和实际准备时间。不要调用 `desktop boot` 或手动 Run 任务替代下一步。
4. 用户保存当前交互工作的状态后，明确执行 Windows“注销”，再重新登录同一用户；锁屏、RDP disconnect 或只重新打开应用不满足。退出会终止该交互会话的工具/应用，因此此动作必须最后审批，并约定回来后从证据目录续接。
5. 在重新登录后的新 PowerShell 7 控制台核对候选进程的真实创建时间/路径/Ready、API 版本、保存的数据，以及这个准确任务的实际触发和启动事件。会话 02 已读当前系统 provider 元数据：119 是“由于用户登录”触发，100 是任务启动，200 是操作启动；必须关联本任务在准备时间后的 119 与 100/200，而不是只检查一个新的进程。记录事件时间、类型与脱敏的任务/程序身份；账户、会话私有标识及原始 XML 不进入交接。失败时收集该隔离目录的实际 `logs/autostart-errors.log` 与启动日志，保留首次失败，不能拿源码测试或手动启动成功替代。
6. 从实际菜单选择 4 正常停止并移除这个隔离目录的登录任务，确认同目录进程和任务均无遗留；保留数据/证据。检查相关日志配置是否按批准范围恢复。

会话 02 只准备并核验该方案，登录任务、注销/重新登录和实际候选应用一律 not_run。候选阶段的授权应明确包含新目录内的配置/文件写入、正常启停、三种真实助手与失败注入、菜单任务 2/4，以及独立的真实注销/登录动作。

## 最终源码服务器专项

在 Linux amd64 上从 `test` 检出本轮完整准确 SHA，完成源码检查后运行：

```bash
python3 -B scripts/acceptance/v016/server.py --source <EXACT_SOURCE_CHECKOUT> --commit <FINAL_SHA> --output <NEW_SERVER_EVIDENCE> --mysql-image <REVIEWED_MYSQL_84_IMMUTABLE_REF> --postgres-image <REVIEWED_POSTGRES_18_IMMUTABLE_REF> --allow-isolated-write --cleanup-owned-containers
```

入口只使用已存在的不可变镜像，`--pull never`；四个容器依次运行，每次只有一个本轮数据库容器。凭据随机生成，经 `docker run --env-file /dev/stdin` 和子进程环境注入；端口只发布到宿主 127.0.0.1；数据放在 384 MiB tmpfs，容器内存上限 768 MiB，不创建持久卷。固定库名 `roam_hourly18`，迁移测试仅在它所保护的 loopback 服务器中创建/清理本轮随机数据库/schema。

每个引擎显式执行 default/jsoniter 两套：`go test -json -count=1 -timeout=6m -run <四个TestServerMigration18的精确集合> ./internal/db/migrate` 和 `go test -json -count=1 -timeout=2m -run '^TestServerHourlyArchivesEncryptedRestoreAndReplay$' ./internal/op`。设置 `ROAM_HOURLY18_DIALECT`、`ROAM_HOURLY18_DSN`、`ROAM_HOURLY18_ALLOW_ISOLATED_WRITE=1`；DSN 不写文件、日志或交接。恢复单项还设置现有隔离 helper 的 `OCTOPUS_W03_TEST_CASE` 为该测试名，使这个已经独立的 Go 进程直接执行实际断言，避免只接受隐藏子进程输出的父级 PASS，并保持原 helper 的两分钟测试时限。

实际服务器补丁版本由 SQL 查询记录。每组要求升级的两个起点、四个回滚/重试点、六个 schema 拒绝分支、正常库保持（共 16 个显式 PASS），以及加密恢复/重放（1 个 PASS）。八次 Go 调用共 68 个必需 PASS；任意缺项、skip、失败或 cached 都拒绝。所有输出先移除 DSN/密码后落盘。

清理先核对本轮名称、捕获 ID 和 `roam.task=v016-server`/`roam.run` 标签，再停止并移除该 ID；只消失本轮 tmpfs 数据。检查容器已不存在，保留源码/镜像/缓存/全部证据。不执行主机目录清理、生产 SQL、生产操作或远端仓库写入。缺少批准/镜像/参数时专项 not_run，不用普通 CI 的 opt-in skip 填充通过。
