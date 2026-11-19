# 手动发布 CI 合同

自 2026-11-12 起，后续开发和发布采用本页的 Windows 原生 / Linux amd64 容器门禁及 `test` 分支流程。已发布版本的支持声明、产物和 WSLc 验收记录保留历史事实，不再为后续任务增加 WSLc 必验项。下文固定版本字段描述当前发布脚本，不表示启动新发布。

普通 push / pull_request 只运行测试。release workflow 仅接受 main 上的手动触发，`publish` 默认 false；即便开启，也必须经过 `prepare → test → build → publish`。publish 使用 `release` environment，其远端保护规则需另行核实和授权配置，文件本身不会替仓库创建审批规则。

普通 CI 保留同名必需检查 `test`，按完整变更范围选择检查，不使用会令必需检查缺席的 workflow 路径过滤：

- 仅修改根目录 `README.md`、`README_zh.md`、`CHANGELOG.md`、`web/README.md` 或 `docs/` 下的 Markdown（不含归档）时，使用 Python 标准库检查 UTF-8、代码围栏、行尾空白和本地链接目标。删除或改名文档时同时检查其他维护中文档的引用。此路径不安装 Go、Node.js、pnpm 或项目依赖，不执行产品全量测试。
- 任意代码、依赖、脚本、workflow、运行时资源、其他路径或文档文件模式变更，均执行现有完整测试。没有差异、缺少比较基线或无法可靠判断时也执行完整测试。
- main 的 push 比较事件中的前后提交；其他工作分支从与 main 的共同祖先比较全部待合入变更，首次 push 同样适用。PR 比较目标基线与实际受测合并提交，避免仅因最新提交改文档就漏测此前的代码变更。
- release 调用始终执行完整测试；可复用 workflow 的 `force_full_test` 默认 true，指定准确 `commit` 时也强制全量。普通文档检查通过仍会产生成功的 `test` 结果，失败会阻止主分支门禁通过。

日常改动使用可复用的 `test` 分支同步 Windows 与 Linux 测试环境，按以下流程合入 `main`：

1. 在本机提交本轮变更并按授权推送 `test`，记录完整 SHA。云机 fetch 后在独立、无未提交改动的测试目录检出该 SHA，并以 `git rev-parse HEAD` 核对；测试期间 `test` 再前进也不改变本轮受测输入。
2. 云机按 workflow 的固定工具版本和锁文件准备依赖，执行本轮所需检查与 Linux 验收，记录 SHA、命令、退出码、环境和产物哈希；GitHub 的 `test` 检查也须对应准确 SHA。文档分流规则保持原样，SSH 连通不能代替项目测试。
3. 晋升前获取最新远端状态，确认 `main` 是受测 SHA 的祖先，再以 `git merge --ff-only <已验证SHA>` 推进本地 `main`，按授权正常推送并核对远端 SHA 与检查结果。若无法快进，先将最新 `main` 合入 `test` 并重新测试最终 SHA；不得用强推或未经重验的 Squash / Rebase 提交替代。
4. `test` 保留复用，下一轮从已验收状态继续，不默认删除重建。Git 只同步已提交文件；被忽略的本地规划、凭据、数据库和依赖缓存不随分支传输。云端任务所需测试脚本纳入版本管理，缺失的任务参数显式传递。

主分支的必需检查和保护继续适用；文档化此流程不会创建远端分支、修改保护规则或授权 commit、推送和发布。实际远端配置在对应操作前回读，具体操作仍按当前任务授权执行。

版本合同仅接受 `v0.1.6` 和 `v0.1.6-rc.N`，N 为无前导零正整数。输入 commit 必须是 main 历史中的完整 40 位小写 SHA；该 commit 的 main.go 版本注释、web/package.json 版本（不带 v）和 Release Notes 首行必须精确匹配。已发布的 0.1.0～0.1.5、其他版本线、local 后缀、非法 RC、错误 commit、未审查说明及已有 tag / Release / 版本镜像均拒绝。普通开发 CI 的 dev / v0.0.0-ci 不进入正式材料合同。

当前合同服务于 v0.1.6 发布准备，候选流程显式使用 `publish=false`。已有标签、Release 和版本镜像的拒绝检查继续生效；v0.1.5 及更早公开产物保持原样。材料生产与验收见[材料入口](release-materials.md)，行为和兼容边界见 [v0.1.6 说明](releases/v0.1.6.md)。正式发布须对准确操作另行授权。

