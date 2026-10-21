# R2 本地差异审查

日期：2026-10-21。基线：`test / 948de764ae9a034fd72899686d24dcc4ecad7fa5`。

**结论：发现两个 P2 问题，建议修复并完成定向回归后再提交、集成 main。普通推送的禁发条件成立；本次审查不代表 R2 集成或远端 CI 已完成。**

本轮审查输入为 31 个已跟踪改动、58 个新增文件，共 89 个文件，含产品修复、验收工具、R1 CI 和发布文档。已提交的 40 个产品提交作为既有基线，本轮检查其祖先关系，没有逐个重新评审历史实现。`master` 的完整 SHA 为 `4a54b59e2f489c92070cb1cec5ed12cd0bb72e22`，是当前 HEAD 的祖先，`HEAD..master` 为 0；本地没有 main。后续可在保留既有提交历史的基础上集成，仍须届时核实远端是否变化。

证据目录：[r2-review-20261021-01](../build/release-local/r2-review-20261021-01/)。本轮只新增独立审查材料和本报告，没有修改业务源码、CI、依赖、既有候选或历史报告，也没有进行 commit、分支切换、远端访问或发布。

## C1 · P2 · 自定义 MySQL CA 丢失无查询参数 DSN 的默认选项

位置：[internal/conf/database.go](../internal/conf/database.go)，第 26、47–48 行；调用链：[cmd/start.go](../cmd/start.go) → [internal/db/db.go](../internal/db/db.go)，第 102–107 行。

触发条件：原 `database.path` 使用没有 `?` 查询参数的 MySQL TCP DSN，然后配置新增的 `database.mysql_ca_file`。原 `initMySQL` 会为这种 DSN 补齐 `charset=utf8mb4&parseTime=True&loc=Local`；新 `ConnectionPath()` 先解析原 DSN，再生成带 `tls=...` 的 DSN，导致后续 `initMySQL` 不再进入补默认参数的分支。

使用生产 `ConnectionPath()`、合成 CA 和合成 DSN 得到：

| 参数 | 原有无查询参数路径 | 启用自定义 CA 后 |
| --- | --- | --- |
| `parseTime` | `true` | `false` |
| `loc` | `Local` | `UTC` |
| 显式 `charset=utf8mb4` | 有 | 无 |

TLS 仍然严格校验；问题是普通连接语义发生变化。当前 MySQL 驱动在 `parseTime=false` 时将日期返回为字节串，应用的 `ChannelRefreshRecord.StartedAt/FinishedAt` 则是 `time.Time`。`InitCache → channelRefreshCache → loadChannelRefreshRecords` 会在启动时读取这些记录，因此已有刷新记录的库可能在重新启动时因日期扫描错误停止初始化。参数变化已运行复现；扫描及启动影响依据当前驱动和调用链确认，本轮没有连接真实 MySQL。

现有 CA 单元测试的 DSN 已显式包含 `parseTime=true`；数据库验收 profile 也预先补齐了这些参数，因此两者不会覆盖此入口。

建议：依据原始 DSN 先保留现有的默认参数行为，再注册、注入自定义 TLS；不要覆盖用户显式给出的选项。增加“无查询参数 DSN + 自定义 CA”的参数回归，覆盖 `parseTime/loc/charset`。

证据：[mysql-defaults.log](../build/release-local/r2-review-20261021-01/mysql-defaults.log)、[合成参数探针](../build/release-local/r2-review-20261021-01/.probes/mysql_defaults.go)。未读取真实 profile，未写入或连接业务数据库。

## C2 · P2 · 更新请求没有绑定前端用于确认的目标版本

位置：[web/src/api/update.ts](../web/src/api/update.ts)，第 154–160 行；相关后端：[internal/update/core_apply.go](../internal/update/core_apply.go)，第 37 行，以及 [internal/server/handlers/update.go](../internal/server/handlers/update.go)，第 62 行。

触发条件：页面已经展示 `v0.1.0-rc.2`，点击更新前发布源新增 `v0.1.0-rc.3`。前端构造的 `targetVersion` 被 `void attempt` 丢弃，POST 没有 body；后端重新查询发布源并选择 rc.3，返回值只有 `update_pending`。前端仍用旧的 rc.2 建立确认记录。

当版本查询随后返回 rc.3 时，`resolveUpdatePhase()` 要求当前版本与保存的目标严格相等，因而不能确认完成，30 秒后显示 `unconfirmed`。这会把一次成功升级误报为未确认，而且界面展示的版本与实际接受的更新版本不一致。

