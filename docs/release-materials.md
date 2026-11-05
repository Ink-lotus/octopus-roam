# v0.1.1 材料生产与验收目录入口

本入口只接受 `v0.1.1` / `v0.1.1-rc.N`（N 为无前导零的正整数）、main 历史中的完整小写 40 位 commit SHA 和该 commit 的 Unix 时间戳。先完成全版回归并冻结准确源码，再收集真实来源、审核许可证、验证 Go 1.27.3 复建/LGPL 再链接，生产材料和全部 11 个产品 ZIP、四架构镜像。当前合同测试与文档适配不代表这些真实材料、产物或双平台升级已经验收，也没有创建 v0.1.1 Release。

沿用[既有支持矩阵](support-v0.1.0.md)：正式支持 Windows amd64/SQLite 与 Windows/WSL Containers 3.0.3.0/Linux amd64 镜像/SQLite，其他目标保持原有实验性或范围外状态。产物合同仍要求全部 11 ZIP、四架构镜像、对应源码、编译版本和完整哈希。`INSTALL.md`、源码包 README、Release Notes 须使用一致的支持范围并绑定最终获审查 commit，不能直接改写已封存或公开的 RC 附件。

Release Notes 固定来自该 commit 的 `docs/releases/v0.1.1.md` Git blob。`main.go` 的版本注释、`web/package.json` 的无 `v` 版本、说明首行和所有命令的版本必须一致。脚本保留说明原字节（包括换行和尾部空行），不读工作区替代文件或旧 `plan/` 路径。说明的 `release_notes_path` / `release_notes_sha256` 贯通材料、生产记录、build、artifact 及派生验收目录；RC 转正式版需要新准确 commit、重新审核和重新生成对应输入，不能只重命名附件。

## 1. 准备审核后的输入

在独立目录准备 `inputs.json`、应用源码模板 ZIP、CSV、五份说明、四份 Alpine ZIP 和 `tool-inputs.json`。输入清单每个路径相对于清单所在目录，必须指向目录内的常规文件；每个文件都填写实际 SHA-256。完整输入清单本身也需审核哈希。

```json
{
  "schema": 1,
  "version": "v0.1.1-rc.1",
  "commit": "<完整候选SHA>",
  "source": {"path": "source-template.zip", "sha256": "<实际SHA256>"},
  "license_report": {"path": "THIRD_PARTY_LICENSES.csv", "sha256": "<实际SHA256>"},
  "tool_inputs": {"path": "tool-inputs.json", "sha256": "<实际SHA256>"},
  "documents": {
    "THIRD-PARTY-NOTICES.md": {"path": "THIRD-PARTY-NOTICES.md", "sha256": "<实际SHA256>"},
    "SOURCE-OFFER.md": {"path": "SOURCE-OFFER.md", "sha256": "<实际SHA256>"},
    "source-offer.html": {"path": "source-offer.html", "sha256": "<实际SHA256>"},
    "REBUILD.md": {"path": "REBUILD.md", "sha256": "<实际SHA256>"},
    "INSTALL.md": {"path": "INSTALL.md", "sha256": "<实际SHA256>"}
  },
  "alpine": {
    "amd64": {"path": "alpine/amd64.zip", "sha256": "<实际SHA256>", "base_image": "alpine@sha256:<审核digest>"},
    "arm64": {"path": "alpine/arm64.zip", "sha256": "<实际SHA256>", "base_image": "alpine@sha256:<审核digest>"},
    "arm": {"path": "alpine/arm.zip", "sha256": "<实际SHA256>", "base_image": "alpine@sha256:<审核digest>"},
    "386": {"path": "alpine/386.zip", "sha256": "<实际SHA256>", "base_image": "alpine@sha256:<审核digest>"}
  }
}
```

源码模板遵守 [发布 CI 合同](release-ci.md) 的源码 ZIP 布局和完整 `SHA256SUMS`，包括目标 commit 的 application、实际 Go/npm 对应源码、原始声明、准确 `release-source.json` 和重建说明。application 中除 `static/out/` 外的文件集合及字节必须与准确 checkout 一致，其中 `application/docs/releases/v0.1.1.md` 必须等于该 commit 的说明 blob。`release-source.json` 仍只有 `schema`、`version`、`commit`、`platforms` 四个字段。Release Notes 是 application 的受跟踪输入，不是 `inputs.json.documents` 的第六份独立说明；五份说明、CSV 必须与独立输入一致。模板不能带上一轮的 `material-production.json` 或 `tool-inputs.json`。

模板可以保留历史前端输出供输入审查，但生产器会明确用这次真实生成的 `static/out/` 替换它；不会继承其旧前端哈希。其他目录、来源目录中的说明、重建脚本和来源记录须按准确候选完成审查，生产器不会猜测或自动修订它们。本版上游前置调整过依赖，不能沿用 v0.1.0 的来源和许可证材料作为最终交付。

`tool-inputs.json` 固定包含 `schema: 1` 和 `tools`；tools 必须有下表六项，每项结构为 `{"version":"<准确版本>","source":"<公开HTTPS来源>","sha256":"<审核的工具分发输入SHA256>"}`。来源不能含凭据、签名查询参数或片段。保留官方来源与校验记录，不能仅填写版本字符串。

