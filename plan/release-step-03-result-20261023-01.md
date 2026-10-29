# 发布准备工作包 3：材料入口、验收适配与 RC 冻结

日期：2026-10-23。工作目录：`D:\tool\AI\octopus-roam`。范围：[RC 交接计划](release-rc-handoff-20261021.md) B。

**工作包 3 完成。** 补充产物门禁、新版普通 CI 与仓库配置已复核；材料生产/上传入口、验收驱动适配及 RC 版本说明已进入 main。最终候选的工作分支与 main 普通 Ubuntu CI 均通过。下一步进入工作包 4，固定消费下面的版本和完整 SHA。

| 冻结项 | 准确值 |
| --- | --- |
| 版本 | `v0.1.0-rc.1` |
| 完整候选 SHA | **`5ec4dc5905149db03159d9d02c8f76646300d477`** |
| Commit timestamp | `2026-10-23T18:42:36+08:00`，即 `2026-10-23T10:42:36Z` |
| SOURCE_DATE_EPOCH | **`1792752156`** |
| 提交树 | `ddd0aab81a355bbcc96b395178d2de3bb108113c` |
| 父提交 | `ac44e88cb558dd43db463a54b7b8a0666bf2d3cf` |
| 最终 commit summary | `修复发布材料的 gzip 前端布局校验` |
| 冻结时间 | `2026-10-23T10:50:27.435707+00:00` |
| 本地及远端 | 本地 HEAD / main、origin/main、远端 main / 默认 HEAD、第二个工作分支均指向候选 SHA |
| 保留的 master | `4a54b59e2f489c92070cb1cec5ed12cd0bb72e22` |
| 输入规模 | 提交树 450 文件；本工作包累计涉及 15 个不同文件 |

权威身份记录为 [rc-freeze.json](../build/release-local/step-03-rc-freeze-20261023-01/evidence/rc-freeze.json)，精确 refs 见 [final-remote-refs-02.json](../build/release-local/step-03-rc-freeze-20261023-01/evidence/final-remote-refs-02.json)。本报告在冻结后新建并保留未提交；报告新增或更新不自动改变候选。后续不得把移动后的 main、旧 `f857486…` 或 `ac44e88c…` 自动作为发布输入。

已读取交接计划、[发布 CI 合同](../docs/release-ci.md)、[主计划 R2–R5](release-plan-v0.1.0.md)及[工作包 1](release-step-01-result-20261022-01.md)、[工作包 2](release-step-02-result-20261023-01.md)实际报告。接手基线为 `f857486093a185669fa0996451949563ade37e9e`，仅工作包 2 报告未提交。前两包证据索引分别复核 177、109 项，443 项原输入及 Go 核验文件全部匹配，旧 CI 日志哈希也匹配，见 [local-preflight.json](../build/release-local/step-03-rc-freeze-20261023-01/evidence/local-preflight.json)。旧计划中的待办表述保留原时点，当前完成状态以本报告及新证据为准。

