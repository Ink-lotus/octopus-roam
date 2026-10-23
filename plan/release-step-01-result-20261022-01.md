# 发布准备工作包 1：Go 1.27.3 与产物门禁本地验收

日期：2026-10-22。**工作包 1（交接 A0 第 1–4 项）本地验收通过。A0 整体、R2 整体及 RC 发布条件仍未完成。**

本轮基线及结束 HEAD 均为 `main / 1af3ec18aac751eac668ace4331ce8544dbf461f`。三份计划文档已包含在该提交中且尚未推送；其中旧的“文档未提交”“HEAD b949271”表述按历史记录理解。本轮保留初始五份未提交版本输入，并在其上应用获准补丁。没有暂存、commit、push、远端设置或 Ubuntu Actions 操作；当前测试绑定的是 HEAD 加明确未提交输入，而不是 HEAD 原树单独通过。

证据目录：[本轮证据](../build/release-local/step-01-go1273-20261022-01/)，隔离验收总结果：[result.json](../build/release-local/step-01-go1273-20261022-01/validation-04/result.json)。工具获取及九文件补丁/环境/验收/报告收尾分别获得明确授权，见 [工具授权](../build/release-local/step-01-go1273-20261022-01/evidence/toolchain-authorization.json)、[补丁授权](../build/release-local/step-01-go1273-20261022-01/evidence/patch-authorization.json)及[审阅说明](../build/release-local/step-01-go1273-20261022-01/review/APPROVAL.md)。缺失的九个锁定模块获取及 26 个既存本地模块元数据复制分别补充获得[模块获取授权](../build/release-local/step-01-go1273-20261022-01/evidence/module-cache-authorization.json)和[元数据复制授权](../build/release-local/step-01-go1273-20261022-01/evidence/transitive-metadata-authorization.json)。遵守用户提供的 AGENTS.md，使用 PowerShell 7，检索/文本抓取使用 tinyfish，没有派发子代理。

## 工具来源与实际版本

Go 官方索引 <https://go.dev/dl/?mode=json> 经 tinyfish 获取，精确版本记录保存在 [官方元数据](../build/release-local/step-01-go1273-20261022-01/evidence/go-official-release.json)。获准从 <https://dl.google.com/go/go1.27.3.windows-amd64.zip> 下载，禁止重定向，保留 TLS 检查；归档 **79,010,309 字节**，SHA-256 **`f26081f438319757f8a1b46d7d0b2c5ec7583d4038e485e0aa725cfde7885fff`**，与官方索引完全一致。

本轮解压目录为 `build/release-local/step-01-go1273-20261022-01/tools/go1.27.3-windows-amd64/go`。`go version` 返回 `go version go1.27.3 windows/amd64`；compiler 与 linker 均为 `go1.27.3`。归档先校验再解压，VERSION、LICENSE、go.exe、compile.exe、link.exe 哈希及实际 GOROOT/GOTOOLCHAIN 见 [工具核验](../build/release-local/step-01-go1273-20261022-01/evidence/go-toolchain.json)。系统 `D:/tool/Go` 保持 Go 1.27.0，未修改持久 PATH、Go 配置或系统工具。

首次受限进程下载发生 TLS 认证失败且未产生归档；扩展权限第一次自动审批超时，按工具明确允许的一次重试执行相同已授权脚本成功。未关闭 TLS/校验，没有改变 URL/hash，见 [获取过程](../build/release-local/step-01-go1273-20261022-01/evidence/go-acquisition-attempts.json)。

[官方 GO-2026-6809](../build/release-local/step-01-go1273-20261022-01/evidence/go-official-vulnerability.json)列出的修复版本包括 Go 1.27.3；已校验归档内的 crypto/x509 源码包含对应拒绝逻辑，见 [源码核对](../build/release-local/step-01-go1273-20261022-01/evidence/go-x509-source-observation.json)。这属于来源/版本证据，不代替最终程序的 TLS 或上游专项回归。

辅助工具：PowerShell 7.6.7、Python 3.14.6、Node.js 24.18.0、pnpm 11.15.1、GCC 16.2.0、Git Bash 5.3.15、TypeScript 6.0.3、Vite 8.2.2。actionlint 复用固定 1.7.7，其可执行文件 SHA-256 为 `6d470a52039a433bccdf57bd65170885a5b23e511155d0de587c2c3ce0eb1c24`；它自身由 Go 1.23.4 构建，仅用于本地 workflow 静态检查。完整版本/路径见 [辅助工具](../build/release-local/step-01-go1273-20261022-01/evidence/supporting-tools.json)和 actionlint 日志。

## 输入与补丁

