# R1 远端只读核实

2026-10-20。**R1 远端只读核实已完成：公开来源、仓库管理员权限、Actions 和保护设置均已核实。用户补充权限后的 attempt 04 确认当前 CLI 已有 read:packages；精确 GHCR 包及版本仍返回 404 / Package not found，当前身份未找到该包。新 CI 尚未在远端生效，线上验收未完成，R1 不记为全部完成；本轮没有获得远端写操作授权。**

用户明确要求“开始R1 远端只读核实”，并批准本轮仅针对目标仓库及其 GHCR 包使用 `gh api --method GET` 的联网例外。公开检索和抓取继续使用 TinyFish。执行会话未登录、刷新认证、修改配置、启用 Actions、触发工作流、创建分支/tag/Release、推送或发布镜像。查询结果不包含凭据值或登录账号信息。

入口：[本轮证据](../build/release-local/r1-remote-readonly-20261020-01/README.md)、[最终只读汇总](../build/release-local/r1-remote-readonly-20261020-01/evidence/final-summary-04.json)、[仓库管理 GET](../build/release-local/r1-remote-readonly-20261020-01/evidence/github-attempt-03.json)、[补充 scope 后的 GHCR GET](../build/release-local/r1-remote-readonly-20261020-01/evidence/github-packages-attempt-04.json)、[包查询结论](../build/release-local/r1-remote-readonly-20261020-01/evidence/package-summary-04.json)、[11 个 action 的引用解析](../build/release-local/r1-remote-readonly-20261020-01/review/action-reference-map.md)。此前本地检查以 [R1 本地报告](release-ci-r1-20261020.md)为准，没有重复构建或数据库/容器验收。首次公开与待认证汇总均保留为历史快照。

## 远端仓库快照

所有下列状态都是本轮 GET 响应的时间点快照；空列表仅描述当前身份可见的对象，不据此排除隐藏的草稿或私有包。

| 核实项 | 实际结果 | 判定 |
| --- | --- | --- |
| 目标仓库 | `Ink-lotus/octopus-roam`，public、fork，未 archived/disabled；已认证响应的 admin/maintain/push/triage/pull 均为 true | 当前 CLI 身份具有仓库管理员权限；该事实不替代任何远端写操作授权 |
| 默认分支与分支列表 | 默认 `master`；可见分支仅 `master`，SHA `4a54b59e2f489c92070cb1cec5ed12cd0bb72e22`；无下一页；`main`、`test` 单项 GET 均为 404 | `main` 尚未建立，按 R2 另行授权集成 |
| 本地与远端历史关系 | 该远端 commit 已在本地对象库；它是本地 HEAD `948de764ae9a034fd72899686d24dcc4ecad7fa5` 的祖先，远端独有 0、本地独有 40 个提交 | 本地历史包含此次观测的远端 master；没有执行 fetch 或更改 refs，后续写入前需复查 |
| 标签、Release | 管理员认证后两个列表仍为空、无下一页 | 当前未见标签或 Release |
| 已登记 workflow、运行 | 管理员认证后 workflows 和 runs 的 `total_count` 仍均为 0；公开 Actions 页面同样显示 0 次运行 | 尚无真实 Actions 运行证据；Actions 总开关另由管理端点确认已开启 |
| 远端 workflow 内容 | 默认分支上的 build/release 均仍为旧文件；release 中存在 `git push`，不含新的四阶段结构；与当前本地候选不同 | 本地 CI 补丁尚未远端生效；本轮没有激活旧流程 |
| 分支保护与 rulesets | `master.protected=false`，管理员读取 rulesets 仍为空；master 保护端点 404 / Branch not protected | 已确认 master 未配置分支保护；main 尚不存在，产品分支保护按 R2 后续方案落实 |
| environments | 管理员读取列表 `total_count=0`，`release` 单项 404 | 已确认尚未创建 release environment，因此 reviewer、防自审、bypass、部署分支规则尚未配置；本地 YAML 的 environment 名称不能写成审批规则已生效 |
| Actions 设置 | `enabled=true`、`allowed_actions=all`、`sha_pinning_required=false` | 当前仓库策略允许这 11 项 action 引用；selected-actions 端点 409 在 all 模式下不适用，不是认证失败，也无需因此改全局设置 |
| token 默认权限 | `default_workflow_permissions=read`，`can_approve_pull_request_reviews=false` | 当前只读默认值与已审查 workflow 的 publish 单独声明相容，不需要仅为此改全局 write；实际 job 写权限仍由后续 Actions 验证 |
| GHCR | attempt 04 的三个响应均包含 read:packages；仓库 GET 200 且 admin=true；精确包元数据和版本 API 均为 404 / Package not found，无下一页 | 当前具备读取 scope 的身份未找到该精确包，scope 补充待办已解除；没有取得可供核验可见性、关联仓库或包级权限的对象，不据此声称实际发布/匿名拉取通过 |

