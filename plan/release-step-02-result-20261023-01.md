# 发布准备工作包 2：新版普通 CI 与仓库配置收口

日期：2026-10-23。工作目录：`D:\tool\AI\octopus-roam`。目标仓库：`Ink-lotus/octopus-roam`。镜像渠道：仅 GHCR。

**工作包 2（交接 A0 第 5 项及 A）完成。新 SHA 的 Go 1.27.3 普通 Ubuntu CI 已通过，main 保护与 release 审批规则已配置并回读。下一步进入工作包 3：材料入口、验收驱动适配与 RC 冻结。**

最终本地 HEAD、远端 main 与 CI SHA 均为 **`f857486093a185669fa0996451949563ade37e9e`**。本报告按本次授权在操作完成后新建，保留未提交；不追加文档提交改变已验收 HEAD。

本轮证据目录：[step-02-ci-config-20261023-01](../build/release-local/step-02-ci-config-20261023-01/)。重点入口：[CI 核验结果](../build/release-local/step-02-ci-config-20261023-01/evidence/ci-success.json)、[设置成功结果](../build/release-local/step-02-ci-config-20261023-01/evidence/settings-result-attempt-02.json)、[最终远端快照](../build/release-local/step-02-ci-config-20261023-01/evidence/final/)、[证据 SHA256SUMS](../build/release-local/step-02-ci-config-20261023-01/evidence/SHA256SUMS)。下述 Actions 时间均为 UTC。

## 1. 接手核对与工作包 1 验收确认

已读取当前 [RC 交接](release-rc-handoff-20261021.md) A0/A、[发布 CI 合同](../docs/release-ci.md)、[主发布计划](release-plan-v0.1.0.md) R2–R5，以及 [工作包 1 实际结果](release-step-01-result-20261022-01.md) 和原始证据。

接手时为 `main / 1af3ec18aac751eac668ace4331ce8544dbf461f`，暂存区为空，有 11 份已跟踪修改及 2 份新增文件。当前 origin 唯一 fetch/push 地址都是 `https://github.com/Ink-lotus/octopus-roam.git`。本轮重新查询确认远端默认分支已是 main，main 为 `b949271f2d9183cc08bf7a5d621f69272187ff4f`，不沿用旧报告中的默认 master、权限或对象状态。

工作包 1 证据复核结果：

| 核对范围 | 本轮实际结果 |
| --- | --- |
| 原 evidence/SHA256SUMS | 177 项逐项匹配 |
| validation-04 的准确源码输入 | 442 项与当前原始字节全部一致 |
| final-state.json 最终文件 | 443 项全部一致，含工作包 1 报告 |
| validation-04 的 9 个命令与日志绑定 | 退出码均为 0，日志哈希全部匹配 |
| 本地工具链证据 | 该验收实际为 `go1.27.3 windows/amd64`，来源及工具核验文件包含在已核对的证据索引内 |
| 既有完整本地验收 | default/jsoniter、两组 vet、关键 race、前端、41 项发布测试、脚本检查及 actionlint 通过；另有 17 项前端 VM 合同 |

核对记录见 [local-preflight.json](../build/release-local/step-02-ci-config-20261023-01/evidence/local-preflight.json)。因此确认工作包 1 的本地验收通过且输入没有漂移；本轮未机械重跑相同本地完整套件。新的 Ubuntu 结果绑定本轮真实提交，详见第 3 节。

## 2. 文件审查、授权与提交推送

本次用户分别明确答复“批准 C：提交 13 份文件并普通推送”和“批准 S：先检查后直推，本人发布审批”。[操作审阅单](../build/release-local/step-02-ci-config-20261023-01/review/APPROVAL.md)、[C 授权记录](../build/release-local/step-02-ci-config-20261023-01/evidence/authorization-c.json)及 [S 授权记录](../build/release-local/step-02-ci-config-20261023-01/evidence/authorization-s.json)保存目标、命令、影响和审阅文件哈希；没有以历史授权或凭据代替本次授权。

纳入本次新提交的 13 份文件如下，全部沿用工作包 1 验收后的内容：

| 文件 | 纳入内容 |
| --- | --- |
| `.github/workflows/build.yaml` | Go 1.27.3 与实际工具链检查 |
| `.github/workflows/release.yaml` | 构建工具链检查，publish 显式安装核验所需 Go |
| `README.md`、`README_zh.md` | 既有 Go 最低版本说明修改 |
| `go.mod` | 既有 Go 1.27.3 基线修改 |
| `scripts/build.sh`、`scripts/ci-test.sh` | 实际版本、离线/只读约束和测试入口 |
| `scripts/release_contract.py` | 实际编译元数据及构建记录门禁 |
| `scripts/tests/test_release_contract.py` | 配套合同 fixture 更新 |
| `scripts/tests/test_release_go_metadata.py` | 新增元数据拒绝路径与真实格式读取测试 |
| `docs/release-ci.md`、`plan/release-rc-handoff-20261021.md` | 工作包 1 的合同及交接更新 |
| `plan/release-step-01-result-20261022-01.md` | 新增工作包 1 实际结果报告 |

