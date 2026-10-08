# 已知 Bug

## BUG-001：个别渠道更新模型时出现 Anthropic 401「未提供令牌」告警

- 记录日期：2026-10-08。
- 状态：已记录，暂不处理。
- 优先级：低。
- 已知涉及渠道：`https://anyrouter.top`；用户反馈仅个别渠道出现。
- 用户确认：告警不影响实际 Anthropic 调用。

### 现象

渠道每次更新模型列表时，出现以下告警：

```text
anthropic: upstream 401 Unauthorized: {"error":{"message":"未提供令牌 (request id: <request-id>)","type":"new_api_error"}}
```

### 原因判断与证据

当前代码会分别探测渠道已启用的 OpenAI 和 Anthropic 协议。在默认路径配置下，两侧都请求 `/v1/models`，但鉴权方式不同：

- OpenAI：`Authorization: Bearer <渠道 Key>`。
- Anthropic：`X-Api-Key: <渠道 Key>` 和 `Anthropic-Version: 2023-06-01`，默认不带 `Authorization`。

实现位于 [channel_refresh.go](../internal/op/channel_refresh.go) 的 `ChannelFetchModelsWithClient`、`fetchOpenAIChannelModels` 和 `fetchAnthropicChannelModels`。

最可能的原因是相关渠道的模型列表接口只识别 Bearer 鉴权，未识别 Anthropic 探测携带的 `X-Api-Key`，因此返回「未提供令牌」。New API 曾有[对应问题记录 #2422](https://github.com/QuantumNous/new-api/issues/2422)。目前没有回退到 Bearer 鉴权的逻辑，因此每次刷新会重复告警。

上述代码行为已通过静态阅读确认；AnyRouter 的具体鉴权限制仍是原因判断，尚未使用该渠道的实际 Key 对比验证。此告警本身不能证明 Key 失效或 Anthropic 消息接口不可用。

### 影响

用户已确认实际 Anthropic 调用正常。反复出现的告警会干扰对真实故障的判断。

根据 `refreshChannelModelsOnce` 的实现，模型同步还存在以下条件性影响：

- OpenAI 探测成功，且对应凭据没有已有的有效 Anthropic 授权时，刷新可以成功，并保留单侧失败的诊断。
- 对应凭据已有有效 Anthropic 授权时，Anthropic 探测失败会阻止本次刷新提交，保留原模型和授权。

第二种情况属于代码层面的潜在影响，尚未确认该渠道本次刷新是否触发。

### 后续可评估的修复方向

用户决定暂不处理。本条仅记录候选方向，尚未实施或验证：

1. 默认保留标准 Anthropic 鉴权；遇到这类 `401 未提供令牌` 时，考虑补充同一渠道 Key 的 `Authorization: Bearer`，对模型列表请求重试一次。
2. 重试成功后消除对应告警；仍失败则保留诊断。
3. 核对回退响应格式和协议授权处理。部分网关可能返回普通 OpenAI 模型列表，不能仅凭 HTTP 200 或成功解析模型 ID，就认定所有返回模型都支持 Anthropic。

后续处理时，应验证标准 Anthropic 渠道、需要 Bearer 的兼容渠道及真实无效 Key 的行为，并确认已有授权保护有效。