证据：[管理员 GET 记录](../build/release-local/r1-remote-readonly-20261020-01/evidence/github-attempt-03.json)、[本地历史关系](../build/release-local/r1-remote-readonly-20261020-01/evidence/local-remote-relation.json)、[公开 Actions 页面](../build/release-local/r1-remote-readonly-20261020-01/evidence/repository-actions-public.json)、[runner/Packages 页面](../build/release-local/r1-remote-readonly-20261020-01/evidence/runner-and-package-public.json)。首次查询和恢复登录后的受限进程尝试均保持原件，不覆盖历史响应。

用户告知“我已经重新登录好了，你再试试”后，同一辅助脚本在受限进程中的 attempt 02 仍得到管理端点 401，另有一次 runs 网络错误；这些不用于否定用户登录。只读环境诊断确认本机有 GitHub CLI 配置且无 GH_TOKEN/GITHUB_TOKEN 覆盖，未读取或输出凭据值。随后同一 GET 在扩展权限下的 attempt 03 成功返回 admin=true 和管理设置；后续需使用可读取系统凭据的执行环境。自动审批未拒绝此次只读重试，执行会话没有登录、刷新或迁移凭据。[受限重试](../build/release-local/r1-remote-readonly-20261020-01/evidence/github-attempt-02.json)、[不含凭据的环境诊断](../build/release-local/r1-remote-readonly-20261020-01/evidence/auth-context-02.json)。

## action 与工具来源

活动两份 workflow 使用的 **11 个不同 action 引用全部解析到当前 commit，并取得相应 commit 的 action.yml**。两个 annotated tag 已剥离到 commit，未将 tag 对象 SHA 错当成 action commit。详情见 [映射表](../build/release-local/r1-remote-readonly-20261020-01/review/action-reference-map.md)及 [结构化来源](../build/release-local/r1-remote-readonly-20261020-01/evidence/public-inspection-02.json)。

这些是标签存在性及声明的 inputs/runtime 证据，不是完整源码审计。major tag 和完整版本 tag 均可能移动；本轮没有修改 CI 引用。如果后续选择改用审查后的 commit SHA，需另准备具体 CI 补丁并取得对应授权。

| 固定工具 | 公开来源核实 | 边界 |
| --- | --- | --- |
| Go 1.27.0 | actions/go-versions 清单和对应 Linux x64 Release 附件一致，附件 state=uploaded、size 非零 | 未下载工具，未在 runner 执行 |
| Node 24.18.0 | actions/node-versions 清单和对应 Linux x64 Release 附件一致 | 同上 |
| Python 3.14.6 | actions/python-versions 清单与 **Linux Ubuntu 24.04 x64** 附件一致 | 同上 |
| pnpm 11.15.1 | npm registry 中准确版本存在，engines 为 Node >=22.13 | 与文档所列 runner 预装 Node 22.23.3 及目标 Node 24.18.0 相容；未安装包 |
| Android NDK r28c | android/ndk 的正式 Release 提供 Linux 官方下载地址；完整版本 `28.2.13676358` | 仅读取下载元数据，没有下载 NDK 或补跑 Android 构建 |
| actionlint 1.7.7 | 正式 Release 和 Go module proxy 的准确版本记录存在 | 此前 Windows 本地 lint 结果保留；本轮未下载/安装工具 |
| ubuntu-24.04 | 官方 runner-images 文档可读，记录镜像版本 `20261004.327.1`、Bash/GCC/Docker 等工具 | 是文档快照，不证明未来分配的 runner 镜像固定，也不替代 Actions 运行 |

