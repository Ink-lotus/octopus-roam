# 2026-10-15 A3 收口与本地补充验收

本轮按用户要求执行第 1、2 项：修订 A3 文档与交接材料，补齐 Windows 生产更新链路、关键 race、最终 Windows 包业务及浏览器状态证据。基线仍为 `test / 948de764ae9a034fd72899686d24dcc4ecad7fa5`，没有 commit 或远端操作。

当前候选：`build/release-local/a23-final-20261015-171713-428045a1/`，版本 `v0.1.0-local.a23-final-20261015-171713-428045a1`，构建时间 `2026-10-15T17:19:37+08:00`。本轮证据位于 `build/release-local/a23-20261015-163351-cb4858db/`，运行验收位于其中的 `final-acceptance/`。旧 A2 候选保留为历史证据。

结论：下表列出的本轮本地检查通过，日志详情正文已在后续可见浏览器中补齐。第三方许可证报告仍缺，L6.5 严格门禁未通过，当前候选不能公开发布。Docker、真实数据库、Unix/Android 和真实远端升级仍按各自环境与授权补齐。

## 第 1 项：A3 文档收口

- [发布计划](release-plan-v0.1.0.md)与[本地结果书](release-local-result.md)同步当前候选、验收矩阵、历史证据和待办；保留 A2 原始材料，纠正结果书误抄的 A2 哈希。
- Go 示例明确产品包集合、执行目录、`-mod=readonly`、退出码和进程环境恢复；不再使用非法的 `GOSUMDB=on`。本轮构建脚本保留调用方合法配置，不新增 `GOSUMDB=off`。
- [Docker 交接](docker-local-test-handoff.md)和[Compose 模板](docker-compose-local-test.yml)指向新候选，先复制到新 staging context 再补非空许可证报告。明确绝对测试目录、Linux 权限、context 清单、本地构建命令、localhost 端口、正常停止宽限和价格任务隔离。
- 增加 MySQL/PostgreSQL 无凭据占位配置；模型接口获取与本地价格预设的说明按实际调用链修正。
- 区分 L6.5 本地门禁与真实数据库、Docker、Unix 实机和远端发布；R0 工具/环境准备可以解除阻碍，不表示候选已经通过。

## 第 2 项：本地验收

下列路径以本轮证据根目录为基准；候选内证据另行标注。

