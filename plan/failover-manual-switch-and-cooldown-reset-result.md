# 故障转移手动切换与冷却重置实施结果

实施与验收日期：2026-10-08。已完成原方案第 1～3 节的行为，并纳入本轮追加的深色提示、成员行内布局及图标工具提示要求。未发现待解决的功能问题。

## 工作区与依据

工作目录为 `D:\tool\AI\octopus-solo`，分支为 `test`，接手与交付时 HEAD 均为 `26f43c9c4d9b86e12627f851edab6c7a63718aab`。接手时仅原方案文档未跟踪。采用用户提供的 AGENTS.md 指令；核对的祖先目录、仓库及改动目录未发现额外适用的 AGENTS.md。

需求依据为 [原方案](failover-manual-switch-and-cooldown-reset.md) 第 1～3 节及本轮追加的界面要求。交付为工作区中的局部修改和新增文件，尚未提交 Git。

## 实现内容

| 范围 | 实现 |
| --- | --- |
| Relay 运行态 | 新增 `route_actions.go`，提供独立的选择和解除冷却操作。自然到期与人工解除共用结束冷却逻辑，重置指定成员的连续失败数、旧退避和请求内尝试周期，保留历史错误、累计信息及其他成员状态。 |
| 到期与并发 | 分组调度最早冷却到期任务，读取、选路、准入和结算同时补做过期处理。成员 Epoch、选路版本、探测持有者、定时器版本和运行态身份分别校验。健康的当前成员不因其他成员结束冷却而被抢占。 |
| 恢复与等待 | 独立保存待真实成功验证的状态，保留恢复探测互斥及渠道容量保护。解除和到期唤醒等待请求；后续失败从新预算重新累计。 |
| 接口 | 新增 `POST /api/v1/group/route/:id/select` 和 `POST /api/v1/group/route/:id/release-cooldown`，接收 `{ item_id, version }`。检查最新模式、成员归属、可用性和版本；成功返回完整分组，冲突返回 `409` 及最新快照。 |
| 快照与缓存 | 公开字符串形式的 `version`、`config_version`、`selection_version` 和 `cooldown_versions`。列表、详情、操作响应和 SSE 共用合并逻辑；覆盖在途旧读取、旧配置和先于配置到达的运行态事件，使用 BigInt 比较版本。 |
| Webhook | 增加恢复判定屏障。到期、解除、计数清零或人工选择不会借用旧成功记录发恢复通知；真实完整成功满足既有条件后仍会通知并去重。 |
| 两个界面入口 | 共用成员操作 hook 和确认组件。卡片选择只改变选路；日志详情成功选择后仅对点击时仍在等待响应的原请求轮次调用中断。重复点击当前项、解除、保持及切换失败均不额外中断。 |
| 追加界面要求 | 确认按钮默认隐藏，仅在点击冷却成员后出现在该成员行中间，卡片同步隐藏“×”。钥匙图标表示解除冷却，锁图标表示保持冷却，悬停和键盘聚焦均显示文字工具提示。按钮各为 24 px，合计占宽 52 px；提交时以同尺寸加载图标反馈。确认、提交和关闭不增加行高，窄行保留成员名称。日志使用相同布局。提示组件使用页面解析后的主题，验收页也复用该组件。三份 locale 同步更新。 |

主要新增文件：

- `internal/relay/route_actions.go`
- `internal/relay/route_actions_test.go`
- `internal/relay/failover_actions_test.go`
- `internal/server/handlers/group_route.go`
- `internal/server/handlers/group_route_actions_test.go`
- `web/src/api/group-cache.ts`
- `web/src/components/modules/group/useMemberActions.ts`
- `web/src/components/modules/group/CooldownConfirmation.tsx`

其余局部修改位于 Relay 的 `route.go`、`health.go`、`handler.go`，分组 handler、Webhook 服务及相关测试，前端分组 API、查询配置、卡片、成员列表、状态标记、日志详情、Sonner 和三份语言文件。

## 实际执行的检查

Go 检查使用仓库已有缓存，仅在命令进程中设置：

