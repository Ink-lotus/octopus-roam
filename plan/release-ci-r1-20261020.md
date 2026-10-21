# R1 CI 补丁准备、审查和本地验证

2026-10-20。**R1 本地收尾完成：九文件补丁及两文件补充门禁均已明确授权并应用，actionlint 1.7.7 和真实 CI 本地入口均通过。远端只读核实随后也已完成；新 CI 尚未在远端生效、线上检查仍待验，不能写成 R1 全部完成或满足公开发布条件。**

远端进展：用户随后授权 R1 只读核实及本轮 GitHub API GET 例外，并自行恢复 CLI 登录。扩展权限 GET 已确认仓库 admin、Actions enabled/all、默认 token read；远端默认 master、尚无 main，master 未保护、release environment 尚未创建。11 个 action 引用和固定工具来源已查明。用户补充权限后的 attempt 04 已确认 read:packages 生效，GHCR 精确包及版本仍为 404 / Package not found，当前身份未找到该包；认证续查已收口。最新结果见 [R1 远端只读报告](release-ci-r1-remote-readonly-20261020.md)，不改变本报告的本地验收结论，也不授权远端配置或推送。

入口：[原九文件补丁](../build/release-local/r1-ci-20261020-01/review/r1-ci.patch)、[原逐文件哈希/增删行](../build/release-local/r1-ci-20261020-01/review/manifest.json)、[两文件补充门禁](../build/release-local/r1-local-closeout-20261020-01/review/pnpm-readonly.patch)、[活动 CI 合同说明](../docs/release-ci.md)、[首次应用复核](../build/release-local/r1-ci-20261020-01/evidence/post-application-20261020-01/summary.json)、[本地收尾证据](../build/release-local/r1-local-closeout-20261020-01/README.md)。

补丁 SHA-256：`14f1d04b7f21ceb387ca4b2d524df91c2f9e33eac324293dbea4a17683f548b4`。

## 起点和保护范围

已读取材料包 delivery/README.md、Alpine 交接报告、主发布计划的范围/R1 合同、workflow 提案及当前两份 workflow、构建脚本和镜像包装入口。工作区仍为 `test / 948de764ae9a034fd72899686d24dcc4ecad7fa5`，保留接手时全部未提交修复。

补丁准备阶段开始和结束均核对当前 mysqlfix-20261018-03 冻结候选的 989 项文件、500 项冻结/工作区输入，全部匹配。该阶段原 `.github/workflows/build.yaml`、`.github/workflows/release.yaml`、`scripts/build.sh` 和 `scripts/dockerfile/Dockerfile` 的原始字节哈希均未改变。授权应用后，989 项冻结文件仍全部匹配；500 项历史工作区输入中，499 项匹配，只有获授权的 `scripts/build.sh` 改变。go.mod/go.sum、前端 package.json/锁文件没有差异。未机械重建产品、重跑已验收数据库或启动产品/容器实例。

原候选和证据位于独立的 `build/release-local/r1-ci-20261020-01/`。准备阶段只生成候选；取得用户授权后才应用到活动的 `.github/workflows/` 和支持文件，尚未提交或推送。补丁准备和首次应用阶段未安装工具、修改环境配置、执行 commit 或访问真实远端服务；HTTP/容器发布测试全部使用合成数据和替身。后续本地收尾另存于 `r1-local-closeout-20261020-01/`，按新的具体授权获取固定 actionlint、修复本地依赖路径并应用补充门禁，详见后文。

## 具体补丁范围

| 文件 | 操作及用途 |
| --- | --- |
| `.github/workflows/build.yaml` | 修改；push/PR/reusable 测试入口，锁定工具版本，补齐 Go/default/jsoniter/vet/race、前端及脚本检查，移除联网价格更新 |
| `.github/workflows/release.yaml` | 修改；手动入口、默认只产 artifact，固定 SHA、四阶段依赖、材料和下载 artifact 校验、最小发布权限、GHCR 渠道 |
| `scripts/build.sh` | 局部修改；材料参数、明确 build ID/构建时间、前端构建包装、标准库 ZIP 打包和产物元数据 |
| `scripts/ci-test.sh` | 新增；供普通 CI 和发布测试复用的检查命令 |
| `scripts/release_contract.py` | 新增；版本/commit/说明、源码与声明、模块 h1、归档、实际镜像层/APK 清单、artifact 和远端存在性检查 |
| `scripts/release-images.sh` | 新增；build 阶段只导出 Docker tar，publish 加载同一批 tar 后推送 GHCR；RC 不改 latest |
| `scripts/dockerfile/Release.Dockerfile` | 新增；发布专用 Dockerfile，显式 Alpine digest，集成应用和 OS 原始声明 |
| `scripts/tests/test_release_contract.py` | 新增；合成材料/镜像和异常路径测试，覆盖实际风险门禁 |
| `docs/release-ci.md` | 新增；完整材料 schema、操作合同、工具版本记录、后续验收边界 |