本版源码检查在隔离的受跟踪源码目录执行完整 `scripts/ci-test.sh`，追加 `node --test scripts/tests/test_model_filter.cjs` 及 `CGO_ENABLED=1 go test -race ./internal/webhook/...`；固定工具和既有检查继续生效。`92bea667c648a507abc776b48928e8a5260120c2` 仅为开发输入，版本和说明进入提交后须绑定新的最终完整 SHA。

本版专项验证 Key 作用域、规则通知的新冷却期/延长边界、旧 Webhook 模板，以及1000～1006恢复/重复导入、1006与两种小时归档共存、无扩展冷却1004/1005和真实v0.1.5拒绝1006且数据不变。旧自定义模板若要接收cooldown须有有效JSON分支。既有 `scripts/acceptance/backup/product.py` 固定真实v0.1.2/v0.1.3及其摘要；本版真实v0.1.5→v0.1.6升级须准备独立验收入口，不能替换旧脚本参数冒充完成。

## 后续平台门禁

当前公开支持范围与首次验收状态统一维护于[支持矩阵](support.md)，安装、升级和材料入口据此同步；下表规定每次发布必须留存的验收证据。

| 平台与部署组合 | 后续定位 | 验收要求 |
| --- | --- | --- |
| Windows amd64 原生程序 + SQLite | 正式支持 | 在 Windows 上验证真实程序的安装、菜单启停、原生更新助手、旧版升级、持久化和备份恢复；实际登录自启仅在该功能相关改动或异常时验收 |
| Linux amd64 宿主 + Docker Engine / Linux amd64 镜像 + SQLite | 正式支持；首次开发产物应用验收已完成 | 使用准确镜像验证启动、正常停止与重启、数据卷权限、旧版升级、容器重建、持久化和备份恢复；维护 Compose 部署入口时覆盖该入口 |
| WSL2 / WSL Containers | 预期兼容 | 不再单列正式支持组合、固定 WSLc 版本或设置发布必验项；遇到 Windows 挂载、权限、网络等集成问题时按实际环境调查 |

Linux 验收可使用 instabox 等主机，由当前代理通过 SSH 直接运行命令。支持对象是上表的运行环境与应用组合，不绑定云商，也不要求云机安装 Paseo 或 Codex。记录实际发行版、内核、架构、Docker / Compose 版本和挂载方式；云机当前预装工具与 CI 不一致时，先按授权准备固定工具版本，不能修改版本断言来迁就环境。

连接 instabox 前必须先通过脚本检查并按需唤醒目标主机，等待 `state=running` 且 `runtime.ready=true` 后再建立 SSH。本机 `plan/tools/Invoke-InstaboxSsh.ps1` 已内置这一顺序，日常连接使用该入口；具体命令、目标区分、冷却和凭据读取说明见 [AGENTS.md](../AGENTS.md)。脚本当前仅本机保存，跨机器缺少脚本或配置时先补齐条件，不能跳过唤醒直接重试 SSH。

两个正式支持目标均须以本次准确 commit 产生的实际产物执行应用级验收，并保留版本 / SHA、ZIP 哈希或镜像摘要、命令、退出码和结果：

1. 验证新装、登录、主要 API / 流式功能、正常启停与重启；使用隔离测试数据和本次产物，不连接生产数据库。
2. 使用真实前一正式版本及升级前完整数据副本验证升级、数据保留、当前加密备份在空实例中的恢复，以及旧产物配合升级前副本的失败恢复。Windows 原生助手的成功更新和失败恢复继续单独验证。实际登录自启按下述条件执行。
3. Linux 容器验证 `/app/data` 的挂载与实际运行用户权限，正常停止后用同一数据卷重建容器并核对数据。SQLite 数据目录使用 Linux 文件系统；Windows 共享目录行为不作为 Linux 门禁的隐含保证。

C02 已在准确提交 `9a7ed524bf763bc776ef291a0998aeb715700ad4` 完成固定工具、Windows/Linux/GitHub 完整 CI 和两平台开发产物应用验收，Linux 包含真实 v0.1.2 升级、卷权限、同卷重建和 Compose；准确版本/产物摘要见[支持与证据](support.md)。这些 `v0.0.0-ci` 结果不替代 v0.1.6 正式版本候选。现有 workflow 没有自动执行全部双平台应用验收，绿色 `test` 或成功构建镜像不能代替本次实际产物记录。

