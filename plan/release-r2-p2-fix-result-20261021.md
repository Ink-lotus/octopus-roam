# R2 C1/C2 修复结果

日期：2026-10-21。基线：`test / 948de764ae9a034fd72899686d24dcc4ecad7fa5`。

**状态：C1/C2 已按本次明确授权应用到活动源码；定向回归、合并验证及最终完整性复核通过，可交付下一步审阅。**

本报告独立于原[修复计划](release-r2-p2-fix-plan-20261021.md)及[审查报告](release-r2-local-review-20261021.md)。输入重新盘点为 31 个已跟踪改动、60 个新增文件，暂存区为空，共保存 437 项非忽略文件基线。适用指令为用户本轮提供的 AGENTS.md；祖先、相关源码及证据目录未发现额外 AGENTS.md。

证据根目录：[r2-p2-fix-20261021-01](../build/release-local/r2-p2-fix-20261021-01/)。所有候选与日志均为新建材料，原报告、缺陷探针、R1 证据及既有冻结候选保留。

## 修复与差异

[独立补丁](../build/release-local/r2-p2-fix-20261021-01/review/c1-c2.patch) SHA-256：`94a5395ed488440a9a4e118690b572cd19ddeea5ddd963dce74113620e5a0f82`。逐文件原始／候选哈希见 [manifest.json](../build/release-local/r2-p2-fix-20261021-01/review/manifest.json)。本补丁以会话开始时的未提交源码为基准，单独呈现本次 8 个文件的局部修改及 3 个新增测试。

| 范围 | 文件与行为 |
| --- | --- |
| C1 | `internal/conf/database.go`、`database_test.go`：仅原 DSN 不含 `?` 时先加 `charset=utf8mb4&parseTime=True&loc=Local`，再沿现有流程注入严格 TLS。显式值、已有或空查询串、无 CA 的语义保留。 |
| C2 后端 | `internal/update/core.go`、`core_apply.go`、`release.go`、`release_test.go`：生产入口强制目标；非法格式为明确错误；同一锁内选定可安装版本后精确比较，过期目标返回冲突；后续 ZIP、SHA256SUMS、替换／调度与回执使用同一版本。 |
| C2 HTTP | `internal/server/handlers/update.go`：单个合法 JSON 请求、必需字符串目标、400/409 错误映射及 `{status,target_version}` 回执；已有认证和其他状态映射保留。无目标旧客户端得到 400，需要刷新。 |
| C2 前端 | `web/src/api/update.ts`：JSON 提交页面目标；409 刷新 latest／now-version 后由用户再次点击；在 mutation 成功处理前校验回执；有效 attempt 的目标来自回执，确认窗为接受后 30 秒。 |
| 新回归 | `internal/update/core_target_test.go`、`internal/server/handlers/update_test.go`、`scripts/tests/test_update_contract.cjs`。 |

核对了 `UpdateCore(`、更新 POST 和旧字符串回执的调用方。设置页原本使用展示的最新 tag 及查询所得当前版本，无需修改；现有 `scripts/test-release-acceptance.mjs` 只直接调用更新 GET，保留原件。通用 API client、数据库初始化、文案、依赖／锁文件及 CI 无需改动。

## 缺陷覆盖与证据

| 场景 | 结果与证据 |
| --- | --- |
| C1 修复前／后 | [修复前](../build/release-local/r2-p2-fix-20261021-01/logs/c1-before-corrected.log)退出 1，仅无查询参数场景复现 `parseTime=false / UTC / charset 缺失`；[修复后](../build/release-local/r2-p2-fix-20261021-01/logs/c1-after.log) conf 全包退出 0。 |
| C1 参数及 TLS | 覆盖无查询、显式 false/UTC/latin1、无关查询、空查询、无 CA、TCP／主机／DSN／CA 反例，以及信任 CA、主机名、TLS 1.2、禁明文回退与脱敏。 |
| C2 后端及 HTTP | [定向日志](../build/release-local/r2-p2-fix-20261021-01/logs/c2-backend-focused.log)：rc.2 请求遇 rc.3 时，Linux／Windows 分支均冲突；只查询一次发布元数据，没有资产下载、stage 创建、备份／文件改动、关服／重启或 pending。合法请求固定 rc.2，即使下载时发布源变为 rc.3；回执 JSON、校验和、下载路径及临时替换内容一致。非法请求、认证、既有门禁、下载／校验／替换失败均有断言。 |
| C2 前端修复前／后 | [旧源码对照](../build/release-local/r2-p2-fix-20261021-01/logs/c2-frontend-before.log)退出 1；[最终合同回归](../build/release-local/r2-p2-fix-20261021-01/logs/frontend-contract.log) 17/17 通过。 |
| 前端完整流程 | 实际 Settings 点击与 Hook、API client 在 VM 中执行，mutation 生命周期使用实际 TanStack `MutationObserver`；覆盖冲突刷新后无自动第二次 POST、再次点击接受新目标、pending 到精确 completed、10 种异常回执、超时、失败优先及终态恢复常规轮询。React 渲染、计时器和 HTTP 响应为测试夹具。 |