```powershell
$env:GOCACHE = 'D:\tool\AI\octopus-solo\.tmp-go-cache'
$env:GOMODCACHE = 'D:\tool\AI\octopus-solo\.tmp-go-mod'
```

| 检查 | 实际结果 |
| --- | --- |
| `go test ./internal/relay ./internal/server/handlers ./internal/webhook -count=1 -timeout=180s` | 修改前基线及实现后的包测试均通过。 |
| `go test ./... -count=1 -timeout=180s` | 全量通过；Relay 53.137 s、handlers 4.559 s、Webhook 7.050 s，其余包通过或没有测试。此后补充的测试由最终 race 包测试覆盖。 |
| `go test -race ./internal/relay ./internal/server/handlers ./internal/webhook -count=1 -timeout=180s` | 两轮通过。包含最终后端测试的最后一轮：Relay 56.443 s、handlers 8.772 s、Webhook 8.573 s。 |
| `go vet ./internal/relay ./internal/server/handlers ./internal/webhook` | 最终检查通过，无输出。 |
| 修改及新增 Go 文件的 `gofmt -l` | 无输出，格式检查通过。 |
| `pnpm --dir web lint` | 最终界面修改后通过。 |
| `pnpm --dir web build --outDir "$failoverIconsBuildDir" --emptyOutDir false` | 最终钥匙图标修改后通过，含 `tsc --noEmit`；Vite 转换 4101 个模块，构建阶段 5.59 s。 |
| `git diff --check` | 通过，仅有 Git 的 LF/CRLF 转换提示。 |
| 独立 Relay 进程生命周期 | 编译本地 helper 后运行两次。第二个进程初始选择为空，拒绝首个进程的旧选择令牌，接受新令牌，且进程版本递增。 |
| 浏览器交互 | 隔离 Edge 中挂载真实分组卡片、日志详情、query/cache 和 hook，以本地模拟 API 验证操作次序、并发、乱序、主题、尺寸及键盘交互；最终运行时异常记录为空。 |

最终前端产物目录：

`C:\Users\INK-LO~1\AppData\Local\Temp\octopus-failover-icons-5db829c385544bb0926e89392d203760`

进程生命周期 helper 的构建命令为：

```powershell
go build -o build/failover-acceptance-20261008/route-lifecycle.exe ./build/failover-acceptance-20261008/.route-lifecycle
```

实际连续调用生成的程序两次，第二次以首次返回的版本作为参数；断言初始选择、旧版本拒绝、新选择成功及进程版本递增，全部通过。后端请求回归使用 `httptest` 本地上游，浏览器验收使用模拟 API。

## 验收矩阵