Windows 实际退出并重新登录的自启动测试不作为每次发布的固定门禁；只有登录任务、桌面启动控制、对应入口发生相关改动，或发现登录自启功能异常时，才执行该项并记录触发原因。普通业务变化、版本号或二进制摘要变化不自动触发。现有菜单启停与计划任务管理测试保留；不将未执行的实际登录测试记为通过。

客户端接入以原生 Codex CLI 和网关 HTTP / SSE 行为验收，不单列 Paseo 现场测试为交付或发布门禁。记录实际客户端版本与场景；取消单独验收不代表新增了 Paseo 现场通过证据。

Linux 原生程序、其他架构、macOS、Android、MySQL/PostgreSQL、其他容器运行时和反向代理等保持各自既有实验性或范围外状态。产物合同仍要求全部 11 ZIP、四架构镜像、对应源码、编译版本和完整哈希。后续版本的 README、安装 / 升级说明、支持矩阵、材料入口、`INSTALL.md`、源码包 README 和 Release Notes 须按新范围统一并绑定最终获准 SHA；[v0.1.0 支持矩阵](support-v0.1.0.md)及已封存或公开的历史产物不回写。

## 唯一发布说明来源

`scripts/release_contract.py` 的 `RELEASE_NOTES_PATH` 固定为 `docs/releases/v0.1.6.md`，`read_release_notes(version, commit)` 从准确 commit 的 Git blob 读取原始字节，同时验证 main 和前端版本。prepare、materials、package、seal、verify、材料生产/审核及派生验收复用该入口；不从工作区或已取消跟踪的旧说明路径回退取值。

prepare / resolve 输出 `release_notes_path` 和 `release_notes_sha256`，workflow 将它们传给后续 job 的同名大写环境变量并再次校验。materials.json、material-production.json、build.json、artifact.json 和派生 build-info.json 均记录说明路径及 SHA-256。对应源码包含 `application/docs/releases/v0.1.6.md`，delivery/RELEASE-NOTES.md 必须与同一 blob 逐字节相同，包含原换行和末尾空行。

新说明须与版本入口一起进入获审查 commit；未提交文案不属于 HEAD。`publish=false` 候选同样使用此路径，可使用本轮选定的 v0.1.6 或合法 v0.1.6-rc.N；首行、main 注释、前端版本及注入值必须与当批准确版本一致。RC 转正式版本或改变文案/源码后，重新审查和绑定 commit、材料与产物，重做受影响验收；改文件名或重算清单哈希不能使旧输入合格。`plan/release-notes-v0.1.0.md` 仅保留历史本地字节，不再参与新发布链路。

## 对应材料输入

build 从本仓库指定的 Actions run 下载唯一名为 `release-materials` 的 artifact。手动触发同时填写审核后的 `materials.json` SHA-256；材料中的版本、commit、说明绑定、完整平台矩阵和所有文件哈希必须一致。材料在构建前独立准备，workflow 不会将旧候选材料改名后继续发布，也不会执行材料包内的脚本。

`scripts/release_materials.py produce` 以审核后的输入清单生产材料，重建实际前端并绑定工具/源码/版本；`audit` / `pack` 核验后打包。`release-materials.yaml` 从本仓库准确 draft 暂存附件取得已审核字节，在 Ubuntu 再建前端并核验后上传唯一 `release-materials`。输入、工具、权限和命令见 [入口说明](release-materials.md)。最终 SHA 冻结后，更新 Go 1.27.3 的来源、校验和、原始声明及 `REBUILD.md`，补应用复建和 LGPL 再链接验证。未变的 Go/npm 来源与声明可经差异审查复用；源码、`release-source.json`、`materials.json` 及相关哈希须重新绑定最终版本/commit。

材料目录只允许下列文件：

```text
materials.json
THIRD_PARTY_LICENSES.csv
octopus-roam-source-<version>.zip
THIRD-PARTY-NOTICES.md
SOURCE-OFFER.md
source-offer.html
REBUILD.md
INSTALL.md
alpine/amd64.zip
alpine/arm64.zip
alpine/arm.zip
alpine/386.zip
```