逐文件原始 SHA-256、入库 blob 和 mode 见 [files.json](../build/release-local/step-02-ci-config-20261023-01/review/files.json)与 [proposed-tree.json](../build/release-local/step-02-ci-config-20261023-01/review/proposed-tree.json)。本地既存提交 `1af3ec18…` 的三份计划修改也随普通推送上传，包括 `plan/release-plan-v0.1.0.md`；保留该提交，没有重复提交或改写历史。

| Git 项目 | 实际结果 |
| --- | --- |
| 本次 commit summary | `验收 Go 1.27.3 并补齐发布产物版本门禁` |
| 新提交 / 最终本地 HEAD | `f857486093a185669fa0996451949563ade37e9e` |
| 父提交 | `1af3ec18aac751eac668ace4331ce8544dbf461f` |
| 审阅预计树 / 实际暂存树 / 提交树 | `3338466a4829f9670099d05bdd522a4bacb61397`，三者一致 |
| 文件规模 | 新提交 13 文件；提交树 443 文件 |
| 远端 main | 从 `b949271f2d9183cc08bf7a5d621f69272187ff4f` fast-forward 到新 SHA |
| 远端默认 HEAD | `refs/heads/main`，指向新 SHA |
| 远端 master | 仍为 `4a54b59e2f489c92070cb1cec5ed12cd0bb72e22` |

实际推送命令：

```powershell
git push --no-follow-tags --recurse-submodules=no origin f857486093a185669fa0996451949563ade37e9e:refs/heads/main
```

推送前再次认证 GET 核对 main 未受保护、有效规则/rulesets 为空，确认是获准的 fast-forward 路径。证据见 [提交记录](../build/release-local/step-02-ci-config-20261023-01/evidence/commit-attempt-01.json)、[推送前回读](../build/release-local/step-02-ci-config-20261023-01/evidence/pre-push/)、[推送结果](../build/release-local/step-02-ci-config-20261023-01/evidence/push-attempt-01.json)和 [最终精确 refs](../build/release-local/step-02-ci-config-20261023-01/evidence/final-remote-refs.json)。普通提交未携带 tag，本次没有创建或合并 PR。

## 3. 新 SHA 普通 Ubuntu CI

