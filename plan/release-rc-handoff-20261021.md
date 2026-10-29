# Octopus Roam RC 发布与正式发布交接

**2026-10-29 工作包 9 准备：**工作包 8 / R4 已完成，已验收 `v0.1.0-rc.2` 的完整 SHA 为 `1a8729a11b122888a683fd1bd9491ce38ceb4e18`；公开产物见[rc.2 发布结果](release-step-08-result-20261029-01.md)，真实升级、恢复和支持范围见[工作包 8 收口结果](release-step-08-result-20261029-02.md)。方案 A 正式支持 Windows amd64/SQLite 与 WSLc 3.0.3.0/Linux amd64 镜像/SQLite，其余完整构建按实验性交付。本包按 F 第 5–6 项 / R5 准备 `v0.1.0`：版本和文档差异经审查后冻结新 SHA，重新绑定材料、执行规定 CI 并审核实际发布 run 的全部产物；正式发布与 `latest` 写入仍须针对该 run 单独授权。正式公开下载/拉取和首次启动通过后再更新为 R5 完成。

编制日期：2026-10-21。计划修订：2026-10-22。工作目录：`D:\tool\AI\octopus-roam`。目标仓库：`Ink-lotus/octopus-roam`。镜像渠道：仅 `ghcr.io/ink-lotus/octopus-roam`。

**当前接续入口：[工作包 1 本地验收结果](release-step-01-result-20261022-01.md)。该包仅覆盖 A0 第 1–4 项；A0 第 5 项的提交推送、新 SHA 普通 Ubuntu CI 及仓库设置留给工作包 2。材料入口和完整 11 目标/四镜像真实构建仍按后续工作包执行，不把本地验收记为 A0 整体完成。**

后续路线仍为：收口新 SHA CI 与仓库配置 → 材料入口及 RC 冻结 → 全部材料与产物重建 → publish=false 和最终候选测试 → 审核实际发布 run 产物 → rc.1 → 真实下载/拉取及 rc.1 到 rc.2 在线升级 → 正式 v0.1.0。

原[主发布计划](release-plan-v0.1.0.md)第 6 节已有 R2–R5 路线。本文件保留 2026-10-21 的完成证据，并按 2026-10-22 的安全工具链调整更新执行顺序及验收清单。主计划早期状态和第 7 节中的旧分支、旧候选及“尚无 Ubuntu CI”等表述属于历史记录；接续时以本文件和所链接的结果证据为起点，并重新核对现场。

历史说明：2026-10-22 的计划修订当时仅更新三份文档；其“未提交”描述已被后续提交取代。工作包 1 接手时三份计划已在 `1af3ec18aac751eac668ace4331ce8544dbf461f` 提交且尚未推送，五份 Go 版本相关文件仍有未提交输入。工具链、门禁和本地检查的本次真实状态以工作包 1 结果为准；下文 2026-10-21 / 第 1.1 节表格保留历史基线，材料入口仍待完成。

## 1. 2026-10-21 已完成的历史基线