| 已收口内容 | 实现及实际边界 |
| --- | --- |
| 补充产物门禁 | 沿用工作包 1 的 package/seal/verify：实际 Go 1.27.3 元数据、GOOS/GOARCH/build ID、11 目标、schema 2 的 build.json/BUILD.json、程序及交付 ZIP 字节、四镜像程序映射继续检查；真实 PE/ELF/Mach-O 读取和拒绝路径在本次 85 项套件中再次通过。 |
| 材料生产 | `scripts/release_materials.py` 提供 produce/audit/pack/resolve/download/unpack。绑定审核输入清单、精确版本/完整 SHA/commit 时间戳、固定工具输入及实际工具文件、生产脚本、重新生成的实际前端和完整材料哈希；源码包新增 tool-inputs.json、material-production.json。 |
| 上传入口 | `.github/workflows/release-materials.yaml` 实现 main 手动 prepare → download → materials，从本仓库精确 draft 暂存附件读取已审核 bundle，在精确 checkout 重建前端并 audit，才上传唯一 `release-materials` artifact。已注册 active，workflow ID **393883230**。上传入口通过静态/合同验证，实际 draft、上传和 dispatch 留给工作包 4。 |
| gzip 实物适配 | F 使前端及源码生产记录同时接受 index.html / index.html.gz 和 JS / JS.gz，仍以实际文件原字节计算 SHA-256。真实 Vite 的 deleteOriginalAssets=true 布局已完成 50 文件材料往返核对；相同解压内容但 gzip 字节变化仍拒绝旧审核。 |
| 新 artifact 验收布局 | `scripts/release_acceptance.py` 先完整 verify，再核对生产记录、原程序及对应源码，从审核源码包提取 application 并复制原始 bin/archives；生成 schema 2、role 为 `derived-acceptance-layout` 的 build-info.json，保留原 artifact/material/source/build 哈希、逐目标 Go 版本和完整冻结源码清单。 |
| 现有验收入口 | `prepare-database-acceptance.ps1`、`test-release-acceptance.mjs` 对新布局要求外部审核的 version/commit/artifact SHA/materials SHA，每次重新验证原 artifact 和派生字节。数据库驱动从冻结源码构建，支持 `-ValidateOnly`、`-SkipRehearsal` 和精确 Go 路径；旧布局保留历史兼容，不能携带新 artifact 身份冒充新候选。 |
| RC 版本及文档 | main.go 为 `// Version v0.1.0-rc.1`，发布说明标题准确；正文区分历史真实两库/容器结果与本 RC 待验项。两份 README 的下载、镜像及 Compose 示例使用准确 rc.1，并注明公开后可用；候选不更新 latest。 |

实现用法和输入 schema 见 [材料与验收入口](../docs/release-materials.md)。材料源码 ZIP 内的脚本不作为上传 job 的执行输入；draft 读取 job 为访问 draft 使用 `contents: write`，实际只执行受约束的 GET，其他 job 使用 read。中间 bundle artifact 保留 1 天，最终材料 artifact 保留 14 天。这些是已实现合同，实际传输、来源/许可证审核和重建结论仍须工作包 4 取得。

本地与远端验证按准确输入分别记录：

| 验证 | 结果与绑定 |
| --- | --- |
| P 完整 Windows 验证 | [validation-01/result.json](../build/release-local/step-03-rc-freeze-20261023-01/validation-01/result.json) 全部 7 个命令退出 0，450 项准确输入；实际 Go 1.27.3、前后 go mod verify、default/jsoniter、两组 vet、关键 race、前端 lint/TypeScript/Vite、三份 Bash 语法、三份 workflow 的 actionlint、83 项发布测试（新增 42 项）及独立 17 项前端 VM 合同通过。此轮绑定基线加 P 输入，随后形成 C 提交。 |
| F 回归 | [gzip-validation-01/result.json](../build/release-local/step-03-rc-freeze-20261023-01/gzip-validation-01/result.json) 的全部 **85 项**发布测试通过，含真实 Go 1.27.3 PE/ELF/Mach-O fixture；新增 gzip-only 完整往返和 gzip 原字节变化拒绝两项。 |
| 真实前端布局 | 同一 F 验证把 P 轮真实 Vite 的 **50 个文件**投入明确的合成材料，执行 produce → pack → unpack → audit，逐字节/哈希一致。生成基线仍是 validation-01/source-inputs.json，最终记录以 `audited_for_candidate` 关联新 SHA；未将其冒充最终材料生产。 |
| 最终 SHA 的 Ubuntu | 工作分支及 main 均运行完整普通 build/test，各 85 项发布测试通过；具体 run/job/check/log 见下表。 |
| 最终 prepare/resolve | [prepare-contract-smoke-02/result.json](../build/release-local/step-03-rc-freeze-20261023-01/prepare-contract-smoke-02/result.json) 两条命令退出 0，真实 SHA 与 epoch 一致，prerelease=true，artifact 名为 `release-v0.1.0-rc.1-5ec4dc5905149db03159d9d02c8f76646300d477`。材料 run、inbox ID 和哈希是明确合成格式输入；无联网、材料生产或数据库启动。 |

