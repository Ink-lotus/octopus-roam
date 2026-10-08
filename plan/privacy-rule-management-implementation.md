# 确定性隐私保护实施验收记录

验收日期：2026-10-08。对应[实现方案](privacy-rule-management.md)，代码对照基线为 `05c46b2`。功能、规定检查与浏览器验收均已完成。

## 实现范围

设置页右列的日志设置之后新增独立“确定性隐私保护”卡片，包含实验性说明、总开关、固定高度规则列表、添加按钮和展开面板。七条内置规则支持详情、逐条启停和样例预览；自定义规则支持字段名、固定文本、Go 正则三种类型，以及创建、编辑、启停和删除。系统卡片中的原总开关已迁入新卡片。

预览支持纯文本、JSON、URL 和 Header，以及“测试当前规则”和“测试已启用规则”两种模式，显示脱敏结果与各规则命中次数。总开关关闭时仍可管理和预览。保存失败保留草稿，修订号冲突刷新规则后保留草稿；样例、模式或草稿变化会使旧结果失效，迟到响应不会覆盖新结果。预览样例仅用于本次计算，不进入共享查询或 mutation 缓存。

规则配置使用版本、服务器修订号和固定内置 ID，严格校验类型、未知字段、ID、容量和正则，拒绝可产生零长度匹配的表达式。规则在发布时编译，保存成功后原子发布；数据库以旧配置值作比较条件，过期更新返回 `409`。配置导入先校验，再在事务中恢复总开关和规则；兼容缺少规则配置的旧备份，失败时保留原配置。

诊断实例固定持有创建时的策略快照。规则启停按发现来源控制，保持跨 Header、URL、JSON 与错误文本的敏感值传播。执行器保留 JSON 格式、重复键与普通数字精度，支持确定的重叠顺序，并保护原始及 URL 编码的已有掩码。Relay 正文缓存和 Webhook 事件、模板渲染、重试复用各自的快照。

配置编辑接口继续使用独立固定掩码策略。上游认证信息、业务正文及响应内容保持原有传输行为。同一进程内相同值的掩码稳定，重启后掩码可能变化，沿用现有 HMAC 约定。

## 主要文件

| 位置 | 实现内容 |
| --- | --- |
| [rules.go](../internal/privacy/rules.go)、[sources.go](../internal/privacy/sources.go)、[execute.go](../internal/privacy/execute.go)、[preview.go](../internal/privacy/preview.go) | 目录、配置校验、来源登记、规则执行与预览 |
| [redact.go](../internal/privacy/redact.go) | 诊断快照入口与固定策略入口 |
| [op/privacy.go](../internal/op/privacy.go)、[privacy_backup.go](../internal/op/privacy_backup.go) | 单条管理、并发冲突、持久化、缓存与备份发布 |
| [model/setting.go](../internal/model/setting.go)、[op/setting.go](../internal/op/setting.go)、[op/backup.go](../internal/op/backup.go) | 设置默认值、通用写入限制及备份接入 |
| [handlers/privacy.go](../internal/server/handlers/privacy.go)、[handlers/setting.go](../internal/server/handlers/setting.go) | 已认证的管理和预览接口，字段错误与容量限制 |
| [relay/privacy.go](../internal/relay/privacy.go)、[webhook/service.go](../internal/webhook/service.go)、[webhook/template.go](../internal/webhook/template.go) | 凭据来源和通知生命周期快照 |
| [api/privacy.ts](../web/src/api/privacy.ts)、[api/client.ts](../web/src/api/client.ts)、[api/setting.ts](../web/src/api/setting.ts) | 类型、共享规则缓存、写入状态、错误详情和预览请求 |
| [Privacy.tsx](../web/src/components/modules/setting/Privacy.tsx)、[System.tsx](../web/src/components/modules/setting/System.tsx)、[设置页入口](../web/src/components/modules/setting/index.tsx) | 独立卡片、规则详情、管理表单与预览 |
| [morphing-dialog.tsx](../web/src/components/ui/morphing-dialog.tsx) | 嵌套弹层键盘处理、焦点循环与关闭恢复 |
| [简体中文](../web/src/locales/zh_hans.json)、[繁体中文](../web/src/locales/zh_hant.json)、[English](../web/src/locales/en.json) | 三语文案与主要验证错误 |

## 自动检查

以下检查均在最终功能代码上通过。文档收尾未再修改功能代码。

| 检查 | 结果 |
| --- | --- |
| `go test ./... -count=1` | 全量通过 |
| `go vet ./...` | 通过 |
| `go test -race ./internal/privacy ./internal/op ./internal/relay ./internal/webhook -count=1` | 全部通过，无数据竞争报告 |
| `pnpm -C web lint` | 通过 |
| `pnpm -C web build --outDir $privacyRulesBuild --emptyOutDir false` | TypeScript 检查与 Vite 构建通过 |
| 三份 locale JSON 解析及隐私键一致性 | 通过；各有 110 个隐私文案键 |
| `git diff --check` | 通过 |

后端检查环境为 Windows amd64，启用现有 CGO/GCC。默认 Go 缓存目录存在权限限制，命令会话内将 `GOCACHE` 指向系统临时目录中的 `octopus-privacy-go-cache`，没有修改持久环境。前端输出使用新建的系统临时目录 `octopus-privacy-rules-<随机标识>`，构建脚本已包含 `tsc --noEmit`。

