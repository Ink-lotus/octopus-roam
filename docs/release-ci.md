# 手动发布 CI 合同

普通 push / pull_request 只运行测试。release workflow 仅接受 main 上的手动触发，`publish` 默认 false；即便开启，也必须经过 `prepare → test → build → publish`。publish 使用 `release` environment，其远端保护规则需另行核实和授权配置，文件本身不会替仓库创建审批规则。

版本合同目前仅覆盖首发的 `v0.1.0-rc.N` 和 `v0.1.0`，N 从 1 开始。输入 commit 必须是 main 历史中的完整 40 位 SHA；main.go 的版本注释和 Release Notes 标题必须精确匹配。当前上游版本注释、草稿说明或已经存在的 tag / Release / 版本镜像都会阻止发布。后续版本线需要单独审查更新合同。

**工作包 3 接续（2026-10-23）：**[工作包 1](../plan/release-step-01-result-20261022-01.md) 的 Go 1.27.3 与产物门禁已进入 main；[工作包 2](../plan/release-step-02-result-20261023-01.md) 记录 `f857486093a185669fa0996451949563ade37e9e` 的 Ubuntu run `39917087653` 成功，以及 main 必需检查和 release 审批配置。最终 RC 提交仍需自身普通 CI。本次新增材料生产/上传与验收布局入口；用法及真实执行边界见 [材料与验收入口](release-materials.md)。真实材料在工作包 4 生产/上传，完整矩阵及最终产品另行演练。

首发采用[方案 A 支持矩阵](support-v0.1.0.md)：正式支持 Windows amd64/SQLite 与 WSLc 3.0.3.0/Linux amd64 镜像/SQLite，其他构建按实验性交付。产物合同仍要求全部 11 ZIP、四架构镜像、对应源码、编译版本和完整哈希；不因支持范围缩减放宽任何构建/材料检查。正式版 `INSTALL.md`、源码包 README、Release Notes 须从最终获准源码使用一致范围重新生成并绑定准确 SHA，不能直接改写已封存或公开的 RC 附件。

## 对应材料输入

build 从本仓库指定的 Actions run 下载唯一名为 `release-materials` 的 artifact。手动触发同时填写审核后的 `materials.json` SHA-256；材料中的版本、commit、完整平台矩阵和所有文件哈希必须一致。材料准备和上传是独立的 R3 工作：这份 workflow 不会将旧候选材料改名后继续发布，也不会自动执行材料包内的脚本。

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

应用源码 ZIP 延续 R0 对应源码包布局：`application/`、`go-cache/cache/download/`、`npm-packages/`、`licenses/`、重建/安装/源码获取说明及 `SHA256SUMS`。另加 `release-source.json`，内容必须精确为 `schema`、`version`、`commit`、`platforms` 四项，分别对应以上值。生产入口还加入 `tool-inputs.json` 和 `material-production.json`，绑定输入哈希、实际工具/生产脚本及实际前端。SHA256SUMS 必须覆盖除自身外的全部常规文件。保留完整原始声明，包括 AGPLv3、LGPLv3、GPLv3、MPL-2.0 原文；顶层五份说明及 `licenses/THIRD_PARTY_LICENSES.csv` 必须与材料目录的副本逐字节一致。

`application/` 必须包含目标 commit 中 `.github`、cmd、internal、scripts、static、web 下的已跟踪文件，以及 main.go、go.mod、go.sum、LICENSE、两份 README、Compose 和 .dockerignore；static/out 以本次实际生成内容为准。不得包含 node_modules、测试 profile、私钥、数据库、日志或工具缓存。新编译程序的实际依赖（含 replace 后的模块）必须有对应模块 ZIP，模块内容 h1 同时匹配实际二进制元数据和对应源码中的 go.sum。源码包内仍需准确的前端来源记录、原始声明和重建工具材料；自动哈希检查不代替材料审核或 LGPL 重新链接演练。

四份 Alpine ZIP 延续 R0 包格式：ALPINE-PACKAGES.json、FILE-SHA256SUMS.json、SOURCE-BINDINGS.json、GITMODES.json、SOURCE-ARCHIVE-FILES.json、NOTICE-INDEX.json、准确 aports/源文件/原始声明及重建说明。逐项验证文件、Git blob、APKBUILD SHA-512 输入和源归档内文件。清单字段区分大小写，实际镜像的 P/V/A/o/c/L 必须与相应平台材料一致。最终 IMAGE-MANIFEST.json 另将本次实际镜像 ID、镜像 tar 哈希、原始包数据库哈希与 OS 源码 ZIP 绑定；不会把历史镜像 ID 当作本次镜像 ID。

## 构建、artifact 和发布

测试使用 Go 全量/default/jsoniter、vet、关键 race、前端 lint/TypeScript/Vite、发布脚本测试及 actionlint。依赖在 setup 阶段按锁文件获取，测试和编译阶段使用 GOPROXY=off、GOTOOLCHAIN=local、-mod=readonly，保留校验。构建不运行联网价格脚本。