共修改 3 个现有文件、新增 6 个文件。原 Dockerfile 保留；发布流程显式选择新增的 Release.Dockerfile。没有产品 Go/前端逻辑改动，也没有产品依赖升级。

## 审查结论

当前 workflow 的确认问题是：测试/构建前已推 tag、无独立发布测试门禁、版本仍源自上游注释、调用 build.sh 未传必填版本/commit、构建时改写价格预设、Docker Hub 无条件登录、仅 CSV 无法覆盖完整对应源码和原始声明。补丁逐项处理这些问题。

新流程的 `prepare` 只允许 main 历史中的完整 SHA，版本限首发 v0.1.0-rc.N/v0.1.0，main.go 和说明标题须精确匹配。`test` 与 `build` 使用同一 SHA；`publish` 依赖 `[prepare, test, build]`，另要求显式 `publish=true`。没有 always()/continue-on-error 绕过，普通 main 推送不会触发 release workflow。

build 阶段先验证审核后的材料 artifact 与根清单 SHA-256，逐字节比对对应应用源码、实际编译依赖（含 replace）的模块 ZIP h1、原始声明及各平台材料，再构建 11 个程序 ZIP、四份镜像 tar 和明确的 Docker context。实际镜像配置/层摘要、标签、程序/声明字节、可执行权限及大小写敏感 APK 数据库全部通过后才能封装 artifact。额外归档、缺少平台、空/变化的报告、旧版本材料、实际 OS 包漂移均停止。

publish 下载本次 run 的确切 artifact 并重新核验，之后检查既有 tag/Release/版本镜像。认证/网络错误也停止。最后才创建目标 SHA 的新 tag，发布同一批已核验镜像及准确源码附件，Release 使用审查后的说明。多个服务的发布不具备原子性；发布阶段失败可能留下部分对象，工作流拒绝自动覆盖/删除，恢复方案须另行审查。

本地验证纠正了两个实现问题：`-trimpath` 不保留 ldflags 元数据，因此改用显式 Go build ID，并通过真实本机夹具输出验证注入值；Vite emptyOutDir 会清掉已跟踪的 static/out/README.md，因此恢复其原始字节后再读取 Go VCS 状态，保留其他源码/锁文件差异检查。首次失败日志和中间证据保留。

## 实际验证

下表记录应用前的准备阶段验证，原证据保持不变；应用后的活动文件复核单独列在后文。

| 检查 | 结果及证据 |
| --- | --- |
| 起点/结束冻结校验 | 989 项候选、500 项工作区输入均匹配；[baseline.json](../build/release-local/r1-ci-20261020-01/evidence/baseline.json)、[final-checks.json](../build/release-local/r1-ci-20261020-01/evidence/final-checks.json) |
| 两份 YAML 解析 | 复用缓存中的 gopkg.in/yaml.v3 v3.0.1，通过；未安装依赖或工具 |
| job 结构与失败门禁 | 6 种状态检查通过，含 test 失败、build 失败/取消、prepare 失败、publish=false；[workflow-contract-checks.json](../build/release-local/r1-ci-20261020-01/evidence/workflow-contract-checks.json) |
| Python 合成材料/镜像及异常路径 | **27/27**；[unit-tests-final-2.log](../build/release-local/r1-ci-20261020-01/evidence/unit-tests-final-2.log) |
| 构建脚本真实 Go 夹具 | 七标准目标真实编译，本机夹具版本/commit/时间一致，ZIP 权限/报告字节通过；5 个预期失败入口均拒绝；[build-contract-checks.json](../build/release-local/r1-ci-20261020-01/evidence/build-contract-checks.json) |
| GHCR 脚本控制流 | recording Docker mock 下 RC、稳定版、构建失败三场景通过，未连接 daemon/registry；[image-script-checks.json](../build/release-local/r1-ci-20261020-01/evidence/image-script-checks.json) |
| 既有真实材料兼容检查 | 当前程序的 110 个实际依赖（含 replace）全部匹配准确模块 ZIP h1；旧 Alpine 附件 18 包/11 origin、文件/Git blob/SHA-512/归档内文件通过；[existing-materials-check.json](../build/release-local/r1-ci-20261020-01/evidence/existing-materials-check.json) |
| 脚本静态检查 | 三份 Bash、18 个 workflow 内联 shell、Python AST 和授权应用辅助脚本 PowerShell 解析通过 |
| 补丁和差异检查 | `git apply --check --whitespace=error-all` 与 `git diff --check` 均通过；已有 LF/CRLF 提示保留，无空白错误 |
| actionlint | 当前未安装，**未运行、未通过**；不能用 YAML 解析替代 |
| GitHub Actions / NDK / 实际多架构镜像 / artifact 上传下载 | 本轮未执行；合成夹具和交叉编译不代替这些证据 |

