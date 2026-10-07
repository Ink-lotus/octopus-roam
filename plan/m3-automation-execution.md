# M3 自动化实施清单

状态：用户已于本次会话明确授权「按清单完成 M3」，实现和验收记录见 `plan/m3-automation-result.md`。

基线：`c451f6e6f046e0b8c9556ac65a5d5d6cf39bb766`（M2 完成提交）。开工检查时工作区干净。

范围依据：`plan/versions/v0.3.0-automation.md` 和 `plan/version-roadmap.md`。

## 已核实的接入点

- `internal/op/channel_refresh.go` 已有公共刷新入口、单渠道执行锁、配置快照复核，以及渠道模型和分组成员的事务同步。自动任务复用这些入口。
- 当前逐 Key 结果仅随手动刷新响应返回，渠道卡片将结果保存在组件状态中；需要增加服务端最近刷新记录。
- `internal/task/task.go` 的周期更新使用无缓冲通道和非阻塞发送，更新可能被丢弃；新任务需要可靠接收配置变化，且同一轮不能重叠运行。
- `internal/relay/handler.go` 在单次上游调用开始后启动响应超时计时器；并发排队必须在上游计时前完成，并监听客户端取消和路由配置变化。
- 渠道编辑使用显式列清单，新增配置必须同时进入保存清单、读取缓存、备份导入校验和前端表单。
- 数据库启动时对 `model.Channel` 执行 GORM AutoMigrate。新增持久字段即涉及数据库结构变更，不能仅视为 Go 类型修改。

## 具体行为与兼容方案

| 项目 | 实施方案 |
| --- | --- |
| 总开关 | 新增 `channel_auto_update_enabled`，默认 `false` |
| 更新周期 | 新增 `channel_auto_update_interval_minutes`，默认 1440 分钟；校验为 1 至 10080 分钟整数；保存后重设下一次执行时间 |
| 自动添加分组成员 | 新增 `channel_auto_update_auto_add_groups`，默认 `false`；复用已有 `auto_add_groups` 和分组匹配规则 |
| 渠道跟随 | 新增 `follow_global_auto_update`；缺省跟随全局，显式关闭有效；总开关首次默认关闭，因此升级不启动刷新 |
| 自动选择 | 只刷新已启用且跟随全局的渠道；开关和渠道配置变化无需重启 |
| 刷新结果 | 保留最近完成时间、来源、状态、逐 Key 结果、模型数量变化和脱敏错误摘要；运行标记与完成记录分开，重复触发不覆盖正在执行任务的结果 |
| 失败重试 | 单轮顺序处理渠道；临时失败最多追加 2 次重试，初始退避 30 秒、再次 60 秒；取消、退出跟随、配置过期和任务冲突不立即重试 |
| 渠道并发 | 新增 `max_concurrency`，非负整数，默认 `0` 表示不限；跨分组、跨 Key 共享渠道额度 |
| 额度生命周期 | 发送上游前取得额度；上游响应完成、失败或取消时释放；重试、冷却和选路等待不占用额度 |
| 配置变化 | 删除、禁用、并发上限变化和路由切换唤醒等待者；降低上限时已在途请求继续完成，暂停新请求直到占用降至上限以下 |
| 诊断 | 在已有渠道管理响应中增加最近刷新记录和并发占用/等待数量，前端提供简要展示 |
| 数据兼容 | 渠道新增配置通过现有 AutoMigrate 增列；最近刷新记录使用独立状态模型和表。旧备份缺少新配置时补默认值，备份导出包含渠道配置；运行诊断不作为可恢复配置导出 |

最近刷新记录的持久化仅保存脱敏后的结果，不保存 Key 值或上游认证信息。启动时不会把此前的运行标记当成仍在执行。

备份格式编号沿用当前值；备份身份撞号的独立修复仍按 R1 文档处理。迁移验证使用临时数据库，核实 SQLite 的旧结构升级和旧备份导入；MySQL/PostgreSQL 未实测时明确记录限制。