P 的源码清单 SHA-256 为 `71102be0d187561cf193e7f6888d78197a27b86d249b093ab655c00d4d90c7c8`，完整 ci-entry 日志 SHA-256 为 `20c088e3359954f1677cc70f5a7a49af8c1049b013b6506b4b3a8266a5d8a85a`。最终 F 的 450 项源码清单 SHA-256 为 **`0f6db10b8b41070dc3ba5bb097e6d667761b5114eee1c1261bea3b7f177c6760`**，提交前和冻结时均与活动文件逐项一致。

F 发布测试日志 SHA-256 为 `10c810e2ab516ff56f5308cb2296ac262d22a6c20e30566a7cd8c0da46764fa0`，真实前端往返日志为 `39c9bbb1c7297968a74ba6a39205e54d7a9ffad7739bdfdf7bf85a45ce9d6a03`。实际前端文件与版本注入记录见 [local-frontend-generated.json](../build/release-local/step-03-rc-freeze-20261023-01/evidence/local-frontend-generated.json)。F 只修改两份 Python 文件，Go/前端源码及依赖不变，复用 P 的完整 Windows 业务检查；受影响 Python 输入完整回归，并由最终新 SHA 的 Ubuntu 完整入口再次验证。

两个最终 CI 均为 `.github/workflows/build.yaml`，workflow ID `391831790`，event 为 push，runner 为 ubuntu-24.04，check 为 `test`，来源为 GitHub Actions app **15368**。18 个步骤均成功，日志在工具记录和测试入口均确认实际 **`go1.27.3 linux/amd64`**，包含 85 项发布测试及新增 gzip 测试。完整入口覆盖前端、Go default/jsoniter、两组 vet、关键 race、发布脚本、actionlint 和源码/锁文件未变检查。