七目标构建针对小型夹具，前端使用 pnpm mock 来核对 VITE_APP_VERSION 环境传递；没有声称重新构建产品前端或验证 Android/其他平台实机。真实材料兼容检查也没有将旧材料重新绑定成新的 RC 发布输入。

## 授权应用后的复核

用户明确回复“授权执行应用这份 9 文件本地补丁并复核”后，执行下文所列的同一授权辅助脚本，返回成功。新增证据位于 [post-application-20261020-01](../build/release-local/r1-ci-20261020-01/evidence/post-application-20261020-01/summary.json)，没有覆盖准备阶段日志或修改原补丁。

| 检查 | 结果及证据 |
| --- | --- |
| 补丁应用 | SHA-256 与授权值一致，三个现有目标的原始哈希、六个新增目标不存在的检查均通过；[apply.log](../build/release-local/r1-ci-20261020-01/evidence/post-application-20261020-01/apply.log) |
| 九个活动文件 | **9/9** 与审查候选在 LF 标准化后完全一致；Git 转换为 CRLF，因此原始字节哈希不同，分别记录，未重写文件掩盖差异；[active-files-check.json](../build/release-local/r1-ci-20261020-01/evidence/post-application-20261020-01/active-files-check.json) |
| 活动文件单元测试 | **27/27**；[unit-tests-2.log](../build/release-local/r1-ci-20261020-01/evidence/post-application-20261020-01/unit-tests-2.log) |
| 活动 workflow | 两份 YAML 解析、结构断言和六种门禁状态检查通过；这不是实际 Actions 运行；[workflow-contract-checks.json](../build/release-local/r1-ci-20261020-01/evidence/post-application-20261020-01/workflow-contract-checks.json) |
| 静态检查 | 三份活动 Bash 文件原始内容及 LF 标准化内容均通过 `bash -n`，18 个内联 shell、两份 Python AST、授权辅助脚本 PowerShell 解析通过；[static-checks.json](../build/release-local/r1-ci-20261020-01/evidence/post-application-20261020-01/static-checks.json)、[tool-checks.json](../build/release-local/r1-ci-20261020-01/evidence/post-application-20261020-01/tool-checks.json) |
| 冻结候选与输入 | 989/989 冻结文件匹配；历史输入 499/500 匹配，唯一差异是本补丁修改的 build.sh；[frozen-checks.json](../build/release-local/r1-ci-20261020-01/evidence/post-application-20261020-01/frozen-checks.json) |
| 工作区保护 | 应用后、更新报告前，425 个补丁范围外文件逐字节未变；随后仅更新本报告状态。分支/HEAD 不变，依赖及锁文件无差异，反向补丁检查及工作区/暂存区空白检查通过；[workspace-checks-before-report.json](../build/release-local/r1-ci-20261020-01/evidence/post-application-20261020-01/workspace-checks-before-report.json)、[workspace-checks.json](../build/release-local/r1-ci-20261020-01/evidence/post-application-20261020-01/workspace-checks.json) |