当前 `go-sql-driver/mysql v1.10.0` 将 charset 存入内部字段，而非 `Config.Params`。因此回归读取驱动重新序列化后解析的查询参数，检查同一语义。[首次测试日志](../build/release-local/r2-p2-fix-20261021-01/logs/c1-before.log)保留了最初直接断言 `Params["charset"]` 的失配；修正测试断言后，在未修复源码上重新确认了唯一的真实缺陷失败。生产方案未扩大。

## 合并验证

以下合并检查针对本次完整源码副本；应用后再次核对，活动源码 440/440 项与副本逐字节相同，且活动源码定向回归和前端合同测试通过。同一输入共同绑定 SHA-256 `53dd86a78d8bb9224af57523851e1f4505214fc66ef5b2d86869d999c531bca1`；各日志旁的同名 JSON 记录完整命令、工作目录、逐文件 SHA-256、退出码、时间及版本输入。未复用旧 R1 运行器。

| 命令／检查 | 退出码 | 证据 |
| --- | --- | --- |
| `go test -count=1 ./...` | 0 | [default](../build/release-local/r2-p2-fix-20261021-01/logs/go-default.log) |
| `go test -tags=jsoniter -count=1 ./...` | 0 | [jsoniter](../build/release-local/r2-p2-fix-20261021-01/logs/go-jsoniter.log) |
| `go vet ./...`、`go vet -tags=jsoniter ./...` | 0、0 | [vet](../build/release-local/r2-p2-fix-20261021-01/logs/go-vet.json)、[vet-jsoniter](../build/release-local/r2-p2-fix-20261021-01/logs/go-vet-jsoniter.json) |
| `go test -race -count=1 ./internal/conf ./internal/db/... ./internal/op ./internal/update ./internal/server/handlers` | 0 | [race](../build/release-local/r2-p2-fix-20261021-01/logs/go-race.log) |
| `node --test scripts/tests/test_update_contract.cjs` | 0，17 项 | [前端合同](../build/release-local/r2-p2-fix-20261021-01/logs/frontend-contract.log) |
| Node 直接调用现有 ESLint、`tsc --noEmit`、Vite build | 0、0、0 | [lint](../build/release-local/r2-p2-fix-20261021-01/logs/frontend-eslint.json)、[TypeScript](../build/release-local/r2-p2-fix-20261021-01/logs/frontend-typescript.json)、[Vite](../build/release-local/r2-p2-fix-20261021-01/logs/frontend-vite.log) |
| `python -B -m unittest discover -s scripts/tests -p test_release_contract.py -v` | 0，27 项 | [发布合同](../build/release-local/r2-p2-fix-20261021-01/logs/release-contract.log) |
| 3 份 Bash 脚本 `bash -n` | 各 0 | [脚本组](../build/release-local/r2-p2-fix-20261021-01/logs/scripts-group.json) |
| `git apply --check --whitespace=error-all` | 0 | [补丁预检](../build/release-local/r2-p2-fix-20261021-01/review/patch-check.json) |
| 活动源码 `go test ./internal/conf ./internal/update ./internal/server/handlers -count=1` | 0 | [应用后定向回归](../build/release-local/r2-p2-fix-20261021-01/logs/active-targeted.log) |
| 活动前端源码合同回归，沿用独立依赖副本 | 0，17 项 | [应用后前端](../build/release-local/r2-p2-fix-20261021-01/logs/active-frontend.log) |
| 工作区／暂存区 `git diff --check`、锁文件与新增文件直接检查 | 均通过 | [最终完整性](../build/release-local/r2-p2-fix-20261021-01/final-checks.json) |

