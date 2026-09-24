package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/gin-contrib/sse"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/anthropic"
	"github.com/looplj/axonhub/llm/transformer/openai"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
	"github.com/tidwall/sjson"
)

// Forward 按客户端协议承载一个请求的完整转发过程: 解析请求, 定位分组, 循环选目标请求上游, 直至提交响应或请求结束。
func Forward(format llm.APIFormat) gin.HandlerFunc {
	// 客户端协议同时定出入站转换器和请求协议位: 后者随请求状态推给界面, 也是每轮选择上游协议的首选。
	var inbound transformer.Inbound
	requestProtocol := model.ProtocolOpenAIChatCompletion
	switch format {
	case llm.APIFormatOpenAIResponse:
		inbound = responses.NewInboundTransformer()
		requestProtocol = model.ProtocolOpenAIResponse
	case llm.APIFormatAnthropicMessage:
		inbound = anthropic.NewInboundTransformer()
		requestProtocol = model.ProtocolAnthropicMessage
	default:
		inbound = openai.NewInboundTransformer()
	}

	return func(c *gin.Context) {
		// 完整读取客户端请求, 正文先登记到请求状态, 后续每轮直接改写为当前目标请求。
		raw, err := httpclient.ReadHTTPRequest(c.Request)
		if err != nil {
			rejectRequest(c, inbound, err)
			return
		}

		// 此处只读取选组和分流所需字段; 完整协议校验由同协议上游或跨协议 pipeline 完成。
		var metadata struct {
			Model     string `json:"model"`  // 客户端请求的分组名称。
			Streaming bool   `json:"stream"` // 客户端是否请求流式响应。
		}
		if err := json.Unmarshal(raw.Body, &metadata); err != nil {
			rejectRequest(c, inbound, err)
			return
		}

		// API Key 限定了模型范围时只放行范围内的模型, 为空表示不限制。
		if allowed, ok := c.Get("supported_models"); ok {
			if names, _ := allowed.([]string); len(names) > 0 && !slices.Contains(names, metadata.Model) {
				rejectRequest(c, inbound, errors.New("model not supported by this api key"))
				return
			}
		}

		// 客户端请求的模型名称即分组名称; 分组不存在说明模型名错误, 等待也不会出现该分组。
		// 分组主键随请求状态一并登记, 界面由此可直接按主键取分组而不必按名称回查。
		group, err := op.GroupGetByName(metadata.Model)
		if err != nil {
			rejectRequest(c, inbound, errors.New("model not found"))
			return
		}

		// 登记进程内请求状态, 返回的记录是后续全部状态写入和前端可视化推送的入口。
		request := newRequestState(c.Request.Context(), metadata.Model, group.ID, requestProtocol, string(raw.Body), c.GetInt("api_key_id"))
		ctx := request.requestCtx
		failedItemID := 0             // 当前累计连续失败次数的成员 ID。
		failures := 0                 // 该成员包含首次请求的连续失败次数。
		failedGeneration := uint64(0) // 路由改变或恢复探测重新开始时重置成员尝试预算。
		probeToken := uint64(0)       // 本请求持有的恢复探测令牌, 取消时必须归还。
		var activeProbe struct {
			group      model.Group
			itemID     int
			generation uint64
			token      uint64
		}
		defer func() {
			releaseRouteProbe(activeProbe.group, activeProbe.itemID, activeProbe.generation, activeProbe.token)
		}()

		// 上游失败后的重试等待按 retry_mode 使用 RetryDelay; 同一请求开始恢复探测后会保留该成员的预算。
		// 故障转移无可用成员时按 routeSelection 的最早冷却截止时间或状态变更信号等待。

		for {
			if ctx.Err() != nil {
				request.markCanceled(ctx.Err(), "", nil)
				return
			}

			// 分组配置和成员随时可改, 故每轮重新读取; 分组被删除时保持原有间隔等待。
			group, err = op.GroupGetByName(metadata.Model)
			if err != nil {
				if !request.wait(ctx, model.DefaultGroupRelayConfig().MemberRetryIntervalSeconds) {
					return
				}
				continue
			}
			// 手动模式取人工指定成员; 故障转移按优先级扫描, 当前健康成员保持固定。
			selection := pickGroupItem(group, probeToken)
			if selection.Wait || selection.Item.ID == 0 {
				request.markRetryingAt(selection.WaitUntil)
				if group.Mode == model.GroupModeFailover {
					if !waitRoute(ctx.Done(), selection) {
						request.markCanceled(ctx.Err(), "", nil)
						return
					}
				} else if !request.wait(ctx, group.RelayConfig.MemberRetryIntervalSeconds) {
					return
				}
				continue
			}
			item := selection.Item
			probeToken = selection.ProbeToken
			if probeToken != 0 {
				activeProbe.group = group
				activeProbe.itemID = item.ID
				activeProbe.generation = selection.Generation
				activeProbe.token = probeToken
			} else {
				activeProbe.token = 0
			}
			attemptLimit := group.RelayConfig.MemberMaxAttempts
			if group.Mode == model.GroupModeManual {
				attemptLimit = group.RelayConfig.ManualMaxAttempts
			}
			resetAttempts := failedItemID != item.ID || failedGeneration != selection.Generation
			request.selectMember(item.ID, item.ChannelName, item.ModelName, 0, attemptLimit, resetAttempts)

			// 成员指向的授权缺失, 凭据被停用或两侧已被删除时等待, 该成员可能很快被改回可用配置。
			// ChannelGrantGet 一次校验齐这几种情况, 取到的授权必然可直接转发, 无需再逐项检查。
			grant, err := op.ChannelGrantGet(item.ChannelGrantID)
			if err != nil {
				request.recordFailure(err.Error())
				if group.Mode == model.GroupModeFailover {
					recordRouteFailure(group, item.ID, group.RelayConfig.MemberMaxAttempts, selection.Generation, selection.ProbeToken)
					probeToken = 0
				} else if !request.wait(ctx, group.RelayConfig.MemberRetryIntervalSeconds) {
					return
				}
				continue
			}
			channelModel := grant.ChannelModel
			channelKey := grant.ChannelKey

			// 成员指向的渠道已被删除时同样等待, 该成员可能很快被改回可用渠道。
			channel, err := op.ChannelGet(channelModel.ChannelID)
			if err != nil {
				request.recordFailure(err.Error())
				if group.Mode == model.GroupModeFailover {
					recordRouteFailure(group, item.ID, group.RelayConfig.MemberMaxAttempts, selection.Generation, selection.ProbeToken)
					probeToken = 0
				} else if !request.wait(ctx, group.RelayConfig.MemberRetryIntervalSeconds) {
					return
				}
				continue
			}

			// 将分组成员配置的真实模型写入本轮上游请求。
			raw.Body, err = sjson.SetBytes(raw.Body, "model", channelModel.Name)
			if err != nil {
				request.markFailed(err, "", nil)
				rejectRequest(c, inbound, err)
				return
			}
			// OpenAI Chat 流式响应需显式要求上游在末尾附带用量。
			if metadata.Streaming && format == llm.APIFormatOpenAIChatCompletion {
				raw.Body, err = sjson.SetBytes(raw.Body, "stream_options.include_usage", true)
				if err != nil {
					request.markFailed(err, "", nil)
					rejectRequest(c, inbound, err)
					return
				}
			}

			// 在渠道授权支持的协议内选出本轮上游协议, 按该协议的路径与授权绑定的凭据构造出站转换器。
			// 先于登记本轮目标: 选中的协议是本轮目标的一部分, 需与渠道和模型一并推给界面。
			outbound, targetProtocol, passthrough, err := buildOutbound(channel, grant, *channelKey, requestProtocol)

			// 为本轮上游调用建立独立取消入口并登记当前目标; 取消原因用于区分人工中止与响应超时。
			roundCtx, cancelRoundCause := context.WithCancelCause(ctx)
			// 人工中止和本轮完成都使用普通 canceled 原因, 超时回调则写入具体的超时错误。
			cancelRound := func() {
				cancelRoundCause(context.Canceled)
			}
			startsAttempt := err == nil
			request.startRound(cancelRound, channel.Name, channelModel.Name, targetProtocol, startsAttempt)

			roundStartedAt := time.Now() // 本轮上游调用的开始时间, 用于统计首个有效响应耗时。

			// 请求上游并等待首个有效响应: 非流式等待完整响应, 流式等待首个事件。
			// 同协议渠道原样直通, 跨协议渠道经转换后请求; 此时尚未写给客户端, 失败仍可换目标重试。
			var result *upstreamResponse
			// 本轮是否真的向上游发出了请求, 决定这次失败是否消耗该成员的尝试次数。
			// 由发送层的传输层回报: 请求构造, 参数解析与代理初始化失败都发生在发起传输之前, 不算发过。
			var sent bool
			if err == nil {
				timeoutSeconds := group.RelayConfig.MemberNonStreamResponseTimeoutSeconds // 非流式等待完整响应, 流式分支改为首事件超时。
				timeoutErr := errors.New("upstream non-stream response timeout")          // 具体错误用于区分超时与人工中止。
				if metadata.Streaming {
					timeoutSeconds = group.RelayConfig.MemberStreamFirstEventTimeoutSeconds
					timeoutErr = errors.New("upstream stream first event timeout")
				}
				// 计时器取消本轮上下文, 让正在等待 HTTP 响应或首个流事件的调用及时返回。
				timeoutTimer := time.AfterFunc(time.Duration(timeoutSeconds)*time.Second, func() {
					cancelRoundCause(timeoutErr)
				})
				// 客户端与渠道协议一致时直接透传, 其余组合通过 pipeline 转换。
				if passthrough {
					result, sent, err = sendPassthrough(roundCtx, format, raw, channel, outbound, metadata.Streaming, channelModel.Name)
				} else {
					result, sent, err = sendConverted(roundCtx, format, raw, channel, outbound, metadata.Streaming)
				}
				// 上游调用返回即结束首响应等待; Stop 失败说明已到期, 主动取消可避免等待异步回调完成。
				if !timeoutTimer.Stop() {
					cancelRoundCause(timeoutErr)
				}
				if context.Cause(roundCtx) == timeoutErr {
					err = timeoutErr
					// 超时与响应返回同时发生时舍弃尚未提交的流结果, 避免把超时误记为成功。
					if result != nil && result.events != nil {
						result.events.Close()
						if result.closeIdle != nil {
							result.closeIdle()
						}
					}
				}
			}

			if err != nil {
				// 记录本轮上游调用已经结束及其失败原因。
				countError := ctx.Err() == nil && context.Cause(roundCtx) != context.Canceled
				if !sent && startsAttempt {
					request.discardUnsentAttempt()
				}
				request.finishRound(err.Error(), countError)
				// 父上下文结束说明客户端已经取消, 归还探测占用并以取消终态结束请求。
				if ctx.Err() != nil {
					releaseRouteProbe(group, item.ID, selection.Generation, selection.ProbeToken)
					probeToken = 0
					activeProbe.token = 0
					request.markCanceled(ctx.Err(), "", nil)
					return
				}
				// 仅人工中止本轮时不计失败也不等待; 响应超时属于真实失败并消耗尝试次数。
				if context.Cause(roundCtx) == context.Canceled {
					releaseRouteProbe(group, item.ID, selection.Generation, selection.ProbeToken)
					probeToken = 0
					activeProbe.token = 0
					continue
				}
				// 本轮真实失败只计入当前渠道和成员, 客户端取消与人工中止不计为渠道故障。
				metrics := model.StatsMetrics{WaitTime: time.Since(roundStartedAt).Milliseconds(), RequestFailed: 1}
				_ = op.ChannelStatsUpdate(channel.ID, metrics)
				_ = op.ChannelModelStatsUpdate(channelModel.ID, metrics)
				_ = op.ChannelKeyStatsUpdate(channelKey.ID, metrics)

				cancelRound()
				// 未真正发出上游请求的失败(如出站转换无法构造)不消耗成员的尝试次数, 它不是该成员的上游失败。
				// 但故障转移模式仍须让它让出路由: 该失败由渠道配置决定, 重试同一成员不会改变结果,
				// 不让出路由会让请求永久停在这个成员上而永不切换到后续成员。手动模式没有路由可让, 只会重试当前成员。
				if !sent {
					if group.Mode != model.GroupModeManual && recordRouteFailure(group, item.ID, group.RelayConfig.MemberMaxAttempts, selection.Generation, selection.ProbeToken) {
						probeToken = 0
						activeProbe.token = 0
						continue
					}
					// 手动模式没有可让出的路由, 未发送错误仍按配置间隔等待后重试。
					if !request.wait(ctx, group.RelayConfig.MemberRetryIntervalSeconds) {
						releaseRouteProbe(group, item.ID, selection.Generation, selection.ProbeToken)
						activeProbe.token = 0
						return
					}
					continue
				}
				// 路由版本或成员变化时重置预算, 同一成员重新进入恢复探测也从第 1 次尝试开始。
				if failedGeneration == selection.Generation && failedItemID == item.ID {
					failures++
				} else {
					failedItemID = item.ID
					failedGeneration = selection.Generation
					failures = 1
				}
				retryDelay := RetryDelay(group.RelayConfig, failures)
				// 手动模式的预算由 manual_max_attempts 给出, 0 表示不限额; 故障转移模式仍由
				// member_max_attempts 决定成员何时让出路由。
				if group.Mode == model.GroupModeManual {
					if group.RelayConfig.ManualMaxAttempts > 0 && failures >= group.RelayConfig.ManualMaxAttempts {
						request.markFailed(err, "", nil)
						rejectUpstream(c, inbound, err, retryDelay)
						return
					}
				} else if recordRouteFailure(group, item.ID, failures, selection.Generation, selection.ProbeToken) {
					// 成员耗尽自身尝试次数并进入冷却, 立即重新选路而不消耗额外等待。
					probeToken = 0
					activeProbe.token = 0
					continue
				}
				// 同一成员的重试按配置的退避策略等待: 第 failures 次失败后等待的是第 failures 次重试。
				// failures 随成员更换归 1, 故指数退避在成员之间不累积, 每个成员都从初始间隔重新开始。
				if group.Mode == model.GroupModeFailover {
					if !request.waitRetry(ctx, retryDelay, selection.Changed) {
						releaseRouteProbe(group, item.ID, selection.Generation, selection.ProbeToken)
						activeProbe.token = 0
						return
					}
				} else if !request.waitDuration(ctx, retryDelay) {
					return
				}
				continue
			}
			// 记录本轮已经取得可提交的上游响应。
			request.finishRound("", false)
			// 请求级取消可能与上游成功同时到达, 此时不应提交响应或继续重试。
			if ctx.Err() != nil {
				releaseRouteProbe(group, item.ID, selection.Generation, selection.ProbeToken)
				activeProbe.token = 0
				request.markCanceled(ctx.Err(), "", result.usage)
				return
			}
			roundWaitTime := time.Since(roundStartedAt).Milliseconds() // 流式响应只统计等待首帧的时间。
			// 上游成功后解除该成员冷却; 当前成员继续固定使用。
			recordRouteSuccess(group, item.ID, selection.Generation, selection.ProbeToken)
			probeToken = 0
			activeProbe.token = 0
			// 同协议透传时原样返回上游响应头; 跨协议响应没有需要透传的响应头。
			for key, values := range result.header {
				c.Writer.Header()[key] = values
			}

			// 非流式响应已经完整取得, 提交后一次写给客户端。
			if !metadata.Streaming {
				cancelRound()
				if c.Writer.Header().Get("Content-Type") == "" {
					c.Header("Content-Type", "application/json")
				}
				// 非流式响应已有完整用量, 本轮渠道和成员统计可在提交前一次完成。
				metrics := usageMetrics(channelModel.Name, result.usage)
				metrics.WaitTime = roundWaitTime
				metrics.RequestSuccess = 1
				_ = op.ChannelStatsUpdate(channel.ID, metrics)
				_ = op.ChannelModelStatsUpdate(channelModel.ID, metrics)
				_ = op.ChannelKeyStatsUpdate(channelKey.ID, metrics)
				request.markCommitted(false)
				n, err := c.Writer.Write(result.body)
				if err == nil && n != len(result.body) {
					err = io.ErrShortWrite
				}
				if err != nil {
					if ctx.Err() != nil {
						request.markCanceled(ctx.Err(), string(result.body), result.usage)
					} else {
						request.markFailed(err, string(result.body), result.usage)
					}
					return
				}
				request.markSucceeded(string(result.body), result.usage)
				return
			}

			// 首帧提交后仍需逐个事件判断协议终态: 上游发出结束事件后未必立即关闭响应体, 继续读取会一直阻塞到
			// 客户端断开, 从而把已完整交付的响应误判为 context canceled。
			if c.Writer.Header().Get("Content-Type") == "" {
				c.Header("Content-Type", "text/event-stream")
			}
			var encoded bytes.Buffer
			var chunks []*httpclient.StreamEvent
			event := result.first
			last := result.last // 已转发的最后一个事件是否已按客户端协议结束整个响应流。
			committed := false
			for {
				if event != nil {
					chunks = append(chunks, event)
					// 每个事件计一个输出字符供日志页展示, 按节流间隔发布。
					request.addOutput()
					encoded.Reset()
					if encodeErr := sse.Encode(&encoded, sse.Event{Id: event.LastEventID, Event: event.Type, Data: event.Data}); encodeErr != nil {
						err = encodeErr
						break
					}
					if !committed {
						request.markCommitted(true)
						committed = true
					}
					n, writeErr := c.Writer.Write(encoded.Bytes())
					if writeErr == nil && n != encoded.Len() {
						writeErr = io.ErrShortWrite
					}
					if writeErr != nil {
						err = writeErr
						break
					}
					c.Writer.Flush()
				}
				if last {
					break
				}
				if !result.events.Next() {
					err = result.events.Err()
					break
				}
				event = result.events.Current()
				// 已提交的响应不能再换目标重试, 结束事件自身携带的失败原样转发给客户端, 并在转发后作为本请求终态。
				last, err = inspectStreamEvent(format, event)
			}
			request.finishStream()
			result.events.Close()
			// 事件流已读完, 渠道专用代理的独占连接池到此归还。
			if result.closeIdle != nil {
				result.closeIdle()
			}
			cancelRound()
			// 使用客户端协议转换器聚合已转发事件, 统一取得最终响应正文和用量。
			responseBody, meta, aggregateErr := inbound.AggregateStreamChunks(context.WithoutCancel(ctx), chunks)
			if aggregateErr == nil {
				result.usage = meta.Usage
			}
			// 流式响应结束并聚合出用量后, 按最终结果完成本轮渠道和成员统计。
			metrics := usageMetrics(channelModel.Name, result.usage)
			metrics.WaitTime = roundWaitTime
			if err == nil {
				metrics.RequestSuccess = 1
			} else {
				metrics.RequestFailed = 1
			}
			_ = op.ChannelStatsUpdate(channel.ID, metrics)
			_ = op.ChannelModelStatsUpdate(channelModel.ID, metrics)
			_ = op.ChannelKeyStatsUpdate(channelKey.ID, metrics)
			if err != nil {
				if ctx.Err() != nil {
					request.markCanceled(ctx.Err(), string(responseBody), result.usage)
				} else {
					request.markFailed(err, string(responseBody), result.usage)
				}
				return
			}
			request.markSucceeded(string(responseBody), result.usage)
			return
		}
	}
}

