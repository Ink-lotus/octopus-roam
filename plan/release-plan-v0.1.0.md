# Octopus Roam v0.1.0 发布计划与会话交接

编制日期：2026-10-10。状态更新：2026-10-20。R0 已完成两种本地数据库各 35/35、应用许可证/对应源码材料，以及 WSL Containers 下 Linux amd64/SQLite 容器补验与镜像 tar 交付。新镜像 `…-licenses.1` 已集成声明、核对版本和哈希，并通过页面备份实际下载/恢复、严格 HTTPS 校验、权限重启写入、重建持久化和容器更新保护。详见[Docker 交付报告](release-docker-delivery-20261019.md)。继承 Alpine 的本地源码/声明附件现也已完成，见[Alpine 材料与 R1 交接](release-alpine-materials-20261019.md)。**R1 本地收尾完成：九文件 CI 补丁及两文件补充门禁均已授权应用，actionlint 1.7.7、真实前端构建、Go 全量/default/jsoniter/vet/关键 race 和 27 项脚本测试通过。远端只读核实也已完成；read:packages 已确认生效，GHCR 精确包及版本仍为 404，当前身份未找到该包。新 CI 尚未在远端生效，线上验收尚未完成。** 具体状态和证据以 [R1 本地报告](release-ci-r1-20261020.md)和[远端只读报告](release-ci-r1-remote-readonly-20261020.md)为准。公开源码入口、其他平台实机和远端发布按后续阶段验收。

**R0 数据库条件已收口（2026-10-18）**：用户确定镜像渠道仅 GHCR，并授权使用 `D:\tool\db-test\` 中的工具和独立实例验收。首轮旧候选 MySQL 导入失败、PostgreSQL 35 项通过；完成 MySQL 保留字查询和原始程序自定义 CA 支持修复后，第二轮新候选两引擎各 35 项全部通过，四个主/恢复角色的 migration 3–13 均为 success。两个自建数据库正常停止，测试数据及两轮证据保留。当前候选 989 项文件清单、500 项冻结/工作区输入、7 个归档及 8 份许可证副本已核对；七平台的实际 Go 依赖元数据与旧候选一致，沿用已有源码可用性证据。详见[本地数据库验收报告](release-local-database-acceptance-20261018.md)及第 6 节；`L6_5_strictly_passed=false`。

工作目录：`D:\tool\AI\octopus-roam`。编制时分支为 `test`，HEAD 为 `948de764ae9a034fd72899686d24dcc4ecad7fa5`，最近提交为“新增 Windows 服务管理菜单并清理旧自启入口”。新增本计划前工作区干净。本计划本身尚未提交。

**当前候选为 `build/release-local/mysqlfix-20261018-03/`，版本 `v0.1.0-local.mysqlfix-20261018-03`。七目标构建、989 项文件清单和 500 项冻结输入校验通过；最终源码 default/jsoniter、vet、五个相关包 race 均通过，日志为 `r0-local-db-20261018-01/go-*-final-v3.log`。绑定驱动位于 `mysqlfix-acceptance-20261018-03/`，独立 SQLite 演练 34 项通过、1 项行锁待真实引擎；随后当前候选两种真实引擎的行锁及其他全部检查均已通过。B 组真实许可证报告继续沿用；A2/A3/B、dbfix 与中间 mysqlfix 候选及历史失败证据完整保留。历史生产更新/浏览器范围见[补充验收报告](release-acceptance-20261015.md)，本次未重跑这些场景，不自动进入 CI 或远端发布。**

**历史第 4 项环境验收（2026-10-15）已按明确授权连接两个测试库，均未通过**：MySQL 在迁移前因证书链不受信任（`tls_unknown_authority`）停止；PostgreSQL 18.6 通过两次空库检查，但当时 migration 5 仍要求旧 `channels.key`，迁移失败，留下 17 张表（含迁移表）及 7 条迁移记录，其余 16 张业务表均无数据。未继续云库业务、行锁、备份或原始程序/API/重启，也未清理或重跑。SQLite 驱动的列检测误判掩盖了该迁移问题，补充只读证据及详细边界见[环境验收报告](release-environment-acceptance-20261015.md)。用户明确沿用全部平台，缺少实机的保留待验；Docker 由用户人工验收。两个别名均无空恢复库，A2/A3/B 候选保持冻结。

**第 4 项数据库验收已在本地真实引擎通过**：[修订后的计划](release-database-acceptance-fix-plan-20261015.md)所列迁移修复，以及 2026-10-18 的 MySQL SQL/TLS 修复，均已进入当前候选并完成两库完整验收。详细范围见[本地数据库验收报告](release-local-database-acceptance-20261018.md)。这不改变历史云库的失败状态，也不授权续跑或清理部分初始化目标；完整 L6.5 仍受其他门禁约束，`L6_5_strictly_passed=false`。

## 1. 目标与已确认的范围

首个独立版本沿用 `v0.1.0-rc.1` → `v0.1.0` 的发布路线；如验收需要，可在获得发布授权后增加后续 RC。对外版本号独立于上游、数据库迁移编号和备份格式版本。

用户已确认：

1. 恢复本产品的版本检查与一键更新，接入 Octopus Roam 自己的发布源。
2. 整理上游基线与本项目新增功能的差异，完善 README 和发布文档。
3. 收口版本、Compose、构建信息、下载地址、镜像地址和发布流程。
4. 验收实际安装包和镜像；数据库验证包含在这一项中。
5. 后续同步改动并切换到产品 `main` 分支，再通过 RC 验收后正式发布。
6. 本项目尚未独立发布，首发按全新安装、当前版本备份恢复和重启持久化验收。旧版本迁移、降级兼容、更名前测试数据迁移不作为首发任务。
7. 第一轮以本地工作为主；用户准备两种专用云测试数据库，由执行会话在具体授权后自动验收。该数据库例外不提前其他远端工作；用户只负责 Docker 人工验收。
8. 两个云数据库专门用于本次测试。通过稳定别名选择仓库外的一次性明文 profile；测试结束后由用户删除档案、重置远程数据库并处理测试账号。真实连接信息不进入工具输出、报告或会话上下文；需要更强权限隔离时另行配置独立 Windows 用户或测试服务，边界见云数据库交接说明第 1.1 节。
9. 2026-10-18 的数据库验收改用 `D:\tool\db-test\`：两轮各建独立实例及私有配置，两主库和两恢复库，结束停止自建进程并保留数据。第二轮当前候选已全部通过；本轮未连接历史云库，历史各一次授权不用于重跑。
10. 2026-10-19 用户授权执行会话改用已安装的 WSL Containers 3.0.3.0 构建和验收容器，并继续完成镜像声明集成、tar 交付与缺项补验。最新结果为 `r0-docker-delivery-20261019-03/`，取代“等待用户 Docker 人工验收”的当前待办；历史分工记录保留。仅覆盖所列本机场景，不提前远端发布或扩展其他平台结论。

本文是本次交接的执行入口。旧的 `plan/r1-*.md`、`plan/version-roadmap.md` 和里程碑文档提供历史材料；其中“先完成旧版本迁移验收”“首发仅保留手动升级”等旧口径，以本次用户确认的范围为准。此调整不要求删除历史记录、取消已有兼容代码或绕过现有校验。

本轮自更新的目标是恢复版本检查和用户主动执行的更新操作；渠道模型自动刷新属于已有独立功能。首发范围不增加无人确认的后台自动安装，也不扩展到生图、WebSocket、companion 或流式前导事件缓存第二阶段。

历史云端分工见 L5.4 和[云数据库交接说明](cloud-database-test-handoff.md)。当前两种数据库已改用独立本地实例验收，Docker 已由执行会话使用 WSLc 完成本机 amd64/SQLite 验收并交付 tar；用户可以按新交付 README 导入测试。旧云库的失败/非空状态及授权边界继续保留。

## 2. 编制时已核实的状态

| 项目 | 当前事实 | 执行时的处理 |
| --- | --- | --- |
| 上游基线 | 从上游 v0.13.6 分叉，选择性吸收 v0.13.8；后续 UA 修复基线为 `1654a38` | 先整理本地记录，不声称已经追平最新上游 |
| 独立名称与地址 | Go 模块、环境变量前缀、Compose、构建仓库信息、README 下载链接已改为 Octopus Roam | 核对一致性，避免重复更名 |
| 发布版本 | `main.go` 仍为 `// Version v0.13.6` | 第一轮用构建参数注入本地版本；正式版本注释留到第二轮 |
| 自更新 | 本轮 loopback 发布源经生产 API/调度/helper 实际替换、重启和版本确认通过，浏览器三终态通过；更新夹具仅注入发布源及 RC 版本 | 真实 Release、真实下载及两个远端版本之间的在线升级仍待验 |
| 归档 | B 候选 7 个 ZIP 的 Unix creator、权限、路径、解压清单和哈希通过；真实许可证报告已嵌入 | 旧候选不追改；公开发布前继续核对 LGPL/MPL 等分发材料要求 |
| CI | 2026-10-20 已授权应用九文件补丁及两文件补充门禁；普通 push/PR 只测试，发布为显式手动入口并固定同一 SHA，publish 默认 false；actionlint/完整本地入口通过 | 只读 GET 已确认仓库 admin、Actions enabled/all、默认 token read；尚无线上运行，未提交或推送，详见 R1 远端报告 |
| 镜像 | Compose 和新 CI 均使用 GHCR；旧 CI 的 Docker Hub 无条件登录已移除，RC 不更新 latest | read:packages 生效后，精确包及版本仍为 404 / Package not found；当前身份未找到该包。包级权限/可见性、真实多架构镜像与发布按 R3/R4 验收 |
| Git | 本地分支为 `test`、`beta`、`master`；没有 `main` | 第一轮在当前分支准备改动，分支集成和默认分支切换留到第二轮 |
| 远端状态 | 已认证 GET 确认目标 public/fork，默认及唯一可见分支 master；未见标签、Release、登记的 workflow/run；master 未保护、release environment 未创建 | main 集成、分支与 environment 保护需另定具体方案并获准执行；GET 不证明实际发布权限或发布成功 |
| 既有验收 | 更名记录包含 Go 全量/race、前端检查、Windows 实际运行、SQLite 恢复及 Linux amd64 编译通过 | 作为历史证据，最终候选产物仍需本轮验证 |
| 数据库改动 | 新增 Webhook 和刷新记录 2 张表；渠道新增 3 列、分组新增 1 列；扩展 JSON 配置、刷新事务/行锁和备份快照 | 验证新增读写路径，沿用上游数据库驱动和连接方式 |
| 本地工具 | Go、Node.js、pnpm、Git Bash、GCC 可用；actionlint 1.7.7 已按具体授权放入本次任务目录并验证，未加入 PATH；数据库/容器工具沿用各 R0 报告 | 固定版本工具获取及后续新增范围仍遵守具体授权；本地工具结论不能替代 runner 环境证据 |
| 运行时网络 | `internal/task/init.go` 注册启动即执行的模型价格更新；恢复版本检查后 UI 也可能访问发布源 | 启动本地验收实例前落实隔离，不能仅凭绑定 localhost 就认为不会访问远端 |
| 历史格式告警 | `git diff --check master...test` 报 `plan/v1.0plan.md:1612` 末尾空行 | 区分本轮新增问题与历史告警；按允许范围修正或准确记录 |

### 必读材料

- 本计划和会话提供的 `AGENTS.md` 指令。
- [云数据库凭据与会话交接](cloud-database-test-handoff.md)：一次性明文 profile、输出边界、两个测试库的验收与恢复范围。
- [更名验收记录](octopus-roam-rename-result.md)、[上游同步记录](upstream-sync-v0.13.8-result.md)。
- [升级与备份说明](../docs/upgrade-r1.md)、[本地部署说明](../docs/local-deployment.md)、[已知问题](../docs/known-bugs.md)。
- [历史 R1 结果](r1-result.md)、[迁移审计](r1-migration-audit.md)、[版本路线图](version-roadmap.md)。
- [流式错误第一阶段边界](stream-error-cross-request-retry-fix-stage1-result.md)、[隐私规则实现记录](privacy-rule-management-implementation.md)、[手动切换与冷却重置记录](failover-manual-switch-and-cooldown-reset-result.md)。
- 实现入口：`internal/update/`、`internal/server/handlers/update.go`、`web/src/api/update.ts`、`web/src/components/modules/setting/Info.tsx`、三份 locale、`scripts/build.sh`、`scripts/windows/`、`.github/workflows/`。

## 3. 两轮边界与授权规则

