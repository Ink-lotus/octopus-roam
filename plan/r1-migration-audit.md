# R1 数据迁移与备份兼容审计

日期：2026-10-07。审计起点：M3 提交 `6fa0722`。R1 修改备份文件格式及导入实现，不新增编号迁移，不修改模型的数据库列定义。`001`–`013` 历史迁移保持原样。

## 字段与默认值

| 范围 | 新增/独立产品字段 | 旧数据处理 |
| --- | --- | --- |
| 设置 | `group_route_defaults` | 缺键由设置缓存初始化补 `DefaultGroupRelayConfig()`；旧备份缺键保留目标现值 |
| 设置 | `channel_auto_update_enabled=false`、`channel_auto_update_interval_minutes=1440`、`channel_auto_update_auto_add_groups=false` | 初始化补默认值；导入校验开关和值域 |
| 阶段基线设置 | `model_filter=""`、`webhook_manual_failure_threshold=10`、`webhook_failover_channel_threshold=3` | 空过滤不限制；`013` 幂等补通知阈值，后续设置初始化补缺键 |
| 渠道 | `allowed_protocols=14` | GORM 补旧列；JSON 零值在读写边界解释为全部协议 |
| 渠道 | `follow_global_auto_update`、`max_concurrency=0` | 缺省跟随为 true，显式 false 保留；并发 0 不限，负值拒绝 |
| 分组 | `mode=manual`、`model_match_mode=keyword` | 旧行由模型默认值补齐；旧备份导入归一化，非法枚举拒绝 |
| 分组 | `relay_config` | 下表列出每个字段；手动和故障转移只校验/应用当前模式相关参数 |
| 诊断表 | `channel_refresh_records` | M3 已由 AutoMigrate 创建；诊断不加入备份，运行标记只在内存 |
| Webhook | endpoint URL、method、content type、headers、body template、timeout | 已有 `webhook_endpoints` 表原样备份；headers 等敏感内容置于加密容器内 |
| 文件元数据 | `DBDump.product`、内容版本 `1000`；加密容器版本 `1` | 文件字段，不触发数据库迁移；无标识 `6` 明确拒绝 |

| `relay_config` 字段 | 默认值 |
| --- | --- |
| `member_max_attempts` | 2 |
| `member_retry_interval_seconds` | 3 |
| `member_non_stream_response_timeout_seconds` | 120 |
| `member_stream_first_event_timeout_seconds` | 30 |
| `member_cooldown_seconds` | 60 |
| `member_affinity_seconds` | 0，旧 JSON 保留字段兼容但不生效 |
| `retry_mode` | `fixed` |
| `retry_initial_interval_seconds` | 1 |
| `retry_max_interval_seconds` | 600 |
| `manual_max_attempts` | 0，不限总尝试次数 |

渠道启用/禁用筛选是前端展示条件，不新增数据库字段。上游渠道拆分字段、凭据/模型/授权及 API Key `supported_models` 数组转换属于已有 `011`/`012`，R1 不重复迁移。

## 备份写入修复

原 `gorm:"default:true"` 会在结构体创建时把 `enabled=false` 改为 true，涉及渠道、渠道 Key 和客户端 API Key。R1 使用仅用于导入的嵌入行类型覆盖这一列的 GORM 默认值，保留其他字段和 JSON serializer；不改普通创建接口或数据库默认值。

实测新增三类禁用记录保持 false；重复导入时渠道和客户端 API Key 保持目标配置，渠道 Key 按备份覆盖 true/false。旧渠道仍补协议 `14`、默认跟随与并发 `0`，空分组补 `manual`/`keyword`。

失效成员引用仍按约束拒绝并回滚整个导入，不静默删除数据；这一行为在新格式、旧格式和加密恢复中沿用。导入错误不把驱动可能带凭据的细节返回浏览器。

产品身份优先于版本；裸 JSON 与响应包裹混用会拒绝，不再通过“若干表为空”猜测包裹形态。加密容器在认证和内容版本校验通过后才进入写入逻辑。导出全部数据存在应用内存中，下载文件只含密文；加密不等于数据库静态加密。

导出使用只读可重复读事务，让 14 张表来自同一数据库快照。SQLite 的并发删除回归已实测；MySQL/PostgreSQL 的相同隔离配置仍需实例验证。

## 迁移、验证与限制

- SQLite：已有旧表升级及二次 AutoMigrate 测试通过；禁用行恢复、旧 `0`/`5`、空模型/空分组、重复导入、失效引用事务回滚由本轮测试覆盖。
- 新配置：既有 M1/M3 测试继续验证全局策略、渠道跟随退出、并发上限、设置合法性和备份导出保留。
- 备份不导出管理登录账号、迁移表、刷新诊断和运行态。恢复到新安装使用新安装的管理账号；完整数据库回滚应使用数据库副本。
- MySQL/PostgreSQL：缺少测试实例，不能宣称迁移/恢复通过。驱动代码可编译不替代实例实测。
- 所有测试使用合成数据和临时数据库，没有改动业务数据库。

最终命令与浏览器结果见 [R1 结果](r1-result.md)；实际升级步骤见 [升级说明](../docs/upgrade-r1.md)。
