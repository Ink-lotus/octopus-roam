# 发布准备工作包 4：准确材料、复建与上传验收

日期：2026-10-24。范围：[RC 交接计划](release-rc-handoff-20261021.md) C。工作目录：`D:\tool\AI\octopus-roam`。

**工作包 4 完成。** 对应冻结候选的应用、Go/npm、四架构 Alpine 源码与声明已审核；Go 1.27.3 七目标复建、独立构建比对、LGPL 修改后再链接、本地材料往返、Actions 材料工作流及回下载验收全部通过。工作包 5 可消费下面的准确材料 run。完整汇总见 [work-package-04-result.json](../build/release-local/step-04-materials-20261023-01/evidence/work-package-04-result.json)。

| 固定输入 | 准确值 |
| --- | --- |
| 仓库 / 版本 | `Ink-lotus/octopus-roam` / `v0.1.0-rc.1` |
| 完整候选 SHA | **`5ec4dc5905149db03159d9d02c8f76646300d477`** |
| 提交树 | `ddd0aab81a355bbcc96b395178d2de3bb108113c` |
| Commit UTC / SOURCE_DATE_EPOCH | `2026-10-23T10:42:36Z` / `1792752156` |
| 普通 main CI | [39941450556](https://github.com/Ink-lotus/octopus-roam/actions/runs/39941450556)，attempt 1，success，同一完整 SHA |
| 材料 workflow / run / attempt | `393883230` / [40088771849](https://github.com/Ink-lotus/octopus-roam/actions/runs/40088771849) / **1** |
| 最终 artifact | **`release-materials`**，ID **`12557912716`** |
| 本地材料 / 成功回下载 | `build/release-local/step-04-materials-20261023-01/materials-01/` / `artifact-download-02/materials/` |

材料身份与传输哈希如下。两个 ZIP 的外层封装不同，各自均核对对应哈希；最终证明包含全部 12 个内部材料文件原字节一致。

| 审核对象 | SHA-256 | 字节数 |
| --- | --- | ---: |
| `materials.json` | **`65640fab073bc98826a2d4e49f9bad2536d8c5fb3ff6a8a6709627d639b6e2b7`** | 见清单 |
| 上传 bundle | `2278e055aecd5342e1beeaac2c6b83f4303c33dc81869ec9619fa6709e5957c4` | 588,582,305 |
| 下载的 Actions ZIP | `de4dc8e7973af822f33080a921810dd17fa89358e031f12544a56e685ff6bfee` | 588,582,257 |
| 最终应用源码 ZIP | `e25b26a4f20f086ade4fcec939db7d66219ffb3fe07b48b824cb650793710995` | 345,187,137 |
| 审核输入 `inputs-04/inputs.json` | `0d777ac6e01da78c44c6465667cd7d561b5f782183c7df86f2f3c31571cc4908` | 见文件 |
| 原应用源码模板 ZIP | `b9850397fb13aff0bf14cb2e16d461468414b0adf04f40c93bbea7fea8567f99` | 345,182,225 |

[审核输入记录](../build/release-local/step-04-materials-20261023-01/evidence/reviewed-inputs-01.json)核对 374 个应用文件的准确 Git blob、47,932 个模板文件、110 个 Go 模块、345 个 npm 包及四架构材料。Go 的 11 目标闭包与模块校验和、冻结 `go.sum` 对应。npm 来源中 341 个沿用相同字节，4 个换为本次 Linux 平台的准确 native 包。最终源码中的既有清单条目字节保持一致，新增 `tool-inputs.json`、`material-production.json`，并重建 `SHA256SUMS`。

[源码与声明复核](../build/release-local/step-04-materials-20261023-01/evidence/source-review-resumed-01.json)覆盖 460 行许可证记录：114 行 Go、1 行 Go 标准库、345 行 npm；573 条原始声明引用、301 份不同原始声明文件以及完整 AGPL/LGPL/GPL/MPL 文本。Go 1.27.3 官方完整源码与 39 份原始声明随材料提供。历史与当前 `go.mod` 只改变 directive：`1.26.4` → `1.27.3`；历史实际编译器为 Go 1.27.0，两个版本含义分别记录。

go-licenses v2.0.1 的分类结果依据未变的准确来源复核，目标集合在新工具链下重算；本轮没有重新执行 classifier。[工具源码再校验](../build/release-local/step-04-materials-20261023-01/evidence/go-licenses-source-reverified.json)确认实际缓存 ZIP 与工具清单 SHA-256 一致。应用及依赖来源的边界审核通过，归档不含本地数据库、运行配置或编译缓存。

生产和本地审核使用 Linux amd64 的 Go **1.27.3**、Node **24.18.0**、pnpm **11.15.1**、Python **3.14.6**；Actions 日志也核对了四项实际运行版本。完整公开来源与分发输入校验和见 [tool-inputs.json](../build/release-local/step-04-materials-20261023-01/inputs-04/tool-inputs.json)。其中 Go Linux 分发输入 SHA-256 为 `65efbc80fdc1fa2cd2631cad5b481777a68e8725f34e89f50fc6ac714e379b40`。NDK r28c 已列入审核工具输入，Android 产品构建留给工作包 5。

四架构固定使用 Alpine 3.24.3，基础镜像索引为 `alpine@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a`。每架构绑定真实库存的 18 个包、11 个 origin，包含 141 个准确 aports 文件、8 个上游源码归档和 23 个原始声明索引；[四架构来源审核](../build/release-local/step-04-materials-20261023-01/evidence/alpine-source-01.json)全部通过。amd64/386 库存来自 `alpine-inputs-03`，arm64/arm 来自 `alpine-inputs-06`。

| Alpine 材料 | SHA-256 |
| --- | --- |
| `alpine/amd64.zip` | `0fd0472d6c2022ab175819a02587914a0868c3521bc1c0d57b3358bfd00ed9d9` |
| `alpine/arm64.zip` | `398b60e4cf2e37e81df9c1a7e110037775e623868cbc42bd66d38e311f888e89` |
| `alpine/arm.zip` | `639acd26e377ae774a258dbb7d7fe16950ee97d5bfe6bdfe8fa731aa23ec6084` |
| `alpine/386.zip` | `6e5900b6aa0fc5369b9edd2ee1e660dcc655fb39207fd2c3ef473c744f03ba64` |

应用复建及 LGPL 验收总结果为 [rebuild-checks-04/result.json](../build/release-local/step-04-materials-20261023-01/evidence/rebuild-checks-04/result.json)。

| 验收 | 实际结果与证据 |
| --- | --- |
| 七目标源码复建 | linux-amd64、linux-arm64、linux-arm、linux-386、windows-amd64、darwin-arm64、darwin-amd64 全部通过；逐个复核 Go 1.27.3、build ID、依赖与程序哈希，见 [standard/result.json](../build/release-local/step-04-materials-20261023-01/evidence/rebuild-checks-04/standard/result.json)。 |
| 前端 | 50 个文件与审核材料原字节一致，含 gzip 文件原字节；TypeScript 检查通过。 |
| 独立复建 | 全新模块与编译缓存重新编译 Linux amd64，与标准程序原字节一致，见 [independent/result.json](../build/release-local/step-04-materials-20261023-01/evidence/rebuild-checks-04/independent/result.json)。 |
| LGPL 再链接 | recipient 副本修改 LGPL 库，经独立 `recipient.mod` 重新链接完整应用并执行修改标记，见 [lgpl/result.json](../build/release-local/step-04-materials-20261023-01/evidence/rebuild-checks-04/lgpl/result.json)及同目录 `recipient-changes/`。 |
| 本地材料往返 | `produce → audit → pack → unpack → audit` 五条命令均退出 0，12 个材料文件原字节一致，见 [production-01/result.json](../build/release-local/step-04-materials-20261023-01/evidence/production-01/result.json)。 |
| Actions 材料工作流 | `prepare`、`download`、`materials` 三个 job 全部 success；准确 checkout 重建前端并通过 audit，见 [jobs.json](../build/release-local/step-04-materials-20261023-01/evidence/artifact-verification-02/jobs.json)。 |
| artifact 回下载 | API digest、准确文件集合、全部 12 文件哈希、源码生产记录及实际工具日志全部通过，见 [artifact-verification-02/result.json](../build/release-local/step-04-materials-20261023-01/evidence/artifact-verification-02/result.json)。 |
| 下载后 Linux audit | 在冻结 checkout 执行已核对 Git blob 的本地脚本；50 个前端文件及全部材料再次通过，退出 0，见 [downloaded-linux-audit-01/result.json](../build/release-local/step-04-materials-20261023-01/evidence/downloaded-linux-audit-01/result.json)。 |

LGPL 标记为 `octopus-roam-go1273-recipient-lgpl-relinked`，证明程序 SHA-256 为 `e5357fd4d7147713f4f74eccc0b359aed7e26b542ee8bdf978666e84b2ef8098`。以上程序用于源码附件复建，使用 `-buildvcs=false`；没有启动正常产品、价格任务或数据库。完整 11 个产品 ZIP、Android/NDK 与四镜像实物映射仍由工作包 5 的产品构建验证。

复建恢复保留了各轮真实状态：第 02 轮在 linux-386 失败；恢复临时环境后的第 03 轮六目标成功、darwin-amd64 失败，错误日志没有明确诊断。当时观察到 `/tmp` 所在约 8 GiB 文件系统仅余 322,457,600 字节，不能据此声称已证明具体 errno。第 04 轮改用 D 盘新目录，保留并核对第 03 轮六个成功目标，只新编译剩余 darwin-amd64，再复核全部七程序元数据。独立比较和 LGPL 检查随后均通过。[组合来源记录](../build/release-local/step-04-materials-20261023-01/evidence/rebuild-checks-04/preserved-successful-work.json)明确记录这一边界，第 03 轮整体仍保留为失败。

用户在审核准确文件、哈希、命令及影响后，明确答复“批准 U：上传材料并生成 artifact”。[上传操作单](../build/release-local/step-04-materials-20261023-01/review/upload-01/APPROVAL-U.md)与[授权记录](../build/release-local/step-04-materials-20261023-01/evidence/authorization-u.json)绑定操作计划 SHA-256 `ae723b559fce6e0a11d7e449249ef5281cd32436dcfeb3da2467073f02674815`，执行脚本 SHA-256 为 `680f07f712ffea01b91d894cae3f1155608a67763d23d449c14d906cb8c55496`。

实际执行 `python -B build/release-local/step-04-materials-20261023-01/review/stage-reviewed-materials.py --execute`：先复核冻结 main、成功 CI、审核文件和同名对象，再创建 draft，上传唯一 bundle，并用刚核对的 Release/asset ID 和固定哈希 dispatch 材料 workflow。[远端执行记录](../build/release-local/step-04-materials-20261023-01/evidence/remote-stage-01/result.json)及相邻请求文件保留完整参数。

| 上传对象 / 保留期限 | 实际值 |
| --- | --- |
| draft inbox | `release-materials-inbox-20261024-102003-2278e055` |
| Release ID / bundle asset ID | `422672701` / `672010369` |
| 材料 run | `40088771849`，attempt 1，`workflow_dispatch`，main / 固定候选 SHA |
| 最终 artifact 创建 UTC | `2026-10-24T11:12:35Z` |
| 最终 artifact 到期 UTC | **`2026-11-07T11:12:29Z`**，即北京时间 **2026-11-07 19:12:29** |
| 实际创建至到期 | 1,209,594 秒；workflow 配置为 14 天 |
| 中间 artifact | ID `12558187448`，创建 `2026-10-24T11:10:37Z`，到期 `2026-10-25T11:10:30Z`；配置为 1 天 |

期限来自 [artifacts.json](../build/release-local/step-04-materials-20261023-01/evidence/artifact-verification-02/artifacts.json) 的真实 API 回读。工作包 5 应在最终材料到期前消费该 run，启动前再次检查可用性。准确 draft 附件继续保留。

下载首轮返回 HTTP 401，120 字节响应和[失败记录](../build/release-local/step-04-materials-20261023-01/evidence/artifact-verification-01/failure.json)均保留；之后一次 artifact 列表预检 GET 失败，尚未创建新下载目录。目标 GET 恢复后，在 `artifact-download-02` 完成认证下载和全部校验。下载目录编号 02 不改变 Actions attempt 1。恢复期间没有重新上传或重复 dispatch。最终 draft 在受限执行上下文返回 404，随后目标限定的认证回读成功，未把 404 当作对象不存在。

完整 workflow 日志 SHA-256 为 `8d763aa2092f9d8f61c98aab842acd884a2db978ff3e884e43f203ce7bdbf6d6`，保存在回下载验收证据目录。最后一次 Linux audit 于 `2026-10-24T13:24:45.811755+00:00` 完成，stdout SHA-256 为 `7453ee94d4d9b485c06140b96ec92e62e3e397584c5440cb958259dc29b9f14c`，stderr 为空。

[最终远端回读](../build/release-local/step-04-materials-20261023-01/evidence/final-remote-readback-01.json)确认：受保护 main 仍为冻结 SHA；inbox 保持 draft、published_at 为 null，唯一附件大小及 digest 一致；tag 与公开 Release 列表为空，`release.yaml` 的运行数为 0。本包没有公开 RC，也没有推送程序或镜像。

工具、临时环境、真实库存及源码收集按本任务已有的 T/T2、A/A2、Q/Q2/Q2R、S/S2 具体授权执行，操作单与授权文件保存在本任务 `review/`、`evidence/`。ARM 的[第 06 轮恢复记录](../build/release-local/step-04-materials-20261023-01/evidence/worker-arm-06/result.json)确认临时 binfmt 处理器已注销、挂载已释放、原有条目字节一致。最后一次 audit 后写入任务停止标记，[Linux 保活进程退出记录](../build/release-local/step-04-materials-20261023-01/evidence/linux-runtime-keeper-02-ended.json)确认于 `2026-10-24T13:58:37.594548+00:00` 正常退出；旧失败结果、缓存和 D 盘成果保留。全程未派发子代理。

[本地收尾核验](../build/release-local/step-04-materials-20261023-01/evidence/local-final.json)复查 450 个冻结文件的原字节、HEAD/main/tree、空暂存区及 `git diff --check`。工作包 3 原报告保持原字节，本报告新建并保留未提交；本轮未新增 commit 或 push。只有两份阶段报告未跟踪，产品源码、依赖和 CI 保持冻结状态。

[SHA256SUMS](../build/release-local/step-04-materials-20261023-01/evidence/SHA256SUMS)覆盖本任务审阅文件、证据、最终输入、材料、本地往返、上传 bundle、两次下载目录和本报告；路径相对仓库根目录。大型来源副本与编译缓存使用已有清单及哈希记录，未逐项加入此索引。索引自身与校验收据不纳入自身，校验结果保存在相邻 `index-verification.json`。

工作包 5 的固定消费输入为：

```powershell
$rcVersion = 'v0.1.0-rc.1'
$rcCommit = '5ec4dc5905149db03159d9d02c8f76646300d477'
$materialsRun = '40088771849'
$materialsSha = '65640fab073bc98826a2d4e49f9bad2536d8c5fb3ff6a8a6709627d639b6e2b7'
```

按交接 D 准备具体 `release.yaml`、`publish=false` 演练操作，取得该次 dispatch 授权后完成 Go 1.27.3 的 11 个产品 ZIP、NDK r28c 四 ABI、四架构镜像及 artifact 上传/下载/verify；随后使用实际最终产物补 TLS、最新 C1/C2 和可用主平台验收。材料里的源码获取页面、语言和本地链接已核对，公开附件安排已提供；公开入口的真实免费可获取性须在 RC 发布后验证。工作包 4 的完成结论限于准确材料及其复建、传输与审核，不代替这些产品和公开渠道验收。