### 第一轮：本地准备、实现与验收

使用已有本地源码、Git 对象、依赖缓存、工具、测试数据及本地模拟服务。允许在任务范围内准备文档、局部补丁、构建脚本方案、测试和隔离产物。生产构建、数据库和更新验收均使用本次创建的测试目录与合成数据。

第一轮具体边界：

- Git 查询仅使用本地状态与对象；不联网查询或同步仓库，不创建/推送标签，不创建或切换 `main`。
- 不查询 GitHub/镜像仓库的实际服务状态，不访问真实发布 API，不下载真实 Release，不拉取或推送远端镜像，不运行线上 Actions。
- 不联网追踪上游新提交，不调用真实供应商模型或 Webhook，不执行联网价格更新、图片生成、依赖下载或工具安装。
- 本地 HTTP 模拟服务只服务合成数据。更新元数据、归档、校验文件、模型接口和通知接收端全部由本地夹具提供。
- 既有本地数据库或镜像仅在已确认属于测试资源、使用范围明确且不接触业务数据时使用。缺少资源则记录待环境项，继续完成其他独立工作。
- 唯一可单独授权的远端例外是用户指定的 MySQL/PostgreSQL 云测试库。按云数据库交接说明完成本地测试入口和范围审查后连接；不因取得凭据而扩大到其他数据库、云管理接口或外部服务。
- 当前源码中的价格更新、UI 版本查询、构建脚本中的工具下载、Dockerfile 中的软件包安装都可能引发出站请求；必须先审查并隔离。隔离方案尚不可用时，不启动对应验收步骤。
- Go 使用本地工具链与已有缓存，禁止自动下载工具链/模块；可在命令进程中使用 `GOTOOLCHAIN=local`、`GOPROXY=off` 等限制，不关闭校验或修改持久环境。前端优先直接使用已有 `node_modules`，缺依赖时记录事实。
- 临时构建、日志、数据库、模拟服务材料放在 `build/release-local/<本次唯一编号>/`；不能复用或覆盖既有运行目录。

### 用户授权门禁继续适用

本次指令授权编写计划；执行会话按用户当时的执行指令推进。计划中列出某项工作，不等于用户已批准该项所有具体命令，也不代表获得任何远端授权。

遇到 `AGENTS.md` 明列的删除文件、整文件覆盖、批量修改、大规模重构、依赖调整、计划外 commit、Git 历史改写、环境或 CI 修改、数据库变更等操作，先完成可独立进行的检查，给出具体目标、文件/实例、命令和影响，再取得针对该操作的授权。涉及的补丁和方案应先整理到可审查程度，避免只拿抽象意向要求批准。

普通只读核查、已授权范围内的局部修复与必要检查继续推进。已获得针对具体操作的授权后，不重复询问同一操作。缺少某一项授权或环境时，只暂停依赖它的步骤。

第一轮不预设 commit；整理差异和中文 commit summary 草稿即可。不得把“计划里写了后续发布”解释为允许远端操作。不得因文档引用了历史子代理流程就自行派发代理；用户未要求时单会话执行。

### 第二轮：等待用户决定

以下全部留到第二轮：联网补齐材料和工具、最新上游在线比对、远端状态核查、仓库/凭据/权限配置、分支集成与默认分支切换、版本发布提交、标签、push、PR/MR、Release、镜像发布、真实在线升级、公开下载和拉取验收。

第二轮授权也应对应具体目标与操作。L5.3 中单独获准的云测试数据库访问可提前执行，其余边界保持上述约定。发布项目不自动包含对用户现有业务实例或生产环境的部署。

## 4. 任务总览

| ID | 工作 | 主要产出 | 前置条件 |
| --- | --- | --- | --- |
| L0 | 核对基线、材料、环境与本地网络隔离 | 环境清单、起点状态、可执行/待环境项目 | 无 |
| L1 | 本地上游差异整理与发布文档 | 功能对照、双语 README、CHANGELOG、Release Notes 草稿 | L0 |
| L2 | 独立发布配置、构建与 CI 本地准备 | 地址/版本/归档约定、本地构建入口、CI 补丁或待授权提案 | L0 |
| L3 | 恢复独立自更新并用本地发布源测试 | 后端、界面、安装替换流程与针对性测试 | L2 的版本和产物约定 |
| L4 | 最终代码回归 | 与最终源码对应的检查记录 | L1–L3 实现收口 |
| L5 | 本地产物、数据库自动验收与 Docker 人工交接 | 安装包、运行记录、三数据库验证矩阵、Docker 测试包 | L4；数据库环境与具体访问授权就绪 |
| L6 | 审查差异并交接第一轮结果 | 本地结果书、产物清单、待办和远端操作草案 | 所有可执行本地工作完成 |
| L6.5 | 本地最终候选收口与第二轮入口检查 | 冻结源码快照、可审查候选包、归档权限/许可证/哈希证据、受影响回归和安全清理 | L6；最后一次本地修复、构建输入和工具状态已确认 |
| R0–R5 | 远端准备、main 集成、RC、线上验收、正式发布 | 独立 Release 与镜像 | 用户另行决定并授权 |

L1 和 L2 的资料整理可以交错进行；修改同一文件时顺序执行。L4 之后如有代码修复，补跑受影响检查并重新构建受影响产物；不要拿修复前的程序继续作为最终验收对象。

## 5. 第一轮详细执行计划

### L0：接手检查与本地执行环境

**目标**：确认真实起点、可用工具和缓存，防止继承过期结论或误操作现有运行实例。

只读核查命令示例，在仓库根目录分别执行：

```powershell
git status --short --branch
git rev-parse HEAD
git branch -a
git log -12 --oneline
git diff --stat
git diff --check
Get-Command go,node,pnpm,bash,gcc,docker,mysql,psql -ErrorAction SilentlyContinue
```

执行要求：

1. 记录当前 HEAD、分支、已有未提交/未跟踪文件及来源。本计划是交接文件，不应作为无关文件删除。HEAD 变化时先阅读差异，按实际状态续作。
2. 检查 `.tmp-go-cache/`、`.tmp-go-mod/`、`web/node_modules/`、现有工具和构建记录；只读确认可能已安装但不在 PATH 中的 Docker/数据库工具。
3. 如存在 Docker，先确认实际连接的是本机 daemon。不要操作远端 Docker context，也不要为检查可用性拉镜像。
4. 建立本轮独立目录和证据索引，记录工具版本、测试程序和数据库的本地资源范围。
5. 为本地测试确定出站隔离方案：单元测试注入 HTTP client/transport；完整进程使用已有可用的隔离执行环境或经批准的隔离方案。仅设置应用代理不能证明所有直接请求都被隔离。
6. `internal/task/init.go` 的价格任务启动即执行；直接运行完整程序前必须处理这条访问路径。若只能用测试入口关闭/替换后台任务，应明确那是测试证据，完整生产入口验收仍保留待验状态。

**验收**：得到明确的本地可执行清单；阻碍附带实际检查结果。工具缺失、隔离不足或缓存不足不等于测试失败，更不等于测试通过。

### L1：上游差异与用户文档

**材料**：`README.md`、`README_zh.md`、`CHANGELOG.md`、`docs/upgrade-r1.md`、`docs/local-deployment.md`、`docs/known-bugs.md` 及第 2 节列出的实现记录。

**交付物**：

- `plan/release-upstream-feature-comparison.md`：以本地已知基线整理“上游已有、已选择吸收、本分支新增、明确未纳入”的对照，附代码或提交依据。
- 完善后的双语 README、CHANGELOG 和升级/部署说明。
- `plan/release-notes-v0.1.0.md`：可供用户审查的首发说明草稿，明确尚未发布。

**内容要求**：

1. 写清 fork 来源 v0.13.6 与选择性吸收 v0.13.8；与“最新上游”的在线差异另列为第二轮待核对，不据本地记录宣称最新。
2. 覆盖路由参数与默认策略、故障转移手动切换/冷却重置、模型刷新与分组同步、严格匹配、协议限制、禁用渠道筛选、自动化与并发限制、日志/SSE、Webhook、隐私规则管理/预览、加密备份和 Windows 菜单。
3. 核实每个功能的默认值与实际限制；渠道模型自动刷新和程序版本更新分别说明。
4. 清理用户文档中的旧 `Solo` 文案；保留正确的上游出处、许可证及明确标注的历史记录。
5. 修正“拦截所有上游错误，避免中断 Agent 任务”等超出实现范围的承诺：流式响应提交后发生错误仍可能结束响应，第一阶段主要保护跨请求重试与健康状态。
6. 自更新文档随 L3 的最终支持行为更新；不得提前声称线上更新已通过。RC 前 Compose 需要显式 RC 标签，首个稳定镜像发布前 `latest` 不可作为可用安装示例。
7. 数据库说明采用当前版本新安装/恢复口径，不新增旧数据库迁移项目；已有备份身份、格式校验与导入语义如实记录。
8. 核对安装命令、支持平台、截图与当前界面是否一致，记录需要更新的素材；实际界面证据使用本地测试数据。
9. BUG-001 保持已有“低优先级、暂不处理”决定，并在发布说明中适当列出；不借发布收尾扩大修复范围。

**必要检查**：链接和引用路径、双语内容一致性、示例地址与构建配置一致；文案与代码核对。无需为纯文档变更编写照搬文案的单元测试。

### L2：独立配置、构建与 CI 的本地准备

**材料/允许讨论的改动范围**：`main.go`、`internal/conf/version.go`、`docker-compose.yml`、`scripts/build.sh`、`scripts/dockerfile/`、`.github/workflows/{build,release}.yaml`、`web/src/components/modules/setting/Info.tsx` 和相关文档。实际修改按授权边界执行。

**统一约定**：

| 项目 | 目标 |
| --- | --- |
| 仓库/模块 | `Ink-lotus/octopus-roam` / `github.com/Ink-lotus/octopus-roam` |
| 产品/可执行文件 | `Octopus Roam` / `octopus-roam`，Windows 加 `.exe` |
| 环境变量 | `OCTOPUS_ROAM_` |
| 发布页 | `https://github.com/Ink-lotus/octopus-roam/releases` |
| GHCR | `ghcr.io/ink-lotus/octopus-roam` |
| Docker Hub | 待用户决定是否启用；禁止虚构用户名或依赖占位凭据发布 |
| 本地构建版本 | `v0.1.0-local.<本次编号>`；前后端一致，记录源码状态 |
| 公开候选/稳定版本 | 第二轮确定并使用 `v0.1.0-rc.N` / `v0.1.0` |
| 备份身份/内容/容器 | `octopus-roam` / `1000` / `1`，本计划不要求修改 |

**构建工作**：

1. 复核已改好的仓库、作者、镜像、模块和产物名称，保留正确配置。
2. 为本地构建提供显式版本和独立输出目录，不以创建 Git 标签的方式骗过 `git describe`；后续 CI 也应将确认过的版本明确传给前后端和归档步骤。
3. 第一轮不修改根目录 `main.go` 的发布版本注释。准备第二轮所需的具体补丁/步骤，本地通过 ldflags 与 `VITE_APP_VERSION` 注入版本。
4. 现有 `scripts/build.sh` 固定要求 Linux NDK、会清理输出，并可能通过 `go run ...go-licenses` 下载工具。审阅后再使用；不能直接把它当作本地离线构建命令。
5. 若增加本地构建模式，支持已有工具可以构建的平台子集，保持完整发布模式的目标矩阵和失败检查。子集成功只报告子集成功。
6. 归档名继续遵守 `octopus-roam-<os>-<arch>.zip`；内部可执行文件名、Windows 支持文件、README、LICENSE、第三方许可证报告及 `SHA256SUMS` 与更新程序一致。
7. 构建使用仓库中的价格预设；第一轮不执行联网 `scripts/updatePrice.py`。第二轮若重新生成预设，须记录生成后的实际构建输入，补做受影响验证，保证可追溯。

**CI 本地工作**：先形成可审查补丁与影响说明。实际改动 CI 文件属于用户授权门禁；未获对应授权时，交付 `plan/release-workflow-proposal.md`，继续其他本地任务。

补丁需要解决：