本机为 Windows amd64，Go 1.27.0、CGO/GCC 可用，Node 24.18.0、TypeScript 6.0.3、Python 3.14.6。Go 使用现有 `.tmp-go-mod`／`.tmp-go-cache`；设置 `GOTOOLCHAIN=local`、`GOPROXY=off`、`GOFLAGS=-mod=readonly`。前端独立复制 47,052 个依赖文件及 1,360 个链接，链接全部指向副本内部，直接调用现有 Node CLI，并保留 `pnpm_config_verify_deps_before_run=error`。没有依赖获取或持久环境修改。

前端构建输入为 `v0.1.0-local.r2p2-20261021` 与基线完整 HEAD，同时以源码清单标明含未提交修复；新产物位于 [frontend-dist](../build/release-local/r2-p2-fix-20261021-01/frontend-dist/)。这是本地前端构建验证，未生成新的发布 ZIP 或替换历史冻结候选。

## 授权应用与最终复核

用户明确回复“授权应用上述补丁并完成活动源码复核”后，执行了[授权说明](../build/release-local/r2-p2-fix-20261021-01/review/APPROVAL.md)中的同一命令；授权说明保留申请时原文，实际[授权记录](../build/release-local/r2-p2-fix-20261021-01/approval-record.json)与[应用结果](../build/release-local/r2-p2-fix-20261021-01/application-result.json)另存。普通 Git 试应用只有换行转换导致原始字节哈希不同，11/11 归一化内容相同；实际应用采用已演练的局部字节差异程序，保留未修改行的原始换行，11/11 目标文件与候选字节一致。

[最终核对](../build/release-local/r2-p2-fix-20261021-01/final-checks.json)确认：440 项活动源码与完整验证输入一致；429 个本次修改范围外的基线文件、484 项原审查／R1 证据及原前端产物未变；原依赖的 3 份路径元数据和 1,360 个链接未变。分支与 HEAD 未变，暂存区为空且 index 原始哈希不变。当前为 31 个已跟踪改动、64 个新增文件；相对本次输入只增加 3 个测试和本报告，并局部修复上述 8 个文件。

活动前端验证使用 [active-frontend.cjs](../build/release-local/r2-p2-fix-20261021-01/active-frontend.cjs) 加载当前工作区中的真实回归脚本、Hook、API client 和 Settings 源码，仅将测试依赖解析到本次独立副本。没有复制状态算法，也没有以候选旧路径冒充活动源码。应用后没有新的产品代码调整，因此未重复完整 Go／构建矩阵。

本次差异可单独查看[授权补丁](../build/release-local/r2-p2-fix-20261021-01/review/c1-c2.patch)；包含原有未提交改动的完整已跟踪差异另存为 [final-worktree.patch](../build/release-local/r2-p2-fix-20261021-01/final-worktree.patch)。新增文件按最终源码清单和直接检查交付。未执行暂存、commit、Git 历史修改或远端操作。

收尾检查首轮因先校验尚未生成的 `final-worktree.patch` 报告链接而退出 1；[失败记录](../build/release-local/r2-p2-fix-20261021-01/final-checks-attempt-01.json)已保留。将证据生成移到链接检查之前后重新核对通过，未调整产品代码或放宽链接检查。

## 未验证项与适用范围

- 合成 DSN／CA 回归未连接真实 MySQL，也未验证带既有刷新记录的真实 MySQL 启动。
- 新 C2 用例是包内 loopback、临时替换文件、调度记录、handler 合同和前端 VM；完整 Go 验证含既有 Windows helper 合成进程测试，均不代表真实产品在线升级或真实 Release 间替换成功。
- 本机 Windows 条件下，既有 `TestUnixReplaceReturnsBackupDirectoryContainingPreviousExecutable` 按其平台条件跳过；没有原生 Unix 文件重命名／替换语义验收。
- 未执行真实浏览器端到端验收、新 Windows 发布候选、Ubuntu Actions、Android/NDK、多架构真实镜像、公开发布或真实 RC 间升级。
- 旧缓存客户端空 POST 会按选定合同得到 400，需刷新加载新前端；200 回执表示接受目标，最终完成仍以运行版本精确相等确认。

下一步审阅仍按数据库组（C1）、更新组（C2）、工具组（前端回归）、文档组（本报告）划分。暂存、commit、集成、Git 历史及远端操作仍属后续单独授权范围。
