# 发布准备工作包 7：v0.1.0-rc.1 发布及真实公开渠道验收

日期：2026-10-25/26。工作目录：`D:\tool\AI\octopus-roam`。目标仓库：`Ink-lotus/octopus-roam`。范围：交接计划 E 以及 F 第 1–2 项。

**结论：工作包 7 完成，`v0.1.0-rc.1` 已公开发布并通过普通用户公开下载、源码获取、匿名 GHCR 拉取和隔离 WSLc 启动/重启/换容器持久化验收。`rc.2` 明确留给工作包 8。**
`release-plan-v0.1.0.md`、`release-rc-handoff-20261021.md` 和 `release-ci-r1-20261020.md` 的 E/F 门禁与当前实现已回读；工作包 3–6 的实际结果见 [工作包 3](release-step-03-result-20261023-01.md)、[工作包 4](release-step-04-result-20261024-01.md)、[工作包 5](release-step-05-result-20261025-01.md) 和 [工作包 6](release-step-06-result-20261025-01.md)。

## 冻结输入、门禁和发布 run

| 项目 | 实际值 |
| --- | --- |
| 版本 | `v0.1.0-rc.1` |
| 冻结 commit | `5ec4dc5905149db03159d9d02c8f76646300d477` |
| 冻结 tree | `ddd0aab81a355bbcc96b395178d2de3bb108113c` |
| `SOURCE_DATE_EPOCH` | `1792752156` |
| 材料 run | `40088771849` |
| `MATERIALS.json` SHA-256 | `65640fab073bc98826a2d4e49f9bad2536d8c5fb3ff6a8a6709627d639b6e2b7` |
| 预发布演练 | 工作包 5 的 `publish=false` 完整 11 目标/NDK 四 ABI/四镜像构建和下载 verify 通过；工作包 6 的 RC 公开前强制验收通过 |
| 实际发布 run | [`40199841094`](https://github.com/Ink-lotus/octopus-roam/actions/runs/40199841094)，`publish=true`，重新执行 prepare/test/build，不复用演练 artifact |
| 环境/部署 | `release` environment 审批已通过；deployment `7314252583`；`prepare/test/build/publish` 全部 success |

实际 run artifact 审核见 [run-artifact-review.json](../build/release-local/step-07-publish-20261025-01/run-artifact-review.json)：artifact ID `12601557963`，API digest `sha256:40dc3e3e5001ab0d8b5096245d1906a3feda846a1f59f33d3610afd606da48c9`，artifact `artifact.json` SHA-256 `4dafa998cf2ecc4eb06663e35a4cf960ebe2df352848e7796e1b32f41254f3a4`，`BUILD.json` SHA-256 `5582167e1d3e943d27c3fa5850fd091509902815687e1ccd0aa5fd7420ca2ac0`。11 个程序实际编译版本均为 `go1.27.3`，`vcs.revision` 为冻结 SHA，`vcs.modified=false`；版本、材料、程序及镜像映射通过。artifact 共 513 个文件（实物字节数 `2762007303`）。四架构实际镜像 tar 和 image ID 为：

| 架构 | image ID | tar SHA-256 |
| --- | --- | --- |
| `linux/amd64` | `sha256:92da6493da0b0de6dba98fa17e8b7829ab4df9ac0de6b06642d1eafa5e45201a` | `af4d4557a527bf93b3fbb63a100221857ad83f0300cb1cc20dc9bc81f65712ed` |
| `linux/arm64` | `sha256:3841a3f7830d2487ac9f575b0fe06dccc996b55a4e7b3f6bb0ba0b8047245ae0` | `b9358dc647a849bf7a1e45f9dfa4f93efee19e104f8f0a5b8b6f5e004b318eaf` |
| `linux/arm/v7` | `sha256:0f0744de7b1d027ead1887daff9f11406ef867a3754341ba27492032eea54d3c` | `f8f02b3b933519af71cf998471aba839e6b0db7a509c55249be16ad5afe51c50` |
| `linux/386` | `sha256:e2d492c520579e78d0a07b81e934dfd13cd3df2d964f532ad62239e6087cf61a` | `a5436cd15ec573b07680627435a3f7291f24342945514b4c393415058da07b02` |

## 真实 Release、tag 和附件

公开 Release：[v0.1.0-rc.1](https://github.com/Ink-lotus/octopus-roam/releases/tag/v0.1.0-rc.1)，Release ID `423488386`，tag ref `refs/tags/v0.1.0-rc.1`，tag object SHA `5ec4dc5905149db03159d9d02c8f76646300d477`，`draft=false`，`prerelease=true`，27/27 附件状态为 `uploaded`。公开回读保存在 [release-public.json](../build/release-local/step-07-publish-20261025-01/public-channel-01/release-public.json) 和 [tag-ref-public.json](../build/release-local/step-07-publish-20261025-01/public-channel-01/tag-ref-public.json)。

普通用户无 `Authorization` header 下载结果：27/27 附件 HTTP 200，GitHub API digest、大小和下载 SHA-256 全部匹配；公开 `SHA256SUMS` 的 26/26 项通过；11 个核心材料与本 run `delivery/` 逐字节匹配。完整逐项证据见 [public-hash-comparison.json](../build/release-local/step-07-publish-20261025-01/public-channel-01/public-hash-comparison.json) 和 [download-summary-retry.json](../build/release-local/step-07-publish-20261025-01/public-channel-01/download-summary-retry.json)。

| 附件 | 字节数 | 公开/下载 SHA-256 |
| --- | ---: | --- |
| `BUILD.json` | 1685 | `5582167e1d3e943d27c3fa5850fd091509902815687e1ccd0aa5fd7420ca2ac0` |
| `IMAGE-MANIFEST.json` | 19381 | `f7f56bf725778e5649d9a5669786994d87d2a1932f0176e5584d0f1f88e49d89` |
| `INSTALL.md` | 1729 | `37cfb3082efd4c029de0debc6b2cd49cc0c5338b0f52f54bf67109df79bce588` |
| `MATERIALS.json` | 1923 | `65640fab073bc98826a2d4e49f9bad2536d8c5fb3ff6a8a6709627d639b6e2b7` |
| `octopus-roam-alpine-source-386-v0.1.0-rc.1.zip` | 60314353 | `6e5900b6aa0fc5369b9edd2ee1e660dcc655fb39207fd2c3ef473c744f03ba64` |
| `octopus-roam-alpine-source-amd64-v0.1.0-rc.1.zip` | 60314381 | `0fd0472d6c2022ab175819a02587914a0868c3521bc1c0d57b3358bfd00ed9d9` |
| `octopus-roam-alpine-source-arm-v0.1.0-rc.1.zip` | 60313773 | `639acd26e377ae774a258dbb7d7fe16950ee97d5bfe6bdfe8fa731aa23ec6084` |
| `octopus-roam-alpine-source-arm64-v0.1.0-rc.1.zip` | 60313794 | `398b60e4cf2e37e81df9c1a7e110037775e623868cbc42bd66d38e311f888e89` |
| `octopus-roam-android-386.zip` | 24041692 | `50746fdf7c91e570e9749b2e43cd9dd87f194f5df7b9d3aead28e8ffd5933865` |
| `octopus-roam-android-amd64.zip` | 23409086 | `467a98e61430c1c516c814f75bcc3f13aa2c4da69aa008ef30a14b548512a288` |
| `octopus-roam-android-arm.zip` | 22902820 | `61c7c89874512d7c3922338ff957641871cce2ca214e0a4890f68cebbf5ad190` |
| `octopus-roam-android-arm64.zip` | 22112651 | `a4e165e016d834494ee705e926ddac4ab81a0f223baa4d03e9cce2e70f9c5b62` |
| `octopus-roam-darwin-amd64.zip` | 23218047 | `c9f874d412fc9d489c4304ad9124230229a2a47dee35ba8b37fdb8c0427fd41c` |
| `octopus-roam-darwin-arm64.zip` | 22040066 | `23387f41f4e02bee675235164a088350fbe08b6639a4d592d403618642940c00` |
| `octopus-roam-linux-386.zip` | 21985391 | `e8ce959998d7547300064229b897503c8800acc9a575da99b18f332dbe50bb51` |
| `octopus-roam-linux-amd64.zip` | 22925172 | `ced42314fcde67cf12f7542746b260835b7265b2efd905614aff46d4412129f3` |
| `octopus-roam-linux-arm.zip` | 21965905 | `521df0968dde0a49937dc5b9008e3b02143207134d5ae7fc9221268bb78316b2` |
| `octopus-roam-linux-arm64.zip` | 21360554 | `9bdddc2ee3bb81754c52044ca431c9667af1e483b484de83129814491dc5bfd7` |
| `octopus-roam-source-v0.1.0-rc.1.zip` | 345187137 | `e25b26a4f20f086ade4fcec939db7d66219ffb3fe07b48b824cb650793710995` |
| `octopus-roam-windows-amd64.zip` | 23154296 | `19739b687b0dd28a9dabf62d2b184f78c431027d0e4e319d6a10375fe4b4678f` |
| `REBUILD.md` | 7084 | `92248ebec2ffde39437f8e0b88f594cb61684f87d18dff76a1d80f8c1a1d3b09` |
| `RELEASE-NOTES.md` | 5384 | `b2518d862b7dcaa8ad221ab8fa3203b0b28609af00a6773f770a7f0f59e54d90` |
| `SHA256SUMS` | 2434 | `2ee78f126bef0642c1b53e7346725e0c9d94146f5776752166657f3aacfc12e1` |
| `source-offer.html` | 1815 | `472150962377ce325c849e6ef13d74e495f082b74676086883638cc570d31c39` |
| `SOURCE-OFFER.md` | 1821 | `d30297de5b3da3e7597d1767effcd6c8058886b78e81314510449ed2dc932789` |
| `THIRD-PARTY-NOTICES.md` | 2501 | `b353a9bc9936a70e544cf750e706370674e0bb8b98987447870cd130b0e9bac7` |
| `THIRD_PARTY_LICENSES.csv` | 2120406 | `4f8e3ea855859526f7b2614d10756c133d33f926e7f869674ed196a5fffac350` |

Release source ZIP 可读，`release-source.json` 的版本/SHA 匹配；tag tarball/zipball 也均匿名 HTTP 200、可读，顶层目录包含 `5ec4dc5`。证据见 [source-results-retry.json](../build/release-local/step-07-publish-20261025-01/public-channel-01/source-results-retry.json)。

`/releases/latest` 公共查询返回 `page_not_found`；工作流使用 `--latest=false`，因此 RC 没有更新 `latest`。

## GHCR 公开 manifest、匿名拉取和镜像身份

匿名 registry token 获取成功，查询和拉取均未使用仓库凭据或 `Authorization` header。多平台 RC manifest 的 media type 为 `application/vnd.docker.distribution.manifest.list.v2+json`，index digest 为 `sha256:a33ee63e846fffb01ac1d2ff3e12e42db6018ffee6769429a6dfef8043905fb4`。完整回读见 [ghcr-manifest-public.json](../build/release-local/step-07-publish-20261025-01/public-channel-01/ghcr-manifest-public.json)；平台解析见 [ghcr-platforms-public.json](../build/release-local/step-07-publish-20261025-01/public-channel-01/ghcr-platforms-public.json)。

| 平台 | manifest digest |
| --- | --- |
| `linux/amd64` | `sha256:c0244a01dbad3bd7cc3ea17a3a82827ba3d8ee1d90939756154bc6a7dce284fc` |
| `linux/arm64` | `sha256:b167cab5436d00642fd7c1d459b5768f2b78f39c3c6338d6101bc152ac160bdb` |
| `linux/arm/v7` | `sha256:653fa2dc1a24442fb61fba6fbdf537b739650e756457e382b6bb2f5be61af025` |
| `linux/386` | `sha256:051582cb4ea2a2ac579562b6c5ef37527faec8f319e4463d0e7542d0787d1439` |

架构标签回读：`v0.1.0-rc.1-amd64`=`sha256:c0244a01dbad3bd7cc3ea17a3a82827ba3d8ee1d90939756154bc6a7dce284fc`，`v0.1.0-rc.1-arm64`=`sha256:b167cab5436d00642fd7c1d459b5768f2b78f39c3c6338d6101bc152ac160bdb`，`v0.1.0-rc.1-arm`=`sha256:653fa2dc1a24442fb61fba6fbdf537b739650e756457e382b6bb2f5be61af025`，`v0.1.0-rc.1-386`=`sha256:051582cb4ea2a2ac579562b6c5ef37527faec8f319e4463d0e7542d0787d1439`。`latest` manifest 查询 HTTP 404 (`MANIFEST_UNKNOWN`)。

按公开无凭据路径执行 `wslc pull ghcr.io/ink-lotus/octopus-roam:v0.1.0-rc.1`，退出码 0，返回相同 index digest；本地 manifest inspect 的 image ID 为 `sha256:92da6493da0b0de6dba98fa17e8b7829ab4df9ac0de6b06642d1eafa5e45201a`，OCI labels 为版本 `v0.1.0-rc.1`、revision `5ec4dc5905149db03159d9d02c8f76646300d477`。日志和结果见 [image-pull-01](../build/release-local/step-07-publish-20261025-01/public-channel-01/image-pull-01/)。

## 公开渠道 WSLc 启动冒烟

在管理员 PowerShell 7、WSLc `3.0.3.0` 中使用公开 tag、全新数据目录 `build/release-local/step-07-publish-20261025-01/public-channel-01/container-smoke-02/run-data` 和端口 `127.0.0.1:18128`/`18129` 完成隔离验收。证据目录为 [container-smoke-02](../build/release-local/step-07-publish-20261025-01/public-channel-01/container-smoke-02/)，汇总为 [public-channel-summary.json](../build/release-local/step-07-publish-20261025-01/public-channel-01/public-channel-summary.json)。

| 场景 | 结果 | 证据
| --- | --- | ---
| 主容器公开 tag 启动、根页 HTTP 200、版本 | 通过，`v0.1.0-rc.1` | `primary-check.json`、`container-running-inspect.json` |
| 合成设置/渠道/分组/API key、加密备份 | 通过；设置持久化值 `17`，备份 4634 bytes | `primary-check.json`、`backup.encrypted.json` |
| 正常停止、`wslc start` 重启、重新验证 | 通过 | `api-after-restart.log`、`verify-check.json`、inspect 文件 |
| 同一数据目录换容器启动、重新验证 | 通过；端口 `18129` | `api-after-recreate.log`、`container-recreate-*.json` |
| 容器退出码、端口、日志 | 两容器退出码 `0`；端口已释放；runtime error 匹配数 `0` | `public-channel-summary.json`、日志文件 |

首个公开容器尝试使用了错误的 harness 证据路径，产品 API 写入完成后仅在读取 `proof/run-data` 阶段退出 1；现场位于 [container-smoke-01](../build/release-local/step-07-publish-20261025-01/public-channel-01/container-smoke-01/)，已正常停止且未删除。随后使用全新目录/端口的 `container-smoke-02` 完成修正验收，未把 harness 问题记为产品失败。

## 网络、认证和部分发布处置

Windows `curl.exe`/`Invoke-WebRequest` 的 Schannel `SEC_E_NO_CREDENTIALS`、首次源码 API 的错误 `Accept: application/octet-stream`（HTTP 415）以及受限权限 WSLc 的 `ERROR_FILE_NOT_FOUND` 均保留为失败证据；改用 Python/OpenSSL、普通 GitHub JSON Accept、管理员 WSLc 后成功。认证/网络错误没有被当作对象不存在。用户终端触发 run 时曾出现 CLI 认证/请求参数错误（包括 `401` 与 `environment_id` 422）；最终实际 run `40199841094` 成功完成并使用了 `release` environment 审批。

发布链条最终完整：tag、prerelease Release、27 个附件和四架构 GHCR manifest 均已创建并回读；没有发现需要恢复的部分发布。不删除、覆盖公开资产，不强推，不改写 Git 历史；所有容器、镜像、数据和失败现场均保留。

## 交接

工作包 7 到此结束于 `rc.1` 公开及真实渠道验收。`rc.2` 的版本、材料、构建、发布授权、公开下载/校验、替换、重启、运行版本确认和失败恢复另行留给工作包 8；本报告不执行 `rc.2`。本报告创建后只做内容和 Git 状态只读核对，不 commit、不 push。
