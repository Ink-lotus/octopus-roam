# B 组：许可证工具准备与具体授权范围

准备及执行日期：2026-10-15。用户已回复“授权执行”。固定版本工具已隔离安装，真实许可证报告已生成并完成内容核验；新候选的构建与独立验收结果见本文末尾。A2、A3 候选作为历史证据保留。

## 固定版本与来源

| 工具 | 固定版本与本机状态 | 用途与来源 | 获取方式及影响 |
| --- | --- | --- | --- |
| `go-licenses` | **v2.0.1**；准备时缺失，现已隔离安装并核对构建元数据与 SHA-256 | 分析发布条件下的 Go 依赖，生成真实 CSV，定位原始 LICENSE/NOTICE。来源：[固定版本 README](https://github.com/google/go-licenses/blob/v2.0.1/README.md)、[固定版本 go.mod](https://github.com/google/go-licenses/blob/v2.0.1/go.mod)、[版本记录](https://github.com/google/go-licenses/releases/tag/v2.0.1) | `go install github.com/google/go-licenses/v2@v2.0.1`。联网下载该工具及其固定依赖，编译 Windows amd64 工具到本轮隔离目录；不使用 `@latest`。这是本次唯一新增的工具。 |
| `pnpm` | **11.15.1**；已安装、已验证版本，入口为 Corepack shim | 从已安装的前端依赖读取许可证元数据；[官方命令文档](https://pnpm.io/cli/licenses)，参数同时以本机 `pnpm licenses list --help` 为准 | 复用现有缓存，执行前检查精确版本，并在该进程设置 `COREPACK_ENABLE_NETWORK=0`；获取命令为“无需获取”，不执行安装或升级。 |
| Go / Node.js | **go1.27.0 windows/amd64 / v24.18.0**；已安装 | 编译上述工具，执行已有构建及本地报告处理 | 复用现有安装，不下载 toolchain，不修改系统配置。`go-licenses` 的 v2.0.1 模块声明 `go 1.23.0`；是否兼容当前实际源码与 Go 1.27，以安装后的运行结果判定。 |

上游 v2.0.1 README 的安装示例使用 `@latest`；本轮明确替换为固定的 `@v2.0.1`。上游工具依赖来自它自己的 go.mod/go.sum，与产品依赖分离。若固定版本出现兼容问题，保留原始失败记录，先报告具体原因和补丁范围，不自动升级工具或产品依赖。

2026-10-15 已通过 `tinyfish search query` 的 snippet 选择官方来源，再通过 `tinyfish fetch content get` 读取固定版本 README/go.mod 和 pnpm 文档。没有运行升级提示中的 `tinyfish upgrade`。抓取的 pnpm `11.x` 链接会重定向到当前文档，不能把其中新版本选项归给本机 11.15.1；本轮只使用本机 help 已支持的选项。

## 隔离目录与获取命令

本次编号为 `20261015-181705-6ebd8c65`。创建前已确认下面两个目录不存在；本轮实际使用这些新目录，不覆盖历史候选。

```text
build/release-local/b-licenses-20261015-181705-6ebd8c65/   工具、下载缓存、原始报告与独立验收证据
build/release-local/b-final-20261015-181705-6ebd8c65/      新候选，构建脚本独占并最终冻结
```

下列 PowerShell 7 命令在仓库根目录执行。环境变量仅影响当前工具进程，退出前还原；不执行 `go env -w`，不修改 PATH、用户配置、产品 go.mod/go.sum、package.json 或锁文件。下载只使用公共 Go module proxy 与校验数据库，不回退到 Git/direct 下载。

```powershell
$ErrorActionPreference = 'Stop'
$licenseRoot = Join-Path (Get-Location).Path 'build/release-local/b-licenses-20261015-181705-6ebd8c65'
if (Test-Path -LiteralPath $licenseRoot) { throw '本轮证据目录已经存在，需使用新编号。' }
$licenseBin = Join-Path $licenseRoot 'tools/bin'
$licenseWork = Join-Path $licenseRoot 'tools/work'
$licenseTemp = Join-Path $licenseRoot 'tools/tmp'
New-Item -ItemType Directory -Path $licenseBin, $licenseWork, $licenseTemp | Out-Null
$licenseEnv = @{
    GOENV = 'off'; GOWORK = 'off'; GOTOOLCHAIN = 'local'
    GOBIN = $licenseBin
    GOPATH = (Join-Path $licenseRoot 'tools/gopath')
    GOMODCACHE = (Join-Path $licenseRoot 'tools/modcache')
    GOCACHE = (Join-Path $licenseRoot 'tools/gocache')
    GOPROXY = 'https://proxy.golang.org'; GOSUMDB = 'sum.golang.org'
    GOPRIVATE = ''; GONOPROXY = ''; GONOSUMDB = ''
    GOFLAGS = '-mod=readonly'; GOOS = 'windows'; GOARCH = 'amd64'
    GOARM = ''; CGO_ENABLED = '0'; TEMP = $licenseTemp; TMP = $licenseTemp
}
$savedLicenseEnv = @{}
foreach ($name in $licenseEnv.Keys) {
    $savedLicenseEnv[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
}
Push-Location -LiteralPath $licenseWork
try {
    foreach ($name in $licenseEnv.Keys) {
        [Environment]::SetEnvironmentVariable($name, $licenseEnv[$name], 'Process')
    }
    & go install github.com/google/go-licenses/v2@v2.0.1
    if ($LASTEXITCODE -ne 0) { throw '固定版本许可证工具安装失败。' }
    & go version -m (Join-Path $licenseBin 'go-licenses.exe')
    if ($LASTEXITCODE -ne 0) { throw '无法核对许可证工具的构建信息。' }
    Get-FileHash -LiteralPath (Join-Path $licenseBin 'go-licenses.exe') -Algorithm SHA256
} finally {
    Pop-Location
    foreach ($name in $savedLicenseEnv.Keys) {
        [Environment]::SetEnvironmentVariable($name, $savedLicenseEnv[$name], 'Process')
    }
}
```

实际执行时另将 stdout/stderr、退出码、工具版本、工具二进制 SHA-256 与 `go version -m` 的工具模块/依赖版本保存到本轮证据目录。工具和缓存占用本地磁盘，失败材料也保留；不清理旧资源。安装阶段不改项目缓存 `.tmp-go-mod`。

## 已完成的只读预检

| 检查 | 实际结果 |
| --- | --- |
| A2 历史候选 `a2-20261012-193213-9b75afef` | 969 条文件哈希、485 条冻结源码输入全部匹配 |
| A3 候选 `a23-final-20261015-171713-428045a1` | 973 条文件哈希、487 条冻结源码输入全部匹配；487 条当前工作区输入也全部匹配 |
| 产品依赖、锁文件与 CI | `git diff --numstat -- go.mod go.sum web/package.json web/pnpm-lock.yaml .github/workflows` 无输出 |
| 前端生产依赖预检 | `pnpm --dir web licenses list --prod --json --long` 成功；151 个名称分组、152 个实际版本条目，没有 unknown/unlicensed 分组 |
| 前端全部已安装依赖预检 | `pnpm --dir web licenses list --json --long` 成功；324 个名称分组，没有 unknown/unlicensed 分组；包含开发和构建依赖，不能称为全部进入最终 bundle |
| 现有构建合同 | `build-local.ps1 -LicenseReportPath` 和 `build.sh --license-report` 已可接收非空 CSV；七平台 ZIP 与 Docker context 均复制该报告 |

全部已安装依赖中的 `caniuse-lite` 标注 CC-BY-4.0，`lightningcss` 系列标注 MPL-2.0；它们不在上述生产依赖清单中。正式报告应明确其构建工具身份，保留真实许可证材料，不能把构建工具依赖的许可证直接归到最终业务 bundle。`victory-vendor` 的生产依赖元数据是 `MIT AND ISC`，合并报告时不得简化成单一许可证。

这些是元数据预检，没有完成 Go 分析、许可证正文核对或正式打包，不能据此把许可证门禁标为通过。

首次安装因 Go 校验数据库尝试写入用户默认 GOPATH 被权限阻止；原始 `tool-install.json` 和日志保留。第二次把 GOPATH 同样隔离到本轮目录后成功，`GOSUMDB=sum.golang.org` 保持启用，没有申请写入用户目录或降低校验。

## 已授权的执行范围

1. **核对固定工具与冻结输入**：按上面命令获取 go-licenses v2.0.1，记录工具来源和哈希；再次核对产品源码、锁文件及既有候选。将报告输入复制到本轮新的分析目录，并记录输入清单，排除历史 build 副本、真实 profile、运行数据与工具缓存。
2. **分析真实 Go 依赖**：工作目录为新的分析源码目录；`GOTOOLCHAIN=local`、`GOPROXY=off`、`GOFLAGS='-mod=readonly -tags=jsoniter'`、`CGO_ENABLED=0`，复用已有产品依赖缓存。依次设置实际发布的 `GOOS/GOARCH`：Windows amd64、Linux amd64/arm64/arm/386、macOS amd64/arm64；arm 设置 `GOARM=7`。执行 `go-licenses report . --ignore github.com/Ink-lotus/octopus-roam`，分平台保存原始 CSV、独立 stderr 和退出码，不混入日志。另用其固定版本模板能力记录实际版本及 LICENSE 路径，读取对应原文及 NOTICE，保留可核对的正文/哈希。不会把被忽略的本项目依赖一起忽略。
3. **整理前端报告**：复用 pnpm 11.15.1 与已有 node_modules，禁用 Corepack 联网。分别保存上述 `--prod` 和全部已安装范围的 JSON；展开 `versions`/`paths`，按实际 package.json 和锁文件核对，而不是把每个名称分组当成单个版本。只从包自身的 LICENSE/NOTICE 等材料读取正文，不执行包脚本或安装依赖。
4. **校验与合并**：保留工具原始输出；生成有明确列定义的 `THIRD_PARTY_LICENSES.csv` 汇总报告，包含生态、依赖、实际版本、许可证表达式、来源、平台/范围和可追溯的原始许可证正文。多行正文按标准 CSV 转义；前端为归一化条目，不冒称 go-licenses 原始输出。校验未知许可证、实际源码覆盖、replace 模块、跨平台差异和重复项；记录非 Go 文件与 URL 定位告警的实际处理。报告或材料不完整时不把“文件非空”当作验收成功。需要补查来源时仍用 `tinyfish`，不自动扩大安装范围。
5. **在新目录重建候选**：以正式报告作为新的构建输入，调用下列入口；构建、回归与验收使用已有缓存离线执行。校验七个包及 Docker context 的报告与本轮报告逐字节一致、报告实际内容、源码/文件/归档哈希和 Unix 权限。补跑受影响构建、归档及最终 Windows 包本地冒烟；复用既有回归时必须核对对应源码/依赖输入并记录来源。仅在本轮新目录内使用合成 SQLite 和 loopback 测试进程；证据放在独立证据目录，不向冻结候选追加文件。不会复用会向 A3 历史证据目录写入 SQLite 的旧 verifier。

   ```powershell
   pwsh -NoProfile -File scripts/build-local.ps1 `
     -OutputRoot 'build/release-local/b-final-20261015-181705-6ebd8c65' `
     -Version 'v0.1.0-local.b-final-20261015-181705-6ebd8c65' `
     -LicenseReportPath 'build/release-local/b-licenses-20261015-181705-6ebd8c65/reports/THIRD_PARTY_LICENSES.csv'
   ```

6. **报告并停止**：用局部补丁同步本清单、发布计划、本地结果书、Docker 交接及 Compose 默认候选版本，写明实际报告结果、新候选哈希、证据来源与遗留环境项；这是本次申请的文档批量更新范围，不修改 CI。执行后核对 A2/A3 旧候选完整性、依赖/CI 差异和本轮进程退出，保留全部历史候选及失败记录。许可证到位只解除对应阻碍，其他平台/数据库/Docker/远端验收继续按实际证据判定。

此阶段暂不需要修改现有构建脚本：报告内容（包括许可证原文）放入它们已支持的 CSV 输入，随七个平台归档和 Docker context 一起冻结。若实际发现脚本不满足这一合同，再针对具体补丁说明原因，不提前把其他构建或 CI 工作并入 B 组。

## 授权记录

用户已明确授权：**下载并隔离安装 `github.com/google/go-licenses/v2@v2.0.1` 及其固定工具依赖；按上列命令生成报告、完成新候选本地验收，并局部同步本清单、发布计划、本地结果书、Docker 交接与 Compose 默认候选版本。** 合成 SQLite 写入仅限新建验收目录。既有产品依赖、CI、真实数据库、已有运行实例和远端仓库状态不在操作范围内。

授权门禁依据是用户本会话提供的 AGENTS.md：“必须先说明目标、命令、影响，并获得针对该操作的明确授权：……批量修改……新增 / 升级 / 移除依赖……修改环境配置……数据库变更……”。本次具体确认已完成，不再重复询问。

## 实际许可证报告

- 工具二进制：`tools/bin/go-licenses.exe`，SHA-256 为 `ae230897c2af264315569c41ea894211c302711e5976fa8d2a06dab34ef485fe`；模块版本和工具依赖校验值保存在本轮 `logs/tool-build-info.log`。
- [真实汇总 CSV](../build/release-local/b-licenses-20261015-181705-6ebd8c65/reports/THIRD_PARTY_LICENSES.csv)：460 条归一化记录，包含 114 个 Go 许可证库、345 个 npm 版本条目及 1 条 Go 标准库/运行时记录；npm 中 152 条属于生产依赖图、193 条属于构建/开发依赖图。报告不把开发工具全部视为已进入前端 bundle。
- CSV 为 UTF-8、有明确列名，包含完整许可证/NOTICE 正文及原始材料 SHA-256；Go 多个 classifier 匹配按分号保存，不擅自推断 AND/OR；前端原始表达式（含 `MIT AND ISC`）保留。不能按 go-licenses 原始三列 CSV 解析此汇总；原始三列输出和模板输出另外完整保留。
- 报告为 2,018,791 字节，SHA-256 为 `7282dfd529a5ab2904d5d084b61677a28bc63feeef14f4a8edf43e9944db8b16`。Python 标准 CSV 及 PowerShell Import-Csv 均完成往返解析；862 条本地元数据/正文输入在生成结束前复核，518 条原始材料记录保留。
- 七个平台的实际外部 Go 包全部有许可证覆盖，未知许可证及缺少完整条款均为 0；两个 replace 模块均按实际 fork/版本取材。15 个外部汇编包只引用 Go 汇编头文件，CGO=0 的选定图内无 Cgo/C/Syso 文件；原始告警与审阅清单保留。
- 工具 URL 自动探测被 loopback 代理拒绝，以遵守本会话的 TinyFish 获取约定。原始输出中的 Unknown URL 不改写；汇总对应条目改用明确标注的版本化模块来源引用，并保留缓存中的许可证正文与哈希，不冒充已核对所有上游许可证链接。

来源限制也进入报告 `materials` 列：`react-remove-scroll-bar` 和 `victory-vendor` 的补充上游 LICENSE 以原始 Git blob 哈希固定，但未证明精确发布提交绑定；`keyv` 使用 npm 发布前最后一次 LICENSE 提交中的完整条款，并保留无法精确绑定 release commit 的说明。`natural-compare` 保留版本化 README 的作者署名及作者链接的 MIT 条款，补充文本经过 TinyFish 规范化，其哈希不冒称原始 HTTP 字节哈希。`@humanfs/types` 自身只声明 Apache-2.0，报告明确附的是 SPDX 标准条款；`@thesvg/react` 则额外核对固定上游提交中的 package.json 为 3.3.2。没有用当前分支或另一版本材料悄悄冒充精确版本的包内 LICENSE。

报告识别出编译依赖 `github.com/looplj/axonhub/llm` 的 LGPL-3.0 与 `github.com/go-sql-driver/mysql` 的 MPL-2.0；原文和相关 NOTICE 已纳入，并为 LGPLv3 补齐其引用的 GPLv3 标准条款。报告生成通过表示清单、正文与证据核对通过；正式发布仍须核对这些许可证要求的源码交付、再链接材料和来源限制，不能将工具退出码当作发布批准。

## 新候选与本地验收

候选：`build/release-local/b-final-20261015-181705-6ebd8c65/`，版本 `v0.1.0-local.b-final-20261015-181705-6ebd8c65`，构建时间 `2026-10-15T18:55:43+08:00`。源码基线仍为 `test / 948de764ae9a034fd72899686d24dcc4ecad7fa5`，未提交的产品源码与 A3 一致；本次变化是注入版本/时间和外部许可证输入，未修改产品构建脚本。

| 检查 | 实际结果与证据 |
| --- | --- |
| 七平台离线构建、前端 TypeScript/Vite | 通过；[构建命令及退出码](../build/release-local/b-licenses-20261015-181705-6ebd8c65/candidate-build.json)、冻结候选 `checks/logs/commands.jsonl` |
| 候选文件/源码/工作区哈希 | 976 条文件、487 条冻结源码及 487 条工作区输入全部匹配；[候选核查](../build/release-local/b-licenses-20261015-181705-6ebd8c65/candidate-audit.json) |
| 旧候选保留 | A2 的 969 文件/485 输入、A3 的 973 文件/487 输入仍全部匹配；未向旧目录追加材料 |
| ZIP 合同与许可证 | 7 个 ZIP 的 Unix creator、程序 0100755、其他文件 0100644、路径/重复/符号链接、解压内容与 SHA256SUMS 均通过；7 个 package 与 Docker context 共 8 个报告副本逐字节匹配，ZIP 内再次解析各为 460 条 |
| 真实报告内容 | [校验结果](../build/release-local/b-licenses-20261015-181705-6ebd8c65/reports/license-report-validation.json)、原始 Go CSV/TSV 与前端 JSON、`license-material-index.json`、`license-validation-inputs.json`、来源补充和全部失败记录均保留 |
| 普通/jsoniter Go 回归、五包 race | 按全部 487 源码/依赖输入一致复用 A3 通过日志；[来源与哈希](../build/release-local/b-licenses-20261015-181705-6ebd8c65/regression-provenance.json)。本次没有声称重新运行这三项 |
| 发布条件 vet、冻结前端 ESLint | 新运行通过；B 证据 `go-vet-result.json`、`frontend-eslint-result.json`；stdout/stderr 分别保存 |
| 新 Windows 包 PS 7 / PS 5.1 SQLite | 两者通过；[结果](../build/release-local/b-licenses-20261015-181705-6ebd8c65/sqlite-results.json)。覆盖空库恢复、关联/禁用状态、加密备份、错误口令/损坏导入回滚及重启持久化；使用本轮合成数据，`-PreserveRuntime` 保留证据 |
| 新 Windows ZIP 业务与正常重启 | [三项检查全部通过](../build/release-local/b-licenses-20261015-181705-6ebd8c65/final-acceptance/runtime-acceptance/results.json)：原始归档/binary 哈希和新版本、普通/流式、按序切换、客户端/API 取消、日志/SSE、精确统计、设置/分组持久化和正常退出 |
| 精确业务统计 | 成功 3、失败 2、输入 token 9、输出 token 6；取消上游连接实际关闭，取消未记作渠道故障；见 `business-evidence.json` |
| 生产更新链路与浏览器 | B 没有重做这两类检查；A3 三场景生产调度/helper/重启和 visible 浏览器证据继续保留，源码完全一致。B 独立业务 driver 从现有脚本截取，生成方式及原脚本/副本哈希写入 `business-smoke-provenance.json`，未改产品脚本或伪造浏览器通过 |
| Docker | 4 个 Linux 程序及报告 context 核对通过，状态 `license-report-present; daemon-not-run`；没有 daemon、镜像或 tar，C1–C9 待用户验收 |

B 运行验收全部写入独立证据目录；候选的 `build-info.json` 没有随验收追改，`verification` 留空是因为未使用构建内 verifier。完整核查、回归来源和运行结果应从本报告链接的独立证据读取，不能把候选内清单误当作全部运行验收目录。

| 平台 | 二进制 SHA-256 | ZIP SHA-256 |
| --- | --- | --- |
| Windows amd64 | `36c57490e6bde13e6e927c680b232feb3004c3ada9116da1918b5cc2088b66bc` | `32a4ff52429b3e80a0d744fa92e7aa875c1f04fd46d65f3997ff62fb7a377c94` |
| Linux amd64 | `1b8ff2919b64fa480500ed59e04269b64778320150b4a5f802a7a685de8a752a` | `8f11bdd8b0b41a66ae8962505640ffe035aecbb89ef0b304b7d6d26a5bd197b4` |
| Linux arm64 | `0cf2f0be277239315cbd37ad5a535cfe5c2a4d80e3ca6fead9338c86c38f4cf1` | `db92d8a8ebf7ca11dec25fe790f8667f21cf03dfde1260a7ca2ca47eee48c92b` |
| Linux arm/v7 | `f6d4b37828c23220dae4b0d39df72325816352764df796cc6b8147ad9c17b52c` | `0a1f6cb4f0077dcc63cbf576438c39a4431a2dad48aa33c7360ba2d529f6592e` |
| Linux 386 | `139d6af992defdd1027885bce67ea249198e5ae19871225248693cc1f4fb52dc` | `b0704b5f2168c0e8d2bc98fa1125fffe32081208f66955fdb9d19189196b937d` |
| macOS amd64 | `e5470b4992a54ca5c72c021811f9d7844c5e89e51e34a50e609aeeb1eae46941` | `0ef73eae69c9dbd64376b2e13bc558d0ce872b9a76b0cbb72a223ce945914ddc` |
| macOS arm64 | `173f0cce5ddac8c3b1d9e085fd764a972c02434b84a757743d8df4261e9e9f55` | `dafdb14db66fad2339ca88e4ce4f3af2e0c67765edda78c2ed0d5351b6bc870f` |

Linux/macOS 仍为交叉编译、许可证及归档证据；实际运行/更新、Android/NDK、真实 MySQL/PostgreSQL、Docker daemon 和真实远端下载/两版本在线升级继续保留。A3 验收报告及其原始失败材料不追改。

## 本轮停止位置

B 组工具、报告、新候选及表内本地验收已完成，发布计划、本地结果书、Docker 交接及 Compose 默认版本同步。许可证报告缺失这一阻碍已解除；LGPL/MPL 源码交付/再链接材料和上述来源限制未因生成 CSV 自动解决，L6.5 不自动判为严格通过，也没有批准公开发布。

本轮工具下载与原文补查均按授权范围执行；没有产品依赖变动、CI 修改、真实数据库连接、commit、分支/标签、push、PR、Release 或镜像发布。测试实例使用正常停止路径，已有仓库外 PID 2828 保留；合成 SQLite、工具缓存和失败材料保留，不作为公开附件。最终文档/格式、差异、安全扫描与进程收尾记录在本轮 `final-checks.json`。
