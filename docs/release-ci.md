# 手动发布 CI 合同

普通 push / pull_request 只运行测试。release workflow 仅接受 main 上的手动触发，`publish` 默认 false；即便开启，也必须经过 `prepare → test → build → publish`。publish 使用 `release` environment，其远端保护规则需另行核实和授权配置，文件本身不会替仓库创建审批规则。

普通 CI 保留同名必需检查 `test`，按完整变更范围选择检查，不使用会令必需检查缺席的 workflow 路径过滤：

- 仅修改根目录 `README.md`、`README_zh.md`、`CHANGELOG.md`、`web/README.md` 或 `docs/` 下的 Markdown（不含归档）时，使用 Python 标准库检查 UTF-8、代码围栏、行尾空白和本地链接目标。删除或改名文档时同时检查其他维护中文档的引用。此路径不安装 Go、Node.js、pnpm 或项目依赖，不执行产品全量测试。
- 任意代码、依赖、脚本、workflow、运行时资源、其他路径或文档文件模式变更，均执行现有完整测试。没有差异、缺少比较基线或无法可靠判断时也执行完整测试。
- main 的 push 比较事件中的前后提交；其他工作分支从与 main 的共同祖先比较全部待合入变更，首次 push 同样适用。PR 比较目标基线与实际受测合并提交，避免仅因最新提交改文档就漏测此前的代码变更。
- release 调用始终执行完整测试；可复用 workflow 的 `force_full_test` 默认 true，指定准确 `commit` 时也强制全量。普通文档检查通过仍会产生成功的 `test` 结果，失败会阻止主分支门禁通过。

日常改动使用工作分支验证后合入 main，再删除该分支；无需另建一个仅用于验证同一提交的分支。主分支的必需检查保护仍适用，文档分流本身不会允许未经检查的直接推送。

版本合同仅接受 `v0.1.2` 和 `v0.1.2-rc.N`，N 为无前导零正整数。输入 commit 必须是 main 历史中的完整 40 位小写 SHA；该 commit 的 main.go 版本注释、web/package.json 版本（不带 v）和 Release Notes 首行必须精确匹配。旧 0.1.0 / 0.1.1、其他版本线、local 后缀、非法 RC、错误 commit、未审查说明及已有 tag / Release / 版本镜像均拒绝。普通开发 CI 的 dev / v0.0.0-ci 不进入正式材料合同。

本轮正式目标为 v0.1.2。已有标签、Release 和版本镜像的拒绝检查继续生效；v0.1.1 的公开产物保留原样。材料生产和验收命令见[材料与验收入口](release-materials.md)，本轮产品变化和兼容边界见 [v0.1.2 说明](releases/v0.1.2.md)。

支持范围沿用[既有支持矩阵](support-v0.1.0.md)：Windows amd64/SQLite 与 Windows/WSL Containers 3.0.3.0/Linux amd64 镜像/SQLite，其他构建按实验性交付。产物合同仍要求全部 11 ZIP、四架构镜像、对应源码、编译版本和完整哈希。正式版 `INSTALL.md`、源码包 README、Release Notes 须从最终获准源码使用一致范围重新生成并绑定准确 SHA，不能直接改写已封存或公开的 RC 附件。

## 唯一发布说明来源

`scripts/release_contract.py` 的 `RELEASE_NOTES_PATH` 固定为 `docs/releases/v0.1.2.md`，`read_release_notes(version, commit)` 从准确 commit 的 Git blob 读取原始字节，同时验证 main 和前端版本。prepare、materials、package、seal、verify、材料生产/审核及派生验收复用该入口；不从工作区或已取消跟踪的旧说明路径回退取值。

prepare / resolve 输出 `release_notes_path` 和 `release_notes_sha256`，workflow 将它们传给后续 job 的同名大写环境变量并再次校验。materials.json、material-production.json、build.json、artifact.json 和派生 build-info.json 均记录说明路径及 SHA-256。对应源码包含 `application/docs/releases/v0.1.2.md`，delivery/RELEASE-NOTES.md 必须与同一 blob 逐字节相同，包含原换行和末尾空行。

新说明须与版本入口一起进入获审查 commit；未提交文案不属于 HEAD。候选同样使用此路径，但首行、main 注释、前端版本及注入值必须使用当批 RC。转正式或改变文案/源码后，重新审查和绑定 commit、材料与产物，重做受影响验收；改文件名或重算清单哈希不能使旧输入合格。`plan/release-notes-v0.1.0.md` 仅保留历史本地字节，不再参与新发布链路。

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
  "release_notes_path": "docs/releases/v0.1.2.md",
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

`application/` 必须包含目标 commit 中 `.github`、cmd、internal、scripts、static、web 下的已跟踪文件，以及 main.go、go.mod、go.sum、LICENSE、两份 README、Compose、.dockerignore 和 `docs/releases/v0.1.2.md`；static/out 以本次实际生成内容为准。不得包含 node_modules、测试 profile、私钥、数据库、日志或工具缓存。新编译程序的实际依赖（含 replace 后的模块）必须有对应模块 ZIP，模块内容 h1 同时匹配实际二进制元数据和对应源码中的 go.sum。源码包内仍需准确的前端来源记录、原始声明和重建工具材料；自动哈希检查不代替材料审核或 LGPL 重新链接演练。

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

## 本轮后续验收与准确输入

固定工具版本、真实程序的编译元数据和产品行为分别留证。发布脚本测试中的合成 11 目标/四镜像及真实小型 PE/ELF/Mach-O 程序只验证合同读取器，不代表完整产品已构建或运行。

1. 用最终源码执行 `scripts/ci-test.sh` 的 default/jsoniter、两种 vet、规定 race、前端及发布脚本检查，并完成 014/015、渠道两个兼容开关、1000～1003、退役设置、联合恢复及真实旧程序拒绝矩阵。完整 CI 使用独立源码目录，避免默认前端输出影响旧证据。
2. 审查准确 commit、版本和 Notes blob/SHA。重新生成新增或变化的依赖来源、许可证 CSV、原始声明、工具清单、REBUILD/INSTALL 及源码材料；未变来源只能经差异审查复用。补齐应用复建和 LGPL 再链接证据。
3. 使用同一输入执行完整构建、四镜像构建、package/seal/verify；记录材料 SHA、全部 ZIP 哈希和镜像摘要。后续验收使用同一批准确产物，不能沿用旧二进制或更换名称。
4. 用真实 v0.1.1 程序及升级前完整快照，在 Windows amd64/SQLite 和指定 WSLc/Linux amd64/SQLite 上完成升级、重启、恢复及功能联合检查；保留 v0.1.0 首次布局迁移说明；新原生助手再次更新/失败恢复单列。既有 TLS 和其他数据库检查按实际回归入口保留，不扩大正式支持声明。
5. 实际发布前回读远端状态、权限和 environment 保护，并对准确目标、命令及影响取得当次授权。源码、版本、说明、锁文件或材料改变时保留旧证据，重新绑定输入、补受影响检查及构建/平台验收。

本页不授权远端写入，也不从历史归档继承待办。v0.1.2 的全版回归、正式材料、候选产物、平台验收和发布结果须分别记录真实执行事实。
