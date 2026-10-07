# M3 自动化实现与验收记录

日期：2026-10-06。基线：`c451f6e6f046e0b8c9556ac65a5d5d6cf39bb766`（M2）。

范围依据：`plan/versions/v0.3.0-automation.md`。本次批量修改和新增数据库结构已取得用户针对 `plan/m3-automation-execution.md` 的明确授权。

后续审阅修复：用户已授权处理 `plan/m3-automation-review.md` 的五项问题，修复行为与复验记录见 `plan/m3-automation-review-result.md`。以下首次验收记录保留；最新接口将统计中的刷新正文拆为摘要与单渠道详情。

## 交付行为

- V030-01：全局渠道自动更新开关、分钟周期、默认自动添加分组成员选项，以及渠道跟随开关。总开关和自动添加默认关闭；周期默认 1440 分钟，支持 1–10080 分钟。旧渠道缺省跟随，显式关闭可保存。
- V030-02：独立调度循环逐个处理已启用且跟随全局的渠道。手动和自动刷新复用执行锁、配置快照复核、事务及分组同步服务，重复手动触发返回 HTTP 409。失败最多追加两次重试，分别等待 30、60 秒；取消、任务冲突及配置过期不立即重试。
- V030-02：持久化最近一次刷新来源、开始/结束时间、状态、逐 Key 结果、刷新前后模型数及脱敏错误摘要。运行标记只保存在内存，重复触发不会覆盖当前任务记录。渠道删除后清理诊断，迟到任务不会重新生成孤立记录。
- V030-03：渠道共享 FIFO 并发队列，`max_concurrency = 0` 表示不限。跨分组、跨 Key 的请求共用额度，流式请求持续占用到流结束；重试、冷却和选路等待不占用额度。排队支持取消，配置变化会唤醒等待者；调低上限时保留在途请求并暂停新增放行。
- V030-04：设置页、渠道高级设置和卡片接入上述能力。卡片显示占用/等待数、刷新时间及逐 Key 结果；渠道统计每 5 秒刷新。简中、繁中、英文文案一致。

自动刷新成功后，同步现有路由通知、管理端分组 SSE 和模型价格维护。失败时保留旧模型、授权、分组成员和路由状态。自动刷新本身不申请 Relay 调用额度。

## 文件范围

| 范围 | 文件 |
| --- | --- |
| 模型与数据库 | `internal/model/channel.go`、`internal/model/channel_automation.go`、`internal/model/setting.go`、`internal/db/db.go` |
| 配置、刷新和兼容 | `internal/op/channel.go`、`internal/op/channel_refresh.go`、`internal/op/channel_automation.go`、`internal/op/channel_events.go`、`internal/op/setting.go`、`internal/op/backup.go` |
| 调度 | `internal/task/init.go`、`internal/task/channel_update.go` |
| Relay 与管理接口 | `internal/relay/handler.go`、`internal/relay/channel_concurrency.go`、`internal/relay/channel_refresh.go`、`internal/server/handlers/channel.go` |
| 前端 | `web/src/api/{channel,setting}.ts`、`web/src/components/modules/channel/{Card,Form,state}.tsx/ts`、`web/src/components/modules/setting/{Automation,index}.tsx`、三份 locale |
| 测试 | `internal/db/channel_automation_test.go`、`internal/op/channel_automation_test.go`、`internal/task/channel_update_test.go`、`internal/relay/channel_concurrency_test.go`、`internal/server/handlers/channel_automation_test.go` |
| 记录 | 本文及 `plan/m3-automation-execution.md`、路线图中的验收入口 |

## API 和数据兼容

复用 `/api/v1/setting/list` 与 `/api/v1/setting/set`，新增三个键：

| 键 | 默认值 |
| --- | --- |
| `channel_auto_update_enabled` | `false` |
| `channel_auto_update_interval_minutes` | `1440` |
| `channel_auto_update_auto_add_groups` | `false` |

渠道详情与创建/更新请求增加 `follow_global_auto_update`、`max_concurrency`。渠道统计响应增加上述配置、`refresh` 摘要和 `concurrency`；完整诊断改由 `GET /api/v1/channel/refresh-status/:id` 返回。原有手动刷新接口及 `auto_add_groups` 请求选项继续可用，`Automatic` 标记仅供服务端使用，不接受客户端 JSON 设置。