初始 441 个已跟踪文件及五份未提交输入的原始字节均已记录：[initial-state.json](../build/release-local/step-01-go1273-20261022-01/evidence/initial-state.json)、[初始差异](../build/release-local/step-01-go1273-20261022-01/evidence/initial-inputs.patch)。五份输入如下：

| 初始未提交文件 | SHA-256 |
| --- | --- |
| .github/workflows/build.yaml | `ae706f491c391c69579994d3bd6b7393bd98d1f417e92c129d731b9e11f5a3b7` |
| .github/workflows/release.yaml | `a76ec7739452de1012c74c966c8a5881f8ff4441b39393c44ec8a8d3a1407d72` |
| README.md | `5fee94c90f65491a7f19a418600340f9ded73dd80508019592337f326fed27a4` |
| README_zh.md | `a37979fc9b0058a66b36d932f4f5cf9f8275f33639747e422e7e6383e1c3c60e` |
| go.mod | `aa5c0b65f8c8f06c7e801d8a63e712975e54dbe2728ac8697d49affe0b73b0b4` |

本次九文件补丁：[go1273-gates.patch](../build/release-local/step-01-go1273-20261022-01/review/go1273-gates.patch)，SHA-256 `9d0d570cb4952cfc5f71e088cb0bfd3e8b1b04a950f09c74ade1ac5b899c3f65`。应用前逐文件哈希/补丁检查通过，应用后与候选原始字节完全相同，原换行保留；[应用记录](../build/release-local/step-01-go1273-20261022-01/evidence/patch-application.json)与 [manifest](../build/release-local/step-01-go1273-20261022-01/review/patch-manifest.json)提供前后对照。

| 本次文件 | 应用后 SHA-256 |
| --- | --- |
| scripts/release_contract.py | `136a8f5b59da4c6e2d7dd0c49c0c881254c45de8f131c8c96f2b28e77ff7216f` |
| scripts/tests/test_release_contract.py | `2d681522843d0f0f2cebe3d88779cd0077cc102bc529808224bcab4b1494c9c6` |
| scripts/ci-test.sh | `d30a863e62d884a9cce05bf3bc1697def0126bf9d4358d356db14c0616cbc0ff` |
| scripts/build.sh | `9cb273231c8b2d59e1db234b3126ca8fced8a1e04030d3139c6928c3dd39f0cf` |
| .github/workflows/build.yaml | `e89ac3af91ccb8fed17310bb78ab888f02cd3d956ebc280613a7b75a1ccbffb7` |
| .github/workflows/release.yaml | `1f8a68f44fbc88d3d3baecffd3932cb77f1013fef3a4e5f37f55bd9969728ca6` |
| docs/release-ci.md | `0df98507d510b8e3158e0a3384afc17e9c4fe66a0c2133d433342ffafa963f39` |
| plan/release-rc-handoff-20261021.md | `bd714e2383216ccd2bb645aab7f0cc97f34a905ab2643456958531a58186855f` |
| scripts/tests/test_release_go_metadata.py | `9c2af489520145d0a1eaa12e59c81bf0c1e1f0e2d71caaca7ec7527f0a0326e5` |

package、seal、verify 现在均要求检查工具为 Go 1.27.3，从实际程序元数据校验 `go1.27.3`、GOOS/GOARCH 与 build ID。build.json / BUILD.json 改为 schema 2，新增逐目标 go_versions；原 binaries 的 SHA-256 值类型保留。seal/verify 要求 bin 哈希、ZIP 程序字节及版本记录一致，四镜像继续关联已检查的 Linux 程序实际字节；公开 BUILD.json、交付 ZIP 和公开校验清单也重新比较。旧 schema、缺失或虚假元数据/记录一律拒绝。

两个 workflow 都记录实际 Go；publish 显式 setup-go 1.27.3/cache=false，核验阶段保持本地工具选择和离线约束。build.sh、ci-test.sh、测试入口与 CI 文档同步。materials.json、release-source.json、artifact.json 仍各为 schema 1，详见[消费链审查](../build/release-local/step-01-go1273-20261022-01/evidence/callers-audit.json)。build-local.ps1 / prepare-database-acceptance.ps1 的历史 build-info.json 冻结布局不消费 build.json；新 Actions artifact 的材料/数据库驱动接入仍按交接 B.4 显式适配。

## 本地检查结果

验收使用 **442 项准确源码输入**的隔离副本，包含本次新增测试及原未提交修改；[source-inputs.json](../build/release-local/step-01-go1273-20261022-01/validation-04/source-inputs.json) SHA-256 为 `37973aa0c75170f8eae30ab7581059743adc80fe0a56e57ad2bac55ee0e6a9a6`。依赖独立复制 47,052 个文件、1,360 个链接，链接指向副本内部，仅重映射三份副本路径元数据；后续轮次复用 validation-01/source，并再次检查源码、路径元数据和链接，未重复复制依赖树。