- 将版本解析、测试、构建与对外发布步骤分开；测试或构建失败时不能已经向远端推了标签。
- 给发布作业建立对同一源码版本检查结果的依赖；独立的普通 build 作业成功不能替代测试门禁。
- 解决“构建从 Git tag 取版本”与“构建成功后才公开 tag”的顺序依赖。
- `changelogithub` 依赖 HEAD 上的标签；调整顺序时同时处理它，防止按分支名意外生成额外 Release。首发说明使用已经审查的内容。
- 保留 RC 的 prerelease 标记和不覆盖镜像 `latest` 的规则；稳定发布时才更新 `latest`。
- 为首次推送/创建 `main` 设置明确的禁发条件，避免携带旧 `v0.13.6` 触发意外发布；不能仅凭路径过滤假定首次推送不会触发。
- 把可选镜像渠道和登录行为对应起来，避免只准备 GHCR 却因无条件 Docker Hub 登录失败。
- 明确失败重试行为：遇到已有标签/同名产物先核实其指向，不自动改写标签或覆盖已公开版本。

**验收**：版本、源码、下载/镜像地址、归档内容形成一致约定；已有本地工具完成 shell/YAML 检查。未安装 actionlint 时记录静态审查范围，不联网安装，不声称线上 Actions 已通过。

### L3：恢复独立自更新

**材料与改动范围**：`internal/update/`、`internal/server/handlers/update.go`、`web/src/api/update.ts`、`web/src/components/modules/setting/Info.tsx`、三份 locale；如需 Windows 替换辅助程序，仅扩展 `scripts/windows/` 中直接相关部分。先确定具体补丁范围；需要新增依赖时另行处理授权和本地资源条件。

**实现要求**：

1. 生产更新源固定为 Octopus Roam 独立仓库。查询、下载、校验来自同一版本；不得退回上游仓库，不通过普通请求参数接收任意下载地址。
2. 定义清楚版本策略：稳定版只升级到更高稳定版；官方 RC 可升级到更高 RC 或对应更高稳定版；`dev`、`unknown` 和本地构建明确显示开发状态，不自动推断其先后关系。版本比较覆盖数字段与预发布序号，禁止直接按字符串大小比较。
3. 区分“尚无独立版本”“已经最新”“请求失败”“当前平台不支持在线替换”，界面给出可理解的状态。没有发布时不提供会失败的安装按钮。
4. 保留管理员认证。更新接口、前端状态、轮询与重启过程一致；新程序尚未确认启动时，不能展示已经更新成功。
5. 按当前平台选择正确 zip，并校验对应 `SHA256SUMS`；HTTP 状态、超时、大小、缺失资产、截断和校验失败都在文件替换前处理。对凭据、重定向和日志保持必要限制，不把 PAT 带给任意下载主机。
6. 先下载和校验到隔离临时位置，再执行替换。保留解压路径校验，归档内容不得写到目标安装范围之外；安装包不能覆盖用户配置、数据库和备份目录。
7. 同一实例最多一个更新操作；和 Windows 启停/更新已有互斥机制协调。正常退出、替换、重启要有明确失败状态及可恢复的旧程序/支持文件。
8. Windows 在原进程退出后替换占用中的 exe；如果版本需要更新菜单或支持脚本，应随同一归档更新并保持一致。检查空格/中文路径及当前工作目录；不修改用户实际登录自启项来完成测试。
9. 容器部署提供版本信息和镜像升级指引，阻止在容器内执行不持久的程序替换。真实镜像更新留到第二轮。
10. Linux/macOS/Android 依据实际实现与运行环境分别记录支持范围；不能以 Windows 验收代替其他平台重启与替换证据，也不能静默缩减已有发布平台。

**本地测试方式**：

- 用本地 HTTP 测试服务/传输层注入模拟 GitHub 元数据、zip 和校验文件；第一轮禁止访问实际 GitHub API 或资产域名。注入路径不能变成公开的任意源升级接口。
- 用两份隔离的测试构建执行一次实际进程更新，确认前后端版本变化、原数据保留、旧进程退出和新进程启动。可通过构建参数模拟 RC 版本，不创建真实 Git tag。
- 替换现有“始终停用更新”的测试断言，覆盖恢复后的合同；仍保留防止跨产品来源和未认证更新的检查。

**最小用例集**：

| 场景 | 预期 |
| --- | --- |
| 独立仓库无发布、版本相同/更旧 | 清楚反馈，禁止无效替换 |
| `v0.1.9` / `v0.1.10`，`rc.2` / `rc.10`，RC → 稳定版 | 版本顺序与通道规则正确 |
| 错误平台、缺少 zip/校验项、请求失败、损坏归档、哈希不符 | 原程序和数据保留 |
| 并发点击、写权限不足、旧进程无法正常退出 | 拒绝或明确失败，不产生半次更新 |
| 替换/新程序启动失败 | 旧程序/支持文件可以恢复，界面不报成功 |
| Windows 完整包更新、重启后菜单及版本检查 | exe 与支持文件一致，配置和数据保留 |
| 容器内请求更新 | 返回对应限制及镜像升级方式 |
| 本地模拟成功升级 | API、界面、进程和持久化结果一致 |

**验收**：针对性 Go/前端检查及可用平台的真实本地进程更新通过，支持矩阵和失败恢复过程有记录。远端源可用性与真实 Release 升级仍明确标为第二轮待验。

### L4：最终代码回归

对 L1–L3 收口后的源码运行规定检查，保留命令、退出状态和日志路径。先确认工具链/依赖已经可离线使用，所有测试上游均为本地合成服务。

```powershell
go test ./... -count=1
go test -tags=jsoniter ./... -count=1
go test -race ./internal/relay ./internal/op ./internal/task ./internal/server/handlers ./internal/update -count=1
go vet ./...
pnpm --dir web lint
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File scripts/windows/test-menu.ps1
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File scripts/windows/test-autostart.ps1
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File scripts/windows/test-update-local.ps1 -Layout Packaged
git diff --check
```

发布实际使用 `-tags=jsoniter`，因此需要覆盖该构建条件。Windows race 使用现有 GCC；若缺少条件，记录具体原因，不用非 race 的结果冒充。

前端生产构建包含 TypeScript 检查；输出指向本次新建目录，并注入与后端相同的本地版本。命令形式为 `pnpm --dir web build --outDir <本次隔离源码目录>/static/out --emptyOutDir false`，执行前把占位符替换为已确认的绝对路径。

检查还应覆盖三份 locale 的新键一致性，以及自更新状态、认证、取消/失败反馈的实际交互。核心回归沿用已有路由、模型同步、并发、日志、Webhook、隐私与备份测试，不为发布计划重新复制一套相同测试。

出现失败先定位；发现确属已有问题时记录证据与影响，按是否影响本次发布处理。通过后，只有新改动、失败或未解决问题需要时才复验相应范围。

**验收**：本轮要求的检查通过，或每个未完成项目都有明确原因及发布影响；不能把缺环境或待授权写成通过。

### L5：本地产物、安装与数据库验收

#### L5.1 构建和归档

在 `build/release-local/<本次编号>/` 下复制必要源码到独立构建目录；嵌入本次生成的前端再编译后端。产物记录版本、源码 HEAD、未提交差异及源码/产物哈希，不能只凭 HEAD 标识包含未提交修改的构建。

`scripts/windows/update-local.ps1` 默认还会检查既有运行目录，并非空目录首装入口；不得直接使用它的默认路径，也不得对现有运行实例执行 `-Apply`。先审阅并使用隔离测试入口/准备流程。

当前完整归档矩阵如下，各项分别记录“构建、归档、实际运行、更新验收”状态：

| 平台 | 架构 | 第一轮处理 |
| --- | --- | --- |
| Windows | amd64 | 生产构建、完整 zip、菜单、安装、重启与本地更新验收 |
| Linux | amd64、arm64、arm、386 | 使用已有工具交叉编译；实际运行按本地设备/容器条件记录 |
| macOS | amd64、arm64 | 可用工具构建；无本地运行环境时如实保留待验 |
| Android | amd64、arm64、arm、386 | 需要适配的 NDK/编译环境；不可用则待环境，第一轮不下载 |
| Docker | linux/amd64、linux/i386、linux/arm64、linux/arm/v7 | 只使用本机已有 daemon、镜像/缓存及可离线构建条件；实际运行逐项记录 |

Dockerfile 会执行 `apk`，仅有基础镜像并不代表可以离线构建。镜像测试不得隐式拉取基础镜像或安装包；使用已有完整缓存/离线材料，否则保留待验项。

核对实际 zip 内的可执行文件、Windows 入口与支持目录、README、LICENSE、第三方许可证报告以及校验文件。许可证工具缺少本地缓存时，记录完整打包阻碍，不能用空报告冒充完成。

#### L5.2 实际程序流程

使用全新测试目录和数据库，只绑定本地访问地址，端口避开现有服务；后台进程隐藏窗口，并记录进程归属与正常停止方式。测试结束正常退出本轮启动的进程。

最小流程：首次启动与管理员登录 → 创建渠道/分组/API Key → 本地模拟普通请求与流式请求 → 取消/失败切换 → 日志和统计 → 新增设置保存 → 重启后核对 → 加密备份恢复 → 自更新流程。

Windows 另验完整压缩包解压后的四个菜单操作、重复启动、端口冲突和特殊字符路径。自启使用测试目录/替身，不操作用户真实 Startup 配置。前端确认桌面/窄屏、键盘基本操作、三语和更新后缓存/版本状态。

真实供应商行为及公开网络环境下的更新不是这一轮的证据范围，留到第二轮明确安排。

2026-10-15 补充验收已覆盖：原始最终 Windows 包的普通/流式、取消、失败切换、日志/SSE、精确统计、正常重启和登录/渠道/分组/主页/日志列表；同份冻结源码以 overlay 仅注入 loopback 发布源及 RC 版本，串通生产 API/调度/helper 到实际替换、重启、PID/版本/哈希与设置确认。visible Edge 验证 pending、成功、回滚、30 秒未确认及短轮询停止。早先日志详情断言失败和审批超时记录保留；后续在独立最终包实例及可见 Paseo 页面补齐详情请求/响应正文、原生点击请求体标签和关闭，不再保留该项为待验。完整边界与失败记录见本轮报告，真实发布源和 Unix 实机仍待验。

#### L5.3 数据库验收，作为产物验收的子项

**目的**：在 SQLite、MySQL、PostgreSQL 上确认本分支新增表/字段、JSON 配置、刷新事务/行锁与备份快照正确。沿用上游的驱动支持；不开展旧版本升级、降级或数据库引擎互迁专项。

对每一种数据库使用一次性专用测试库及合成数据，执行同一套流程。测试结束后的远程重置由用户完成；本次空库初始化/恢复使用单独空逻辑库或明确授权后重置的测试区，不能将已有库启动计为空库初始化：

1. 空库启动：自动建表与默认设置成功，新增字段及两张新表可读写。
2. 功能读写：创建/修改渠道和分组，保存协议、并发、自动跟随、匹配模式、路由策略、Webhook 与隐私规则，核对 JSON 内容和显式禁用状态。
3. 模型刷新：本地模拟成功结果、成功空列表与失败结果；核对授权/分组成员同步、刷新记录，失败时不留下部分写入。涉及行锁和事务的新增路径应实际执行。
4. 重启：正常停止并重新启动，配置及持久化状态一致。
5. 当前版本备份：导出口令加密文件，恢复到同一数据库引擎的另一份空库，核对本产品身份、渠道/凭据/分组关联、禁用状态和新增配置。
6. 失败恢复：错误口令、损坏内容及非法关联拒绝写入；一次实际写入失败应回滚，不留下半份导入数据。

备份的既有边界仍适用：管理账号、迁移记录、日志、内存状态、刷新诊断不属于 JSON 配置备份。恢复到空实例后不要求这些内容重现；备份也不代替完整数据库副本。

MySQL/PostgreSQL 采用执行会话自动验收的分工。缺少本地实例时，可由用户提供本次测试专用的云测试库，按[云数据库交接说明](cloud-database-test-handoff.md)在仓库外创建一次性明文 profile，交接仅使用别名和必要的档案路径。执行者先准备集成测试入口、实际命令和影响范围，取得对应授权后仅连接这两个测试目标；没有 profile 或授权时记录“待环境/待授权”，继续其他独立工作。程序已内置驱动，无需本机安装数据库服务或 mysql/psql 客户端。第一轮不为此自动安装工具、拉取镜像或修改云管理设置。

**验收**：A2 的两份独立 SQLite 证据保留；2026-10-15 新候选在 PS 7 与 PS 5.1 均完成初始化、设置/渠道/分组/API Key、禁用/关联状态、正常重启、加密备份、独立空库恢复及错误口令/损坏导入回滚，正常退出码 0。见该历史候选 `checks/logs/sqlite-pwsh.log` 和 `sqlite-powershell.log`。2026-10-18 当前 mysqlfix 候选另在本地 MySQL/PostgreSQL 各通过完整 35 项，包含刷新、行锁、重启、加密空库恢复和回滚，详见[本地数据库验收报告](release-local-database-acceptance-20261018.md)；db-check 的冒烟结果不能代替完整验收。