| 用例 | 实际结果 | 证据与边界 |
| --- | --- | --- |
| 普通产品包 Go 回归 | 通过 | `logs/go-regression.log`；显式检查 `. ./cmd ./internal/... ./static`，不扫描历史 build 副本 |
| 发布条件 jsoniter 全产品包回归 | 通过 | 新候选 `checks/logs/go-jsoniter.log`；复用同轮前一个候选已完成结果，逐项核对 Go/web 输入相同，来源见 `checks/regression-provenance.json` |
| vet | 通过 | 新候选 `checks/logs/go-vet.log` |
| 五个关键包 race + jsoniter | 通过 | `logs/go-race-jsoniter.log`；relay/op/task/handlers/update 全部通过；最终候选核对相同 Go 输入后纳入证据 |
| 前端 ESLint / TypeScript / Vite | 通过 | 本轮 `logs/frontend-eslint.log`、`logs/frontend-typescript.log`；最终候选重新执行生产构建，候选另有冻结前端 ESLint |
| 更新状态决策 | 6 项通过 | `logs/update-state-tests.log`；检查实际生产 resolver，包括目标版本匹配、失败、超时和无关版本变化 |
| PS 7 与 PS 5.1 SQLite | 均通过 | 新候选 `checks/logs/sqlite-pwsh.log`、`sqlite-powershell.log`；独立空库、关联和禁用状态、设置、加密恢复、错误口令/损坏导入回滚、重启持久化和正常退出 |
| 原始最终 Windows ZIP | 通过 | `final-acceptance/runtime-acceptance/results.json`；解压前后核对 ZIP 与 binary 的冻结哈希 |
| 普通/流式请求 | 通过 | 原始最终包连接 loopback 模拟上游；核对正文、流式 Content-Type、分片及 [DONE] |
| 按序失败切换 | 通过 | 故障渠道只调用一次，随后成功渠道；日志目标渠道与错误次数匹配 |
| 客户端取消 / 日志 API 取消 | 均通过 | 上游连接实际关闭；SSE 状态进入 canceled；取消没有误计为渠道失败 |
| 日志/SSE 与精确统计 | 通过 | `business-evidence.json`：成功 3、失败 2、输入 9、输出 6；失败上游计 1 次，取消渠道故障计 0 次 |
| 正常退出与重新启动 | 通过 | Ctrl+Break 正常退出码 0；统计和 3 个分组持久化 |
| Windows 生产更新入口 | 三种场景均通过 | 浏览器 POST → Go 调度握手 → helper → 实际旧进程退出 → 替换/恢复 → 新 PID 与版本/API 状态/哈希确认；核对备份及设置保留 |
| 中文路径与配置参数 | 通过 | 运行目录、配置目录及配置文件含中文和空格；三种生产更新场景均使用这些路径 |
| 浏览器更新状态 | 三种场景均通过 | `final-acceptance/browser-visible-03/results.json`：真实 visible 的 Edge 页面，pending 时按钮禁用；成功显示新版本、回滚显示失败、30 秒未确认后按钮恢复；终态后 3.5 秒内没有短轮询 |
| 原始最终包页面交互 | 通过 | 前一 runner 的导航断言通过、详情断言超时；后续 `final-acceptance/browser-paseo-detail/browser-done.json` 在新实例完成登录、渠道/分组/主页/日志页及详情请求/响应正文，不改写旧 failed summary |
| 浏览器请求后的日志正文/API 统计 | 通过 | `final-acceptance/business-final-api.json`：请求体、响应体均含本轮合成内容；统计为成功 4、失败 2、输入 12、输出 8 |
| 浏览器日志详情请求/响应正文 | 通过 | `final-acceptance/browser-paseo-detail/browser-done.json`：真实 visible 页面打开日志、读取响应、通过原生点击切换请求体、读取请求并关闭；独立最终包实例统计成功 1、失败 0、输入 3、输出 2，正常退出 |
| 文档/脚本格式 | 通过 | `docs-static-validation.json`、`docs-format-validation.json`；链接、PowerShell 命令块、bash -n、YAML/JSON 解析；没有运行 Docker Compose schema/daemon |
| 候选与历史完整性 | 通过 | `final-hash-audit.json`：新候选 973 文件、487 冻结输入及 487 工作区输入全部匹配；历史 A2 969 文件和 485 输入全部匹配 |

Windows 链路的完整结果与进程收尾见 [runtime summary](../build/release-local/a23-20261015-163351-cb4858db/final-acceptance/runtime-acceptance/summary.json)、[results](../build/release-local/a23-20261015-163351-cb4858db/final-acceptance/runtime-acceptance/results.json) 和 [cleanup](../build/release-local/a23-20261015-163351-cb4858db/final-acceptance/runtime-acceptance/process-cleanup.json)。各浏览器原始失败记录继续保留，`browser-done.json` 明确记录通过范围和未完成项。 后续日志详情、页面交互和独立测试实例正常停止见 [browser detail](../build/release-local/a23-20261015-163351-cb4858db/final-acceptance/browser-paseo-detail/browser-done.json)、[detail summary](../build/release-local/a23-20261015-163351-cb4858db/final-acceptance/browser-paseo-detail/summary.json) 与 [detail cleanup](../build/release-local/a23-20261015-163351-cb4858db/final-acceptance/browser-paseo-detail/cleanup.json)，它们补齐早先 browser-done 记录的限制。

### 更新链路夹具的边界

普通/流式、取消、切换、统计及页面运行的是未经修改的最终 Windows ZIP。完整更新场景从同一份冻结源码构建 RC.1/RC.2 测试程序，仅以 Go overlay 注入 loopback 发布源和测试版本；生产 API、归档/哈希校验、调度、Windows helper、退出、替换和恢复逻辑保持实际实现。故障包是立即退出的合成程序；未确认包的实际版本仍为 RC.1。

这证明本地生产更新链路的行为，不证明真实 GitHub Release、下载权限或两个远端版本之间的在线升级。Paseo 宿主曾将页面标为 hidden，暂停后台版本轮询；三种状态的最终证据来自保留自身沙箱、独立 profile 的本机 headless Edge，实际 visibility 为 visible，没有伪造可见性。为补齐完整成功和回滚过程另建测试实例，使用相同 RC binary/ZIP，原始实例及失败记录未回滚改写。