`materials.json` 结构如下，尖括号内容是占位符：

```json
{
  "schema": 1,
  "version": "<exact-version>",
  "commit": "<full-commit-sha>",
  "release_notes_path": "docs/releases/v0.1.6.md",
  "release_notes_sha256": "<sha256-of-exact-committed-blob>",
  "source_date_epoch": 0,
  "platforms": [
    "linux-amd64", "linux-arm64", "linux-arm", "linux-386",
    "windows-amd64", "darwin-arm64", "darwin-amd64",
    "android-amd64", "android-arm64", "android-arm", "android-386"
  ],
  "alpine": {
    "amd64": {"base_image": "alpine@sha256:<reviewed-digest>"},
    "arm64": {"base_image": "alpine@sha256:<reviewed-digest>"},
    "arm": {"base_image": "alpine@sha256:<reviewed-digest>"},
    "386": {"base_image": "alpine@sha256:<reviewed-digest>"}
  },
  "files": {"<every-relative-file-except-materials.json>": "<sha256>"}
}
```

`source_date_epoch` 的实际值必须是目标 commit 的 Unix 时间戳，并大于 0。基础镜像可用同一多平台 index digest，也可分别指定对应平台 digest；不允许浮动标签。apk 安装后的实际包版本仍由镜像检查覆盖，发生漂移时构建失败，需重新准备准确材料，不得关闭检查。

应用源码 ZIP 保留对应源码包布局：`application/`、`go-cache/cache/download/`、`npm-packages/`、`licenses/`、重建/安装/源码获取说明及 `SHA256SUMS`。`release-source.json` 精确包含 `schema`、`version`、`commit`、`platforms` 四项，分别对应以上值。生产入口加入 `tool-inputs.json` 和 `material-production.json`，绑定说明 path/SHA、输入哈希、实际工具/生产脚本及实际前端。SHA256SUMS 必须覆盖除自身外的全部常规文件。保留完整原始声明，包括 AGPLv3、LGPLv3、GPLv3、MPL-2.0 原文；顶层五份说明及 `licenses/THIRD_PARTY_LICENSES.csv` 必须与材料目录的副本逐字节一致。

`application/` 必须包含目标 commit 中 `.github`、cmd、internal、scripts、static、web 下的已跟踪文件，以及 main.go、go.mod、go.sum、LICENSE、两份 README、Compose、.dockerignore 和 `docs/releases/v0.1.6.md`；static/out 以本次实际生成内容为准。不得包含 node_modules、测试 profile、私钥、数据库、日志或工具缓存。新编译程序的实际依赖（含 replace 后的模块）必须有对应模块 ZIP，模块内容 h1 同时匹配实际二进制元数据和对应源码中的 go.sum。源码包内仍需准确的前端来源记录、原始声明和重建工具材料；自动哈希检查不代替材料审核或 LGPL 重新链接演练。

四份 Alpine ZIP 保留既有包格式：ALPINE-PACKAGES.json、FILE-SHA256SUMS.json、SOURCE-BINDINGS.json、GITMODES.json、SOURCE-ARCHIVE-FILES.json、NOTICE-INDEX.json、准确 aports/源文件/原始声明及重建说明。逐项验证文件、Git blob、APKBUILD SHA-512 输入和源归档内文件。清单字段区分大小写，实际镜像的 P/V/A/o/c/L 必须与相应平台材料一致。最终 IMAGE-MANIFEST.json 另将本次实际镜像 ID、镜像 tar 哈希、原始包数据库哈希与 OS 源码 ZIP 绑定；不会把历史镜像 ID 当作本次镜像 ID。

## 构建、artifact 和发布

测试使用 Go 全量/default/jsoniter、vet、关键 race、前端 lint/TypeScript/Vite、发布脚本测试及 actionlint。依赖在 setup 阶段按锁文件获取，测试和编译阶段使用 GOPROXY=off、GOTOOLCHAIN=local、-mod=readonly，保留校验。构建不运行联网价格脚本。

版本显式注入 Go ldflags、可跨平台读取的 Go build ID 和 VITE_APP_VERSION。build.json 使用 schema 2，记录版本、SHA、说明绑定、平台、程序哈希及逐目标实际 Go 编译版本；公开 BUILD.json 是其逐字节相同副本。Vite 构建前缓存 static/out/README.md 原始字节并在结束时恢复，随后检查其他已跟踪源码/锁文件无变化。