版本显式注入 Go ldflags、可跨平台读取的 Go build ID 和 VITE_APP_VERSION。build.json 使用 schema 2，记录版本、SHA、平台、程序哈希及逐目标实际 Go 编译版本；公开 BUILD.json 是其逐字节相同副本。Vite 构建前缓存 static/out/README.md 原始字节并在结束时恢复，随后检查其他已跟踪源码/锁文件无变化。

`package`、`seal`、`verify` 均要求检查工具为精确的 Go 1.27.3，再以 `go version -m` 读取每个实际程序的构建元数据，要求版本为 `go1.27.3`，并检查 GOOS/GOARCH 和 `go tool buildid`。seal/verify 重新读取 bin 中的程序，检查程序哈希、ZIP 内程序逐字节相同及版本记录一致；verify 还比较公开 BUILD.json 和 11 个交付 ZIP 与已检查的内部副本，复核公开 SHA256SUMS。四份镜像继续逐字节比较对应 Linux 程序。清单自报的版本或重新计算的哈希不能替代实际元数据。

构建记录字段固定如下；缺字段、未知字段、schema 1、缺少目标或版本不一致均拒绝，不自动迁移旧产物：

| 字段 | schema 2 合同 |
| --- | --- |
| schema | 整数 2 |
| version / commit | 本次准确版本 / 完整 SHA |
| platforms | 固定 11 目标顺序；显式本地 standard-only 仅记录七目标且不能 seal/verify |
| binaries | `{目标: 实际程序 SHA-256}`；沿用原值类型 |
| go_versions | `{目标: 从该程序元数据读取的 Go 版本}`；每项必须为 `go1.27.3` |

materials.json、release-source.json 和 artifact.json 保持各自 schema 1。build.sh 的 package 调用方及发布测试已同步，未增加产品依赖。`release_acceptance.py prepare` 对外部审核哈希绑定的完整 artifact 执行 verify 后，提取对应源码及原程序，生成明确标为 `derived-acceptance-layout` 的 schema 2 build-info.json。数据库和产品/更新入口均先重新验证此绑定；数据库驱动从冻结源码构建，并可仅验证输入或跳过程序演练。旧 build-local.ps1 布局继续作为历史入口，新旧验收结论分别记录；不把原 artifact 直接传给旧布局驱动。

发布矩阵是七个标准目标加四个 Android/NDK 目标，共 11 个 ZIP；`--standard-only` 只用于明确的本地子集验证，seal/verify 拒绝把它当作完整发布。ZIP 内文件名保留更新器白名单，补充声明和源码入口附入两份 README，程序模式 0100755、其他文件 0100644。

build 创建明确的 docker-context，通过独立的 scripts/dockerfile/Release.Dockerfile 使用审查过的基础镜像 digest，构建四份 Docker tar，不登录发布仓库、不推送镜像。校验 tar 配置/层摘要、平台、标签、实际程序、CSV、声明、entrypoint 和实际 APK 数据库；整个流程不启动产品或数据库。将 build、context、images、materials、delivery 全部封装成含版本和完整 SHA 的单一 Actions artifact，另记录完整文件清单和哈希。

publish 只下载本次 run 的确切 artifact，显式通过 setup-go 安装 Go 1.27.3（cache=false），保持 GOTOOLCHAIN=local、GOPROXY=off 和 -mod=readonly，重新核对所有文件、实际程序版本及对应源码/实际镜像映射，然后核实远端 tag、Release 和版本镜像均不存在。认证失败、网络错误或已有对象均停止。门禁通过后，才创建指向指定 SHA 的新 tag，加载并推送已验证的四份镜像 tar，组合 GHCR 多平台版本标签，并使用审核后的说明创建 Release。RC 只发布版本及架构标签；稳定版才更新 latest。唯一镜像渠道为 GHCR。

`publish=true` 会重新执行 prepare/test/build，不会复用先前 `publish=false` run 的 artifact。先验证 `release` environment 的实际审批规则，再启动获准的发布 run；最终放行须对应该 run 的实际编译版本、版本/SHA、材料及产物哈希。演练通过不代替这次产物的审核。

Release 附件是 delivery/ 的全部文件：11 个程序 ZIP、准确应用源码、四份 OS 源码、声明/源码说明、BUILD.json、MATERIALS.json、IMAGE-MANIFEST.json、Release Notes 和统一 SHA256SUMS。内部 Docker tar/context 不作为公开附件。公开下载、显著网络源码入口及真实拉取/升级需在 R3/R4 实际验收。

发布涉及多个服务，无法原子完成；tag 创建后发生发布错误可能留下部分对象。工作流不自动删除、不覆盖、不强推；保留失败证据后另行审查恢复方案。

## 工具版本和本地验证边界

