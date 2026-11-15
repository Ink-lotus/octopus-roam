# OpenAI Responses 历史清理

渠道的“清理 OpenAI Responses 历史”开关对应 `strip_responses_history`，默认关闭。它只在最终出站协议为 OpenAI Responses 时生效，并在渠道参数覆盖之后执行；同协议透传和转换后的 Responses 请求使用相同规则。OpenAI Chat Completions 和 Anthropic Messages 出站不受此清理器影响。

## 当前行为

开启后只移除两类显式远端历史引用：

- 请求顶层的 `previous_response_id`。
- `input` 第一层数组中 `type` 精确等于 `item_reference` 的整项。

其余客户端提供的历史全部保留，数组顺序不变：

| 内容 | 保留范围 |
| --- | --- |
| `agent_message` | author、recipient、agent、ID、完整 content，包括加密块的 type 和 encrypted_content |
| `compaction` | ID、encrypted_content、agent 及扩展字段 |
| `reasoning` | ID、encrypted_content、summary、content、状态及扩展字段 |
| 消息和工具调用 / 输出 | ID、call_id、name、arguments、output、phase 等全部字段 |
| 顶层 `include` | 全部原值，包括 `reasoning.encrypted_content`、其他选项、空数组和 null |
| 未知条目与嵌套业务数据 | 同名 id、encrypted_content、item_reference 形状、JSON 字符串、大整数、null 和空内容 |
| 其他顶层字段 | metadata、tools、text、提示词、缓存配置等原有值 |

“保留”指字段值、类型和数组顺序保持不变；外层 JSON 的空白或对象键顺序可能变化。非引用子树使用 `json.RawMessage` 承载，不将未知内容重建成 SDK 类型，也不经 `float64` 转换数字。

`input` 为字符串、null、对象或其他非数组形态时保留原值，交给上游校验。缺少合法字符串 `type` 的项不被猜测为引用项。非法请求 JSON 仍报错；清理失败不部分改写正文或已有 JSON 缓存。每次渠道尝试保留原始历史，前一渠道开启清理不会污染后一渠道关闭清理的请求。

## 与旧行为的区别

旧实现会递归删除 `input` 中的 `id`、`encrypted_content`，删除嵌套数组中的引用形状，并过滤 `include` 的密文选项。这可能把合法加密块或压缩项变成残缺对象，也会丢失消息、推理和工具项的身份字段。

当前实现将清理范围收窄到上述两类引用，不再提供剥除全部加密历史或全部 item ID 的效果。开关名称、配置字段、默认值和保存方式不变；无需数据库迁移或修改备份格式。

## 使用前提与限制

客户端必须在请求中提供所需的对话历史。仅提交远端引用而省略必要历史的客户端不适合开启此开关；移除引用不会自动补回对应上下文。

保留密文可以防止代理破坏上下文，但另一个账号或上游是否能解密，仍由上游决定。本项目无法从密文恢复完整明文历史，也不能通过删字段保证任意跨上游续聊。本修复不增加跨协议代理消息转换、客户端历史重建或路由策略。

如尚未部署此修复，或当前客户端需要远端引用，可在隔离测试渠道的编辑表单中关闭该开关并保存，再验证后续请求。关闭后会保留引用和密文；需要可用原始上下文的请求仍应送往兼容的账号 / 上游。已被旧实现丢弃的内容无法由代理自行恢复。

## 验证与交付边界

仓库中的 [单元回归](../internal/relay/responses_cleanup_test.go) 覆盖合法历史项、历史长度变化、清理边界、大整数、幂等性、协议开关、缓存和失败原子性。[HTTP / SSE 回放](../internal/relay/responses_cleanup_integration_test.go) 通过实际 Relay HTTP 入口和本地合成上游验证代理消息回填后连续追加、两轮工具调用、压缩历史、参数覆盖顺序、转换出站及渠道故障转移。合成上游必须检查请求内容后才返回成功。

```powershell
go test ./internal/relay -run 'ResponsesHistory|StripResponses|SanitizeResponses' -count=1
go test -tags=jsoniter ./internal/relay -run 'ResponsesHistory|StripResponses|SanitizeResponses' -count=1
go test ./internal/relay -count=1
```

这些测试使用合成密文，只证明数据被完整保留，不证明真实上游能够解密。Paseo 现场复核需要用户指定的隔离渠道 / 会话和真实上游生成的内容，记录客户端、daemon、Codex 与代理程序版本，再验证代理消息回填后的连续请求和工具调用。未实际复核时应记录“Paseo 现场待复核”。

完整 CI、规定 race、准确提交的云端检查及正式 Windows ZIP / Linux 镜像验收仍遵守 [当前发布门禁](release-ci.md)。局部测试或开发程序回放不能替代实际发布产物的安装、升级、启停、持久化、备份恢复、卷权限和容器重建验收。

协议依据：[OpenAI Beta Responses 请求定义](https://developers.openai.com/api/reference/resources/beta/subresources/responses/methods/create/)、[Responses 请求定义](https://developers.openai.com/api/reference/resources/responses/methods/create/) 与 [推理指南](https://developers.openai.com/api/docs/guides/reasoning)。