已分别运行生产后端查询函数和前端 mutation/状态函数：loopback 发布源使 `GetLatestInfo()` 选择 rc.3；真实前端 mutation 发出的请求确实没有目标版本；输入已安装 rc.3 时，确认窗口后为 `unconfirmed`，输入 rc.2 的对照组为 `completed`。此复现没有执行程序替换，不冒称完成真实升级验收。

建议：让客户端与服务端共享同一个被接受的更新目标。可以在 POST 中传目标并由后端验证、固定该版本，或由后端回传实际接受版本并用回执建立确认记录；保留严格版本确认，避免仅以“版本发生变化”判定成功。增加“展示版本后发布源发生变化”的跨接口回归。

证据：[后端查询日志](../build/release-local/r2-review-20261021-01/update-backend.log)、[前端请求及状态日志](../build/release-local/r2-review-20261021-01/update-ui.log)。Go 探针通过临时 `-overlay` 接入包内函数，没有写入产品目录。

## CI 与已有证据核对

普通 `push`、`pull_request` 只进入 `build.yaml` 的测试 job；`release.yaml` 仅有 `workflow_dispatch`，`publish` 默认为 false。`prepare` 限定 main、完整 commit SHA、版本和说明；`publish` 依赖 `[prepare, test, build]`，没有 `always()` 或 `continue-on-error` 绕过。普通 main 推送不会自动创建 tag、Release 或镜像。

| 检查 | 本轮结果 |
| --- | --- |
| 9 个活动 CI 文件与 R1 最终哈希 | 9/9 一致 |
| R1 远端续查前的 500 项快照 | 497 项一致；差异仅为 R1 收尾记录中已列出的三份报告 |
| 发布脚本合成回归 | 27/27 通过，1.895 秒，无外网访问 |
| MySQL CA 参数探针 | 退出 0，复现 C1 |
| Go 后端版本选择探针 | 通过，使用 loopback，复现 C2 的后端选择 |
| 前端真实 mutation/状态函数探针 | 退出 0，复现 C2 的空 body 与错误终态 |
| 435 项原始非忽略工作区文件 | 审查期间哈希未变 |
| 工作区及暂存区差异检查 | 通过；暂存区为空，HEAD/分支未变 |

哈希核对见 [r1-evidence-binding.json](../build/release-local/r2-review-20261021-01/r1-evidence-binding.json)，本轮回归见 [release-contract-tests.log](../build/release-local/r2-review-20261021-01/release-contract-tests.log)，收尾见 [final-checks.json](../build/release-local/r2-review-20261021-01/final-checks.json)。

R1 的完整 Go default/jsoniter、vet、关键 race、ESLint、TypeScript/Vite 和 actionlint 记录仍可用于对应的未变输入；本轮没有机械重复完整构建，也没有把历史结果写成本轮重跑。Ubuntu Actions、Android/NDK、真实多架构镜像、公开发布及两个 RC 间在线升级仍按原计划验收。当前 `main.go` 的上游版本注释及草稿 Release Notes 被发布 prepare 拒绝属于预期门禁，留到 R3 审查版本时处理。

## 提交边界草稿

以下划分覆盖审查开始时的全部 89 个文件。完整逐文件清单见 [commit-boundaries.json](../build/release-local/r2-review-20261021-01/commit-boundaries.json)。两项缺陷修复后应更新对应分组和输入哈希，再做各组验证；此处没有执行暂存或 commit。

| 顺序 | 中文 commit summary 草稿 | 原始文件数 | 边界及依赖 |
| --- | --- | --- | --- |
| 1 | 修复数据库迁移与 MySQL 自定义 CA 连接 | 23 | 数据库、profile、db-check、SQL 方言修复及相关测试；包含 C1 的后续修复 |
| 2 | 恢复独立发布源更新并完善重启确认 | 14 | 后端更新、handler、前端 API/设置页与三语文案；包含 C2 的后续修复 |
| 3 | 补齐本地构建与隔离验收工具 | 11 | 本地构建、SQLite/数据库/更新验收脚本，以及关闭验收时价格联网任务的入口；依赖前两组 |
| 4 | 建立普通 CI 门禁与手动发布流程 | 10 | R1 九文件及 Release Notes 草稿；脚本测试读取该草稿，应一起提交，避免中间提交缺文件 |
| 5 | 同步部署说明与发布验收记录 | 31 | README、CHANGELOG、Compose 显式版本门禁、计划及历史结果；本报告可随此组提交 |

后续先处理 C1/C2，再复核相应功能和候选证据的适用范围。涉及新的候选构建时使用新目录，保留既有冻结候选；不复用历史数据库执行授权。main 创建/集成、commit、push、默认分支和保护设置仍按会话授权门禁分别准备具体操作。首次获准推送只验证普通 CI，真实检查名以 Actions 返回为准。
