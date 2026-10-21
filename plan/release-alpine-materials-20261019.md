# Alpine 本地源码/声明材料与 R1 交接

本轮已补齐当前 Linux amd64 镜像继承的 Alpine 本地材料。**本地材料交接完成，下个会话可以推进 R1 的 CI 准备。** 其他平台实机、公开源码入口和 CI/远端验收按后续阶段处理，不作为进入 R1 本地准备前必须全部通过的条件。

交付目录：[r0-alpine-materials-20261019-01/delivery](../build/release-local/r0-alpine-materials-20261019-01/delivery/)。使用说明见 [README](../build/release-local/r0-alpine-materials-20261019-01/delivery/README.md)，机器清单见 [ALPINE-MATERIALS.json](../build/release-local/r0-alpine-materials-20261019-01/delivery/ALPINE-MATERIALS.json)。

| 项目 | 结果 |
| --- | --- |
| 对应镜像 | `octopus-roam:v0.1.0-local.mysqlfix-20261018-03-licenses.1`，Linux amd64 |
| 镜像 ID | `sha256:1f51a67ec361cc97369ed3169ddd33600b78e621be23dbb5a12428a0ed922c96` |
| OS 源码附件 | `octopus-roam-alpine-source-linux-amd64-licenses.1.zip` |
| 附件大小 | 60,243,936 字节，约 57.5 MiB |
| 附件 SHA-256 | `328873c234cfa925e165fad42410630c966848d3a830024659e1f0a82ecdedde` |
| 覆盖 | 18 个实际安装包、11 个源码 origin、139 个准确 aports Git blob |
| 原始源文件 | 8 个源码归档、2 个外部原始文本；126 项 APKBUILD SHA-512 输入全部匹配 |
| 声明 | 23 项原始声明/源码头索引；保留原版权、作者、条款及准确来源 |
| 离线核验 | 185 个附件文件、8 个源码归档内 12,076 个常规文件均校验通过 |

## 材料范围与旧清单纠正

从已导出镜像 tar 的实际 `/lib/apk/db/installed` 读取原始数据库，先核验 tar、配置 ID 和各层摘要；没有启动镜像或数据库。清单使用大小写敏感解析。旧 PowerShell 普通 hashtable 和 `-match` 混淆了 `P`/`p`、`A`/`a`：14 个包名被别名覆盖、18 个架构字段被文件权限覆盖。版本、origin 和 aports commit 没有变化。旧镜像/清单保留，准确清单与原始数据库、纠正明细随新附件提供。

准确 origin 包括 alpine-base、alpine-baselayout、alpine-keys、apk-tools、busybox、ca-certificates、musl、openssl、pax-utils、su-exec、zlib。按镜像记录的 commit 获取整个相关配方目录，保留 APKBUILD、补丁、BusyBox 配置及 ssl_client.c、musl 工具源码、安装/触发脚本和 Git 文件模式；源码归档逐项匹配准确 APKBUILD 的 SHA-512。alpine-base 的 release 文件由 APKBUILD 生成，不另造源码归档；alpine-keys 的动态 source 所需公共密钥均已包含并校验。

原始 LICENSE、COPYING、AUTHORS、COPYRIGHT 及源码中的许可/版权头保留原字节。ca-certificates 的上游归档没有独立 COPYING：附件保留完整 certdata.txt、c_rehash.c、mk-ca-bundle.pl 等原始文件，并显式补入其引用的完整 MPL、标准 MIT 模板及 curl 项目 COPYING。补充条款记录准确来源、Git blob/hash，并明确不是从归档找回的缺失原件；不伪造版权、不把 maintainer 当作版权人。源码归档中的其他组件条款也完整保留。

## 验证与影响

先在 package 目录执行独立离线校验，再从实际交付 ZIP 解压到全新 `offline-extract-01/`，重新执行包内 `verify.py`，并对照原始 APK 头字段、交付清单及旧候选。证据见 [delivery-verification.json](../build/release-local/r0-alpine-materials-20261019-01/evidence/delivery-verification.json) 和 [offline-package-verification.json](../build/release-local/r0-alpine-materials-20261019-01/evidence/offline-package-verification.json)。源码附件的构建/安装说明及 `stage-aport.py` 保留准确符号链接/可执行模式恢复方式，标准库校验入口不需要联网或额外依赖。

本轮完成源码完整性、声明保留和交付可用性验证，没有重新编译 Alpine OS 或宣称 OS 二进制可逐字节复现。此附件与原镜像 tar、原应用源码 ZIP 和镜像包装源码配套分发；不是将源码压入镜像后产生的新镜像。原程序、原镜像、原应用源码和历史候选保持冻结，已有运行/数据库证据继续适用。未来基础镜像或 apk 输入变化时，必须基于新镜像实际包数据库重新生成材料，不能沿用本附件冒充对应源码。

检索及 API/配方/声明抓取使用 TinyFish；用户明确授权针对固定清单的 10 个原始源文件改用 `Invoke-WebRequest`，范围和命令见 [APPROVAL.md](../build/release-local/r0-alpine-materials-20261019-01/APPROVAL.md)。受限 shell 的 SSL 连接失败记录保留，随后在扩展工具权限下以相同脚本下载，保持 TLS 和 SHA-512 检查。没有安装/升级依赖、修改环境配置或 CI、启动产品/数据库、改动远端状态或执行 commit。

## 下一会话从 R1 接续

1. 阅读本报告、主发布计划与 `release-workflow-proposal.md`，复核当前工作区和已验收候选的输入是否变化。材料和程序输入未变时无需机械重建、重跑已通过的数据库/容器验收。
2. 准备 `.github/workflows/build.yaml`、`.github/workflows/release.yaml` 及必要 `scripts/build.sh` 的具体补丁；按 AGENTS.md 对 CI 修改的要求取得针对性授权后应用并本地验证。普通 push/PR 只测试；发布固定同一 commit，遵循 `prepare → test → build → publish`，测试/构建失败不得创建 tag。渠道已确定仅 GHCR。
3. CI 包装必须使用正确的大小写敏感 Alpine 清单解析，并将对应源码、原始声明、应用源码和校验材料纳入构建/交付检查；构建输入变化时更新对应材料。检查 YAML/actionlint、版本/commit 注入、artifact 与失败门禁，工具缺失按 R1 范围处理。
4. R1 核实远端条件；R2 集成 main 并验证普通 CI；R3 发布 RC、源码附件和镜像；R4 验证真实下载/拉取及两个 RC 之间的在线升级。公开源码页面模板在本地已准备，实际上线/下载随发布验收。
5. 其他平台在 R0 登记/准备环境，随后对相应候选补实机验收；Android 仍缺 NDK 构建与运行证据，不能把交叉编译写成实机通过。正式发布前落实已声明平台的验收范围。

“本地材料完成”“可进入 R1 本地准备”和“满足公开发布条件”分别记录。历史 `L6_5_strictly_passed=false` 不应再被用作把 R3/R4 验收提前到 R1 之前的总开关。本轮只收口材料与交接，公开托管/显著网络源码入口、线上 Actions、GHCR/Release、其他平台和真实在线升级仍按各阶段待验。