| 最终 SHA CI | 工作分支 | main |
| --- | --- | --- |
| Branch | `release/rc-prep-v0.1.0-rc.1-20261023-02` | `main` |
| Run / attempt | [39941047949](https://github.com/Ink-lotus/octopus-roam/actions/runs/39941047949) / 1 | [39941450556](https://github.com/Ink-lotus/octopus-roam/actions/runs/39941450556) / 1 |
| Job / check | [120372268955](https://github.com/Ink-lotus/octopus-roam/actions/runs/39941047949/job/120372268955) | [120373602959](https://github.com/Ink-lotus/octopus-roam/actions/runs/39941450556/job/120373602959) |
| Check suite | `108255816085` | `108256916409` |
| 开始 / 结束（UTC） | `10:43:00Z` / `10:44:31Z` | `10:46:27Z` / `10:48:08Z` |
| Run / job 结果 | completed / success | completed / success |
| 85 项发布测试用时 | 11.846 秒 | 11.041 秒 |
| 完整日志字节数 | 96,509 | 96,096 |
| 日志 SHA-256 | `566c084ba3d1efb9f9bb40cf6412f566daf34e2cb71993daa27bbf593f95e532` | `811c3cae040bcff2e065746bfb50ad3f94b55ee1ae55df62094d00190ae1c3d8` |

逐项核验结果：[ci-work-success-02.json](../build/release-local/step-03-rc-freeze-20261023-01/evidence/ci-work-success-02.json)、[ci-main-success-02.json](../build/release-local/step-03-rc-freeze-20261023-01/evidence/ci-main-success-02.json)。原 API 快照分别在 `evidence/ci-work2-poll-02/`、`evidence/ci-main2-poll-02/`，完整日志在同一 evidence 目录的 `ci-run-<run-id>-attempt-1.log`。两个最终 run 均首轮成功，无 rerun；实际 11 个产品 ZIP、Android/NDK 和四架构镜像不在普通 CI 的本次产出范围。

本地工具来自已有获验环境，没有新增下载或修改持久环境配置：

| 工具 | 本次记录版本 |
| --- | --- |
| Go | `go1.27.3 windows/amd64`，任务内绝对路径；compiler/linker 同基线 |
| Node.js / pnpm | `v24.18.0` / `11.15.1` |
| Python | `3.14.6` |
| actionlint | `1.7.7`（该检查器自身由 Go 1.23.4 构建） |
| PowerShell / Git | `7.6.7` / `2.55.0.windows.5` |
| GCC | `16.2.0` |

Go 使用 `build/release-local/step-01-go1273-20261022-01/tools/go1.27.3-windows-amd64/go/bin/go.exe`，可执行文件 SHA-256 为 `83b7ba66215e696414fa49462d0db2da4f4cf15c7bebd42f0b189fa04866999d`。官方分发输入为 `https://dl.google.com/go/go1.27.3.windows-amd64.zip`，SHA-256 为 **`f26081f438319757f8a1b46d7d0b2c5ec7583d4038e485e0aa725cfde7885fff`**；官方索引/校验和沿用已复核的工作包 1 证据。工具路径、版本、可执行文件及 Go VERSION/LICENSE/compiler/linker 哈希见 [tool-versions-local.json](../build/release-local/step-03-rc-freeze-20261023-01/evidence/tool-versions-local.json)。系统 Go 未替换；测试使用子进程 GOTOOLCHAIN=local、GOPROXY=off、GOFLAGS=-mod=readonly 及已有锁定缓存。NDK r28c、go-licenses v2.0.1 是材料合同规定的后续工具输入，本包没有执行它们的真实材料任务。

本轮分步授权及执行链完整保留：

| 授权 | 准确操作与记录 |
| --- | --- |
| 只读例外 | 用户批准目标限定的 gh API GET、候选 gh run view 日志和精确 git ls-remote；[READONLY.md](../build/release-local/step-03-rc-freeze-20261023-01/review/READONLY.md)、[授权记录](../build/release-local/step-03-rc-freeze-20261023-01/evidence/authorization-readonly.json)。 |
| P | 用户答复“批准 P：应用补丁并隔离验证”；[审阅单](../build/release-local/step-03-rc-freeze-20261023-01/review/APPROVAL-P.md)、[授权记录](../build/release-local/step-03-rc-freeze-20261023-01/evidence/authorization-p.json)。14 文件补丁 SHA-256 为 `cf3e777a2928bc39532e2e288393ee475bc1c98e6a0d373d9b9b304d549160de`。 |
| C | 用户答复“批准 C：提交并按检查门禁推送至 main”；[审阅单](../build/release-local/step-03-rc-freeze-20261023-01/review/APPROVAL-C.md)、[授权记录](../build/release-local/step-03-rc-freeze-20261023-01/evidence/authorization-c.json)。15 文件形成 `ac44e88cb558dd43db463a54b7b8a0666bf2d3cf`，tree `486198f29466ce99d866b70f11ea9fcb8b49d2db`，summary“准备 v0.1.0-rc.1 发布”；工作包 2 报告原样纳入。该提交的 work/main CI 为 39934341994 / 39934834927，均 attempt 1 成功，属于冻结前历史证据。 |
| F | 用户答复“批准 F：应用修复并按门禁提交推送、冻结”；[审阅单](../build/release-local/step-03-rc-freeze-20261023-01/review/gzip-fix/APPROVAL-F.md)、[授权记录](../build/release-local/step-03-rc-freeze-20261023-01/evidence/authorization-f.json)。两文件补丁 SHA-256 为 `685329688ed5ce52a46320ae398b659f5d57491a3dd3a49733a54e1fba89a8b0`，最终 tree 与审阅值完全一致；[提交结果](../build/release-local/step-03-rc-freeze-20261023-01/evidence/commit-attempt-02.json)。 |

P 调用 apply-reviewed.py 和 run-local-validation.py；C/F 分别执行各自审阅目录下的 commit-reviewed.py、push-reviewed.py。F 的实际两次推送为：

```powershell
git push --no-follow-tags --recurse-submodules=no origin 5ec4dc5905149db03159d9d02c8f76646300d477:refs/heads/release/rc-prep-v0.1.0-rc.1-20261023-02
git push --no-follow-tags --recurse-submodules=no origin 5ec4dc5905149db03159d9d02c8f76646300d477:refs/heads/main
```

第二条在同 SHA 工作分支 CI 完整核验后执行；两次均先回查 main/工作分支及保护规则，使用普通 fast-forward。对应 [work 推送记录](../build/release-local/step-03-rc-freeze-20261023-01/evidence/push-work-attempt-02.json)、[main 推送记录](../build/release-local/step-03-rc-freeze-20261023-01/evidence/push-main-attempt-02.json)均退出 0。未改写历史、强推、删除旧分支或绕过保护。

最终 [配置回读](../build/release-local/step-03-rc-freeze-20261023-01/evidence/final-configuration-02/)与[核验记录](../build/release-local/step-03-rc-freeze-20261023-01/evidence/configuration-verified.json)确认：默认 main；必需 test/app 15368、strict=true、enforce_admins=true，禁止强推及删除，不强制 PR；有效 rules/rulesets 为空。release environment 保留维护者 reviewer、允许本人审批、仅 main 分支。环境管理员 bypass 按工作包 2 用户选择保持 true，本轮没有使用 bypass。Actions enabled，默认 workflow token 为 read，材料 workflow active；最终 tag 和 Release 列表均为空。本包未改远端仓库设置。

两次发现问题时均停止了依赖步骤并保留证据。P 首次 git apply 受系统 core.autocrlf 转换影响，后置原字节哈希检查拒绝；按原审阅字节机械恢复换行后通过，没有修改持久 Git 配置，见 [首次应用](../build/release-local/step-03-rc-freeze-20261023-01/evidence/patch-application-attempt-01.json)和[恢复结果](../build/release-local/step-03-rc-freeze-20261023-01/evidence/patch-application.json)。C 后的真实前端收尾发现 gzip-only 输出被原材料入口误拒绝，`ac44e88c…` 的冻结中止，见 [freeze-attempt-01.json](../build/release-local/step-03-rc-freeze-20261023-01/evidence/freeze-attempt-01.json)；完成 F 修复、85 项回归及新 SHA 两次 CI 后才首次生成有效 rc-freeze.json。原 `awaiting-f.json` 是批准前历史状态，最新授权以 authorization-f.json 为准。

本报告创建前工作树干净，450 项输入与冻结清单一致。创建后的状态、报告哈希及候选未变化证明见 [local-final.json](../build/release-local/step-03-rc-freeze-20261023-01/evidence/local-final.json)；唯一未提交文件为本报告，暂存区为空。本轮 [SHA256SUMS](../build/release-local/step-03-rc-freeze-20261023-01/evidence/SHA256SUMS)覆盖审阅文件、授权、输入清单、各轮结果/日志、CI 快照及冻结/报告证据；大型依赖副本和可重新生成的编译缓存使用各自清单记录，不重新逐文件加入该索引。

工作包 4 应从本报告与 rc-freeze.json 读取下面的固定身份，在对应 SHA 的干净 checkout 准备新材料目录：

```powershell
$rcVersion = 'v0.1.0-rc.1'
$rcCommit = '5ec4dc5905149db03159d9d02c8f76646300d477'
$sourceDateEpoch = 1792752156
```

下一步按 [入口说明](../docs/release-materials.md)准备准确应用/Go/npm/四架构 Alpine 来源、原始声明、工具分发输入哈希及 Go 1.27.3 的 REBUILD 说明，完成应用复建/LGPL 再链接，再生产、审核、pack、按具体授权上传并回下载核验。应优先采用与 Ubuntu runner 一致的 Linux amd64 工具和 Git 文件字节；Windows 生产同样必须满足准确 checkout 字节及后续 Ubuntu 前端原字节比较。保留材料 run/attempt、artifact ID、materials.json/bundle 哈希和保留时间。

**本包没有生产或上传最终 release-materials，没有连接真实数据库，没有 dispatch 材料/发布 workflow，也没有发布 RC。** 冻结记录中的 materials_run_id、materials_artifact_id、materials_sha256 均为 null。11 程序/四镜像真实构建、TLS、最新 C1/C2、最终包及真实 RC 升级继续按后续工作包执行；旧业务记录和本包 fixture 只保留各自范围。本轮使用 PowerShell 7、中文，公开检索沿用 tinyfish，认证查询使用本次明确批准的只读例外；没有派发子代理。