#### L5.4 云端测试分工：数据库自动验收与 Docker 人工验收

本节保留原云端交接方案。2026-10-19 已按用户后续授权改由执行会话使用 WSLc 完成本机 amd64/SQLite 验收并交付 tar，当前结果见[Docker 交付报告](release-docker-delivery-20261019.md)；以下云端方案不表示本机仍缺少镜像或等待用户代测。

用户只人工测试 Docker，数据库的完整功能验证由执行会话自动完成。两项都可以在正式发布之前进行，不需要先推 GitHub、创建 Release 或公开镜像仓库。云数据库自动验收遵守单独授权的目标范围；用户决定何时自行部署 Docker 测试包。Docker 的离线材料、构建命令、占位配置和 C1–C9 清单见[Docker 人工交接材料](docker-local-test-handoff.md)及[本地 Compose 模板](docker-compose-local-test.yml)。

**数据库安排**：

- 用户通过云厂商控制台准备独立测试库及其测试账号即可，账号须具备该测试库内自动建表、改表、索引和读写所需权限。应用会创建表，通常仍需先创建数据库本身。
- 执行会话在本地运行专门的集成测试程序或隔离应用，连接用户提供的云测试库，分别执行 L5.3 的流程；无需用户人工完成数据库功能检查，也不要求本机安装数据库服务或命令行客户端。
- 档案位于仓库外的 `~/.config/octopus-roam/db-profiles.json`，保存 `roam-mysql-test`、`roam-postgres-test` 等别名及完整连接字段（包含密码和 TLS 参数）。用户在独立终端填写本次测试信息；`db-check` 进程在内存中读取字段，仅输出预先允许的结果字段。离线结构检查已实现；`--connect` 只在明确授权后建立指定连接并执行迁移/读写冒烟。它不是应用已有的 DSN 配置；明文档案的暴露边界和更强隔离方案见云数据库交接说明第 1.1 节。
- 两个测试库、账号和 profile 只保留到本次测试结束。方案中明确授权的合成数据清理及设置恢复由测试程序完成；测试结束后由用户删除明文档案、重置远程数据库并处理测试账号，测试程序不自动删库或改密。
- 完整恢复优先使用每种引擎的另一份空逻辑库，同一服务实例即可。只有一个专用逻辑库时先完成其他用例；需要重置专用测试表时，先列出范围和命令并取得对应授权，再执行，最终由用户按本次测试安排重置远程数据库。不能用对原库的增量导入冒充空库恢复。
- 用户的 Docker 人工验收可以先使用 SQLite。若用户另外选择在容器里连接已提供的云测试库，仅补配置/连接冒烟；两种数据库的完整功能验证仍由执行会话负责。
- 记录数据库的实际版本、连接方式及 TLS 要求。优先使用数据库直连地址，连接池代理如有使用则单独记录，避免其会话/事务行为混入首次验收。
- 连接信息由用户在部署环境填入，交付模板和报告使用占位符。MySQL 当前只在 DSN 完全没有查询参数时补默认参数；若云端 DSN 已含 `tls` 等参数，需同时核对 `charset=utf8mb4`、`parseTime=true`、`loc` 等选项。PostgreSQL 的 SSL 参数按云服务要求配置，不直接照抄关闭 SSL 的本地示例。

**镜像交付有两条可行路径**：

| 路径 | 交付内容 | 用户在云端执行 |
| --- | --- | --- |
| 已有可用镜像构建环境 | 指定架构的 Docker 镜像 tar、文件 SHA-256、镜像 ID、版本和源码记录，以及部署模板 | 上传并用 `docker load` 导入，再按模板启动；无需先发布 registry |
| 本机没有可用构建工具/离线基础镜像材料 | 已嵌入前端的 Linux 程序、完整 Docker 构建目录、版本/哈希记录和部署模板 | 在有 Docker 的云主机执行构建，再启动测试；不要求云主机安装 Go、Node.js 或 pnpm |

镜像 tar 必须由实际镜像导出。2026-10-19 已使用 WSL Containers 生成并导出声明集成镜像 `octopus-roam:v0.1.0-local.mysqlfix-20261018-03-licenses.1`，交付在 `build/release-local/r0-docker-delivery-20261019-03/delivery/`，含 tar、同版本应用源码 ZIP、镜像包装源码及 SHA256SUMS；已验证导入 ID、层/配置摘要与材料哈希。原 `mysqlfix-20261018-03/docker-context/` 及其 `daemon-not-run` 状态文件保持历史冻结；新结果见[交付报告](release-docker-delivery-20261019.md)。旧人工模板的 B 阶段路径不作为当前版本选择依据。

构建目录至少包含：

- `build/docker/linux/<目标架构路径>/octopus-roam`，目录结构与 Dockerfile 的 `TARGETPLATFORM` 一致；ARMv7 使用 `linux/arm/v7`。
- `scripts/dockerfile/Dockerfile`、`scripts/dockerfile/entrypoint.sh`、`LICENSE`、`build/THIRD_PARTY_LICENSES.csv`。
- 记录版本、平台、源码状态、程序哈希的构建信息，以及对应的构建命令。命令要显式指定目标平台、Dockerfile 和本次测试镜像标签。
- 指向本次测试镜像的 Compose 模板、仅含占位符的数据库配置示例、启动/检查/正常停止说明和本节的结果表。A3 已准备模板；实际配置修改仍遵守授权门禁。

按云主机 CPU 选择 `linux/amd64` 或 `linux/arm64` 等实际平台；一个架构的云端测试只证明该架构的运行结果。云端构建会获取 Alpine 基础镜像及 `apk` 软件包，这属于用户启动的云端步骤；执行代理不在第一轮自动替用户进行。第三方许可证报告等材料未齐全时应标明构建包尚不完整。

**Docker 验收清单**：

| ID | 检查 | 通过标准 |
| --- | --- | --- |
| C1 | 平台、镜像与版本 | 镜像能在目标架构启动；实际镜像 ID、程序版本、前端版本与交付记录一致 |
| C2 | 首次启动和配置 | 新数据目录可初始化；端口映射、环境变量、管理员登录和 API 可用；没有反复退出/重启 |
| C3 | 容器数据持久化 | 保存配置和测试数据后，正常停止并重新创建容器，挂载数据仍在；SQLite 场景包含完整 `/app/data` 目录 |
| C4 | 容器数据库配置 | 默认可用 SQLite；用户选择云数据库时检查连接参数生效及基本读写。两种数据库的完整 L5.3 验证由执行会话自动完成 |
| C5 | 网关与前端 | 普通请求、流式响应、取消、失败切换、日志/SSE、模型刷新及主要设置操作成功；通过反向代理时另确认流式输出没有被缓冲 |
| C6 | 运行环境 | HTTPS 上游/云数据库连接所需 CA/TLS 正常，时间显示合理；如使用 PUID/PGID，挂载目录可写且重启后权限仍正常 |
| C7 | 正常停止 | 停止容器时应用能正常退出并保存数据；停止宽限时间覆盖本次测试请求，日志没有被强制中断的迹象 |
| C8 | 容器中的备份操作 | 以测试数据检查页面导出/下载与恢复操作可用，人工测试可用 SQLite；云数据库的完整恢复与失败回滚由执行会话自动验证 |
| C9 | 镜像替换方式 | 重建容器或换用另一候选镜像时保留数据挂载/外部数据库配置；版本检查按容器方式提示，不在容器内替换程序 |

当前 Dockerfile 没有定义 HEALTHCHECK；`docker ps` 显示 Up 不能替代页面/API 和数据读写检查。TLS、PUID/PGID、反向代理等项目按实际部署方式记录，未使用的标记“不适用”并说明，不把它们泛化为额外平台支持承诺。

用户回传 Docker 的测试包版本/哈希或镜像 ID、CPU 架构、Docker/Compose 版本、实际使用的数据库类型、C1–C9 的人工结果以及失败步骤和脱敏日志。执行会话自行产出 MySQL/PostgreSQL 的 L5.3 自动验收结果，不要求用户重复人工验证。缺少证据的项目继续保留待验。

这一轮云端自测可作为容器和数据库功能证据。R4 仍需核实正式 RC 仓库中下载/拉取的对象确实可用，并核对是否与已测产物一致；产物一致时只补渠道、版本和启动冒烟，不机械重复整套测试。重新构建后基础镜像、应用或配置发生变化时，复验受影响部分。

### L6：第一轮收口与交接

**交付文件**：

| 路径 | 内容 |
| --- | --- |
| `plan/release-local-result.md` | 最终源码状态、完成项、命令与结果、实际支持矩阵、阻碍和下一步 |
| `plan/release-upstream-feature-comparison.md` | 本地已知上游与本项目功能对照、最新上游待核实部分 |
| `plan/release-notes-v0.1.0.md` | 首发说明草稿，和最终实现一致 |
| `plan/release-remote-actions.md` | 第二轮待决事项、操作顺序、触发条件和待审批的具体动作 |
| `plan/release-workflow-proposal.md`（需要时） | 尚未获准应用的 CI 补丁及其影响；已应用时注明实际文件 |
| `build/release-local/<本次编号>/` | 产物、源码与文件哈希、合成夹具、测试日志及证据索引 |
| 云端自测交付包（采用 L5.4 时） | 实际镜像 tar 或完整构建目录、平台/哈希信息、Compose/数据库占位模板及 C1–C9 结果表 |
| Docker 当前交付材料 | `r0-docker-delivery-20261019-03/delivery/`：实际镜像 tar、应用源码 ZIP、包装源码、运行 README、逐项本地结果与 SHA256SUMS；旧人工/Compose 模板保留为历史方案 |

结果表统一使用：`未开始`、`完成（本地）`、`待环境`、`待授权`、`待数据库自动验收`、`完成（云数据库自动验收）`、`完成（WSLc 本地容器验收）`、`待用户 Docker 验收`、`完成（用户 Docker 验收）`、`待远端验收`、`失败`。完成项附对应执行证据和实际环境；当前 WSLc 结果不要求用户重复代测，待验项目不能合并进“全部通过”。

交接前检查：

- 工作区差异仅包含已说明且获准的改动；特别核对 CI、依赖锁文件、数据库结构/备份版本及 `main.go` 是否符合实际授权。
- 新文件逐一检查，不能只看忽略未跟踪文件的 `git diff`。历史告警和本轮新增告警分别说明。
- 产物与最后一次修复后的源码一致；文档与实际支持行为一致。
- 日志、示例和报告只保留合成凭据，不包含 PAT、真实连接串、账号密钥、会话标识或业务备份内容。
- 本轮启动的进程已经正常停止；临时资源和目录列入清单，清理涉及删除时另按授权执行。
- 保留最终 diff、文件清单和中文 commit summary 建议；未授权不提交、不切分支。

**停止条件**：可执行的本地工作已经完成，或剩余工作都明确依赖缺失环境/授权/远端。此时交付结果并停止在第一轮，不为清空待办而自动联网。若仍有数据库或平台待验，结论必须是“本地准备已交接，存在待验项”，不能写成“已满足正式发布条件”。

### L6.5：本地最终候选收口与第二轮入口检查

**历史 2026-10-12 A1–A3 状态**：A1 修复 C1–C9，A2 在 `build/release-local/a2-20261012-193213-9b75afef/` 生成候选，7 个归档、969 文件/485 输入哈希、Unix 权限、本地更新源和 SQLite 空库/失败回滚通过。当时未运行 race，也未串通最终包生产更新入口；这些历史限制由本轮逐项补充，不回写旧候选或旧日志。A2 哈希表已按真实文件纠正，旧证据核对仍一致。

当前状态：**mysqlfix-20261018-03 候选已完成 SQL/TLS 修复、default/jsoniter、vet、五个受影响包 race 和七目标构建；989 项文件清单、500 项冻结及工作区输入哈希匹配，8 处许可证副本一致。R0 第二轮在 MySQL 8.4.11 与 PostgreSQL 18.6 各通过全部 35 项，包含真实行锁、四库 migration 3–13 success、加密空库恢复和原始程序正常重启，见[本地数据库验收报告](release-local-database-acceptance-20261018.md)。2026-10-19 已完成[应用许可证本地材料](release-license-materials-20261019.md)：对应源码附件、七平台及前端精确离线复建、LGPL 完整应用再链接、前端来源补充和七个附加声明的新归档。WSLc Linux amd64/SQLite 容器补验与声明镜像 tar 已交付，见[Docker 报告](release-docker-delivery-20261019.md)；继承 Alpine 的本地源码/声明附件随后也已完成。本地材料交接完成，可进入 R1 本地 CI 准备；公开源码入口、其他平台实机和真实发布链路按后续阶段验收。**