Go/Node/Python 的上游 digest 和 pnpm integrity 仅作为元数据保存，没有声称验证了未下载的附件字节。upload/download artifact 声明 node20，其余九项 action 声明 node24；实际托管 runner 兼容性、依赖获取和 artifact 服务仍须真实 Ubuntu Actions 验证。TinyFish 部分 URL 首次返回 empty_content/HTTP 错误，随后通过 refs/matching-refs/contents 等公开替代路径补齐，初次错误全部保留。

## 管理项续查与后续操作边界

用户自行恢复登录后的仓库管理项已完成只读核实。attempt 03 当时的 scope 为 gist/read:org/repo/workflow，不含 read:packages，原记录保留。用户随后告知“完成增加权限了”，同一辅助脚本在可读取系统凭据的扩展环境执行 attempt 04，仅 GET 仓库 metadata、精确包及版本三个端点。三个响应均确认已有 read:packages，仓库管理员权限仍有效，包及版本仍为 404 / Package not found。本轮只读续查已完成，无需继续等待认证；CLI scope 与将来 workflow 的 GITHUB_TOKEN 属于不同权限上下文，执行会话未代登录、刷新凭据或修改配置。

已核实与剩余范围：

1. 仓库管理员权限、Actions 启用/允许范围/无 SHA 强制策略、token 默认只读均已确认；当前无需为使本地引用可用而修改这些设置。
2. main 尚未建立、master 无保护、release environment 未创建，已登记为后续方案的具体缺项。产品 main 的必需检查名称应取自首次真实 CI，再审查分支门禁；发布前需确定 environment 的保护方案。
3. GHCR 精确包及版本已在 read:packages 生效后查询，结论为当前身份未找到目标包。包可见性、关联仓库、访问继承与包级发布权限仍需实际对象和后续发布验收；不能将读取 scope 或 404 当作这些配置已通过。
4. 实际 GITHUB_TOKEN 的 contents/packages write、artifact 获取和发布能力仍由后续已授权的 Actions/发布验收证明；只读管理查询不能替代真实 job。

GitHub 官方文档明确：容器包的访问权限继承与可见性分别控制；关联 public 仓库不自动把包设为 public，首次发布默认 private。应把 GHCR 的公开可见性和匿名获取纳入后续发布验收，不把 CI 中的 source label 或 packages write 声明当作已完成公开配置。[文档来源证据](../build/release-local/r1-remote-readonly-20261020-01/evidence/ghcr-documentation.json)、[首次可见性检索](../build/release-local/r1-remote-readonly-20261020-01/evidence/search-ghcr-first-visibility.json)。

续查收尾仅同步本报告、R1 本地报告和主发布计划；相对于 scope 续查前 500 项快照，其余 497 项未变，九个活动 CI 文件的原始字节哈希与本地收尾证据一致。HEAD/分支未变、暂存区空、差异空白及报告链接检查通过，未重复产品构建或测试。历史证据保留原件。见[最终汇总](../build/release-local/r1-remote-readonly-20261020-01/evidence/final-summary-04.json)和[最终工作区核验](../build/release-local/r1-remote-readonly-20261020-01/evidence/workspace-checks-final-04.json)。

建立/切换 main、集成与提交、启用 Actions、配置 branch/environment 保护、改变包权限/可见性、推送、触发 workflow 和发布均需各自具体授权。本轮只报告观察结果和缺项，不执行这些动作。首次 Ubuntu CI、NDK/实际多架构、R3/R4 的 artifact、公开下载和真实升级仍沿用主计划的阶段边界。