runner 为 ubuntu-24.04；Go 1.27.3、Node.js 24.18.0、pnpm 11.15.1、Python 3.14.6、NDK r28c。Go 固定在包含 CVE-2026-111943 修复的版本；Node 与 pnpm 沿用当前已验收本地工具，Python 采用本地实际版本。actionlint 固定 v1.7.7，安装在 runner 临时目录；不修改产品依赖。材料 CSV 在独立材料阶段使用固定 go-licenses v2.0.1 和锁定 pnpm 生成，并按实际目标审查，不以一份非空 CSV 代替源码/声明。

checkout/setup-node/setup-go/setup-python/pnpm/setup-ndk/Docker actions 保留现有 major 引用；upload-artifact v4.6.2、download-artifact v4.3.0。major 引用不是不可变 SHA。仓库权限和 environment 规则已有工作包 2 证据，实际执行前继续回读；如要改为审查后的 action commit SHA，需补齐来源证据后再修改，不猜测 SHA。

本地 YAML 解析与结构/门禁测试不等于 actionlint 或 GitHub Actions 通过。NDK 构建、多架构镜像、artifact 上传/下载、GHCR 首次认证/权限都需要各自环境证据；失败时补齐实际问题，不降低门禁。

## Go 1.27.3 安全基线与 RC 前补充计划

[官方漏洞记录 GO-2026-6809](https://pkg.go.dev/vuln/GO-2026-6809)说明 CVE-2026-111943 涉及 `crypto/x509` 对特定不再受信任中间证书的拒绝检查，修复版本包括 Go 1.26.10、1.27.3。本项目选择 Go 1.27.3，必须重新编译程序才能包含标准库修复。当前配置固定版本与实际产物证明分开验收。工作包 1 实施了上述门禁，本地结果及精确输入见结果报告；以下要求中的新 SHA CI、材料、完整产物和产品实测继续分别验收：

1. **R2 工具链与新 SHA 验收。**核对 Go 1.27.3 的官方来源、校验和及本地/runner 实际版本。工具获取、环境或 CI 调整按具体方案授权执行；保持 `GOTOOLCHAIN=local`、测试/构建阶段 `GOPROXY=off`、`-mod=readonly` 和既有校验。完成规定的完整测试与 workflow 检查，取得新 SHA 的普通 Ubuntu CI 结果；RC 版本提交后再绑定最终 SHA 的结果。
2. **产物编译版本记录与拒绝检查。**在 `scripts/release_contract.py` 的 package、seal、verify 中，从全部 11 个程序的实际构建元数据校验本轮固定版本 `go1.27.3`；在 `build.json` 与公开 `BUILD.json` 增加实际 Go 版本记录，字段/schema 和调用方随补丁审查。下载后的 verify 重新核对实物，不能只相信清单自报版本。四份镜像继续核对其程序与对应 Linux 二进制逐字节一致。
3. **核验工具和回归覆盖。**publish 已显式安装 Go 1.27.3；本地 verify 也必须将已核验的该版本工具置于子进程 PATH，并设置 GOROOT、GOTOOLCHAIN=local、GOPROXY=off、GOFLAGS=-mod=readonly。新增发布回归覆盖三个阶段的旧/缺失/非精确元数据、错误检查工具、记录/实物/公开副本不符、11 目标和四镜像字节绑定；另用真实小型 PE/ELF/Mach-O 程序验证读取器。这些 fixture 不是完整产品矩阵构建证据。
4. **R3 材料与完整重建。**材料生产入口和上述补丁完成后再冻结 RC；更新材料与复建/再链接证据。`publish=false` 用新工具链构建全部 11 个 ZIP、四架构镜像和 sealed artifact，回下载验证。基础镜像 digest 固定后仍核对实际 APK 数据库；未变的来源按差异复用，旧程序、旧镜像及旧哈希不能作为新 RC 产物证据。
5. **TLS 及实际产品验收。**使用最终程序覆盖 MySQL/PostgreSQL TLS、更新元数据与资产下载路径：合法链/自定义 CA 正常，不受信证书及错误主机名拒绝。普通 TLS 正反例证明应用兼容性，不能单独证明此次特定中间证书拒绝机制已生效；保留官方修复说明、工具来源和实际产物版本证据，引用上游回归时另记来源与范围。继续完成 C1 真实 MySQL 启动、C2 页面/进程升级、最终包及容器测试，历史业务结果按受影响范围复用。

后续 rc.1、rc.2 和正式版使用同一已审查基线，各自绑定准确源码、材料和构建结果；若工具链或构建输入再次变化，重新审查并补受影响验证。公开下载/拉取、真实 RC 间升级、失败恢复和声明平台实测继续作为 R4/R5 条件。执行顺序及授权边界见 [RC 发布交接](../plan/release-rc-handoff-20261021.md) A0–F；本节没有授权修改脚本、CI、环境或执行发布。