**最新材料补充与入口判定**：上述 Docker 任务结束后，Alpine 本地附件已覆盖 18 个安装包、11 个源码 origin、139 个准确配方/补丁文件和 8 个源码归档；126 项源码输入、185 个附件文件及实际 ZIP 解压离线核验通过。旧包名/架构解析错误已在独立附件纠正，旧镜像不追改。详见[Alpine 报告](release-alpine-materials-20261019.md)。本地材料已交接，可进入 R1 的补丁准备与获准后的本地 CI 验证。历史严格状态不等于 R1 入口阻塞；公开分发、其他平台与线上结果各自保留待验。

**目标**：在进入 R0/R1 前冻结最后一次本地源码和构建输入，生成可复现、可审查的本地最终候选，并准备 CI 门禁所需的构建脚本和 workflow 补丁。L6.5 不执行远端 Git/GitHub/registry 操作，不实际修改 GitHub Actions，不连接真实数据库，不构建或发布镜像；它也不要求机械重跑所有 L0–L6 检查，只重跑受影响检查和发布候选必须具备的完整证据。workflow 文件的实际修改放在 R1，需单独取得 CI 修改授权。

**当前候选的已知收口事项**：

- 历史 `Compress-Archive` 权限风险已由 GNU tar/libarchive 归档阶段处理；新候选 7 个 ZIP 的 Unix creator/执行权限、路径、解压和清单均通过。目标 OS 实际运行仍不能由交叉编译推导。
- B 组固定 `go-licenses v2.0.1` 和真实 `THIRD_PARTY_LICENSES.csv` 继续沿用。R0 已补齐 AGPL/LGPL/MPL 对应源码、声明与再链接材料，逐项补充前端来源证据，并生成保留原程序哈希的七个交付归档；公开源码托管与网络入口实际下载仍在发布/部署时验收。详见[许可证材料报告](release-license-materials-20261019.md)。
- Docker 本机 linux/amd64/SQLite 条件已由 WSLc 收口，新包装 context 与 tar 位于 `r0-docker-delivery-20261019-03/`。原 context 和镜像保留。其他架构、外部数据库容器 TLS、Docker Engine/Compose、反向代理及远端升级没有由此泛化为通过。
- 候选目录中的合成 profile、测试日志和手工补充文件不得进入发布附件。最终候选应从冻结源码重新生成，哈希、证据索引和构建信息一次性生成，不能在构建后手工修改哈希文件来追认新增材料。
- 源码快照必须包含实际构建所需的全部输入，至少覆盖 `web` 源码、Go 源码、嵌入静态文件、构建脚本和 Docker 构建材料；如果只保留部分快照，必须在证据中明确其边界，不能笼统称为完整源码副本。

**真实数据库失败后的修复与补验（2026-10-18 更新）**：

- migration 5 的当前/旧结构判断、先于 11 的顺序、列元数据错误、migration 8 统计快照及按库迁移记录已按[修复计划](release-database-acceptance-fix-plan-20261015.md)完成修复；当前候选在本地 MySQL/PostgreSQL 新空主库及空恢复库的完整集成均已通过。
- SQLite 演练已通过实际列元数据与只读库结构核对，不能用该结果替代真实两库的迁移、行锁及业务验收。
- 本地两引擎使用独立 CA、主机名验证与强制 TLS，明文及错误 CA 拒绝检查通过；原始 MySQL 程序新增自定义 CA 支持，并在第二轮真实运行通过。历史云端 `tls_unknown_authority` 未改写为通过，禁止关闭 TLS 或跳过证书验证。
- 历史部分初始化 PostgreSQL 云库和两轮本地已初始化库不得当作空库重跑，不改 `migration_records`，不自动清理。两轮各一次执行授权均已消耗；后续数据库变更仍按具体目标与操作授权。

**执行步骤**：

1. **冻结输入和隔离目录**

   - 记录 `git status --short --branch`、HEAD、差异统计、未跟踪文件和 `.github/workflows` 差异。
   - 建立新的 `build/release-local/<唯一编号>/`，拒绝覆盖已有非空目录。
   - 保留现有运行实例和用户数据不变；所有运行验收使用新的临时目录、临时端口、合成凭据和合成数据。
   - 检查环境变量、Go 缓存、前端依赖和构建工具；命令结束后恢复进程环境变量。

2. **冻结源码快照和构建元数据**

   - 从同一份冻结源码构建前端、Go 二进制、Windows 支持文件和 Docker context。
   - 前后端使用同一个本地候选版本；记录源码 HEAD、工作树状态、构建时间、目标矩阵和许可证报告状态。
   - `main.go` 的正式版本注释仍按第一轮边界保留；本地版本通过构建参数注入，不以创建 Git tag 的方式伪造版本。
   - 预审 `scripts/build.sh` 的显式版本、commit 和输出目录参数；发布构建不能依赖预先存在的 Git tag，也不能在构建时联网改写 `internal/price/presets.go`。

3. **生成并审查发布候选包**

   - 重新生成 Windows amd64、Linux amd64/arm64/arm/386、macOS amd64/arm64 二进制和归档。
   - 有许可证工具和材料时生成 `THIRD_PARTY_LICENSES.csv`，并核对它进入归档和 Docker context；工具缺失时明确阻塞，不生成伪造空报告。
   - 每个归档至少检查可执行文件、`README.md`、`README_zh.md`、`LICENSE`、第三方许可证报告；Windows 额外检查 `.cmd` 和 `windows/` 支持脚本。
   - 解压每个归档到临时目录，检查路径穿越、符号链接、重复文件、意外文件、可执行权限和可运行性；不能只检查 ZIP 文件存在。
   - 生成 `archives/SHA256SUMS`、完整 `file-sha256sums.txt`、`build-info.json`、`evidence-index.md` 和 `verification-log.md`，并验证所有记录与实际文件一致。

4. **执行受影响的代码和运行回归**

   ```powershell
   # 在仓库根目录执行；冻结源码目录也可使用同一组显式产品包。
   # 只修改当前 PowerShell 进程，保留已有的合法 GOSUMDB 配置。
   $previousGoEnvironment = @{}
   foreach ($name in @('GOTOOLCHAIN', 'GOPROXY')) {
       $previousGoEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
   }
   try {
       $env:GOTOOLCHAIN='local'
       $env:GOPROXY='off'
       go test -mod=readonly . ./cmd ./internal/... ./static -count=1
       if ($LASTEXITCODE -ne 0) { throw 'Go regression failed' }
       go test -mod=readonly -tags=jsoniter . ./cmd ./internal/... ./static -count=1
       if ($LASTEXITCODE -ne 0) { throw 'jsoniter regression failed' }
       go vet -mod=readonly ./cmd ./internal/... ./static
       if ($LASTEXITCODE -ne 0) { throw 'Go vet failed' }
       go test -mod=readonly -race -tags=jsoniter ./internal/relay ./internal/op ./internal/task ./internal/server/handlers ./internal/update -count=1
       if ($LASTEXITCODE -ne 0) { throw 'Race regression failed' }
       pnpm --dir web lint
       if ($LASTEXITCODE -ne 0) { throw 'Frontend lint failed' }
       pnpm --dir web exec tsc --noEmit
       if ($LASTEXITCODE -ne 0) { throw 'TypeScript failed' }
       pnpm --dir web build
       if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed' }
   } finally {
       foreach ($name in $previousGoEnvironment.Keys) {
           [Environment]::SetEnvironmentVariable($name, $previousGoEnvironment[$name], 'Process')
       }
   }
   ```

   在 Windows 上如果 `pnpm --dir web exec tsc --noEmit` 受到命令 shim 限制，改用 `web\\node_modules\\.bin\\tsc.CMD --noEmit`，并在验证日志中记录实际命令。

   不把仓库根目录的 `go test ./...` 作为本产品回归入口：历史 `build/` 下保留的源码副本可能被 Go 包扫描到。若在仓库根目录运行，使用上面的显式产品包集合；若在冻结源码目录运行，记录该目录和输入哈希。

   - 本轮已运行受影响 Go/vet/race、前端及脚本检查；最终候选按相同输入复用同轮 jsoniter/race 日志的范围与来源写入证据，不能称所有检查都在最后一次构建中重新执行。
   - 本轮新候选在 PS 7/PS 5.1 的隔离 SQLite 流程均通过，覆盖独立空库、业务关联/禁用状态、设置、加密备份、重启持久化和失败回滚。
   - 本轮已补 Windows 生产 API → 调度/helper → 实际替换/恢复 → 新进程/版本/哈希确认，以及 visible 浏览器更新三终态。loopback 发布源和 RC 版本通过 overlay 注入；真实 Release 与 Unix 实机更新仍需另外验收。日志详情请求/响应正文已由后续可见浏览器交互通过。
   - 对最终候选归档而不是工作区裸二进制执行至少一次启动冒烟；正常停止路径和强制终止路径分别记录，不能用 `Stop-Process -Force` 单独代表正常退出。
   - 对 `db-check` 使用合成 profile 验证有效、缺失别名、非法字段、占位符、TLS 策略、连接失败分类和输出脱敏；不连接真实数据库。

5. **安全、清理和证据检查**

   - 扫描候选、日志、报告和 Docker context，确认没有密码、PAT、真实 DSN、真实主机、账号、会话标识或业务备份。
   - profile 文件只放在仓库外；候选包不包含 profile、原始数据库日志或临时凭据。
   - 检查本轮启动的进程已经正常结束；现有仓库外运行实例不纳入本轮清理，也不执行未经授权的删除。
   - 运行 `git diff --check`，逐一检查新增文件；确认没有依赖锁文件、数据库结构、备份版本或 workflow 的计划外变化。

6. **更新交接文件并判定入口状态**

   - 更新 `plan/release-local-result.md`，记录最终候选目录、最新哈希、实际命令、重跑范围、残留阻塞和未执行项目。
   - 确认 README、升级说明、Release Notes、Compose 示例和更新器行为一致；在正式发布前继续保留“尚未发布”表述。
   - 保留 `plan/release-workflow-proposal.md` 作为未授权 CI 修改的提案，并在其中固定 `prepare → test → build → publish` 的门禁顺序；不把提案写成已应用的 workflow。

**L6.5 本地通过标准**：源码和构建输入已冻结；最终候选与该源码对应；归档内容、Unix 权限、真实许可证材料、哈希和证据相互一致；受影响的代码、前端、更新器、SQLite、最终包业务流程和 profile 脱敏检查通过；`scripts/build.sh` 已具备显式版本构建条件；CI proposal 已明确测试门禁、artifact 传递和 tag 顺序；没有密钥泄露、残留本轮进程或计划外 CI/远端变化。A3 已补关键 race、业务及生产更新链路和页面正文；B 已解除工具/报告缺失阻碍并重建验证；R0 已补齐源码附件、LGPL 再链接与前端来源材料，公开访问和镜像内声明随发布验收。许可证本地材料完成不等同于严格验收或发布批准。R0 范围决定和环境准备、经授权的 R1 本地 CI 准备可以继续用于解除阻碍。

**阶段状态分开记录**：当前本地材料完成、可进入 R1 本地 CI 准备；Alpine 源码/声明本地缺项已解除。公开应用/OS 源码托管、显著网络入口与真实下载在 R3/R4 验收；线上 Actions、Release/registry 和版本间在线升级也属于后续阶段。不得把这些下游结果设为进入 R1 的前提，亦不得把可进入 R1 写成已满足公开发布条件。

**L6.5 仍不能覆盖的项目**：其他平台实机与 Android/NDK、GitHub Actions 在线运行、真实 Release/registry、公开源码和远端下载、至少两个远端版本之间的在线升级，以及所选部署的外部数据库容器 TLS/反向代理等未测条件。R0 已完成本机 WSLc 的 linux/amd64/SQLite C1–C9 场景和 tar 交付，不等同 Docker Engine/Compose 或所有架构实测。继承 Alpine OS 的对应源码/声明本地附件已完成，公开获取渠道随 R3/R4 验收。MySQL/PostgreSQL 完整集成及空库恢复已由 R0 在本地真实引擎补齐，不代表历史云端目标通过。用户沿用既有全部平台，缺少实机的继续标为待验；这些下游待验不阻止 R1 本地 CI 准备。