| tools 键 | version |
| --- | --- |
| go | 1.27.3 |
| node | 24.18.0 |
| pnpm | 11.15.1 |
| python | 3.14.6 |
| go-licenses | v2.0.1 |
| ndk | r28c |

生产器实际检查前四项运行版本，并记录所用 Go、compiler、linker、Node、Python 可执行文件及 Go LICENSE 的 SHA-256。go-licenses 和 NDK 是审核后的许可证生成/重建工具输入；此入口不声称已运行这两个工具。来源归档校验、CSV 与实际依赖的对应审核及 NDK 环境准备分别保留其真实证据。

## 2. 生产、审核和打包

在准确候选 SHA 的干净 checkout 中准备锁定依赖。优先使用与 Ubuntu runner 一致的 Linux amd64 工具环境和 Git 文件字节；如果在 Windows 生产，必须同时满足 checkout 字节和后续 Ubuntu 前端逐字节比较。CRLF、平台依赖或前端输出差异会停止上传，不会自动规范化后放行。

依赖获取与生产分阶段。生产阶段使用已审核 Go 1.27.3，并设置子进程 `GOTOOLCHAIN=local`、`GOPROXY=off`、`GOFLAGS=-mod=readonly`；保留锁文件和校验。下面的变量须先填入已审核实际值，输出路径每次使用新目录：

```powershell
python -B scripts/release_materials.py produce --version $rcVersion --commit $rcCommit --inputs-json $reviewedInputs --inputs-sha $inputsSha --output $newMaterialsRoot
python -B scripts/release_materials.py audit --version $rcVersion --commit $rcCommit --materials $newMaterialsRoot --materials-sha $materialsSha
python -B scripts/release_materials.py pack --version $rcVersion --commit $rcCommit --materials $newMaterialsRoot --materials-sha $materialsSha --output $newBundlePath
```

`produce` 调用仓库中的前端入口，注入准确 `VITE_APP_VERSION`，保留 `static/out/README.md` 原字节，再核对源码未变。它重新生成应用源码 ZIP、完整源码校验和及 `materials.json`。源码 ZIP 新增 `tool-inputs.json` 和 `material-production.json`，后者记录版本、完整 SHA、commit 时间戳、输入清单和各输入哈希、实际工具、生产脚本字节及全部实际前端文件哈希。`materials.json` 和 `material-production.json` 均保持 schema 1，并新增必需的 `release_notes_path` / `release_notes_sha256`；缺字段、旧路径、错误哈希及源码包中替换的说明均拒绝。材料目录文件集合、11 目标矩阵和四份 OS 材料继续遵守原合同。

`audit` 要求准确 checkout、固定实际工具版本、有效材料和生产记录，并将当前实际前端与源码包逐字节比较。`pack` 先执行 audit，再创建保留全部审核字节的外层 ZIP，输出外层 SHA-256。原输入、历史候选和已有输出均不覆盖；生产失败留下的目录保留证据，下次使用新目录。

自动检查不替代许可证审核、实际依赖/replace 核对、Linux 前端工具来源、Go 1.27.3 复建/LGPL 再链接或四架构 APK 库存审核。后续 build 仍检查每个真实程序的模块源码 h1、前端、编译元数据和每份真实镜像的包库存。

## 3. 审核后上传为 Actions artifact

上传流程是 `.github/workflows/release-materials.yaml` 的 `prepare → download → materials`。它只允许本仓库 main 上的手动触发；输入包括准确 version/commit、暂存 draft Release ID、asset ID、外层 bundle SHA-256 和内部 `materials.json` SHA-256。

创建材料暂存 draft Release、上传 bundle 和触发 workflow 前，按具体目标、命令和影响取得该次授权。暂存对象采用 `release-materials-inbox-YYYYMMDD-HHMMSS-xxxxxxxx`，target_commitish 必须是完整候选 SHA；它不是候选版本 Release。附件名必须为 `release-materials-<version>-<full-SHA>.zip`。上传后回读 draft、target、asset ID、size、digest，填入 workflow 参数。GitHub CLI 不能直接把本地目录变成 Actions artifact；这个明确暂存步骤用于传递已审核字节。

示意命令需换成实际 ID/路径并获得该次授权后执行：

```powershell
gh release create $inboxTag --repo Ink-lotus/octopus-roam --draft --target $rcCommit --title $inboxTag --notes-file $reviewedInboxNotes
gh release upload $inboxTag $newBundlePath --repo Ink-lotus/octopus-roam
gh workflow run release-materials.yaml --repo Ink-lotus/octopus-roam --ref main -f version=$rcVersion -f commit=$rcCommit -f source_release_id=$inboxReleaseId -f source_asset_id=$inboxAssetId -f bundle_sha256=$bundleSha -f materials_sha256=$materialsSha
```