依赖获取与离线验收分阶段执行。额外获准从 proxy.golang.org（含 Google Storage 的 HTTPS ZIP 重定向）及 sum.golang.org 获取九个精确锁定模块，禁 direct/VCS 回退，源码与 go.mod h1 均匹配原 go.sum；增补 36 个缓存文件，见[获取结果](../build/release-local/step-01-go1273-20261022-01/locked-modules-01/result.json)。后续从本机既有缓存复制 26 个已锁定的 .mod 元数据，共 2,775 字节，逐项核对 SHA-256/h1，未联网，见[复制结果](../build/release-local/step-01-go1273-20261022-01/transitive-metadata-01/result.json)。go.mod、go.sum、前端锁文件和依赖版本均未改，未安装或升级依赖版本。

Go 测试/构建使用 GOTOOLCHAIN=local、GOPROXY=off、GOFLAGS=-mod=readonly、启用的 sum.golang.org 校验数据库和现有模块缓存；编译缓存、临时输出、GOPATH 与 fixture 位于本轮新目录。pnpm verifyDepsBeforeRun=error，保留依赖/锁文件检查；HTTP(S) 代理限制为关闭的 loopback 端口，允许 loopback fixture。环境仅作用于子进程。

| 检查 | 退出码 | 用时 | 证据 |
| --- | --- | --- | --- |
| go-environment | 0 | 0.032 s | [日志](../build/release-local/step-01-go1273-20261022-01/validation-04/logs/go-environment.log) / [命令](../build/release-local/step-01-go1273-20261022-01/validation-04/logs/go-environment.json) |
| go-mod-verify | 0 | 48.647 s | [日志](../build/release-local/step-01-go1273-20261022-01/validation-04/logs/go-mod-verify.log) / [命令](../build/release-local/step-01-go1273-20261022-01/validation-04/logs/go-mod-verify.json) |
| actionlint-version | 0 | 0.021 s | [日志](../build/release-local/step-01-go1273-20261022-01/validation-04/logs/actionlint-version.log) / [命令](../build/release-local/step-01-go1273-20261022-01/validation-04/logs/actionlint-version.json) |
| actionlint | 0 | 0.126 s | [日志](../build/release-local/step-01-go1273-20261022-01/validation-04/logs/actionlint.log) / [命令](../build/release-local/step-01-go1273-20261022-01/validation-04/logs/actionlint.json) |
| frontend-contract | 0 | 1.019 s | [日志](../build/release-local/step-01-go1273-20261022-01/validation-04/logs/frontend-contract.log) / [命令](../build/release-local/step-01-go1273-20261022-01/validation-04/logs/frontend-contract.json) |
| old-go-fixture-build | 0 | 19.014 s | [日志](../build/release-local/step-01-go1273-20261022-01/validation-04/logs/old-go-fixture-build.log) / [命令](../build/release-local/step-01-go1273-20261022-01/validation-04/logs/old-go-fixture-build.json) |
| old-go-binary-rejected | 0 | 0.282 s | [日志](../build/release-local/step-01-go1273-20261022-01/validation-04/logs/old-go-binary-rejected.log) / [命令](../build/release-local/step-01-go1273-20261022-01/validation-04/logs/old-go-binary-rejected.json) |
| ci-entry | 0 | 454.258 s | [日志](../build/release-local/step-01-go1273-20261022-01/validation-04/logs/ci-entry.log) / [命令](../build/release-local/step-01-go1273-20261022-01/validation-04/logs/ci-entry.json) |
| go-mod-verify-after | 0 | 3.08 s | [日志](../build/release-local/step-01-go1273-20261022-01/validation-04/logs/go-mod-verify-after.log) / [命令](../build/release-local/step-01-go1273-20261022-01/validation-04/logs/go-mod-verify-after.json) |

真实 ci-test.sh 入口完成前端 lint、TypeScript/Vite、Go 全量 default/jsoniter、两组 vet、关键 race（conf、db/...、op、update、server/handlers）、三份 Bash 语法及 **41 项发布合同/元数据测试**。另行前端 VM 合同为 **17 项**。两个 workflow 均通过固定 actionlint；候选试应用和 Python/Bash 语法预检也通过。

新增拒绝路径在 package/seal/verify 各覆盖 11 个目标的旧元数据，并覆盖缺失/非精确版本、错误或缺失检查工具、记录/实物/公开副本不一致。真实小型 Go 1.27.3 程序验证 PE/ELF/Mach-O 读取；另用真实 Go 1.27.0 编译的小程序确认被新读取器拒绝。11 目标的发布材料及四镜像合同 fixture 是合成数据，不是完整产品发布构建。

