# R0 许可证材料与对应源码交付

对应候选：`v0.1.0-local.mysqlfix-20261018-03`，基线为 `test / 948de764ae9a034fd72899686d24dcc4ecad7fa5`，包含冻结时的未提交修复。任务目录：[r0-license-materials-20261019-01](../build/release-local/r0-license-materials-20261019-01/)。本报告补充[发布计划](release-plan-v0.1.0.md)的许可证材料条件，原始数据库结果继续见[两轮本地数据库验收](release-local-database-acceptance-20261018.md)。

本地许可证材料已完成：独立前端及七平台复建、修改 LGPL 库后的完整应用重新链接、源码附件和七个平台归档核对均通过。[交付验证](../build/release-local/r0-license-materials-20261019-01/evidence/delivery-validation.json)与[源码目录交叉核查](../build/release-local/r0-license-materials-20261019-01/evidence/source-bindings-audit.json)保留具体证据。公开源码托管及实际网络入口仍随发布/部署验收，不将本地材料完成视作公开发布批准。

## 源码与声明

- `package/application/` 保留候选的 549 个应用文件，含 500 个冻结输入和实际嵌入的 49 个前端产物。每文件哈希与原始构建参数保存在 `provenance/` 和 `catalog.json`；没有用仅对应 HEAD 的 GitHub 自动源码包替代未提交修复。
- 包内含 110 份实际编译 Go 模块的原始源码 ZIP，源码 ZIP 的 Go `h1` 同时匹配 `go.sum` 与七个程序的构建元数据；另附离线模块图元数据。345 个 npm 版本的实际发布内容与内部链接清单一并提供，其中 152 个版本属于生产依赖图，193 个属于构建/开发依赖图。
- [第三方声明](../build/release-local/r0-license-materials-20261019-01/package/THIRD-PARTY-NOTICES.md)显著列出 LGPLv3 与 MPL-2.0 组件；AGPLv3、LGPLv3/GPLv3、MPL-2.0、Go LICENSE/PATENTS、265 份去重后的原始材料及 518 条材料索引均保留。原 460 条 CSV 不改写，SHA-256 继续为 `7282dfd529a5ab2904d5d084b61677a28bc63feeef14f4a8edf43e9944db8b16`。
- [源码获取说明](../build/release-local/r0-license-materials-20261019-01/package/SOURCE-OFFER.md)、[重建/再链接步骤](../build/release-local/r0-license-materials-20261019-01/package/REBUILD.md)、[安装说明](../build/release-local/r0-license-materials-20261019-01/package/INSTALL.md)和网络源码页面模板已准备。修改记录区分冻结源码与 HEAD，精确字节差异也包含 CRLF/LF 变化；原 AGPL 条款未改变。
- `github.com/looplj/axonhub/llm@v0.0.0-20260923093423-859c3e4ed944` 的完整模块源码、bedrock NOTICE 及 LGPLv3 引用的 GPLv3 条款齐备。接收者可修改库和应用、重新组合/链接并安装，调试库修改所需的逆向工程不受额外禁止，不需要分发者私钥或激活凭据。
- `github.com/go-sql-driver/mysql@v1.10.0` 的准确 Covered Source、MPL-2.0 及原始声明随对应模块 ZIP 提供。

## 前端来源限制的补充处理

补充表：[FRONTEND-SOURCE-BINDINGS.json](../build/release-local/r0-license-materials-20261019-01/package/licenses/FRONTEND-SOURCE-BINDINGS.json)。原始 CSV 和历史抓取失败不追改；补充记录只取代对应的来源限制，原始许可证表达式、版权年份、作者与正文保留。