下载 job 为读取 draft 使用 `contents: write`，只执行固定目标 GET；其他 job 为 `contents: read`。凭据只发送到 api.github.com，跳转仅接受规定的 GitHub HTTPS 资产主机，签名 URL 不记录。输入 ZIP 先校验外层哈希、大小和严格路径集合，再校验内部材料。材料包中的任何脚本都不执行。

prepare 从准确 commit 解析说明 path/SHA，materials job 通过 `RELEASE_NOTES_PATH` / `RELEASE_NOTES_SHA256` 接收并再次核对。该 job 固定 Go 1.27.3、Node 24.18.0、pnpm 11.15.1、Python 3.14.6，在准确 SHA 重新生成前端并 audit；通过后上传唯一名为 `release-materials` 的 artifact，在 summary 记录 version、commit、说明 path/SHA、材料和 bundle 哈希。中间 bundle artifact 保留 1 天，最终材料保留 14 天。工作流不运行 release workflow，不创建候选 tag、RC Release 或镜像。

结束后必须回下载核对全部哈希，记录 run/attempt、artifact ID、说明 path/SHA、`materials.json` SHA-256 和保留时间。后续 `release.yaml` 的 `materials_run_id` 使用这个成功 run。源码、版本、说明或锁文件变化时保留旧证据，重验受影响范围后重新生成并审核材料、程序和镜像；报告变化不会自动改变冻结版本或 SHA。

## 4. 从 sealed artifact 准备验收目录

下载后保留完整 artifact（包括 bin、archives、materials、images、delivery 和 artifact.json），取得已审核的 artifact.json SHA-256 和材料 SHA-256。准确 checkout 中使用已校验的 Go 1.27.3：

```powershell
python -B scripts/release_acceptance.py prepare --artifact $downloadedArtifact --version $rcVersion --commit $rcCommit --artifact-sha $artifactSha --materials-sha $materialsSha --output $newAcceptanceLayout
python -B scripts/release_acceptance.py verify-layout --candidate $newAcceptanceLayout --version $rcVersion --commit $rcCommit --artifact-sha $artifactSha --materials-sha $materialsSha
```

prepare 先运行完整 verify，再核对源码、生产记录、全部实际程序的 Go/VCS/build ID/依赖来源，才从审核源码 ZIP 提取 application 和复制原始 bin/archives。只写新的 `build/release-local/` 子目录，不启动程序或数据库。派生 build-info.json 使用 schema 2 / role `derived-acceptance-layout`，保留准确版本/commit、原 artifact/material/source/build 哈希、说明 path/SHA、逐目标 Go 版本及完整源码清单；prepare 和 verify-layout 回执也携带说明绑定。它是布局转换记录，不继承任何旧数据库或产品验收结论。公开 `RELEASE-NOTES.md`、`BUILD.json`、`MATERIALS.json` 必须与各自审核原件一致，重新计算外层哈希不能放行替换内容。

两个现有入口均要求新的派生目录携带四项外部审核身份；每次先重新验证原 artifact 与派生字节。原 artifact 必须仍可访问。只读检查数据库驱动输入：

```powershell
pwsh -NoProfile -File scripts/prepare-database-acceptance.ps1 -Candidate $newAcceptanceLayout -OutputRoot $newDriverRoot -ExpectedVersion $rcVersion -ExpectedCommit $rcCommit -ArtifactSha256 $artifactSha -MaterialsSha256 $materialsSha -GoExecutable $reviewedGoExe -ValidateOnly
```

去掉 `-ValidateOnly`、加 `-SkipRehearsal` 可只测试/编译独立驱动；`-ModuleCache` 可指定已验证的现有缓存。源码、数据库业务包、驱动和正常停止 helper 都取自冻结来源。驱动记录实际 Go 版本和原 artifact 绑定；跳过演练明确记为 `not_run`。不带这两个开关会执行隔离 SQLite/产品启停演练，运行前仍需该范围的授权。真实 MySQL/PostgreSQL 仍需后续单独的 `--connect` 调用与实例授权。

产品业务/更新入口在单独授权后使用：

```powershell
node scripts/test-release-acceptance.mjs $newAcceptanceLayout $newRuntimeRoot $rcVersion $rcCommit $artifactSha $materialsSha
```

其中业务场景使用原程序；loopback 更新场景仍从同源源码编译独立 overlay，以合成的 `v0.1.1-rc.1 → v0.1.1-rc.2` 验证更新机制，报告记录这一边界。它不能替代真实正式 `v0.1.0 → 本版候选` 的升级验收。两个正式支持组合须使用同一批准确产物完成真实旧程序升级、持久化恢复及功能联合回归；候选原生更新助手的再次更新和失败恢复单独记录。首次 Windows 手动迁移及回滚损失见 [升级说明](upgrade-r1.md)。MySQL/PostgreSQL、其他平台和环境保持既有支持状态，不由本入口扩展。

`build-local.ps1` 默认生成 `v0.1.1-local.<timestamp>`，旧 `build-info.json` 路径仍保留历史兼容。开发 CI 继续接受其开发版本；未绑定说明或完整材料的本地构建不能封存、通过发布 verify，亦不能携带新 artifact 审核参数冒充新候选。
