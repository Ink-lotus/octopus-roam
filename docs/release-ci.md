# 手动发布 CI 合同

普通 push / pull_request 只运行测试。release workflow 仅接受 main 上的手动触发，`publish` 默认 false；即便开启，也必须经过 `prepare → test → build → publish`。publish 使用 `release` environment，其远端保护规则需另行核实和授权配置，文件本身不会替仓库创建审批规则。

版本合同目前仅覆盖首发的 `v0.1.0-rc.N` 和 `v0.1.0`，N 从 1 开始。输入 commit 必须是 main 历史中的完整 40 位 SHA；main.go 的版本注释和 Release Notes 标题必须精确匹配。当前上游版本注释、草稿说明或已经存在的 tag / Release / 版本镜像都会阻止发布。后续版本线需要单独审查更新合同。

## 对应材料输入

build 从本仓库指定的 Actions run 下载唯一名为 `release-materials` 的 artifact。手动触发同时填写审核后的 `materials.json` SHA-256；材料中的版本、commit、完整平台矩阵和所有文件哈希必须一致。材料准备和上传是独立的 R3 工作：这份 workflow 不会将旧候选材料改名后继续发布，也不会自动执行材料包内的脚本。

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

应用源码 ZIP 延续 R0 对应源码包布局：`application/`、`go-cache/cache/download/`、`npm-packages/`、`licenses/`、重建/安装/源码获取说明及 `SHA256SUMS`。另加 `release-source.json`，内容必须精确为 `schema`、`version`、`commit`、`platforms` 四项，分别对应以上值。SHA256SUMS 必须覆盖除自身外的全部常规文件。保留完整原始声明，包括 AGPLv3、LGPLv3、GPLv3、MPL-2.0 原文；顶层五份说明及 `licenses/THIRD_PARTY_LICENSES.csv` 必须与材料目录的副本逐字节一致。

`application/` 必须包含目标 commit 中 `.github`、cmd、internal、scripts、static、web 下的已跟踪文件，以及 main.go、go.mod、go.sum、LICENSE、两份 README、Compose 和 .dockerignore；static/out 以本次实际生成内容为准。不得包含 node_modules、测试 profile、私钥、数据库、日志或工具缓存。新编译程序的实际依赖（含 replace 后的模块）必须有对应模块 ZIP，模块内容 h1 同时匹配实际二进制元数据和对应源码中的 go.sum。源码包内仍需准确的前端来源记录、原始声明和重建工具材料；自动哈希检查不代替材料审核或 LGPL 重新链接演练。

四份 Alpine ZIP 延续 R0 包格式：ALPINE-PACKAGES.json、FILE-SHA256SUMS.json、SOURCE-BINDINGS.json、GITMODES.json、SOURCE-ARCHIVE-FILES.json、NOTICE-INDEX.json、准确 aports/源文件/原始声明及重建说明。逐项验证文件、Git blob、APKBUILD SHA-512 输入和源归档内文件。清单字段区分大小写，实际镜像的 P/V/A/o/c/L 必须与相应平台材料一致。最终 IMAGE-MANIFEST.json 另将本次实际镜像 ID、镜像 tar 哈希、原始包数据库哈希与 OS 源码 ZIP 绑定；不会把历史镜像 ID 当作本次镜像 ID。

## 构建、artifact 和发布

测试使用 Go 全量/default/jsoniter、vet、关键 race、前端 lint/TypeScript/Vite、发布脚本测试及 actionlint。依赖在 setup 阶段按锁文件获取，测试和编译阶段使用 GOPROXY=off、GOTOOLCHAIN=local、-mod=readonly，保留校验。构建不运行联网价格脚本。

版本显式注入 Go ldflags、可跨平台读取的 Go build ID 和 VITE_APP_VERSION。build.json 记录版本、SHA、平台和程序哈希。Vite 构建前缓存 static/out/README.md 原始字节并在结束时恢复，随后检查其他已跟踪源码/锁文件无变化。

发布矩阵是七个标准目标加四个 Android/NDK 目标，共 11 个 ZIP；`--standard-only` 只用于明确的本地子集验证，seal/verify 拒绝把它当作完整发布。ZIP 内文件名保留更新器白名单，补充声明和源码入口附入两份 README，程序模式 0100755、其他文件 0100644。

build 创建明确的 docker-context，通过独立的 scripts/dockerfile/Release.Dockerfile 使用审查过的基础镜像 digest，构建四份 Docker tar，不登录发布仓库、不推送镜像。校验 tar 配置/层摘要、平台、标签、实际程序、CSV、声明、entrypoint 和实际 APK 数据库；整个流程不启动产品或数据库。将 build、context、images、materials、delivery 全部封装成含版本和完整 SHA 的单一 Actions artifact，另记录完整文件清单和哈希。

publish 只下载本次 run 的确切 artifact，重新核对所有文件和对应源码/实际镜像映射，然后核实远端 tag、Release 和版本镜像均不存在。认证失败、网络错误或已有对象均停止。门禁通过后，才创建指向指定 SHA 的新 tag，加载并推送已验证的四份镜像 tar，组合 GHCR 多平台版本标签，并使用审核后的说明创建 Release。RC 只发布版本及架构标签；稳定版才更新 latest。唯一镜像渠道为 GHCR。

Release 附件是 delivery/ 的全部文件：11 个程序 ZIP、准确应用源码、四份 OS 源码、声明/源码说明、BUILD.json、MATERIALS.json、IMAGE-MANIFEST.json、Release Notes 和统一 SHA256SUMS。内部 Docker tar/context 不作为公开附件。公开下载、显著网络源码入口及真实拉取/升级需在 R3/R4 实际验收。

发布涉及多个服务，无法原子完成；tag 创建后发生发布错误可能留下部分对象。工作流不自动删除、不覆盖、不强推；保留失败证据后另行审查恢复方案。

## 工具版本和本地验证边界

runner 为 ubuntu-24.04；Go 1.27.0、Node.js 24.18.0、pnpm 11.15.1、Python 3.14.6、NDK r28c。前三项沿用当前已验收本地工具，Python 采用本地实际版本；替换原来的 Node latest、go.mod 推导和 pnpm 11.10.0。actionlint 固定 v1.7.7，安装在 runner 临时目录；不修改产品依赖。材料 CSV 在独立材料阶段使用固定 go-licenses v2.0.1 和锁定 pnpm 生成，并按实际目标审查，不以一份非空 CSV 代替源码/声明。

checkout/setup-node/setup-go/setup-python/pnpm/setup-ndk/Docker actions 保留现有 major 引用；新增 upload-artifact v4.6.2、download-artifact v4.3.0。major 引用不是不可变 SHA。标签可用性、仓库实际权限和 environment 保护规则仍需 R1 远端只读核实；如要改为审查后的 action commit SHA，需补齐来源证据后再修改，不猜测 SHA。

本地 YAML 解析与结构/门禁测试不等于 actionlint 或 GitHub Actions 通过。NDK 构建、多架构镜像、artifact 上传/下载、GHCR 首次认证/权限都需要各自环境证据；失败时补齐实际问题，不降低门禁。