## 6. 第二轮完整路线：R0 收口准备已启动，其余操作按项授权

### R0：决定远端范围，补齐待环境项

先让用户审查 L6/L6.5 的具体结果和未完成项，再确定：是否开始远端工作、GitHub 仓库是否沿用当前目标、是否仅发布 GHCR 或同时 Docker Hub、支持平台/数据库验收环境、需要补齐的工具/镜像/依赖、真实供应商验收范围。

R0 可以先做范围决定、许可证工具/材料和 Docker/数据库环境准备；获准的 R1 本地 CI 准备也可以用于解除构建阻碍。这些准备不改变当前候选的“待门禁”状态，不代表 L6.5 通过，也不授权 R2/R3 的分支、标签、Release、镜像或其他远端变更。

用户可先按 L5.4 自行在云主机部署候选镜像进行 Docker 人工验收；云测试数据库则由执行会话在单独授权后自动验证。完成这些测试后再决定 GitHub/registry 发布，不需要提前公开产物。

如需核对最新上游，在获准后进行在线只读比对；网页检索/抓取按用户要求使用 `tinyfish`。整理差异不自动代表合并上游。若用户决定吸收新功能，重新评估范围并对受影响部分回归。

第一轮缺少的平台测试在 R0 登记并准备环境，随后对相应候选补实机验收；正式发布前须实际补齐声明范围，或由用户明确调整本次公开支持范围。平台待验不阻止 R1 本地 CI 准备，不得将交叉编译写成实机通过。本地 MySQL/PostgreSQL 的完整验收已经完成，历史云端目标状态另行保留。

#### R0 历史云库收口记录（2026-10-17）

本次用户已明确：**仅 GHCR**；真实数据库使用 `C:\Users\Ink-lotus\.config\octopus-roam\db-profiles.json`。用户随后针对下列命令明确回复“授权两库各执行一次”，已串行执行；实际前置检查仍未通过，详见下方结果。全部平台继续沿用，缺少实机的保留待验；Docker 仍由用户人工验收。本次没有 CI 修改或远端发布。

独立证据目录：[r0-readiness-20261017-181558-89c0ffbc](../build/release-local/r0-readiness-20261017-181558-89c0ffbc/)。候选仍为 `dbfix-20261017-01`，基线仍为 `test / 948de764ae9a034fd72899686d24dcc4ecad7fa5`；源码和构建输入没有变化，本次不重建候选或重跑已通过的产品回归。

| 本次复核 | 结果与证据 |
| --- | --- |
| 候选及工作区输入 | `readiness-audit-final.json`：998 项文件、497 项冻结输入和 497 项工作区输入全部匹配；构建输入没有新增/遗漏，候选没有清单外文件 |
| 七个归档 | ZIP 白名单、内容、Unix creator/权限、归档清单及实际 SHA-256 全部匹配；8 份许可证报告副本一致。初次核查脚本未归一化 `SHA256SUMS` 的 `./` 前缀，失败记录保留；修正核查后通过，没有修改归档或清单 |
| 数据库验收入口 | dbfix 驱动、181 项链接输入、原始 Windows 程序及正常停止脚本哈希匹配；历史 SQLite 34 项通过/1 项行锁待验的结论不变 |
| 实际 Go 依赖源码 | `license-source-readiness.json`：从七个二进制的构建元数据取得 110 个实际模块，按两个 replace 后的模块核对；110 份源码 ZIP 的 Go `h1` 同时匹配冻结 `go.sum` 与二进制记录，110 个缓存源码目录逐文件匹配，无缺失/改写/额外文件 |
| 真实 profile 的离线检查 | `profile-offline-check-outside-sandbox.json`：两个别名均 `profile_valid`，`restore_database_configured=false`；只验证结构、别名和 TLS 策略，未验证 CA 文件内容、服务端链或实际空库 |
| 档案读取边界 | 首次沙箱读取被拒绝，不能据此判断档案无效；同一已审查程序在获工具放行后完成只读校验。没有输出/复制 profile、密码、连接参数或原始错误，也没有建立数据库连接 |
| 依赖、CI 与版本注释 | `git diff --check` 通过；`go.mod`、`go.sum`、前端依赖/锁文件、`.github/workflows` 和 `main.go` 没有新增差异 |
| 本机环境与进程 | PATH 和 Docker 常见安装路径未发现 Docker；PATH 未发现 actionlint，不能推断整机绝无安装。当前 `build/release-local/` 下的产品/数据库验收进程为 0；本次没有启动产品运行实例 |

各项条件按下表分别收口（2026-10-19 更新），不合并成一个“已通过”状态：

| 条件 | 当前状态 | 完成判据与下一步 |
| --- | --- | --- |
| 源码、许可证与再链接材料 | 本地材料完成，公开访问待发布验收 | [材料报告](release-license-materials-20261019.md)：549 个应用文件、110 份 Go 模块源码、345 个 npm 版本及原始条款齐备；七平台程序与前端精确离线复建、修改 LGPL 库后完整应用再链接均通过；前端来源限制已补证，源码 ZIP 与七个附声明的新归档已生成 |
| MySQL 主库复验 | 当前候选本地通过，35/35 | MySQL 8.4.11 严格 TLS、migration 3–13、配置导入、业务、行锁、备份及原始程序两次正常启停全部通过，证据在 `r0-local-db-20261018-02/`；首轮导入失败及历史云端 TLS 失败保留 |
| PostgreSQL 主库复验 | 当前候选本地通过，35/35 | PostgreSQL 18.6 严格 TLS、migration 3–13、业务、行锁、备份及原始程序两次正常启停全部通过；历史云端非空目标没有续跑、改写或清理 |
| 两库同引擎空库恢复 | 当前候选两引擎均通过 | 两空恢复库各自完成 migration 3–13、加密备份恢复、恢复后新进程持久化；四库迁移记录全部 success，数据保留、自建进程正常停止 |
| Docker C1–C9 | WSLc / linux amd64 / SQLite 所列场景通过，tar 已交付 | [交付报告](release-docker-delivery-20261019.md)及 `r0-docker-delivery-20261019-03/`：声明集成、49 个前端文件、严格 HTTPS 正反例、65532 权限重启写入、页面备份落盘/恢复、重建持久化和更新保护通过。其他架构、容器外部数据库、Engine/Compose 和远端升级按各自范围处理 |
| 镜像继承的 Alpine OS 材料 | 本地源码/声明完成，公开获取随发布验收 | [Alpine 报告](release-alpine-materials-20261019.md)：18 包、11 origin、139 配方文件、8 源码归档及 126 项源码输入已核验，独立附件保留准确声明和构建说明；旧清单包名/架构错误由新附件纠正，镜像保持冻结。公开下载在 R3/R4 验收 |
| Unix/macOS/ARM 与 Android | 待环境 | 全部平台范围保持；交叉编译不替代实机运行/更新，Android 的 NDK、构建和运行证据仍缺失 |
| R1 本地 CI、真实渠道与在线升级 | 本地 CI 和远端只读核实完成，线上待验 | CI 的 `prepare → test → build → publish` 已授权应用并完成本地验证；read:packages 生效后的 GHCR 精确查询为 404。线上 Actions、实际包权限/可见性、Release、下载/拉取和两个远端版本间升级继续另行验收 |

**源码交付合同**：以本次冻结的 `source/`、实际嵌入静态产物及其前端源码、构建脚本/参数、模块与前端锁文件为来源，生成与七个二进制版本/哈希绑定的独立源码附件。工作树包含未提交修复，因此仅提供 HEAD 对应的仓库自动源码包不足以对应当前候选。排除 profile、数据库、运行日志、工具缓存和整个父目录；冻结候选保持不变。

- 项目 `LICENSE` 为 AGPLv3，按第 1、6、13 节准备完整对应源码、构建/安装说明、获取位置以及网络使用场景下醒目的源码获取入口。实际下载可用性在远端获准后验证。
- `github.com/looplj/axonhub/llm@v0.0.0-20260923093423-859c3e4ed944` 的 LGPLv3 第 4 节要求使用声明、LGPLv3/GPLv3 文本，以及能够与修改后的库重新组合/链接的源码和步骤；候选使用静态编译，需要在新隔离目录完成相应演练并保留结果。
- `github.com/go-sql-driver/mysql@v1.10.0` 的 MPL-2.0 第 3.1/3.2 节要求保留原许可证/声明，并向接收者提供准确的 Covered Source 和获取说明。不能用一份 CSV 代替源码交付。
- 前端来源已按[补充表](../build/release-local/r0-license-materials-20261019-01/package/licenses/FRONTEND-SOURCE-BINDINGS.json)逐项补证：`victory-vendor` 精确 release tag、`keyv` 版本提交与包文件逐字节匹配、`natural-compare` 与 `@humanfs/types` 的准确来源说明。`react-remove-scroll-bar` 的 npm gitHead 仍不可访问，采用发布者明确 MIT 授权源码与四个安装模块的可复现代码对应证明，不冒称找回 release commit 或改写版权年份。构建工具与生产依赖图区分，既有 `@thesvg/react@3.3.2` 证据保留。
- 110 个实际模块 ZIP 的 `h1`、原始源码与每文件清单已核对，并已生成对应源码 ZIP；七个程序在全新缓存、`GOPROXY=off` 下逐字节复建成功，49 个前端产物也完全一致。修改 LGPL 库后的完整应用重新链接和执行标记通过。历史完整依赖图 `go mod verify` 因未使用模块 `github.com/bytedance/gopkg@v0.1.4` 无离线来源而未完成的记录保留，没有为此补装依赖或禁用校验。新证据与原始边界见[材料报告](release-license-materials-20261019.md)；公开源码上传、网络入口实际下载和未来镜像内材料核对继续留在发布/部署验收。

应用许可证交付目录为 `build/release-local/r0-license-materials-20261019-01/delivery/`，含源码 ZIP、七个附声明的平台包和新 `SHA256SUMS`。程序哈希与 mysqlfix-20261018-03 一致，可继续引用相同程序的数据库证据；归档 SHA-256 已改变，后续交付使用新清单。旧候选及原 Docker context 保持冻结；其 `docker-license-payload/` 已复制到新的 `r0-docker-delivery-20261019-03/`，完成镜像内集成和 tar 交付。

**Docker 当前材料的使用口径**：旧人工/Compose/远端模板保留历史路径，镜像及应用交付以 `r0-docker-delivery-20261019-03/delivery/README.md`、`IMAGE-MANIFEST.json` 和 `SHA256SUMS` 为准，并同时提供 `r0-alpine-materials-20261019-01/delivery/` 的 OS 源码/声明附件。显式选择 `octopus-roam:v0.1.0-local.mysqlfix-20261018-03-licenses.1`，ID 为 `sha256:1f51a67ec361cc97369ed3169ddd33600b78e621be23dbb5a12428a0ed922c96`；程序和原 CSV 哈希不变，299 份镜像内材料逐项核对通过。Alpine 包名/架构纠正清单在新附件中，旧冻结镜像、context、清单与失败记录保留。公开应用/OS 源码获取渠道随发布验收。

**本次已授权执行的真实数据库命令**：目标仅为上述 profile 中的 `roam-mysql-test`、`roam-postgres-test` 主库，串行各执行一次。入口首先校验可信 TLS 和空库；迁移进程写入前再次检查空库。下列块为本次命令记录，**已经执行，结果目录已存在，不可直接重跑**。实际启动器额外复核离线 profile 仍未配置恢复库，并将子进程 stdout/stderr 仅留在内存；校验允许的 JSON 字段/值后输出和保存脱敏汇总。