三个实际小程序的 SHA-256、完整 go version -m 输出、build ID 和旧程序拒绝结果见[实际二进制元数据](../build/release-local/step-01-go1273-20261022-01/evidence/real-binary-metadata.json)，目标分别为 windows-amd64、linux-arm64、darwin-amd64。

## 失败轮次与恢复

| 轮次 | 实际结果与处理 |
| --- | --- |
| [validation-01](../build/release-local/step-01-go1273-20261022-01/validation-01/result.json) | 源码/前端依赖副本建立并校验完成。Windows CreateProcess 仍按父进程 PATH 找到 Go 1.27.0，离线校验按版本要求拒绝。该旧结果中 toolchain 字段是驱动预置的计划值；真实版本以该轮 go-environment.log 的 go1.27.0 为准，不能将其算作通过。 |
| [validation-02](../build/release-local/step-01-go1273-20261022-01/validation-02/result.json) | 驱动改为按已授权子进程 PATH 显式解析可执行文件绝对路径，实际 GOVERSION 已为 go1.27.3；go mod verify 因九个已锁定模块缓存缺失而停止。没有跳过检查。 |
| [validation-03](../build/release-local/step-01-go1273-20261022-01/validation-03/result.json) | 九个锁定模块获取后，go mod verify 继续发现传递依赖的旧版本 .mod 元数据缺失；审查 Go 依赖图后从本机缓存补齐 26 个 h1 相符的文件。 |
| [validation-independent-01](../build/release-local/step-01-go1273-20261022-01/validation-independent-01/result.json) | 等待本地元数据复制授权期间，沿用已授权环境独立完成 17 项前端合同和 41 项发布测试；只代表该子集通过。 |
| [validation-04](../build/release-local/step-01-go1273-20261022-01/validation-04/result.json) | 缓存补齐后使用真实 Go 1.27.3，规定的完整入口及前后 go mod verify 全部通过；本报告的结论取自此轮。 |

前三轮日志和源副本完整保留；驱动修复见[恢复说明](../build/release-local/step-01-go1273-20261022-01/evidence/validation-recovery.json)。诊断期间一次对系统 Go 的缓存路径查询意外触发其默认自动工具链选择，在目录创建处被文件权限拒绝，未生成归档或工具链目录；见[诊断记录](../build/release-local/step-01-go1273-20261022-01/evidence/system-go-query-observation.json)。后续继续使用任务内 Go 的绝对路径和 local/off 子进程环境，没有关闭 TLS、h1、锁文件或版本检查。

验收前后准确源码和锁文件、static/out/README.md 原始字节、活动源码、原依赖的三份元数据及 1,360 个链接均无变化。暂存区为空，Git index 原始字节未变。最终未提交清单/全部输入哈希和 Git 差异见 [final-state.json](../build/release-local/step-01-go1273-20261022-01/evidence/final-state.json)、[最终已跟踪差异](../build/release-local/step-01-go1273-20261022-01/evidence/final-worktree.patch)；新增文件由最终清单和原件记录。证据索引为 [SHA256SUMS](../build/release-local/step-01-go1273-20261022-01/evidence/SHA256SUMS)，覆盖审阅文件、证据、各轮日志/结果/输入清单和获取/恢复记录。源码副本哈希见 source-inputs.json；依赖文件复制时逐字节校验，dependencies.json 保存文件计数、链接和路径元数据记录。大型副本与可重新生成的编译缓存不逐文件列入该索引；工具 ZIP、任务内 Go 的核验文件及真实小程序列入索引。本结果报告在测试完成后新增，属于验收说明，不属于被编译源码。

## 限制与下一步

- 本轮是 Windows amd64 + Git Bash 的本地验收，不是新 SHA 的 Ubuntu Actions 通过；旧 b949271 / run 39620815636 只代表 Go 1.27.0 历史基线。
- 未构建实际 11 个产品 ZIP、Android/NDK 或四架构容器，也未制作/上传最终 release-materials、重新链接 LGPL、生成发布 artifact；完整真实矩阵留给工作包 5，材料入口和最终候选绑定按后续工作包完成。
- 未启动真实产品/数据库、操作生产环境或执行最终程序 TLS、浏览器/进程升级、真实下载/拉取与 RC 间升级验收；这些条件继续按交接计划推进。
- 工作包 2 审查本轮未提交输入与提交划分，获准后提交推送/远端配置并获取新 SHA 的 Go 1.27.3 Ubuntu CI。没有因为本轮本地通过而把 A0 第 5 项、A0 整体或 R2 整体标记完成。