| 包 | 新证据与保留边界 |
| --- | --- |
| `victory-vendor@37.3.6` | 精确 annotated tag `v37.3.6` 指向 `d9d9ca2d5038d6ef9de91f2cef39e6fb2733baa6`；同提交 package.json 确认版本与 `MIT AND ISC`，原始 MIT 文本及 13 份 vendor 条款保留 |
| `keyv@4.5.4` | 版本变更提交 `e4d608eccdfc094b80f6e9545c5307ee0810af72` 下的 package.json、实现与类型文件均与安装包逐字节一致，许可证来自同一提交 |
| `react-remove-scroll-bar@2.3.8` | npm gitHead 仍无法访问，未声称找回精确 release commit。安装包的四个 ES2015 模块均与发布者明确 MIT 授权提交 `7301c160fda44cb8cf2b9fdfde61efad35736196` 的 TypeScript 源码在转译及格式/注释归一后匹配；保留 2025 原始版权，不伪装成 2024 发布提交。原源码、授权、包内容和可离线复现的比较脚本均随附件提供 |
| `natural-compare@1.4.0` | npm gitHead 的实现仅有 CRLF/LF 差异；准确版本 README 保留 2012–2015 Lauri Rooden 署名和作者 MIT 链接，作者条款的 TinyFish 规范化抓取如实标注，不冒称 HTTP 原始字节 |
| `@humanfs/types@0.15.0` | 精确 npm gitHead 的 package.json 与包内 README 明示 Apache-2.0，保留作者署名及标准条款；没有杜撰缺失的包内 LICENSE 或版权 |

既有 `@thesvg/react@3.3.2` 固定提交绑定证据继续保留。GitHub contents 的 base64 原文同时核对 Git blob SHA-1 和 SHA-256；公开证据通过 TinyFish 获取并随包保存。

## 独立验证

LGPL 演练已通过：[rebuild-modified-01/result.json](../build/release-local/r0-license-materials-20261019-01/rebuild-modified-01/result.json)。演练把库提取到独立目录，新增导出函数，使用临时 `recipient.mod` 的本地 replace 重新编译完整应用，执行库内标记，并核对二进制构建元数据中的替换路径。只运行在正常命令执行前退出的证明入口，没有启动应用服务或数据库；原程序与原库源码未改写。

前端代码对应性也已仅使用附件内的 TypeScript 工具和源码复现：[portable-frontend-comparison.json](../build/release-local/r0-license-materials-20261019-01/evidence/portable-frontend-comparison.json)，四个模块全部匹配。

七平台原程序与前端独立重建使用新的应用副本、模块缓存和编译缓存，`GOPROXY=off`、`GOTOOLCHAIN=local`、`-mod=readonly`、CGO=0，不禁用校验、不下载或升级依赖。参考工具链为 Go 1.27.0、Node.js 24.18.0；包内前端 native 工具对应本次 Windows x64 构建机。原始嵌入资源保留，Go 跨平台复建不依赖 Node.js。

| 验证 | 实际结果 |
| --- | --- |
| 独立前端 TypeScript/Vite | 通过；49 个文件的路径和内容与原始嵌入产物完全一致，差异和新增均为 0 |
| 七平台离线原程序复建 | Windows amd64、Linux amd64/arm64/arm/386、macOS amd64/arm64 全部逐字节匹配；见 [rebuild-original-01/result.json](../build/release-local/r0-license-materials-20261019-01/rebuild-original-01/result.json) |
| LGPL 修改与重新链接 | 完整应用链接本地修改库、执行标记、二进制元数据中的 replace 均通过；没有正常启动服务/数据库 |
| 源码 ZIP | 47,968 个文件全部经过路径、重复、常规文件类型及逐文件内容哈希核对；同时交叉核对 549 个应用文件、110 个模块 ZIP、345 个 npm 包的 46,643 个文件及原始许可证材料目录 |
| 七个平台 ZIP | 路径集合保持、程序 0100755/其他文件 0100644、原程序哈希、原 CSV 哈希、Windows 支持文件全部通过；只改变新归档的两份 README |
| 旧候选和工作区输入 | 原候选 989 项文件及 500 项冻结/工作区输入全部匹配，没有向旧候选追加材料 |

本次执行命令分别为 `assemble-source.py`、`supplement-licenses.py`、`prepare-source.py`，然后在两个独立输出目录运行 `rebuild-source.ps1` 的七目标/前端和 `-DemonstrateModifiedLibrary` 模式，最后执行 `finalize-delivery.py`、`audit-source-bindings.py`。全部位于本任务目录；生成器不升级依赖，详细可复现命令在源码附件 `REBUILD.md`，实际子进程命令和 stdout/stderr 在两个 rebuild 目录的 `logs/`。