```powershell
$repoRoot = 'D:\tool\AI\octopus-roam'
$candidateRoot = Join-Path $repoRoot 'build/release-local/dbfix-20261017-01'
$driverRoot = Join-Path $repoRoot 'build/release-local/dbfix-acceptance-20261017-01'
$profilePath = 'C:\Users\Ink-lotus\.config\octopus-roam\db-profiles.json'
$retestRoot = Join-Path $repoRoot 'build/release-local/r0-database-retest-20261017-01'
$driver = Join-Path $driverRoot 'database-acceptance.exe'
$program = Join-Path $candidateRoot 'bin/octopus-roam-windows-amd64.exe'
$stopScript = Join-Path $repoRoot 'scripts/windows/send-stop-signal.ps1'
$programHash = 'f02ff889e1359020b9d8a940af5bcec0ee8d0225e7fbd814ad617b08ae41f100'
$expectedInputs = @{
    $driver = '6c33f0d3d5dd2a45e9afac4be130e4c6b0613c8b5a58f7a7a8159d7de33c1e7e'
    $program = $programHash
    $stopScript = 'f6744b4f73f3c5a4f261e390096cdaa207ac1ba141288b064372c3a0e474dfe5'
}
foreach ($inputPath in $expectedInputs.Keys) {
    if ((Get-FileHash -LiteralPath $inputPath -Algorithm SHA256).Hash.ToLowerInvariant() -ne $expectedInputs[$inputPath]) {
        throw 'Acceptance input hash mismatch.'
    }
}
if (Test-Path -LiteralPath $retestRoot) { throw 'Retest output must be new.' }
New-Item -ItemType Directory -Path $retestRoot -ErrorAction Stop | Out-Null

& $driver --profile $profilePath --alias roam-mysql-test --connect `
    --output (Join-Path $retestRoot 'roam-mysql-test') `
    --executable $program --executable-sha256 $programHash `
    --stop-script $stopScript --expected-version 'v0.1.0-local.dbfix-20261017-01'
$mysqlExitCode = $LASTEXITCODE

& $driver --profile $profilePath --alias roam-postgres-test --connect `
    --output (Join-Path $retestRoot 'roam-postgres-test') `
    --executable $program --executable-sha256 $programHash `
    --stop-script $stopScript --expected-version 'v0.1.0-local.dbfix-20261017-01'
$postgresExitCode = $LASTEXITCODE
```

已授权的写入范围：只在通过空库检查的所选主库中执行当前迁移/建表/索引、默认设置与测试管理员初始化，以及合成渠道、凭据、授权、分组、API Key、Webhook、隐私/路由设置和刷新记录的增删改查；包括事务回滚、行锁、备份快照、错误导入拒绝、新进程持久化，以及原始候选程序两次启动/API/正常停止。测试使用 `fixtureID=910001`、`rollbackID=910099` 和非法关联探针 `invalidID=2140000000`，模型/Webhook 仅用本地合成目标。若执行到这些阶段，会真实提交应用表和合成数据，并在新证据目录保留合成加密备份；不承诺把主库恢复为空库，结束后由用户重置测试目标。**本次两个目标均在最初前置检查失败，上述写入阶段和原始程序启动均未执行，也未生成真实库备份。**

本次授权未包含清库、删表、新建其他数据库、修改旧迁移记录、修改 profile/证书/云设置，亦不包含尚未配置的空恢复库。原始连接材料仅由已审查驱动在内存使用；完整程序阶段原定通过本地命名管道取得配置并禁用后台价格访问，本次未运行到该阶段。输出只保留允许的结果字段和错误分类。MySQL 失败后，独立的 PostgreSQL 别名仍按授权执行了一次；没有自动重试或清理。

本次实际结果见[独立数据库复验汇总](../build/release-local/r0-database-retest-20261017-01/authorized-execution-summary.json)：

| 目标 | 套件结果 | 已执行及未执行范围 |
| --- | --- | --- |
| `roam-mysql-test` | 退出 1；`preflight / primary_empty_before_migration` 为 `tls_validation_failed`；外部计时 0.663 秒 | 未确认空库；未执行迁移、应用写入、业务、行锁、备份或原始程序重启。后续只读 Ping 诊断为 `tls_unknown_authority` |
| `roam-postgres-test` | 退出 1；`preflight / primary_empty_before_migration` 为 `database_not_empty`；外部计时 10.173 秒 | 已连接并只读检查空库条件；未执行迁移、应用写入、业务、行锁、备份或原始程序重启。只读诊断为 PostgreSQL 18.6、当前 schema 30 个所查类型对象、17 张固定应用表、7 条迁移记录；migration 5 仍为 failed，其余 16 张业务表共 0 行，与历史部分初始化状态一致 |

失败后复用了历史已审查的只读诊断入口，重新核对程序和四项输入哈希；MySQL 只 Ping，PostgreSQL 只在 repeatable-read 的只读事务中查询固定应用表和数字迁移记录。结果见[只读诊断汇总](../build/release-local/r0-database-retest-20261017-01/read-only-diagnosis/summary.json)，不覆盖历史诊断、候选或失败材料。PostgreSQL 的 migration 3、4、8、10、11、12 为 success，5 为 failed；诊断未改写记录，表/记录数量一致也不用于推断真实目标的私有身份。结束后本轮产品、验收和诊断进程均为 0。

授权依据为会话 AGENTS.md 的“数据库变更”门禁：本次目标、命令和影响已列明，用户已批准，两个套件执行已结束。下一步先解决 CA 信任和非空目标，再针对新的具体执行范围复验；本次批准不扩展为清理旧目标或任意重跑。许可证、空恢复库、Docker、其他平台及远端门禁继续保留，`L6_5_strictly_passed=false`。