// rejectUpstream 以客户端协议的错误格式返回上游侧的最终失败: 重试与切换都已耗尽, 请求无法再继续。
// 状态码与错误类型属于对外契约: 客户端据此区分"请求本身有问题"与"上游暂时不可用, 可以稍后重试"。
func rejectUpstream(c *gin.Context, inbound transformer.Inbound, err error, retryDelay time.Duration) {
	c.Header("Retry-After", strconv.FormatInt(retryAfterSeconds(retryDelay), 10))
	response := inbound.TransformError(c.Request.Context(), &llm.ResponseError{
		StatusCode: http.StatusServiceUnavailable,
		Detail:     llm.ErrorDetail{Message: err.Error(), Type: "upstream_error"},
	})
	c.Data(response.StatusCode, "application/json", response.Body)
	c.Abort()
}

// retryAfterSeconds 将退避时长向上取整为整数秒, 保证头部值不会短于下一次重试等待。
func retryAfterSeconds(delay time.Duration) int64 {
	seconds := int64(delay / time.Second)
	if delay%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		return 1
	}
	return seconds
}

// rejectRequest 以客户端协议的错误格式返回请求级失败, 用于尚未登记状态因而无需定稿的请求。
func rejectRequest(c *gin.Context, inbound transformer.Inbound, err error) {
	response := inbound.TransformError(c.Request.Context(), &llm.ResponseError{
		StatusCode: http.StatusBadRequest,
		Detail:     llm.ErrorDetail{Message: err.Error(), Type: "invalid_request_error"},
	})
	c.Data(response.StatusCode, "application/json", response.Body)
	c.Abort()
}