## 交付和剩余发布条件

最终[源码 ZIP](../build/release-local/r0-license-materials-20261019-01/delivery/octopus-roam-source-v0.1.0-local.mysqlfix-20261018-03.zip)和七个附加声明后的平台包已生成在本任务 [delivery/](../build/release-local/r0-license-materials-20261019-01/delivery/)。源码 ZIP 为 **315,766,311 字节**（约 301 MiB），SHA-256 为 `3c397bac4a21deecb3b0d6777969679b9f160090335a9c7c5269c436f5ee4514`。最终 [SHA256SUMS](../build/release-local/r0-license-materials-20261019-01/delivery/SHA256SUMS)覆盖源码和七个平台包，`MATERIALS-SHA256SUMS`覆盖声明、页面及交付 manifest。

七个程序保持原 SHA-256，只更新新归档中的两份 README，附入醒目的使用声明、源码获取说明、前端补充证据及 LGPLv3/GPLv3 原文；原 CSV 和 Windows 支持文件不变。这样保持当前更新器固定文件白名单，避免新增文件名导致更新拒绝。新的归档哈希另记在新目录，不覆盖旧候选或其清单。本轮交付应选择这些补齐声明后的归档，并同时提供对应源码附件；原始冻结 ZIP 继续只作为历史证据。

本任务只完成本地许可证材料。公开时必须将准确版本源码 ZIP 和校验清单与程序下载一起提供，并向网络使用者提供显著、免费且可实际下载的源码入口。`source-offer.html` 目前是部署模板，现有 Settings → GitHub 链接不能证明这些未提交修复已经公开。源码上传、真实页面/下载验证和镜像内材料核对仍属于后续获准发布与部署验收。

`docker-license-payload/` 已提供后续 context 构建可用的补充声明和集成说明；它不是镜像或镜像 tar，冻结 Docker context 不变。Docker C1–C9、其他平台实机、Android/NDK、CI 与远端版本间升级继续按发布计划保留，`L6_5_strictly_passed=false`。

本次未修改产品依赖、产品源码、CI 或环境配置，未连接数据库，未操作已有运行实例，未 commit、push、创建 tag/Release 或发布镜像。新材料由任务内脚本生成，历史候选、中间结果和失败记录保留。

最终[私有材料核查](../build/release-local/r0-license-materials-20261019-01/evidence/private-boundary-check.json)在内存中读取两轮本地测试密码及私钥片段，扫描 47,864 个源码/声明/报告文件，命中 0；未输出私有值、路径或指纹。固定公共模块 ZIP 另由 `h1` 和 SHA-256 绑定。公开抓取记录未发现会话、认证或 cookie 元数据字段，两份计划文档的本地链接和行末空白检查通过。

[最终检查](../build/release-local/r0-license-materials-20261019-01/evidence/final-checks.json)通过：8 个源码/平台归档及 3 个声明 sidecar 校验一致，依赖/CI/版本文件差异为 0，`git diff --check` 通过，本轮构建/演练与 release 目录产品进程均为 0，进程路径无法核实项为 0。原有工作区改动和仓库外运行实例保留。

## 后续 Docker 集成（同日独立任务）

用户授权继续镜像交付后，补充声明已在新的 `r0-docker-delivery-20261019-03/` context/image 中集成。299 个材料文件与镜像内容逐项核验；新镜像 tar、未改变的同版本应用源码 ZIP、包装源码及新 SHA256SUMS 已交付，原本任务目录和冻结候选不覆盖。镜像、版本、页面下载/恢复和运行检查见[Docker 交付报告](release-docker-delivery-20261019.md)。此前“镜像内材料待核对”只描述本任务结束时状态。

这次也确认继承的 Alpine 3.24.2 OS 包属于应用 Go/npm 材料之外的分发范围。已输出准确包版本、许可证及 aports commit 清单；当前应用源码 ZIP 不包含这些 OS 包的完整源码。公开分发镜像前还须提供/核验适用的 OS 对应源码、原始声明与实际获取渠道；本地镜像可供测试，不能据此宣布所有公开分发条件已齐备。公开应用源码、显著网络入口和真实下载仍待授权发布后验收。