**后续本地数据库准备（2026-10-17）**：用户已决定改用本地 MySQL/PostgreSQL 补验，随后将工具目录指定为 `D:\tool\db-test\` 以便其他项目复用，并明确授权修改目录后安装。**MySQL 8.4.11 LTS 与 PostgreSQL 18.6（EDB Windows x64 ZIP 构建 18.6-4）已安装完成，10 项 CLI 版本检查全部通过**。现有 VC++ x64 Runtime、GPG 和 OpenSSL 已复用，没有新增系统服务、PATH、自启、防火墙或安全策略变更。工具、原始 ZIP、证据和共享说明合计约 **1.967 GiB**；后续实例仍按原总预留 8 GiB 准备。

具体来源、校验依据、命令及结果见[本地数据库工具安装记录](../build/release-local/local-db-install-20261017-184812-3174254e/INSTALL-PLAN.md)；共享说明已放在 [D:\tool\db-test\README.md](/D:/tool/db-test/README.md)。首次 MySQL 入口返回 403 后使用既定白名单内的官方 CDN 下载同一 ZIP；网页提取的签名文本缺少 ASCII armor 空行，已另存仅补回空行的副本、核对解码后的签名字节不变，并以新独立 keyring 验证退出 0 和固定官方 fingerprint 后才解压。PostgreSQL 官方包的 SHA-256 匹配固定的独立安装记录，该校验值不表述为厂商签名。所有原件及中间失败记录保留，最终状态以 [installation-result.json](/D:/tool/db-test/evidence/completion-20261017-01/installation-result.json) 和 [handoff-verification.json](/D:/tool/db-test/evidence/completion-20261017-01/handoff-verification.json) 为准。

上述 2026-10-17 工具安装阶段没有初始化或连接数据库，没有创建本地 profile、主库或恢复库；该阶段结束时数据库工具进程为 0，3306/5432/13306/15432 均未监听，候选及验收驱动保持原样。随后 2026-10-18 的独立实例初始化及实际验收见下一节；历史云端失败记录继续保留，不复用此前各一次的连接授权，`L6_5_strictly_passed=false`。

#### R0 本地数据库执行与收口（2026-10-18）

用户授权首轮新建 `r0-20261018-01` 独立实例、私有 TLS/profile、两主库和两恢复库，并对两引擎各验收一次。旧 dbfix 候选在 PostgreSQL 18.6 的 35 项全部通过；MySQL 8.4.11 主库迁移成功，但第 7 项配置导入因保留字 SQL 失败，后续阶段未执行。两数据库正常停止，首次数据和[失败证据](../build/release-local/r0-local-db-20261018-01/execution-summary.json)保留。

随后局部修复隐私条件中的 `key` 和快照 JOIN 别名 `keys`，并补齐原始程序进程内加载 MySQL 自定义 CA 的支持。最终源码 default/jsoniter、vet、五包 race 通过；重新冻结并构建 `mysqlfix-20261018-03` 七平台候选，989 项文件清单、500 项输入一致；绑定驱动有 184 项链接输入和 5 项驱动源码，独立 SQLite 演练 34 项通过、1 项行锁待真实引擎。构建和回归日志均在候选外保存，不回写冻结材料。

用户针对已准备的[第二轮执行清单](../build/release-local/r0-local-db-20261018-02/APPROVAL.md)明确要求执行，已运行该目录的 `run-local-databases.ps1 -ExecuteAuthorized`，仅使用新的 `r0-20261018-02` 实例与私有配置。**当前候选 MySQL 8.4.11、PostgreSQL 18.6 各 35/35 全部通过**，包括严格 TLS 及明文/错误 CA 拒绝、真实行锁、刷新事务、备份快照、加密空库恢复、新进程持久化、实际 Windows ZIP 程序两次 API/正常启停。四库 migration 3–13 全 success，两个数据库正常停止，13306/15432 无监听；既有仓库外运行实例保留。

当前结果以[第二轮执行汇总](../build/release-local/r0-local-db-20261018-02/execution-summary.json)、[迁移记录](../build/release-local/r0-local-db-20261018-02/migration-records.json)和[完整报告](release-local-database-acceptance-20261018.md)为准。两轮测试数据、私有材料和历史候选全部保留；两次各一次执行授权已消耗，不能直接重跑。数据库及空库恢复条件已收口，其余许可证、Docker、平台实机、CI 和远端发布条件继续按上表处理，`L6_5_strictly_passed=false`。

[最终只读交接核对](../build/release-local/r0-local-db-20261018-02/final-handoff-checks.json)通过，覆盖候选/旧候选/驱动输入、七包及八份许可证、逐平台依赖元数据、四库迁移、四次原始程序正常停止、端口与进程保留状态；两轮私有密码/私钥片段在 845 份公共文本中的精确匹配为 0。该核对没有再次连接数据库。

#### R0 WSLc 镜像交付与补验（2026-10-19）

用户授权执行会话使用 WSL Containers 完成 Docker 验收，随后授权开始“声明集成并导出 tar”和“补验并同步记录”两项。当前新包装应用字节与 mysqlfix 候选一致，仅添加声明层；构建、原层比对、299 个材料文件、49 个前端文件、tar 摘要和导入 ID 均已核验。真实 Edge 页面下载落盘、空库页面恢复、严格 HTTPS 主机名/CA 正反例、PUID/PGID 重启写入、停止重建持久化和容器更新禁用已通过，全部自建测试进程正常停止。范围及证据见[Docker 交付报告](release-docker-delivery-20261019.md)。

上一轮 `r0-wslc-docker-20261019-02/` 的“C1–C9 全通过”口径过宽，旧 JSON/日志保留；以新报告逐项证据和边界为准。当前 tar 可供本地导入测试；本轮识别的 Alpine OS 源码/原始声明分发材料、公开应用源码入口、其他平台、CI 和远端渠道仍待处理，`L6_5_strictly_passed=false`。本轮授权不延伸为修改 workflow、操作旧业务库、安装依赖、commit、push、tag、Release 或公开镜像。

#### R0 Alpine 本地材料收口与 R1 入口

本地 Alpine 对应源码、原始声明和构建说明已形成独立附件，并通过实际 ZIP 解压离线校验。附件与原 `…-licenses.1` 镜像 tar、准确应用源码和包装源码配套交付，未重新构建镜像或改动程序；详情和旧清单纠正见[Alpine 材料与交接](release-alpine-materials-20261019.md)。当前可以进入 R1 本地 CI 准备。未来 CI 构建需使用大小写敏感的包数据库解析，按实际基础镜像/apk 输入生成对应材料；公开托管和实际获取在后续发布阶段验收。

### R1：核实远端条件并落地 CI 门禁

R1 分为“本地 CI 补丁”和“远端只读核实”两部分。实际修改 workflow 文件前必须取得针对 `.github/workflows/build.yaml`、`.github/workflows/release.yaml` 的明确授权；R1 本地验证不创建 tag、Release、镜像或远端分支。

2026-10-20 进展：用户已明确授权并应用九文件补丁及两文件 pnpm 依赖获取门禁，详见 [R1 本地报告](release-ci-r1-20261020.md)。actionlint 1.7.7 和真实 CI 入口在 Windows/Git Bash 的独立副本中通过（含真实前端构建、Go default/jsoniter、两组 vet、关键 race、27 项脚本测试），本地收尾完成。随后已按授权完成远端只读核实；read:packages 生效后精确 GHCR 包及版本仍为 404，当前身份未找到目标包，无剩余认证待办。Ubuntu Actions 尚未运行、新 CI 尚未远端生效，R1 不记为全部完成。989 项历史冻结产物保持不变，500 项历史工作区输入中只有获授权的 `scripts/build.sh` 改变；不能再将该历史输入清单表述为当前 500 项全部未变。首次共享依赖目录问题已按授权修复并保留备份，后续验证采用独立依赖副本。

**CI 修改范围**：

- `build.yaml` 改为普通 push/PR 的测试门禁，覆盖 Go 全量测试、`jsoniter`、`go vet`、关键 race、前端 lint/TypeScript/生产构建和必要脚本检查；测试 job 不执行任何发布动作。
- `release.yaml` 改为显式 `workflow_dispatch` 或等价的受保护发布入口，接收版本、目标 ref 和是否发布 Docker Hub 等输入；首次推送 `main` 不能自动发布。
- `prepare` 校验目标 ref、版本格式、`main.go` 版本注释、commit 和既有 tag；当前上游 `v0.13.6` 或 `dev` 不能进入独立 Release。
- `test` 和 `build` 固定使用同一 commit。`build` 显式传入版本和 commit，只上传 Actions artifact，不创建或推送 tag。
- `publish` 必须声明 `needs: [prepare, test, build]`，下载并核对 artifact、归档清单和 SHA-256 后才创建 tag、Release 和镜像。
- Release body 使用已审查的 `plan/release-notes-v0.1.0.md` 或显式 body；不再依赖未打标签的 HEAD 让 `changelogithub` 猜版本。
- RC 只发布版本号镜像标签；稳定版才更新 `latest`。Docker Hub 登录和推送仅在显式启用且 secrets 完整时执行，GHCR 与 Docker Hub 不能无条件绑定。
- 测试、构建、发布分别使用最小权限；`contents: write` 和 `packages: write` 只授予 publish job。Action、Go、Node.js、pnpm 和许可证工具版本必须固定或有明确的升级记录。

**CI 支持脚本修改**：

- `scripts/build.sh` 接收显式 `VERSION`、`COMMIT` 和仓库信息，不以 `git describe` 或预先存在的 tag 作为发布版本来源。
- 发布构建直接使用已经审查并提交的价格预设；不得在 workflow 中运行联网的 `scripts/updatePrice.py` 后改变源码。
- 构建脚本必须生成完整平台归档、许可证报告、`SHA256SUMS` 和 Docker context，并在许可证报告缺失、为空或哈希不一致时失败。

**本地验证**：

- 对两个 workflow 做 YAML 解析和 `actionlint` 检查；当前环境没有 `actionlint` 时记录为待工具项，不把静态阅读写成通过。
- 用本地固定版本和合成输入验证 `scripts/build.sh` 的版本注入、归档命名、许可证报告检查、artifact 文件清单和失败退出路径。
- 检查测试失败或构建失败时 publish job 不会执行；检查首次普通 commit 推送不会创建 tag。
- 用同一 commit 对照前端版本、Go `conf.Version`、归档名称、Release Notes、Docker tag 和哈希记录。

**远端只读核实**：

获准后核实目标仓库、当前默认分支、已有分支/标签/Release、Actions 启用状态和发布所需权限。核实 GHCR 可见性及包权限；如启用 Docker Hub，再核实对应账号、仓库和发布凭据。只读核实不能代替 CI 补丁的本地验证，也不授权推送、创建 tag、Release、镜像或修改仓库设置。

本轮实际结果见[远端只读报告](release-ci-r1-remote-readonly-20261020.md)：现有 CLI 身份具有仓库 admin 权限；Actions 已启用、允许 all、未强制 SHA，默认 token read 与 publish job 的单独 write 声明相容。远端只有未保护的 master，尚无 main、release environment 或真实 workflow/run。11 个 action 引用和固定工具来源已查明。用户补充权限后的 attempt 04 已确认 read:packages 生效，GHCR 精确包及版本仍为 404 / Package not found，当前身份未找到目标包；只读核实已收口，原待认证快照保留。本轮 GET 不验证未来 GITHUB_TOKEN 的实际写入能力，也未取得可核验包级权限的对象。main/保护方案和真实 CI、实际包的公开可见性及匿名拉取须按后续阶段落实。

不输出凭据值。需要创建仓库、设置凭据/权限或调整发布配置时，先提供具体目标、命令/界面操作和影响，取得针对性授权。已在本地准备好的地址不能代替这一步的在线核实。

### R2：集成 main，并切换默认分支

审查最终差异和提交划分，按授权提交本地改动，commit summary 使用中文。创建/集成产品 `main`，保留 `master` 的上游同步定位；避免覆盖其他已存在的远端分支改动。

先确认 L6.5 和 R1 的本地检查通过、首次推送禁发条件和手动发布入口已经生效，再推送普通源码 commit 到 `main` 并切换默认分支。普通 commit 不应携带手工创建的 tag；不能把当前带 `v0.13.6` 的树直接推送后寄望于路径过滤不会触发发布。

推送后的第一次远端验证只看 `build.yaml` 测试结果和 workflow 状态，不触发 Release。若采用分支保护，先把测试 job 设置为合并门禁，再继续发布准备。

`main`、默认分支与最终 CI 就绪后才进入 RC 版本发布。分支、标签、push、PR 或仓库设置变更均按用户授权执行。

### R3：发布 RC

确定候选提交和版本 `v0.1.0-rc.1`，再按授权修改 `main.go`、提交并推送普通源码 commit。通过发布入口指定同一 commit 和版本后，workflow 依次执行 `prepare → test → build → publish`；只有前三个 job 成功，`publish` 才能创建并推送 tag、创建 prerelease Release 和发布候选镜像。批准内容必须覆盖修改 workflow、推送 commit、创建 tag、Release 和镜像的完整触发链。

验收线上流水线：同一 commit 的测试门禁 → 指定版本构建和 artifact → 归档/许可证/哈希校验 → 对应候选 tag → prerelease Release → 候选镜像。确认构建输入、tag、commit、前后端版本和发布说明一致；测试或构建失败时远端不得出现本次候选 tag。

候选版不更新 Docker `latest`。发生失败时先核实已经产生哪些远端对象，再审查补救动作；不自动改写公开标签、覆盖原产物或强推。

### R4：用真实远端产物验收

使用 Release 下载的实际 zip、发布的 `SHA256SUMS` 和 registry 拉取的实际镜像，重复必要的安装、运行、持久化、当前备份恢复与更新冒烟检查。确认普通用户可以按 README 下载/拉取，镜像平台 manifest 与实际资产一致。

自更新至少需要两个版本才能验证真实升级。建议按授权发布下一 RC，执行 `rc.1` → `rc.2` 的在线查询、下载校验、替换和重启，覆盖失败恢复；每次新增 RC 都是一次需授权的发布。第一轮的模拟源成功不能替代这里的在线结果。

验证正式版通道与 RC 通道规则、容器升级指引及前后端缓存更新。若用户提供真实供应商环境，再执行获准范围内的普通/流式调用、取消与失败切换；不能把模拟服务结果当成所有真实渠道兼容证据。

### R5：正式发布 v0.1.0

满足条件：候选版本规定检查通过；声明支持的数据库完成本轮新安装/恢复测试；公开产物与独立自更新实测通过；没有未解决的发布阻塞问题；文档/Release Notes 与最终行为一致；用户明确批准正式发布。

按授权发布 `v0.1.0`，核对 stable Release、版本标签、安装包、校验文件、镜像版本及 `latest` 指向。README 去掉适用位置的“尚未发布”提示，并核实最终安装地址与说明。

如果 GA 相比已验收 RC 只有版本和文案变化，完成相关版本/构建/产物检查即可；有功能或构建逻辑变化则回到受影响的回归和验收步骤。正式发布后补做下载/拉取与首次启动冒烟，记录可用版本和已知限制。

发布现有项目不自动授权改动用户业务数据库或替换当前运行服务；此类部署另行安排。

## 7. 下一个会话可直接使用的交接指令

> 阅读本计划、`plan/release-local-database-acceptance-20261018.md`、历史本地修复/环境验收报告和会话 AGENTS.md。基线仍为 test / `948de764ae9a034fd72899686d24dcc4ecad7fa5`，当前候选 `build/release-local/mysqlfix-20261018-03/`，绑定驱动 `mysqlfix-acceptance-20261018-03/`。SQL/TLS 修复、最终 Go default/jsoniter/vet/五包 race、七平台构建及独立 SQLite 演练已完成；989 项文件清单和 500 项冻结/工作区输入已核对。源码/构建输入未变时不重复构建，不自动进入 CI 或远端发布。
>
> 最新数据库结果是 2026-10-18 第二轮本地验收：mysqlfix-20261018-03 在 MySQL 8.4.11 和 PostgreSQL 18.6 各 35 项全部通过，含严格 TLS、迁移、业务/行锁、加密空库恢复和原始 Windows 程序两次正常启停。四库 migration 3–13 全 success，两个数据库正常停止。证据在 `r0-local-db-20261018-02/`。首次本地旧候选的 MySQL 导入失败及 PostgreSQL 35 项通过，保留在 `r0-local-db-20261018-01/`，不要混作同一候选的结果。
>
> 仅在源码/构建输入变化或明确产物问题时另建唯一目录重建；旧候选、中间构建和失败记录全部保留，文档修订不要求机械重建。A3 生产更新/浏览器及 B 业务历史证据保留，本次没有重跑这些场景。本地两轮已初始化库和历史部分初始化云库不得当作空库重跑，不得改写迁移状态或擅自清理；两轮各一次授权已消耗。不能以 SQLite/交叉编译结果替代其他平台实机验收。
>
> R1 九文件 CI 补丁和两文件补充门禁已授权应用，本地收尾完成。先读 `plan/release-ci-r1-20261020.md` 和 `build/release-local/r1-local-closeout-20261020-01/evidence/summary.json`；最终内容按两份补丁叠加核对，不要重复应用旧补丁或运行只断言旧候选未变的验证器。普通 commit 只运行测试，`prepare → test → build` 成功且显式开启 publish 后才允许创建 tag、Release 和镜像。远端进展另读 `plan/release-ci-r1-remote-readonly-20261020.md` 和本轮 `final-summary-04.json`：仓库 admin、Actions enabled/all/default read 已确认，master 未保护、main 和 release environment 未创建。read:packages 已生效，GHCR 精确包及版本仍为 404，当前身份未找到目标包；只读核实已收口，无需等待认证。查询使用可读取系统凭据的扩展执行环境，未代登录/刷新。新 CI 远端生效、Ubuntu Actions、NDK/实际多架构镜像和线上发布验收尚未完成。镜像渠道仅 GHCR；Alpine 清单解析区分大小写，构建输入变化时重新绑定源码/声明。公开下载和在线升级不作为完成 R1 本地准备的前提。
>
> Docker 最新结果先读 `plan/release-docker-delivery-20261019.md`。`r0-docker-delivery-20261019-03/delivery/` 已有实际导出的 Linux amd64 镜像 tar、对应源码 ZIP、包装源码和 SHA256SUMS；新标签为 `octopus-roam:v0.1.0-local.mysqlfix-20261018-03-licenses.1`。WSLc/SQLite 的本轮 C1–C9 场景逐项通过，其他平台/容器外部数据库/Compose/远端升级没有被覆盖。旧 JSON 的宽泛全通过口径已在新报告修订；历史证据不覆盖。两轮真实数据库实例及私有 profile 仍保留，凭据、私钥和数据库数据不得进入交付包。
>
> 本次没有连接旧云库，其历史 TLS/非空失败记录保持原样；不复用云库或本地已结束的一次性授权。本轮仅授权公开 TinyFish 核实及目标仓库/精确 GHCR 包的 GitHub API GET 例外，未扩大到工具/依赖下载、真实 GitHub 更新、镜像拉取/推送、远端 Git 状态变更或发布。CI、数据库变更、批量修改等具体操作遵守已有授权门禁；不得默认清库、删表或新建其他库。
>
> 应用许可证本地材料先读 `plan/release-license-materials-20261019.md`：原目录有对应源码 ZIP、七个附声明的平台归档及校验清单，离线复建与 LGPL 再链接证据继续有效。补充声明已进入新的 Docker 镜像，并随 tar 提供准确应用源码和包装源码。Alpine 独立源码/声明附件已在 `r0-alpine-materials-20261019-01/delivery/` 完成并核验，应与原镜像及应用源码配套交付；未来镜像输入变化须更新材料。公开源码上传、显著网络入口与真实下载按 R3/R4 验收；其他平台按可用环境对候选补实机。当前本地材料完成、可进入 R1 本地准备，公开发布条件仍待后续验收。不得自行 commit、切换 main、创建标签、push 或发布。