`package`、`seal`、`verify` 均要求检查工具为精确的 Go 1.27.3，再以 `go version -m` 读取每个实际程序的构建元数据，要求版本为 `go1.27.3`，并检查 GOOS/GOARCH 和 `go tool buildid`。seal/verify 重新读取 bin 中的程序，检查程序哈希、ZIP 内程序逐字节相同及版本记录一致；verify 还比较公开 BUILD.json 和 11 个交付 ZIP 与已检查的内部副本，复核公开 SHA256SUMS。四份镜像继续逐字节比较对应 Linux 程序。清单自报的版本或重新计算的哈希不能替代实际元数据。

构建记录字段固定如下；缺字段、未知字段、schema 1、缺少目标或版本不一致均拒绝，不自动迁移旧产物：

| 字段 | schema 2 合同 |
| --- | --- |
| schema | 整数 2 |
| version / commit | 本次准确版本 / 完整 SHA |
| release_notes_path / release_notes_sha256 | 固定路径及同一 commit 的原始说明 SHA-256；本地非发布构建可为 null，但不能 seal/verify |
| platforms | 固定 11 目标顺序；显式本地 standard-only 仅记录七目标且不能 seal/verify |
| binaries | `{目标: 实际程序 SHA-256}`；沿用原值类型 |
| go_versions | `{目标: 从该程序元数据读取的 Go 版本}`；每项必须为 `go1.27.3` |

materials.json、release-source.json、material-production.json 和 artifact.json 保持各自 schema 1；build.json 与派生 build-info.json 仍为 schema 2。缺少新增说明绑定的旧记录不能进入新发布合同，不迁移或重封存旧产物。`release_acceptance.py prepare` 对外部审核哈希绑定的完整 artifact 执行 verify 后，提取对应源码及原程序，生成明确标为 `derived-acceptance-layout` 的 build-info.json，包含相同说明 path/SHA。数据库和产品/更新入口均先重新验证此绑定；数据库驱动从冻结源码构建，并可仅验证输入或跳过程序演练。build-local.ps1 的 local 版本布局继续用于本地验证，不能冒充准确的发布 artifact。

发布矩阵是七个标准目标加四个 Android/NDK 目标，共 11 个 ZIP；`--standard-only` 只用于明确的本地子集验证，seal/verify 拒绝把它当作完整发布。ZIP 内文件名保留更新器白名单，补充声明和源码入口附入两份 README，程序模式 0100755、其他文件 0100644。

build 创建明确的 docker-context，通过独立的 scripts/dockerfile/Release.Dockerfile 使用审查过的基础镜像 digest，构建四份 Docker tar，不登录发布仓库、不推送镜像。校验 tar 配置/层摘要、平台、标签、实际程序、CSV、声明、entrypoint 和实际 APK 数据库；整个流程不启动产品或数据库。将 build、context、images、materials、delivery 全部封装成含版本和完整 SHA 的单一 Actions artifact，另记录完整文件清单和哈希。

publish 只下载本次 run 的确切 artifact，显式通过 setup-go 安装 Go 1.27.3（cache=false），保持 GOTOOLCHAIN=local、GOPROXY=off 和 -mod=readonly，重新核对所有文件、实际程序版本及对应源码/实际镜像映射，然后核实远端 tag、Release 和版本镜像均不存在。认证失败、网络错误或已有对象均停止。门禁通过后，才创建指向指定 SHA 的新 tag，加载并推送已验证的四份镜像 tar，组合 GHCR 多平台版本标签，并使用审核后的说明创建 Release。RC 只发布版本及架构标签；稳定版才更新 latest。唯一镜像渠道为 GHCR。

`publish=true` 会重新执行 prepare/test/build，不会复用先前 `publish=false` run 的 artifact。先验证 `release` environment 的实际审批规则，再启动获准的发布 run；最终放行须对应该 run 的实际编译版本、版本/SHA、材料及产物哈希。演练通过不代替这次产物的审核。

Release 附件是 delivery/ 的全部文件：11 个程序 ZIP、准确应用源码、四份 OS 源码、声明/源码说明、BUILD.json、MATERIALS.json、IMAGE-MANIFEST.json、Release Notes 和统一 SHA256SUMS。内部 Docker tar/context 不作为公开附件。verify 同时核对公开 BUILD/MATERIALS/Notes 与已验证内部记录及 Git blob；公开下载、网络源码入口和真实拉取/升级仍需本轮实际验收。