## 自动化行为覆盖

| 范围 | 验证内容与测试位置 |
| --- | --- |
| 配置与规则执行 | 七条内置来源独立启停、异常格式回退、全部关闭返回原文、字段/固定文本/正则、Unicode/引号/转义、短凭据、重叠与命中计数、前置回显、重复键、精确数字、原始和编码掩码幂等；见 [rules_test.go](../internal/privacy/rules_test.go) 和 [execute_test.go](../internal/privacy/execute_test.go) |
| 预览 | 两种模式、草稿替换、与正式执行一致、容量和类型错误、错误中不包含样例；见 [preview_test.go](../internal/privacy/preview_test.go) |
| 管理与保存 | CRUD、修订冲突、严格操作结构、缓存重载、非法配置与保存失败保留有效策略；见 [op/privacy_test.go](../internal/op/privacy_test.go) |
| 备份 | 导出导入往返、重新生成修订号、旧备份保留目标规则、非法正则、重复设置、未知版本/内置 ID、失败无部分写入；见 [privacy_backup_test.go](../internal/op/privacy_backup_test.go) |
| HTTP 接口 | 认证、CRUD、预览、`400` / `404` / `409` / `413` 以及配置凭据掩码；见 [handlers/privacy_test.go](../internal/server/handlers/privacy_test.go) |
| 请求与通知 | 请求进行中改变规则仍保持旧快照，新请求采用新规则；正文缓存并发读取；Webhook 从准备到重试保持快照；见 [relay/privacy_rules_test.go](../internal/relay/privacy_rules_test.go)、[diagnostic_body_test.go](../internal/relay/diagnostic_body_test.go)、[webhook/privacy_rules_test.go](../internal/webhook/privacy_rules_test.go) |

全量测试同时覆盖既有本地模拟上游用例，确认认证、业务正文及响应传输不受诊断脱敏影响；既有配置编辑凭据掩码用例也已通过。

## 浏览器验收

使用独立临时 SQLite 实例、本地后端和 Vite 代理执行，样例均为测试数据。桌面视口为 `1280 × 800`，窄屏为 `375 × 812`。

| 场景 | 结果 |
| --- | --- |
| 卡片与默认值 | 独立卡片仅一张；新实例总开关关闭、内置规则 `7 / 7` 启用；系统设置原位置已迁出 |
| 总开关与逐条开关 | 保存成功，关闭总开关仍可管理与预览，卡片与展开列表同步 |
| 自定义规则 | 创建、编辑、启停保持顺序；取消删除保留规则，确认删除后两处列表同步移除，刷新后仍不存在 |
| 内置详情 | 展示固定 ID、匹配对象、识别方式、回退边界及适用范围；URL 样例预览命中用户名和密码共 2 次，再次预览输出不变且无新增命中 |
| 草稿和错误 | 非法正则保存失败显示字段错误并保留草稿；模拟旧修订冲突后刷新规则、保留草稿，再次保存成功；读取失败显示错误并禁用相关操作，恢复后正常 |
| 预览更新 | 修改样例使旧结果失效；注入迟到响应，未覆盖后续结果 |
| 嵌套弹层与键盘 | Escape 先关表单并回到原按钮，外层保持打开；点击内层外部同样只关内层；Tab 在外层循环；关闭外层后焦点回到展开入口并释放滚动锁 |
| 窄屏与三语 | 表单宽约 343 px、高度受 780 px 约束，页面宽度等于 375 px 视口，无横向溢出；英文、简体中文、繁体中文切换无缺失翻译 |
| 重启恢复 | 最终后端重启后，总开关、内置启停、自定义 ID、名称、类型、匹配内容和启停状态逐项一致 |
| 浏览器日志 | 未发现组件运行或缺失翻译异常；临时故障注入与确认函数均已恢复 |

## 性能对比

环境：Windows amd64，Intel Core i7-12650H。复用既有 `BenchmarkReplaceKnown`、`BenchmarkRedactorBody` 的相同输入，当前默认配置使用新策略执行器；新增 [BenchmarkRuleLimit](../internal/privacy/redact_bench_test.go) 覆盖 64 条字段、固定文本和正则混合规则。下表为同机单次基准结果，适合比较这些样例，不代表所有业务负载。

| 基准 | 基线 ns/op | 最终 ns/op | 基线 B/op | 最终 B/op | 基线 allocs/op | 最终 allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| ReplaceKnown，无短凭据 | 1,432 | 2,828 | 2,132 | 2,398 | 5 | 13 |
| ReplaceKnown，含短凭据 | 58,298 | 4,418 | 7,008 | 4,605 | 23 | 15 |
| RedactorBody | 1,873,080 | 687,741 | 335,291 | 281,570 | 7,198 | 2,209 |
| 64 条混合规则 | — | 2,126,507 | — | 516,860 | — | 6,602 |

正文样例耗时约降低 63%，分配次数约降低 69%。不含短凭据的单独替换样例增加约 1.4 μs；达到规则上限的代表性正文约为 2.13 ms/op。规则在配置发布时编译，诊断正文继续复用已有缓存。