### 本轮补充修正

1. `internal/update/core_apply_handshake_test.go` 在清理时再次 Wait，与生产 reaper 争用进程等待状态。去掉测试中的第二次 Wait 后，五包完整 race 通过；生产退出等待机制保留。
2. `web/src/api/update.ts` 的普通模块变量变化未通知 React，POST 后 pending 提示和按钮状态不及时。改用已有 Zustand 状态订阅；完成只接受本次目标版本，浏览器已验证三种终态。
3. `scripts/build-local.ps1` 不再强制关闭 GOSUMDB，继续使用离线缓存及 `-mod=readonly`。

这些源码/构建输入变化已进入本次新候选。没有新增依赖、修改 CI 或接触真实数据库。

早先的浏览器 runner 把尚未完成切换的页面文字作为就绪条件，日志详情断言失败；另一次请求体标签的 JavaScript click 没有触发 Tabs 的原生指针事件。后续检查等待日志页与卡片就绪，并用浏览器原生点击请求体标签后，通过实际页面正文及对应 API 路径确认。Edge 的两次审批超时没有执行补查；最终使用现有 Paseo 浏览器完成，不降低浏览器沙箱或伪造检查结果。

## 新候选哈希

| 平台 | 二进制 SHA-256 | ZIP SHA-256 |
| --- | --- | --- |
| Windows amd64 | `7bdd0d9194dea63e88c3a5b305455cb9429376f451e61cd0cb6c55a0d511675f` | `9f9eb3bac1485a097f2f693dffb298febfbe020a623ce5346e38241a244b12e6` |
| Linux amd64 | `9826c5e3165364eb786fd8bae3bdb9785a9c1cde0aa2bcafc90de81872d22bb5` | `9560061b61db03b4ada5bc55ea989659c79d100583fd0eac224ccabf1e612817` |
| Linux arm64 | `fb4cf22cc5718c629cf9a88a33aaa142efdf3430116bfb293f36a980525ceb27` | `88021ec4d46c5b246cd49d6056bfd4e2895f66c6c690617d65f3b8f9160037d5` |
| Linux arm/v7 | `7aa9453ef70825ec4cbef45191bf3e6f871959f4da82585f4cfb74397575c28c` | `08660a76e66f5fdf17991be582adb431677932abe17cb2f0121c3f7aa0b065b2` |
| Linux 386 | `0d5f0d398094132d8a4da1dcbc24148e6e45bdb124228cf53e3b3739ca19908b` | `3485365f898e6ee9fb62367c0b226cc1c6f4681f1633c628400c2abc027496ee` |
| macOS amd64 | `6b282c9ead079c8563c470390f47ce0a68e53ee0768f88c529c23aafce167e86` | `5a5269f12bbb8660066b9650b53c9d27390bffbf1727838e726eba9a089bfd30` |
| macOS arm64 | `d540e5743d982b990754175d9f2a0f04ef7ee2ca174a4360a1b6c9e2d4cfc535` | `cf79f88c71a3f5465f37080ac991c42ed9724699d1899167c6403697fced6973` |

七个 ZIP 均验证 Unix creator、程序 `0100755`、普通文件 `0100644`、无路径穿越/重复路径/符号链接，并核对解压清单。Linux/macOS 仍仅为交叉编译与归档证据。Docker context 为四个 Linux 目标，状态 `incomplete-license-report`，没有镜像或镜像 tar。

## 保留的材料与后续范围

本轮停止在用户要求的两项内。测试程序和 helper 的退出检查见证据；合成 SQLite、浏览器 profile、失败尝试和历史候选全部保留，不作发布附件。已有仓库外实例 `D:\tool\AI\octopus-roam-runtime\octopus-roam.exe` 保留，收尾时 PID 为 2828。

许可证工具/非空报告、Docker C1–C9、MySQL/PostgreSQL 完整集成、Unix 实机更新和 Android/NDK 仍待具体环境。CI、分支集成、标签、Release、registry、真实下载/在线升级和 GA 按原计划单独授权。本轮没有安装工具/依赖、删除历史材料、commit、Git/远端状态变更或真实数据库操作。