发布涉及多个服务，无法原子完成；tag 创建后发生发布错误可能留下部分对象。工作流不自动删除、不覆盖、不强推；保留失败证据后另行审查恢复方案。

## 工具版本和本地验证边界

runner 为 ubuntu-24.04；Go 1.27.3、Node.js 24.18.0、pnpm 11.15.1、Python 3.14.6、NDK r28c。Go 固定在包含 CVE-2026-111943 修复的版本；Node 与 pnpm 沿用当前已验收本地工具，Python 采用本地实际版本。actionlint 固定 v1.7.7，安装在 runner 临时目录；不修改产品依赖。材料 CSV 在独立材料阶段使用固定 go-licenses v2.0.1 和锁定 pnpm 生成，并按实际目标审查，不以一份非空 CSV 代替源码/声明。

checkout/setup-node/setup-go/setup-python/pnpm/setup-ndk/Docker actions 保留现有 major 引用；upload-artifact v4.6.2、download-artifact v4.3.0。major 引用不是不可变 SHA。仓库权限和 environment 规则在实际执行前回读；如要改为审查后的 action commit SHA，需补齐来源证据后另行修改，不猜测 SHA。

本地 YAML 解析与结构/门禁测试不等于 actionlint 或 GitHub Actions 通过。NDK 构建、多架构镜像、artifact 上传/下载、GHCR 首次认证/权限都需要各自环境证据；失败时补齐实际问题，不降低门禁。

## 后续版本验收与准确输入

固定工具版本、真实程序的编译元数据和产品行为分别留证。发布脚本测试中的合成 11 目标/四镜像及真实小型 PE/ELF/Mach-O 程序只验证合同读取器，不代表完整产品已构建或运行。

1. 用最终源码执行 `scripts/ci-test.sh` 的 default/jsoniter、两种 vet、规定 race、前端及发布脚本检查，并完成本阶段所需数据回归；014/015/016/017、渠道两个兼容开关、1000～1003、退役设置、联合恢复及真实旧程序拒绝矩阵的未变专项可按源码/fixture 差异和摘要映射复用，写明原受测输入。新候选的真实 v0.1.5→v0.1.6 升级、持久化和备份恢复仍须执行；既有 v0.1.2/v0.1.3 遗留专项按未变输入差异和摘要映射保留。v0.1.6 还须对最终准确源码显式运行 opt-in MySQL 8.4 与 PostgreSQL 18 小时迁移/备份集成测试，记录实际服务器版本、退出码和结果；缺少 loopback 数据库参数而导致 `t.Skip` 不能计为通过。完整 CI 使用独立源码目录，避免默认前端输出影响旧证据。
2. 审查准确 commit、版本和 Notes blob/SHA。重新生成新增或变化的依赖来源、许可证 CSV、原始声明、工具清单、REBUILD/INSTALL 及源码材料；未变来源只能经差异审查复用。补齐应用复建和 LGPL 再链接证据。
3. 使用同一输入执行完整构建、四镜像构建、package/seal/verify；记录材料 SHA、全部 ZIP 哈希和镜像摘要。后续验收使用同一批准确产物，不能沿用旧二进制或更换名称。
4. 用本次指定的真实前一正式版本及升级前完整快照，在 Windows amd64 原生 / SQLite 和 Linux amd64 Docker 镜像 / SQLite 上完成上述升级、重启、恢复及功能联合检查；保留 v0.1.0 首次布局迁移说明；新原生助手再次更新 / 失败恢复单列。WSLc 不再作为必验项。既有 TLS 和其他数据库检查按实际回归入口保留，不由本次调整扩大其正式支持声明。
5. 实际发布前回读远端状态、权限和 environment 保护，并对准确目标、命令及影响取得当次授权。源码、版本、说明、锁文件或材料改变时保留旧证据，重新绑定输入、补受影响检查及构建/平台验收。

本页不授权远端写入，也不从历史归档继承待办。v0.1.5 及更早版本的已完成结果保留在原记录中；v0.1.6 的全版回归、正式材料、候选产物、平台验收和正式发布分别记录真实执行事实。