现有 GORM AutoMigrate 为 `channels` 增加两列，并创建 `channel_refresh_records` 表；不需要数据搬迁或额外编号迁移。旧行升级后并发不限；二次启动保留用户显式关闭跟随和已保存上限。新增配置包含在原有渠道备份中，旧备份缺字段时补默认值，导入时校验新设置和并发上限。刷新记录是运行诊断，不进入备份格式；备份编号沿用 6，格式身份撞号问题继续由 R1 的既有设计处理。

自动更新使用独立调度器，保留现有模型价格更新任务。设置广播采用可关闭的通知通道，更新不会因调度器正在执行任务而丢失；全局设置变化重设计时器并取消旧批次，渠道退出跟随会取消对应自动请求。

## 自动检查

Go 命令使用当前进程的 `GOCACHE = <workspace>/.tmp-go-cache` 和已有系统模块缓存，`GOPROXY=off`；没有修改持久环境配置。首次尝试工作区模块缓存时因缺少当前 AxonHub 版本而未能开始测试，切换到已存在的系统缓存后完成检查。

| 命令 / 检查 | 结果 |
| --- | --- |
| `go test ./... -count=1` | 通过 |
| `go vet ./...` | 通过 |
| `go test -race ./internal/task ./internal/op ./internal/relay ./internal/server/handlers -count=1` | 通过；后续修正后重新检查对应包 |
| Relay 并发、流式取消、刷新与调用同时运行、故障转移的定向 `-race` 检查 | 通过 |
| `pnpm --dir web lint` | 通过 |
| `pnpm --dir web build --outDir "$env:TEMP/octopus-v030-build" --emptyOutDir false` | 通过，含 TypeScript 检查；仅有构建插件耗时提示 |
| 三份 locale 键一致性 | 通过，每份 503 个键 |
| `git diff --check` | 通过 |

新增回归覆盖设置默认值与非法值、可靠通知、周期/开关热更新、单批次执行、退避上限、渠道退出取消、手动与自动冲突、失败原子性、脱敏记录及重载、旧表升级和旧备份导入、跨分组并发上限、上限降低、不限流计数、排队取消、禁用/删除/路由变更唤醒、流式结束与取消、故障转移资源释放。

回归中发现并修复了未实际排队时提前发布等待状态的问题；现在仅在额度不足时发布等待，原有重试次数、等待截止时间和错误计数测试继续通过。

## 本地端到端验收

使用临时 SQLite 数据库、`httptest` 上游、实际管理服务器及一分钟真实调度周期，前端运行当前 Vite 源码。隔离实例包含一个默认跟随渠道、一个退出跟随渠道，以及一个严格匹配分组。

| 场景 | 观察结果 |
| --- | --- |
| 总开关默认关闭 | 创建实例后无自动模型请求 |
| 浏览器启用自动更新 | 设置 API 持久值变为 `true`，下一周期自动执行 |
| 新模型与严格匹配 | 跟随渠道取得 `m3-model` 和 `m3-model-extended`；分组仅追加 `m3-model`；退出渠道未刷新 |
| 上游 HTTP 503 | 最近结果显示失败和逐 Key 摘要；已有 2 个模型和 1 个分组成员保留 |
| 上游成功返回空列表 | 渠道模型和分组成员清零，分组实体仍存在 |
| 编辑渠道 | 在高级设置中将退出渠道重新设为跟随、并发上限设为 3；保存后 API 值一致 |
| 重新加入 | 后续周期两个渠道都取得 2 个模型，无需重启 |
| 全局关闭后手动刷新 | 全局设置保存成功；手动刷新返回 HTTP 200 且 `success=true` |
| 刷新记录展示 | 渠道卡片 DOM 包含自动来源、时间、数量变化及失败诊断 |

浏览器交互使用 DOM 操作并与 API 保存值核对；截图工具持续返回“tab has not painted yet”，未取得可用截图，因此不把本次检查记为像素级视觉验收。浏览器控制台未出现应用错误。

## 限制与后续

SQLite 的旧结构升级和备份兼容已实测。当前环境未配置 MySQL/PostgreSQL 实例，尚未执行这两种数据库的迁移实测，需在 R1 回归中补齐。

本次交付停留在工作区，进入 R1 前可依据上述结果审查差异。对外版本号及发布操作按路线图在 R1 单独处理。