| 编号 | 实际验证及结果 |
| --- | --- |
| A01 | API、浏览器选择及 `TestCooldownCompletionRenewsHealthWithoutPreemptingCurrent` 通过：可从 B 切回 A，保持故障转移，A 再达阈值后仍选备用。 |
| A02 | API 重复选择保持版本；卡片和日志重复点击当前项均无额外提交或中断。通过。 |
| A03 | 浏览器打开确认、Tab/Enter 选择保持、Escape 关闭；无 API 操作，原冷却和选择保留。通过。 |
| A04 | 共用状态测试和接口真实本地请求验证计数/退避重置；卡片、日志解除均只有 release 操作，不主动选择、不发送测试请求、不调用 stop。通过。 |
| A05 | 选路补做过期及空闲定时器测试通过；A 到期归零，B 持续作为当前成员。 |
| A06 | 新周期阈值 2 的首次失败计 1、第二次冷却，指数等待重新计算；阈值 1 首次新失败冷却。通过。 |
| A07 | 自然到期和人工解除均覆盖旧指数退避晚于冷却截止的情况。通过。 |
| A08 | 普通退避后计数仍为 1，下一次有效失败能达到阈值。通过。 |
| A09 | `TestFailoverReleaseWakesSameRequestWithFreshAttemptBudget` 及恢复预算回归通过，同一入站请求重新获得成员预算。 |
| A10 | 恢复准入互斥、取消/未发送释放资格、容量排队及共享截止复核测试通过。 |
| A11 | 旧周期成功/失败不修改新周期、不释放新探测；旧探测成功不抢回人工选择；旧定时器不结束新冷却。通过。 |
| A12 | 并发 API 操作仅允许一个预期选择版本成功；旧确认不能解除新冷却。浏览器提交防重、冲突快照回填和旧确认失效通过。 |
| A13 | 实际保留旧列表/详情读取至新选择到达后才完成，随后投递旧快照和旧 SSE，均不回退。运行态先到、旧配置、大整数版本和 SSE 断线重连补取也通过。 |
| A14 | 全冷却下的自然等待及人工解除唤醒真实请求测试通过，沿用渠道容量和取消检查。 |
| A15 | `TestFailoverCardSelectionPreservesSentRequestAndReroutesCapacityWaiter` 通过；浏览器卡片选择没有 stop 操作。 |
| A16 | 后端原轮次中断回归通过。浏览器按 select → stop 次序提交；选择在途时日志由第 5 轮更新到第 6 轮，仍仅中断点击时的第 5 轮。 |
| A17 | `TestStreamingDoesNotFailoverAfterFirstEventCommit` 和浏览器 committed 日志场景通过：只有选择操作，流不中断、不重放。 |
| A18 | `TestCooldownReleaseIsScopedToOneGroupMember` 通过，同渠道其他成员、其他分组的冷却和失败数保留。 |
| A19 | 禁用渠道、禁用凭据、移除成员、改为手动和删除分组后，两个接口均按最新配置拒绝旧操作。浏览器成员失效关闭确认并恢复列表焦点。通过。 |
| A20 | `TestFailoverCooldownResetCannotReusePreviousRecovery` 及既有通知测试通过：无新成功时不恢复，真实成功后正常通知并去重。 |
| A21 | 两个入口确认区、键盘进出、到期自动关闭、焦点恢复、拖拽和移除均通过，无嵌套按钮。最终行内布局中卡片行高为 46 px；日志行展开前后均约 51.66 px。320 px 窄卡片能保留模型名，提交期间尺寸稳定。 |
| A22 | 手动模式、连接失败、响应超时、非流式失败、流式中断、客户端写入失败、取消和未发送的既有回归通过；浏览器手动当前项点击仍可取消选择。 |
| A23 | 两个独立进程验证重启隔离和旧令牌拒绝；配置重建、删除、备份处理及旧定时器回归通过。浏览器整页刷新保留服务端当前选择。 |

追加主题要求单独验收：已显示的提示从深色切换到浅色、再切回深色，主题属性和实际背景颜色均同步变化。

图标调整后补做了前端 lint、类型检查和构建，并在两个入口验证钥匙/锁图标、52 px 占宽、工具提示、键盘保持与 Escape、焦点恢复、固定尺寸加载状态及防重。日志解除仍只有 release 操作，独立选择后才中断原轮次；全部通过。工具提示切换时的检查以处于打开状态的浮层为准，避免命中退出动画中的旧浮层。

## 界面记录与工具情况

- [分组成员行内图标](../build/failover-acceptance-20261008/group-inline-cooldown-icons-dark.png)
- [320 px 窄卡片](../build/failover-acceptance-20261008/group-inline-cooldown-icons-narrow.png)
- [日志成员行内图标](../build/failover-acceptance-20261008/log-inline-cooldown-icons-dark.png)
- [深色提示](../build/failover-acceptance-20261008/toast-dark-theme.png)

默认 Go 缓存目录起初权限不足，改用上述仓库已有缓存后检查通过。单独执行 `pnpm --dir web exec tsc --noEmit` 曾无法找到命令，最终正式 build 内的 `tsc --noEmit` 已通过。浏览器工具尝试存在超时，随后使用本地隔离 Edge/CDP 完成验收。临时验收页热更新曾出现重复挂载及 DnD 提示，整页重载后重新验证了焦点、拖拽、日志轮次和缓存场景。

临时验收材料保留在 Git 忽略的 `build/failover-acceptance-20261008/` 中，包括模拟服务、浏览器控制器、进程 helper 和截图。验收浏览器及本地测试服务均已退出。剩余功能问题：无已知问题。