首次活动文件测试因验证命令漏建夹具父目录而全部停在 setUp；失败日志 [unit-tests.log](../build/release-local/r1-ci-20261020-01/evidence/post-application-20261020-01/unit-tests.log) 保留。创建该目录后以同一活动测试文件重跑通过，无需修改补丁。该次活动文件与已验候选内容一致，因此没有机械重复七目标夹具构建、产品构建或数据库验收。首次应用复核时 actionlint 仍未安装、未运行；此待工具项随后在本地收尾中处理。线上与真实多平台验收仍待补齐。

## 本地收尾与补充门禁

用户要求开始本地收尾，随后明确授权 [三项具体执行范围](../build/release-local/r1-local-closeout-20261020-01/APPROVAL.md)：修复首次隔离验证影响的依赖链接/路径元数据、应用两文件补充 CI 门禁、仅对固定官方 actionlint ZIP 使用 Invoke-WebRequest 下载并校验。它们均已执行，无 commit、分支/远端设置变更、push 或发布。

首次真实入口验证使用共享 node_modules junction。pnpm 11.15.1 默认 `verifyDepsBeforeRun=install`，项目路径变化触发自动整理；原目录的 50 个链接和 3 份生成元数据引用了临时副本。原链接仍可解析，434 个原始源码输入、锁文件和 66 个原前端产物未变化（随后主计划按任务更新）。这是隔离方式的问题；保留 [完整事件及首次结果字段纠正](../build/release-local/r1-local-closeout-20261020-01/evidence/dependency-incident.json)，不能声称首次命令没有整理依赖。

按明确授权将 50 个链接恢复到原 `.pnpm` 路径，修正三份元数据中的路径，保留旧链接和元数据备份，并验证 ESLint、TypeScript、Vite 可解析；修复不下载包或变更依赖/锁文件版本。随后完整复制现有依赖到独立副本，严格拒绝自动整理后再验证。常规测试仅使用临时 SQLite/loopback 合成数据，不重跑已验收的 MySQL/PostgreSQL 实例。

| 收尾项 | 结果与证据 |
| --- | --- |
| 两文件补充 CI 门禁 | `ci-test.sh` 和 `release_contract.py` 强制 `pnpm_config_verify_deps_before_run=error`；已授权应用，SHA-256 `e677556de59efaa2638e2311f71c8d60d2bb03239e0309da26dfbf20db7e4b13`；[补丁/哈希](../build/release-local/r1-local-closeout-20261020-01/review/manifest.json)、[活动文件检查](../build/release-local/r1-local-closeout-20261020-01/evidence/applied-guard-checks.json) |
| 新门禁回归 | 两个真实 pnpm 缺依赖场景均拒绝且未创建 node_modules；27 项候选单元回归通过；[拒绝检查](../build/release-local/r1-local-closeout-20261020-01/evidence/pnpm-guard-checks.json)、[回归日志](../build/release-local/r1-local-closeout-20261020-01/evidence/candidate-unit-tests.log) |
| actionlint | **1.7.7，两份活动 workflow 退出 0**；[版本](../build/release-local/r1-local-closeout-20261020-01/evidence/actionlint-version.log)、[结果](../build/release-local/r1-local-closeout-20261020-01/evidence/actionlint-result.json) |
| 真实新 CI 入口 | **退出 0，450.153 秒**；独立副本中的真实 ESLint、TypeScript/Vite、Go default/jsoniter 全量、两组 vet、conf/db/op/update/handlers race、三份 Bash 和 27 项脚本测试全部通过；[完整日志](../build/release-local/r1-local-closeout-20261020-01/evidence/ci-entry-isolated.log)、[执行结果](../build/release-local/r1-local-closeout-20261020-01/evidence/ci-entry-isolated-result.json) |
| 原依赖路径修复 | 50 个链接、3 份路径元数据已修复，备份保留；[修复结果](../build/release-local/r1-local-closeout-20261020-01/evidence/dependency-repair-result.json) |

actionlint ZIP SHA-256 为 `7f12f1801bca3d480d67aaf7774f4c2a6359a3ca8eebe382c95c10c9704aa731`，匹配通过 TinyFish 获取的 Microsoft winget-pkgs 固定版本清单，不将其表述为厂商签名。受限 shell 的 TLS 认证失败后，保留失败目录，以相同 URL/hash 在扩展权限下重试成功；没有关闭 TLS 或修改系统 PATH/持久环境。只联网获取了工具和公开来源，未核实目标仓库或 GHCR 状态。