## 任务定义

### M3/V030-01

- 产出定义：完成三个全局设置、渠道跟随字段、校验和热更新；总开关关闭时自动任务不发请求，手动刷新正常可用。
- 材料位置：`internal/model/{setting,channel}.go`、`internal/op/{setting,channel,backup}.go`、`internal/task/`、`internal/server/handlers/{setting,channel}.go`、`web/src/api/{setting,channel}.ts`、设置页和渠道表单。
- 改动边界：这些模块中的自动更新配置、任务注册和相关前端；不调整模型价格任务的设置语义。
- 必要检查：默认值、非法值、设置读写、注册与开关/周期热更新；`go test ./internal/task ./internal/op ./internal/server/handlers -count=1`；前端 lint 和构建。

### M3/V030-02

- 产出定义：自动任务顺序调用公共刷新服务，复用单渠道锁；所有启用 Key 成功才提交，失败保留旧模型及成员；记录最近结果并按规则退避。
- 材料位置：`internal/op/channel_refresh.go`、`internal/model/channel.go`、`internal/db/db.go`、`internal/task/`、渠道处理器、前端渠道 API 和卡片。
- 改动边界：新增刷新状态模型和存取逻辑；公共服务仅扩展任务状态、取消和结果记录，保留 M2 的匹配与事务语义。
- 必要检查：手动/自动重复触发、多 Key 失败、空列表提交、严格匹配追加、取消、配置变更、脱敏与结果查询；`go test ./internal/op ./internal/task ./internal/server/handlers -count=1`。

### M3/V030-03

- 产出定义：渠道并发额度和可取消等待队列；流式、非流式、错误、取消、重试及故障转移路径都正确释放；返回占用数和等待数。
- 材料位置：`internal/model/channel.go`、`internal/op/channel.go`、`internal/relay/{handler,state,upstream}.go`、渠道处理器及表单。
- 改动边界：新增渠道并发控制文件，局部接入每轮发送与关闭路径；不修改故障转移冷却契约。
- 必要检查：延迟上游的并发上限、无限制、排队取消、流生命周期、动态上限、禁用/删除/切换和重试无泄漏；`go test ./internal/relay ./internal/server/handlers -count=1`、对应 `-race` 检查、`go vet ./...`。

### M3/V030-04

- 产出定义：完成组合回归、本地可控上游验收和结果文档；列出修改文件、接口/数据变化、兼容策略、命令结果与遗留事项。
- 材料位置：上述实现及测试、`plan/versions/v0.3.0-automation.md`。
- 改动边界：必要的集成测试、三份 locale 及 `plan/m3-automation-result.md`；按后端实现及 Go 检查、前端实现、完整回归的顺序推进。
- 必要检查：`go test ./... -count=1`、`go vet ./...`、相关包 `-race`、`pnpm --dir web lint`、`pnpm --dir web build --outDir "$env:TEMP/octopus-v030-build" --emptyOutDir false`、locale 键一致性、`git diff --check`，以及本地可控上游端到端验收。

## 本次授权操作

目标：完成以上 M3 四项任务。

命令/工具：使用 `apply_patch` 对上述范围做局部修改并新增实现、测试及验收文档；`gofmt -w` 格式化本次 Go 文件；执行上述 Go 和 pnpm 验证命令。数据库结构仅在测试临时数据库和本地隔离验收实例中执行 AutoMigrate；交付代码在将来启动时会给已有渠道表增列并创建刷新状态表。

影响：跨后端、前端和三份 locale 的批量修改；新增渠道持久配置和刷新状态存储；更新本地构建缓存及临时构建产物。具体文件以本清单的模块范围和最终 diff 为准。

本次授权范围不含文件删除、整文件覆盖重写、依赖变动、Git 提交/推送、远程仓库操作、环境配置、CI 或生产数据库操作。