| 项目 | 当时确认的结果 |
| --- | --- |
| 本地分支 / HEAD | `main / b949271f2d9183cc08bf7a5d621f69272187ff4f` |
| 提交说明 | 完善独立发布流程、数据库兼容和更新确认 |
| Tree | `e5399692770645b7591d0585cf84288b69b1e4b1` |
| 远端 main | 同一完整 SHA，首次普通 push 成功 |
| 历史关系 | main 包含领先 master 的 41 个提交，master 是其祖先 |
| 远端 master / 默认 HEAD | `4a54b59e2f489c92070cb1cec5ed12cd0bb72e22`；推送后核验时默认分支仍为 master |
| Ubuntu CI | [run 39620815636](https://github.com/Ink-lotus/octopus-roam/actions/runs/39620815636)，attempt 1，push / main / 同一 SHA，completed / success |
| 实际 job | [test](https://github.com/Ink-lotus/octopus-roam/actions/runs/39620815636/job/119293382453)，`ubuntu-24.04`，所有步骤 success，用时 6 分 19 秒 |
| 本地完整性 | 编写原交接前工作区、暂存区干净；推送后 441 项审查文件原始字节未变 |
| 发布状态 | 该次执行没有创建 tag、Release、GHCR 镜像，也没有手动触发 release workflow |

权威依据：[上传与 CI 结果](../build/release-local/r2-main-push-20261021-01/RESULT.md)、[CI 成功证据](../build/release-local/r2-main-push-20261021-01/evidence/ci-final-success-20261021-01.json)、[精确 refs](../build/release-local/r2-main-push-20261021-01/evidence/remote-refs-verified-20261021-01.json)、[本地完整性](../build/release-local/r2-main-push-20261021-01/evidence/local-integrity-20261021-01.json)。

Ubuntu 已覆盖 Go default/jsoniter、两组 vet、定向 race、前端 lint/TypeScript/Vite、发布合同和脚本语法、actionlint，以及源码/锁文件完整性。它通过的是测试 workflow，未执行发布包、NDK、Docker 多架构或 release artifact 的真实构建。

该次 workflow 固定 Go 1.27.0，成功记录只证明旧工具链和旧 SHA 的测试结果。Go 1.27.3 修改进入新的提交后，需要新的完整普通 CI；最终 RC SHA 还须独立绑定其测试、材料和构建证据。

`internal/update/core_restart_test.go` 中的 Unix 替换单测只在 Windows 跳过；此次 Ubuntu 全量与定向 race 已覆盖该包。因此不再把“Unix 临时文件替换单测缺环境”列为待办；运行中 Linux/macOS 产品的升级、进程重启和失败恢复仍是另一项实测。

### 1.1 2026-10-22 安全工具链调整与待办

[Go 官方漏洞记录 GO-2026-6809](https://pkg.go.dev/vuln/GO-2026-6809)将 CVE-2026-111943 定位在 `crypto/x509`：对已不再信任的一组中间证书增加拒绝检查，修复版本包括 Go 1.26.10、1.27.3。本项目已选择精确的 Go 1.27.3 构建基线。程序需要重新编译才能包含该标准库修复。

| 项目 | 本次只读核对与后续处理 |
| --- | --- |
| 工作区配置 | `go.mod`、普通测试和发布构建已改为 Go 1.27.3，README 与 CI 文档已同步；相关改动尚未提交 |
| Git / 远端 | 本地仍为 `main / b949271f2d9183cc08bf7a5d621f69272187ff4f`；本次未重新查询远端，默认分支、保护及包状态须在 A 回读 |
| 本地工具链 | 在 `GOTOOLCHAIN=local` 下，`go version` 返回 `go1.27.0 windows/amd64`；本地验证前须准备符合新基线的工具链 |
| 产物版本检查 | 当前 `package` 读取 `go version -m`，但未校验实际 Go 版本；`build.json` 未记录 Go 版本，seal/verify 也未检查。A0 所列补充门禁待实现、待验收 |
| 材料与新证据 | `release-materials` 生产/上传入口仍缺失；Go 1.27.3 完整测试、新 RC 材料及全部产物重建仍待执行 |

## 2. 可沿用的历史证据与尚未覆盖的范围

| 范围 | 已有证据 | 下一步如何使用 |
| --- | --- | --- |
| MySQL / PostgreSQL | [2026-10-18 数据库报告](release-local-database-acceptance-20261018.md)：旧冻结候选在两种本地真实引擎各 35/35，通过严格 TLS、迁移、业务、行锁、备份、空库恢复及原始 Windows 程序启停 | 保留对应候选的结果；用 Go 1.27.3 最终程序补两库 TLS 定向验证及最新 C1 的真实 MySQL 启动 |
| Windows 业务与更新 | [历史验收报告](release-acceptance-20261015.md)：实际 Windows 包业务、SQLite、页面；同源 overlay 的 loopback 发布源串通生产更新链路 | 不能作为最新 C2 或真实 GitHub RC 间升级已经通过的证据 |
| 最新 C1 / C2 | [2026-10-21 修复结果](release-r2-p2-fix-result-20261021.md)：DSN/TLS、固定更新目标、400/409/回执及前端 VM 合同；Go 1.27.0 基线的完整本地与 Ubuntu 回归通过 | 补 Go 1.27.3 回归，以及最终 RC 程序上的 C1 启动和 C2 页面/进程测试 |
| 容器 | [Docker 交付报告](release-docker-delivery-20261019.md)：WSL Containers、Linux amd64、SQLite 的所列 C1–C9 场景及实际镜像 tar 交付；本 RC 门禁接受管理员 WSLc 作为 Docker Engine/Compose/GHCR 等价验收环境 | 四架构镜像全部使用新编译的程序重建；外部数据库容器、反向代理和真实版本升级仍按各自范围验收 |
| 应用许可证与源码 | [应用材料报告](release-license-materials-20261019.md)：Go 1.27.0 下的历史七平台复建、对应源码、原始声明和 LGPL 再链接验证 | 审查依赖/输入差异，复用未变来源；更新 Go 1.27.3 重建说明并补复建/再链接，为新 RC SHA 和完整矩阵重新绑定材料 |
| Alpine OS 材料 | [Alpine 材料报告](release-alpine-materials-20261019.md)：历史 Linux amd64 镜像的准确源码/声明附件 | 新四架构镜像必须分别核对实际包数据库，旧 amd64 ZIP 不能冒充四架构材料 |
| 其他平台 | 历史标准七目标交叉构建；本 RC 明确不进行 Linux `arm64/arm/386`、macOS、Android 原生实机验收 | 构建/归档材料仍记录；正式版若声明支持对应范围，再补实机或由用户调整支持范围 |

本次工具链输入已经变化，11 个程序和四架构镜像都须重新构建，旧程序、镜像及哈希不作为新 RC 的产物证据。未变的 Go/npm 模块来源、原始声明和业务验收结果按差异复用；两库各 35/35 等历史结果继续绑定原候选，不机械重跑全部业务套件。新增验证重点是新工具链完整 CI、TLS 路径、C1/C2、最终包运行及材料复建/再链接；出现相关差异或失败时再扩大回归范围。

`build/` 的证据与材料处于本地忽略目录。下个会话若仍在同一工作区可直接读取；换机器时另行携带所需公共证据和材料，不把私有 profile、证书私钥或数据库数据混入交付。

## 3. 授权与执行约束

沿用用户提供的 AGENTS.md 原文，以下是本流程的关键约束：

- 默认中文，shell 使用 PowerShell 7；commit summary 使用中文。
- 删除、整文件覆盖重写、批量修改、依赖/环境/CI/数据库变更、计划外 commit、历史改写、生产操作和任何远端状态变更，先准备可审查的具体目标、文件或实例、命令与影响，再取得针对当前操作的明确授权。
- 本次本地 main 创建、提交历史上传和普通 Ubuntu CI 的授权已经执行完成；不扩展为默认分支、保护、environment、版本提交、材料上传、workflow dispatch、tag、Release 或镜像发布授权。
- 已有凭据、历史授权、工具放行和计划条目均不构成当前操作授权。获准同一具体操作后按授权继续，无需重复确认。
- 联网检索/内容抓取默认使用 `tinyfish search query "关键词"`、`tinyfish fetch content get "<url>"`，先用 `ConvertFrom-Json` 筛选。认证管理 GET、二进制下载和 registry 操作若需要其他工具，列明目标限定的本次例外；不沿用上一轮 `gh GET` 例外。
- 只读核对和本地准备可以先进行；本任务单会话推进，不主动派发代理。图片若确需生成或编辑，使用 `$local-imagegen`。
- 新构建、测试、日志和补丁放在新的 `build/release-local/<唯一编号>/`；保留旧候选、失败证据和已初始化测试数据。
- 数据库及升级测试仅使用获准的隔离测试实例与合成数据。旧云库部分初始化、旧本地两轮实例已结束，不作为空库直接重跑。
- 保留 TLS、类型约束、认证、校验和和材料检查。启动测试程序前落实联网价格任务、发布源、供应商/Webhook 的隔离；不要把“localhost 监听”等同于无出站请求。
- 不输出密钥、真实 DSN、账号凭据、私钥、私有连接参数或会话标识。只停止本次创建且可核实的测试进程；清理删除另按授权处理。

## 4. 按顺序执行

### A0. R2 前置：验收 Go 1.27.3 基线并补齐产物检查

1. 核对现有 Go 版本改动、官方工具来源与校验和，以及本地/runner 实际版本。工具获取或环境调整先列出具体命令与影响并获准；本地可使用任务目录内的工具链和子进程环境，继续保持 `GOTOOLCHAIN=local`，依赖获取与离线测试/构建分阶段执行。
2. 准备补充门禁的具体补丁：从 11 个程序的实际构建元数据读取并校验 `go1.27.3`，在 `build.json` / `BUILD.json` 记录实际 Go 版本，package、seal、verify 均检查；四份镜像继续通过实际程序字节一致性关联这些程序。不能只信清单中自行填写的版本。字段/schema、材料驱动适配和发布合同测试一起审查。
3. 当前 publish job 没有显式安装 Go；若新增 verify 使用 Go 读取元数据，补丁须为该 job 及本地核验说明准备确定的工具，不能假设 runner 已有正确版本。相关脚本及 CI 修改按具体补丁授权应用，补旧工具链、缺失元数据、记录不一致等拒绝路径验证。
4. 用 Go 1.27.3 完成现有完整测试入口和 workflow 检查：default/jsoniter、两组 vet、关键 race、前端 lint/TypeScript/Vite、发布合同及脚本检查、actionlint、源码/锁文件完整性。旧 Go 1.27.0 结果保留为历史记录；本次文档修订不代表这些检查已执行。
5. 审查提交划分，按获准的 push 或 PR/合并路径将改动进入 main，记录新 SHA 与其 Go 1.27.3 普通 Ubuntu CI。A 的配置方案可以同步准备；B 的最终 RC 提交若产生新 SHA，仍需该 SHA 自身的普通 CI。

通过标准：实际工具链来源和版本可核对，新工具链完整检查及新 SHA 普通 CI 通过，补充产物检查已实现且拒绝路径通过。工具链准备、产物门禁和 B 的材料入口都在最终 RC 冻结前收口。

### A. 接手只读核对，准备 R2 配置方案

1. 读取本交接、主计划 R2–R5、[发布 CI 合同](../docs/release-ci.md)、当前 `.github/workflows/build.yaml` / `release.yaml`、`scripts/build.sh` / `release_contract.py` / `release-images.sh`。
2. 核对当前 main、HEAD、工作区、origin 唯一推送地址及精确远端 refs。本交接、主计划、CI 文档及第 1.1 节所列 Go 版本修改是已知的未提交输入，不能当作意外污染而删除。现场 HEAD 若已变化，先比较，不能重复创建 main 或覆盖用户改动。
3. 核对当前仓库、默认分支、保护/rulesets、environment、Actions workflow、tag/Release 和 GHCR 状态。认证 GET 需要本次联网例外时先申请，只查询目标仓库和精确包。历史“包不存在”不等于此刻可发布。
4. 准备三项分别可审查的远端设置：默认分支改为 main；main 的保护方案；`release` environment 的部署门禁。真实普通 CI 检查名为 `test`、来源为 GitHub Actions，保护时核对实际 check context。
5. main 的 PR、审批和直推规则按维护方式明确；若新保护要求 PR，后续版本提交走获准的 PR/合并路径。environment 的审批人、分支限制及能否完成审批先核实，再写具体设置请求。
6. 获准后按具体命令应用并回读设置。确认默认 main 上注册的是新的手动 release workflow，普通 push 仍只测试。

通过标准：main 默认分支与已选择的保护方案明确并落实；release environment 的实际规则已核对，发布流程能等待最终放行。若用户决定不采用某项保护，记录该明确选择，不能默默宣称已配置。

### B. 完成 RC 版本、材料入口与补充门禁，冻结最终提交

1. 使用 `v0.1.0-rc.1`；读取 `main.go` 和 `plan/release-notes-v0.1.0.md`，准备具体补丁。当前仍是 `// Version v0.13.6` 和草稿，发布 prepare 会拒绝。
2. 发布说明标题必须是准确 RC 版本，并审查正文。纠正旧的“数据库未验”“Docker 环境不可用”等过时描述，写清已测候选与按本 RC 门禁明确排除的原生平台；保留真实限制。README/Compose 的候选安装示例使用确切 RC 标签，稳定版尚未发布时不以 `latest` 作为可用候选安装渠道。
3. 补齐 `release-materials` 的准备和上传入口，使用 Go 1.27.3，并绑定准确版本、完整 SHA 和实际前端生成结果。当前三个 workflow 没有这个生产入口；`release.yaml` 只消费已存在 artifact。先提交具体补丁和输入来源给用户审查，取得该 CI 修改授权后应用验证。
4. `scripts/prepare-database-acceptance.ps1` 读取旧本地候选的 `build-info.json` 与冻结 source 清单；新 Actions artifact 使用 `build.json`。复用驱动时先审核布局适配和输入绑定，不能直接用旧候选证明新 RC 程序通过。
5. 确认 A0 的实际编译版本检查、构建记录/schema、配套测试及本步材料生产入口均已完成并通过验证，相关输入全部进入待冻结的源码；不在材料制作完成后再补这些 CI/脚本改动。
6. 按批准的文件清单核对局部差异、暂存树和未提交文件，再执行获准的中文版本 commit，例如“准备 v0.1.0-rc.1 发布”。本交接是否纳入该提交也必须在清单中说明。
7. 按获准的推送或 PR/合并路径进入 main，记录最终完整 SHA，验证这个新 SHA 使用 Go 1.27.3 的普通 Ubuntu CI。`b949271` 是交接基线，不是自动选定的 RC 发布 SHA。

通过标准：RC 版本注释、发布说明、源码、产物编译版本检查及材料生产入口的最终状态可审查；最终 RC commit 在 main 历史中，当前 SHA 的 Go 1.27.3 普通测试通过。后续材料和发布只绑定该版本/完整 SHA，不能先制作材料再随意更改代码。

### C. 生成并审核准确 release-materials

以[发布 CI 合同](../docs/release-ci.md)和 `scripts/release_contract.py` 为准，准备：

- `materials.json`：准确版本、完整 commit、commit 时间戳、11 目标矩阵、四架构 Alpine 固定基础镜像 digest、全部材料文件 SHA-256。
- `THIRD_PARTY_LICENSES.csv` 和 `octopus-roam-source-<version>.zip`；准确应用源码、实际 Go 模块/replace 来源、锁定前端源码、原始声明、重建说明及 `release-source.json`。
- `THIRD-PARTY-NOTICES.md`、`SOURCE-OFFER.md`、`source-offer.html`、`REBUILD.md`、`INSTALL.md`。
- `alpine/amd64.zip`、`arm64.zip`、`arm.zip`、`386.zip`；每份绑定准确 aports/source、声明和相应镜像实际包字段。

复用旧材料前比较依赖和来源；程序、前端生成结果和文档字节必须对应新 commit。应用源码中的 `static/out` 以这次实际前端构建为准；后续 build 还会检查它，不能将旧 dist 直接换名。锁定且未变的 Go/npm 模块源码及原始声明可按差异复用。

本次将 `REBUILD.md`、工具来源及校验和更新为 Go 1.27.3，核对相应 Go 原始声明，并使用新工具链补应用复建与 LGPL 再链接验证。应用源码 ZIP、`release-source.json`、`materials.json` 和全部相关哈希重新绑定最终 RC SHA；旧 Go 1.27.0 复建结果不能直接记为新材料已经通过。

固定基础镜像 digest 后，Dockerfile 仍会执行 apk 安装；实际包输入漂移必须更新材料或修复构建方案，不能关闭准确包核验。材料审核包含本地源码获取页面与实际公开入口的安排；公开提供源码附件时仍要验证用户能显著、免费地获取它。

先本地核验，再按获准的材料 workflow/上传命令形成**本仓库**唯一名为 `release-materials` 的 artifact，回下载并检查全部哈希，记录来源 run ID、artifact ID、`materials.json` SHA-256 与保留时间。CLI 不能凭空把一份本地目录当作已上传的 Actions artifact。

通过标准：完整材料与同一 RC SHA 绑定，源码/声明审查及 Go 1.27.3 复建/再链接通过，上传下载一致，发布 build 可从指定 run 消费。只有历史 amd64 材料、非空 CSV 或合成 fixture 均不满足本项。

### D. publish=false 完整演练，补最终候选测试

1. 填入已核对的 version、commit、materials run ID 和材料 SHA，准备当前 `release.yaml` 的手动触发命令及影响；取得本次 workflow dispatch 授权后以 `publish=false` 在 main 运行。
2. 必须真实完成 `prepare → test → build`：用 Go 1.27.3 新编译全部 11 个程序 ZIP（标准七目标 + Android 四 ABI / NDK r28c），使用这些程序重建四架构镜像 tar；核对实际编译版本、准确应用/OS 源码、版本和 build ID、归档权限、材料/镜像映射、统一哈希及 sealed artifact。
3. 确认 publish job 因输入为 false 跳过，并只读确认未产生候选 tag、Release 或版本镜像。保存实际 run/commit/version/日志/artifact 记录。
4. 下载演练 artifact，在新目录执行已补齐编译版本检查的 `verify`，然后使用其中的实际程序和镜像完成第 5 节的“RC 公开前”测试，包含 TLS 定向验证及原 C1/C2 待办。调用程序或写测试数据库前仍需相应隔离范围和写入授权。
5. 发现问题先保留失败证据，准备具体修复；代码/材料变更后回到 B/C 重新冻结与绑定，工具链再变化时同时回到 A0，不覆盖旧候选。

通过标准：同一 RC SHA 的 Go 1.27.3 完整构建、实际编译版本和 artifact 上传/下载/verify 全通过；TLS、最新 C1/C2、可用主平台最终包冒烟无发布阻塞问题；其他实机待验逐项登记在 RC 说明。

### E. 审查实际发布产物，发布 rc.1

当前 workflow 再次以 `publish=true` dispatch，会重新执行 prepare/test/build，**不会自动复用 publish=false 演练 run 的 artifact**。最终发布批准要对应这次实际构建出来的产物。

推荐保留现有同一 run 结构：先核实 `release` environment 的审批门禁已生效；用户批准启动发布 run 的构建并等待审批后，下载审核该 run 的 sealed artifact，向用户说明确切版本/SHA、实际 Go 编译版本、资产/镜像/材料哈希和写入命令，取得最终发布授权，再放行该 run 的 publish。若改为直接发布之前演练的 artifact，先另行审查并授权 CI 修改。

publish 阶段必须：

1. 下载本 run 的准确 artifact，重新 `verify`，确认全部程序实际编译版本及镜像内程序映射；不能拿本地另建目录或其他 run 的同名包替代。
2. 查询并拒绝已有 tag、Release、版本镜像。授权/网络失败先停止；不能把任意查询失败当作对象不存在。
3. 校验通过后才创建指向审核 SHA 的 tag；推送已经 sealed 的四份镜像并组合版本 manifest；创建 prerelease Release，上传 delivery 的完整附件。
4. `rc.1` 只更新版本及架构镜像标签，Release 为 prerelease，`latest` 不更新。
5. 回读 tag SHA、Release prerelease 状态、所有附件哈希和镜像 digest/platform，记录真实结果。

发布链条涉及 GitHub 与 GHCR，多服务写入不是原子的。失败时先列出已经产生的对象并准备恢复方案；不自动删除、覆盖、强推或重建替换公开资产。

### F. 真实产物验收、rc.2 和正式版

1. 用普通用户路径从 RC Release 实际下载 ZIP/`SHA256SUMS`/应用和 OS 源码，匿名拉取对应 GHCR 镜像；核对哈希、实际 Go 编译版本及镜像内程序与发布证据完全一致。
2. 在隔离实例验证最终公开产物的安装、当前备份恢复、正常停止/重启与持久化。产物一致且既有业务未变时补渠道与运行冒烟，避免机械重复完整业务套件。
3. 为完成真实自更新验收，另行审查并授权 `v0.1.0-rc.2` 的版本、材料、构建和发布；两个 RC 都使用 Go 1.27.3，随后实际执行 `rc.1 → rc.2` 的查询、目标确认、真实下载/校验、替换、重启及运行版本确认。失败恢复在隔离故障场景验证，不篡改公开 RC 资产。
4. 补正式版要求的全部支持平台实机；无法补齐时由用户明确决定公开支持范围及相应 CI/文档变更。RC 未测声明不等于正式版已验收。
5. 候选规定检查、数据库新安装/恢复、公开下载/拉取、源码入口、真实在线升级与失败恢复都通过，文档准确且无发布阻塞问题后，准备 `v0.1.0` 的具体补丁、提交、构建和正式发布申请。
6. 正式版本获准后，用同一审查后的 Go 1.27.3 基线重建全部程序和镜像、绑定正式版本材料并复核实际编译版本，再发布 stable Release、版本镜像和 `latest`，补正式产物下载/拉取和首次启动冒烟。若只变版本/文案，执行规定 CI 和版本/构建/产物检查，业务套件按影响复用；功能、工具链或构建输入变化则回到对应阶段补受影响回归。若后续安全修复要求更新工具链，先重新审查基线和材料，不自动沿用本次通过结论。

## 5. 剩余测试矩阵

**本 RC 门禁调整（2026-10-25，用户明确指定）：** Linux `arm64/arm/386`、macOS、Android 原生平台不纳入本 RC 的原生实机验收；已有交叉构建、归档和静态材料检查仍照常记录。管理员权限下的 WSL Containers（WSLc）最终镜像启动、数据挂载、正常停止、重启和换容器持久化结果，作为本 RC 的 Docker Engine/Compose/GHCR 容器验收等价证据。该调整不自动改变正式版未来的支持范围。

| 项目 | RC 公开前 | RC 公开后 / 正式版前 |
| --- | --- | --- |
| 最新 SHA 普通 CI | 新工具链提交及最终 RC SHA 均需 Go 1.27.3 完整普通 Ubuntu CI | 源码、工具链或构建输入变化时重新验证 |
| 实际 Go 编译版本 | 官方工具来源/校验和、runner 实际版本；11 个程序元数据为 `go1.27.3`，记录与实物一致，四镜像程序映射通过；新增拒绝路径测试通过 | 公开下载/拉取后复核；rc.2 与正式版使用同一已审查基线或重新验收的新基线 |
| TLS 定向验证 | 用最终程序覆盖两库 TLS、更新元数据与资产下载路径：合法链/自定义 CA 正常，不受信证书及错误主机名拒绝；保留严格 TLS 和脱敏 | 验证真实 Release HTTPS 路径；证书链或部署输入变化时补受影响验证 |
| C1 真实 MySQL | 自定义 CA + 无查询参数 DSN；含既有合成刷新记录启动，确认时间字段读取/连接默认值与严格 TLS；用最终候选程序 | 公开包哈希相同则补下载与启动确认；数据库输入变化时复验 |
| C2 页面与更新确认 | 最终前端实际浏览器：目标 JSON、400/409、刷新后再次点击、成功回执、pending/成功/失败/超时及轮询停止；隔离测试程序验证完整更新/重启 | 两个真实 RC 间在线升级、数据保留与隔离失败恢复 |
| Windows / SQLite 最终包 | 实际新 ZIP 的安装、前后端版本、业务冒烟、当前备份恢复与正常重启 | 从公开渠道下载同包后核对与启动 |
| Linux/macOS 进程升级 | 本 RC 不纳入 Linux `arm64/arm/386`、macOS 原生平台验收；Linux amd64 容器生命周期由 Docker 项覆盖 | 若正式版支持范围纳入对应 OS/架构，再补实机升级/恢复 |
| Android / NDK | 本 RC 不纳入 Android 原生实机验收；四 ABI 构建、归档与版本/hash 检查仍需有材料 | 若正式版支持范围纳入 Android，再补声明范围的实机运行和升级验收 |
| Docker | 四架构真实构建、tar/平台/程序/实际 APK 与源码映射；管理员 WSLc 启动/持久化/重启实测按本 RC 门禁等同 Docker Engine/Compose/GHCR | 真实公开渠道下载/拉取和版本升级按工作包 8 或正式版范围处理；外部数据库、反向代理按声明部署范围补验 |
| 材料 / artifact | 准确来源/声明、Go 1.27.3 重建说明及复建/再链接、最终 SHA 源码/归档/hash、上传下载、seal/verify | 公共源码入口、真实下载与可获取性 |
| 稳定与 RC 通道 | 现有回归 + 候选页面与版本一致 | 真实 RC 升级、稳定通道及容器指引核对 |

TLS 正反例用于验证应用在新工具链下的兼容性；普通不受信证书或错误主机名被拒绝，不能单独证明此次特定中间证书拒绝机制已经生效。修复证据须包含官方修复说明、审查后的工具链来源及实际产物编译版本；引用上游回归或另做定向验证时单独记录其来源和范围。

准备 RC 的工作可以与未纳入本 RC 门禁的原生平台环境准备并行推进；正式版若声明支持这些平台，再补齐对应验收或修改支持范围。真实 Release 下载和两个 RC 之间的在线升级依赖已发布的 RC，不提前设为 rc.1 构建的前置条件。

## 6. 命令与证据模板

接手时的本地只读核对：

```powershell
git -c 'core.excludesFile=' status --short --branch
git rev-parse HEAD
git rev-parse 'refs/heads/main'
git remote get-url --push --all origin
git rev-list --left-right --count master...main
```

下列为**待填真实值、核对当前脚本/保护规则，并获得该步授权后**执行的模板；本计划没有执行这些命令：

```powershell
$rcVersion = 'v0.1.0-rc.1'
$rcCommit = '<已审查的40位RC提交SHA>'
$materialsRun = '<本仓库release-materials来源run-id>'
$materialsSha = '<已审查materials.json的64位SHA256>'

git -c 'core.excludesFile=' push --no-follow-tags --recurse-submodules=no origin ($rcCommit + ':refs/heads/main')

gh workflow run release.yaml --repo Ink-lotus/octopus-roam --ref main -f version=$rcVersion -f commit=$rcCommit -f materials_run_id=$materialsRun -f materials_sha256=$materialsSha -f publish=false
```

直接推送模板仅在已批准的分支保护/推送路径允许时使用；否则按已批准的 PR 路径进入 main。最终发布 dispatch 的 `publish=true`、environment 放行、tag/Release/GHCR 写入另按 E 的实际产物申请执行。

演练或发布 artifact 下载后，在已 checkout 同一审核 SHA 的工作目录核验：

```powershell
$artifactRoot = '<新的已解压artifact绝对目录>'
python -B scripts/release_contract.py verify --version $rcVersion --commit $rcCommit --output $artifactRoot --materials-sha $materialsSha
```

verify 会比较当前 checkout 的 Release Notes，因此不能在旧 b949271 工作区拿它验证新 RC。工作包 1 的新门禁要求本地明确使用已核验的 Go 1.27.3（子进程 PATH/GOROOT；GOTOOLCHAIN=local、GOPROXY=off、GOFLAGS=-mod=readonly），从实物复查版本并拒绝旧 schema 1 构建记录；须使用包含该实现的最终 SHA。材料生产、下载解压、数据库写入和实际进程更新的入口由执行会话读取当前实现后列出准确命令；这里不猜测尚未实现的工具参数。

每阶段结果至少记录：版本、完整 SHA、工具链来源/校验和及实际 Go 编译版本、输入差异/哈希、run 与 attempt、artifact ID/名称/完整哈希、各检查退出码、实际平台与程序/镜像 digest、已执行授权范围、失败与恢复记录、未验收项。新结果独立落盘，并更新本交接的进度或新增阶段结果，保留历史材料。

## 7. 可直接复制给下个会话

> 先读取 [工作包 1 结果](release-step-01-result-20261022-01.md)、本交接、`docs/release-ci.md` 和主计划 R2–R5，遵守本交接第 3 节及用户的 AGENTS.md。工作包 1 基线为 `main / 1af3ec18aac751eac668ace4331ce8544dbf461f`，五份初始未提交 Go 版本输入已保留，任务内 Go 1.27.3 已核验。具体补丁、源码哈希、本地检查和限制见结果报告；系统 Go 与任务工具分开记录。`b949271` 与 Ubuntu run `39620815636` 仅为 Go 1.27.0 历史证据。
>
> 工作包 2 从 A0 第 5 项的新提交、新 SHA Go 1.27.3 普通 CI 接续，同时准备 A 的默认分支/保护/release environment 方案。A0 整体尚未完成。B 的 RC 版本、材料生产入口和补充门禁全部完成后再冻结最终 SHA。目标路线为新材料与复建/再链接 → Go 1.27.3 全部 11 程序/四镜像的 `publish=false` 演练 → TLS、最新 C1/C2 与最终包测试 → 审核另一次实际发布 run 的 artifact → `v0.1.0-rc.1` → 真实下载/拉取与另行授权的 `rc.1 → rc.2` → 正式 `v0.1.0`。实际编译版本检查的实现与本地证据见工作包 1；材料入口和完整真实构建仍待后续执行，不把 fixture 或文字要求当作发布产物通过。
>
> 先完成本地、只读和可审查准备；工具获取/环境调整、远端设置、CI/版本补丁、提交推送、材料上传、workflow dispatch、数据库/升级写入和公开发布，按当前具体目标、命令和影响逐步取得授权，不把本段当作发布批准。旧来源和未变业务证据按差异复用，旧程序/镜像不能作为新 RC 产物；不重复创建 main，不覆盖旧候选或清理旧数据库，不主动派发代理。