原九文件补丁、首次应用日志和候选哈希均保持历史原件。当前最终活动内容应按“原九文件补丁 + 已授权两文件补充门禁”核对；不能再直接运行只断言原候选不变的旧验证脚本。

实际前端产物含本地版本 `v0.1.0-local.r1ci-20261020`；副本 README 原始字节保留，434 项副本源码输入在纳入两文件获授权补丁后没有意外变化。最终活动九文件叠加核对、989 项历史冻结文件、499 项未变历史输入（仅 build.sh 为已授权差异）、66 个原前端产物和工作区保护结果见 [收尾汇总](../build/release-local/r1-local-closeout-20261020-01/evidence/summary.json)及 [最终完整性](../build/release-local/r1-local-closeout-20261020-01/evidence/workspace-final.json)。分支仍为 test，HEAD 仍为 `948de764ae9a034fd72899686d24dcc4ecad7fa5`。本轮 local closeout 不生成新 RC/材料或产品候选，不重复既有数据库/容器验收。

## 后续需要的证据

1. **本地九文件应用、两文件补充门禁、actionlint 1.7.7 和完整真实入口验证已完成。** 本地收尾无剩余待工具项；Windows/Git Bash 结果仍不替代 runner 上的 Ubuntu Actions 运行证据。
2. **R1 远端只读条件核实已完成**：仓库管理设置、分支/environment 的缺项、11 个 action 引用和工具来源已记录；read:packages 生效后精确 GHCR 包及版本仍为 404，当前身份未找到。包可见性/关联/发布权限留到实际对象的后续验收。major action tags 并非 immutable SHA；没有猜造上游 action SHA。
3. R2 按另行授权集成 main、提交和验证普通 CI。当前 main.go 的 v0.13.6 注释和草稿 Release Notes 会被 prepare 正确阻止，留待 R3 审查版本和说明时修改。
4. R3 为准确发布 commit 准备/审核并上传 `release-materials` artifact，来源 run 和 materials.json 哈希作为输入。当前本地 amd64 材料不满足新的 11 程序/四镜像矩阵。材料准备/上传入口及 NDK/多架构构建仍需对应环境工作；本补丁提供消费与拒绝不完整材料的合同，没有提前声称这些新材料已生成。
5. 在线 artifact、GHCR/Release、公开源码入口和真实下载、两个 RC 间升级按 R3/R4 验收。平台实机按既定发布范围补齐。

这些后续项仍待各阶段验收，不能被写成本轮已经通过。

## 授权与实际应用

已授权并执行的目标：将本报告所列的 **9 文件补丁**应用到本地工作区，使未来普通 CI 测试和手动发布按上述合同运行，并复跑本地可用检查。影响是修改两份活动 workflow 和 build.sh，新增六份支持文件；后续提交/推送时才可能激活相应 CI 行为。

已执行 PowerShell 7 命令：

```powershell
pwsh -NoProfile -File build/release-local/r1-ci-20261020-01/apply-approved.ps1 -ApprovedPatchSha256 14f1d04b7f21ceb387ca4b2d524df91c2f9e33eac324293dbea4a17683f548b4
```

该辅助脚本先验证补丁 SHA-256、三个现有目标的原始字节哈希及六个新增目标不存在，然后依次执行：

```powershell
git apply --check --whitespace=error-all build/release-local/r1-ci-20261020-01/review/r1-ci.patch
git apply --whitespace=error-all build/release-local/r1-ci-20261020-01/review/r1-ci.patch
git diff --check
```

应用后已复跑本地 YAML/脚本/单元检查并核对活动文件。此授权目标只包含本地补丁落地和可用验证；工具安装、commit、main 集成、push、线上 Actions、tag、Release、镜像和远端设置均属于各自另行授权的操作。

依据是用户提供的 AGENTS.md 授权门禁：**“必须先说明目标、命令、影响，并获得针对该操作的明确授权”**，其中列有“修改环境配置或 CI”。Alpine 交接同样要求先审查具体 workflow 补丁再取得 CI 文件修改授权。用户随后明确授权本补丁应用和复核；本轮已据此执行，无 commit 或远端写操作。