| 项目 | 实际值 |
| --- | --- |
| Workflow | `build`，`.github/workflows/build.yaml`，ID `391831790` |
| Run / attempt | [39917087653](https://github.com/Ink-lotus/octopus-roam/actions/runs/39917087653) / **1** |
| Event / branch | `push / main` |
| 完整 SHA | `f857486093a185669fa0996451949563ade37e9e` |
| Run 结果 | `completed / success` |
| 实际 job / check | [test / 120292873855](https://github.com/Ink-lotus/octopus-roam/actions/runs/39917087653/job/120292873855) |
| Check suite | `108190403728` |
| Check 来源 | GitHub Actions，`github-actions`，app ID **15368** |
| Runner | `ubuntu-24.04`；日志显示 Ubuntu，实际 Go 平台为 `linux/amd64` |
| 实际 Go | **`go version go1.27.3 linux/amd64`** |
| Job 开始 / 结束 | `2026-10-23T07:20:33Z` / `2026-10-23T07:27:04Z` |
| Job 用时 | 391 秒，即 6 分 31 秒 |
| 步骤结果 | 18 个步骤全部 `completed / success` |

实际 Go 输出在 setup-go、`Record and check actual Go toolchain`、`Test committed inputs` 三处一致，后两处与已提交脚本中的精确版本检查绑定。没有仅凭 workflow 中的版本字符串宣称实际版本。

| 检查 | 结果及证据含义 |
| --- | --- |
| Checkout / immutable input | 检查实际 checkout SHA 与本 run 输入一致 |
| 工具与依赖准备 | Go/Node/pnpm/Python setup、锁定依赖和固定 actionlint 成功 |
| Workflow 静态检查 | 两份 workflow 的 actionlint 步骤成功 |
| 前端 | lint、`tsc --noEmit`、Vite 8.2.2 构建成功 |
| Go | 全量 default/jsoniter、两组 vet、关键包 race 完整入口成功；Ubuntu 包含 internal/update 的 Unix 测试路径 |
| 发布合同与元数据 | 41 项通过，用时 11.785 秒，含真实 PE/ELF/Mach-O 读取 fixture |
| Bash | 三份发布/测试脚本语法检查随完整入口成功 |
| 源码与锁文件 | 最后的 `git diff --exit-code` 成功 |

权威证据为 [run/job/check 最终响应](../build/release-local/step-02-ci-config-20261023-01/evidence/ci-poll-04/)、[完整日志](../build/release-local/step-02-ci-config-20261023-01/evidence/ci-run-39917087653-attempt-1.log)和 [逐项绑定结果](../build/release-local/step-02-ci-config-20261023-01/evidence/ci-success.json)。日志 80,757 字节，SHA-256：`f47816895966fffca7b008376698a6dd77a4573fd4b2fe4f199d5df155941660`。

本轮普通 CI 没有失败或 rerun，也没有因仓库设置调整产生新源码 SHA。上述是普通测试和合成/小程序门禁 fixture，不包含真实 11 个产品 ZIP、Android/NDK 或四架构镜像构建。

## 4. 仓库设置与可执行审批方案

所有设置在上述 CI 成功且实际 check 来源核对后执行。操作前还确认仓库为公开仓库、当前身份有 admin 权限、main SHA 未变化，原保护与 environment 尚未出现其他修改。

| 设置 | 配置后回读 |
| --- | --- |
| 默认分支 | `main`；接手时已正确，本轮无需 PATCH |
| main 保护 | 已启用传统 branch protection；rulesets / rules API 仍为空，不把它们混作传统保护结果 |
| 必需检查 | `checks=[{context: "test", app_id: 15368}]`；回读兼容字段 `contexts=["test"]` |
| 最新分支要求 | `required_status_checks.strict=true` |
| 管理员约束 | `enforce_admins.enabled=true` |
| 强推 / 删除 | `allow_force_pushes.enabled=false`、`allow_deletions.enabled=false` |
| PR / 历史 | 按用户选择不强制 PR；`required_pull_request_reviews` 未启用；`required_linear_history.enabled=false` |
| 讨论 | `required_conversation_resolution.enabled=true` |
| Actions | enabled/all，默认 workflow token read；build/release/template-check 均 active |
| release environment | 新建成功，ID `25107283969` |
| Required reviewer | 唯一 `User: Ink-lotus`，ID `67992994`；当前仓库权限为 admin |
| 本人审批 | `prevent_self_review=false`，唯一维护者可以审批本人触发的 run |
| 计时器 | 请求 `wait_timer=0`；回读未启用 wait_timer 保护规则 |
| 环境管理员绕过 | **`can_admins_bypass=true`**，按用户明确选择保留；本会话未执行绕过 |
| 部署分支 | 自定义规则仅 `name=main, type=branch`，policy ID `64062722`；未添加 tag 规则 |

实际成功请求为：

```powershell
$reviewRoot = 'build/release-local/step-02-ci-config-20261023-01/review'
gh api --method PUT repos/Ink-lotus/octopus-roam/branches/main/protection --input "$reviewRoot/main-protection-v2.json"
gh api --method PUT repos/Ink-lotus/octopus-roam/environments/release --input "$reviewRoot/release-environment.json"
gh api --method POST repos/Ink-lotus/octopus-roam/environments/release/deployment-branch-policies --input "$reviewRoot/release-branch-main.json"
```

成功写入响应及紧随其后的 GET 分别在 [settings-writes-02](../build/release-local/step-02-ci-config-20261023-01/evidence/settings-writes-02/) 和 [settings-after-02](../build/release-local/step-02-ci-config-20261023-01/evidence/settings-after-02/)。最终再次回读的 [main 保护](../build/release-local/step-02-ci-config-20261023-01/evidence/final/main-protection.json)、[环境](../build/release-local/step-02-ci-config-20261023-01/evidence/final/environments.json)、[仓库](../build/release-local/step-02-ci-config-20261023-01/evidence/final/repository.json)与配置一致；[部署分支响应](../build/release-local/step-02-ci-config-20261023-01/evidence/settings-after-02/release-branch-policies.json)确认唯一 main branch。

**审批可执行性已在配置与权限层核实。** 仓库公开，required reviewer 已生效；唯一审批人拥有足够权限，允许本人审批；main 没有被部署规则排除。默认 main 上的 release workflow 已注册且 active，远端 workflow blob 与本次提交一致，仅有 workflow_dispatch 入口，publish job 明确使用 `environment: release`。因此没有“只有本人但禁止本人审批”或“发布 main 被环境规则排除”的配置死锁。本会话按范围不 dispatch release，未执行真实等待/审批；后续发布 run 仍须审核该 run 的实际产物并取得单独放行。

**后续 main 使用先检查、后直推的维护方式。** 当前 PR 模板及 template-check workflow 要求“不包含测试文件”，否则打开 PR 后会自动关闭；本包包含测试，不能如实勾选该声明。用户据此明确选择不强制 PR。后续先把获准的新 SHA 推到工作分支，让同一 SHA 的 `test` 通过，再 fast-forward 到 main，并记录 main 普通 CI。管理员也受检查约束。若以后改用强制 PR，先审查并授权修订上述模板/CI 约束；本轮未改模板、未虚填人工审查声明。

## 5. 失败、恢复与范围记录

- 首次受限环境的 Git TLS 凭据访问失败，普通仓库 GET 的权限字段为 null。后续通过同一目标的扩展执行环境认证 GET 确认 admin；没有把权限缺字段当成无权限或成功认证的证据。
- 一次补充 GET 的自动审批因服务过载/stream disconnect 未完成，操作未执行；沿同一审批路径重试成功。这不是安全拒绝，没有绕过自动审批。记录见 [prepared-execution-checks.json](../build/release-local/step-02-ci-config-20261023-01/evidence/prepared-execution-checks.json)。
- main 保护首次 PUT 返回 **422**：空的旧 `contexts` 数组与新版 `checks` 同时出现，触发接口 oneOf schema 冲突；后续 environment 请求未执行。原 [失败响应](../build/release-local/step-02-ci-config-20261023-01/evidence/settings-writes/main-protection.json)、[失败结果](../build/release-local/step-02-ci-config-20261023-01/evidence/settings-result.json)和原请求均保留。
- 兼容修正只省略空旧字段 `required_status_checks.contexts`，`test` / app 15368、strict 及其他所有批准设置逐项不变；执行前再次 GET 确认仍未保护且 environment 为空。在原 S 的同一目标、方法和保护语义内重试，三项设置成功并回读。见 [修正前后记录](../build/release-local/step-02-ci-config-20261023-01/evidence/settings-request-compatibility.json)。没有追加设置选择，也没有声称另获一次用户答复；无需改源码或重新 CI。

联网公开检索/抓取使用 PowerShell 7 下的 tinyfish，经 ConvertFrom-Json 选择字段；认证例外按本次目标限定为 GitHub API GET、目标 origin 的 git ls-remote，以及同一 run 的 gh run view 日志 GET。没有输出凭据、刷新认证、修改依赖或系统工具配置，没有派发子代理。

最终回读 [tag 列表](../build/release-local/step-02-ci-config-20261023-01/evidence/final/tags.json)与 [Release 列表](../build/release-local/step-02-ci-config-20261023-01/evidence/final/releases.json)均为空；[release workflow runs](../build/release-local/step-02-ci-config-20261023-01/evidence/final/release-runs.json)为 0。精确 GHCR 包及版本在当前认证身份下仍返回 `404 Package not found`，见 [包](../build/release-local/step-02-ci-config-20261023-01/evidence/final/ghcr-package.json)及 [版本](../build/release-local/step-02-ci-config-20261023-01/evidence/final/ghcr-versions.json)；后续发布前仍须重新核实。本会话没有触发 release workflow，没有创建版本 tag、Release 或镜像。

## 6. 本地收尾与工作包 3 入口

报告创建前，443 项已跟踪文件原始字节仍与工作包 1 最终清单一致，暂存区和工作区干净，HEAD/tree 与新 CI 绑定值一致，见 [报告前完整性](../build/release-local/step-02-ci-config-20261023-01/evidence/local-before-result-report.json)。报告创建后的 [最终本地记录](../build/release-local/step-02-ci-config-20261023-01/evidence/local-final.json)记录报告哈希与 Git 状态；唯一未提交文件为本报告，暂存区为空。

下一会话先读本报告、交接 B 和三份当前计划。旧计划中“工作包 2 尚待完成”等文字保留其历史时点，本轮当前事实以本报告及回读证据为准。先只读核对现场，按已选择的“工作分支同 SHA CI → main fast-forward”路径准备新的具体授权。

工作包 3 继续完成 Go 1.27.3 的 release-materials 生产/上传入口、验收驱动对新 artifact 布局的适配、准确 RC 版本与说明，再冻结最终 RC SHA。当前 `f857486…` 是工作包 2 的通过 SHA，不是最终 RC 冻结或完整产物验收结果；任何新源码 SHA 都要取得自身普通 CI。材料、11 目标/四镜像真实构建、TLS/产品与真实 RC 升级验收仍按后续工作包执行。
