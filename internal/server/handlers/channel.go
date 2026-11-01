package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"github.com/Ink-lotus/octopus-roam/internal/price"
	"github.com/Ink-lotus/octopus-roam/internal/relay"
	"github.com/Ink-lotus/octopus-roam/internal/server/middleware"
	"github.com/Ink-lotus/octopus-roam/internal/server/resp"
	"github.com/Ink-lotus/octopus-roam/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	op.SetChannelRefreshMaintenance(func(ctx context.Context, added []string) []string {
		var warnings []string
		if err := addChannelModelPrices(added, ctx); err != nil {
			warnings = append(warnings, fmt.Sprintf("price update: model prices were not fully saved or loaded; %v", err))
		}
		if err := op.LLMCleanupGhosts(ctx); err != nil {
			warnings = append(warnings, fmt.Sprintf("price cleanup: unused model prices may remain; %v", err))
		}
		return warnings
	})
	op.OnChannelModelsRefreshed(func(id int, previous []model.Group) {
		groupMutationMu.Lock()
		defer groupMutationMu.Unlock()
		affected := make(map[int]bool)
		for _, group := range previous {
			if len(channelMembers(group, id)) > 0 {
				affected[group.ID] = true
			}
		}
		for _, group := range op.GroupList() {
			if affected[group.ID] || len(channelMembers(group, id)) > 0 {
				publishGroupSnapshot(group)
			}
		}
	})
	router.NewGroupRouter("/api/v1/channel").
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/detail/:id", http.MethodGet).
				Handle(getChannelDetail),
		).
		AddRoute(
			router.NewRoute("/stats", http.MethodGet).
				Handle(listChannelStats),
		).
		AddRoute(
			router.NewRoute("/refresh-status/:id", http.MethodGet).
				Handle(getChannelRefreshStatus),
		).
		AddRoute(
			router.NewRoute("/grants", http.MethodGet).
				Handle(listChannelGrant),
		).
		AddRoute(
			router.NewRoute("/create", http.MethodPost).
				Handle(createChannel),
		).
		AddRoute(
			router.NewRoute("/update", http.MethodPost).
				Handle(updateChannel),
		).
		AddRoute(
			router.NewRoute("/enable", http.MethodPost).
				Handle(enableChannel),
		).
		AddRoute(
			router.NewRoute("/delete/:id", http.MethodDelete).
				Handle(deleteChannel),
		).
		AddRoute(
			router.NewRoute("/fetch-model", http.MethodPost).
				Handle(fetchModel),
		).
		AddRoute(
			router.NewRoute("/refresh-model/:id", http.MethodPost).
				Handle(refreshChannelModels),
		)
}

// getChannelDetail 返回单个渠道的完整配置, 供编辑表单打开时读取。
// 与列表分开: 整份配置带着路径, 代理与凭据明文, 只有正在编辑的那一个渠道用得上。
func getChannelDetail(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	detail, err := op.ChannelDetailGet(id)
	if err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	resp.Success(c, detail)
}

// getChannelRefreshStatus 只返回指定渠道的刷新诊断，不携带编辑配置或凭据。
func getChannelRefreshStatus(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	if _, err := op.ChannelGet(id); err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	resp.Success(c, op.ChannelRefreshStatus(id))
}

// listChannelStats 返回全部渠道及其模型的累计统计, 也是渠道列表页的数据来源。
// 不带整份配置: 统计每次转发都在变, 界面按更短的间隔刷新它, 而路径, 代理与凭据明文只在编辑时用得上。
func listChannelStats(c *gin.Context) {
	stats := op.ChannelStatsList()
	responses := make([]channelStatsResponse, len(stats))
	for i := range stats {
		stats[i].Concurrency = relay.ChannelConcurrencyStats(stats[i].ChannelID)
		state := relay.ChannelCooldownStateOf(stats[i].ChannelID)
		responses[i] = channelStatsResponse{ChannelStats: stats[i], RuleCooldowns: state.RuleCooldowns, RuleCooldownVersion: state.Version}
	}
	resp.Success(c, responses)
}

type channelStatsResponse struct {
	model.ChannelStats
	RuleCooldowns       []relay.CooldownHit `json:"rule_cooldowns"`
	RuleCooldownVersion string              `json:"rule_cooldown_version"`
}

// listChannelGrant 返回全部渠道授权候选, 供分组页选取成员。
func listChannelGrant(c *gin.Context) {
	resp.Success(c, op.ChannelGrantCandidates())
}

func createChannel(c *gin.Context) {
	var req model.ChannelSaveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		if errors.Is(err, model.ErrInvalidCooldownRule) || strings.Contains(strings.ToLower(err.Error()), "cooldown") {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	groupMutationMu.Lock()
	defer groupMutationMu.Unlock()
	previousGroups := allGroupsByID()
	channel, err := op.ChannelCreateWithOptions(&req.ChannelDetail, req.ChannelSaveOptions, c.Request.Context())
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, model.ErrInvalidCooldownRule) || strings.Contains(strings.ToLower(err.Error()), "cooldown") {
			status = http.StatusBadRequest
		}
		resp.Error(c, status, err.Error())
		return
	}
	refreshChangedChannelGroups(channel.ID, previousGroups, allGroupsByID())
	if err := addChannelModelPrices(channel.Models, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, channel)
}

func updateChannel(c *gin.Context) {
	var req model.ChannelSaveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		if errors.Is(err, model.ErrInvalidCooldownRule) || strings.Contains(strings.ToLower(err.Error()), "cooldown") {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if req.ID == 0 {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	groupMutationMu.Lock()
	defer groupMutationMu.Unlock()
	previousGroups := allGroupsByID()
	previousChannel, _ := op.ChannelDetailGet(req.ID)
	channel, err := op.ChannelUpdateWithOptions(&req.ChannelDetail, req.ChannelSaveOptions, c.Request.Context())
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, model.ErrInvalidCooldownRule) || strings.Contains(strings.ToLower(err.Error()), "cooldown") {
			status = http.StatusBadRequest
		}
		resp.Error(c, status, err.Error())
		return
	}
	refreshChangedChannelGroups(req.ID, previousGroups, allGroupsByID(), sameChannelExceptCooldownRules(previousChannel, *channel))
	if err := addChannelModelPrices(channel.Models, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	if err := op.LLMCleanupGhosts(c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, channel)
}

func enableChannel(c *gin.Context) {
	var request struct {
		ID      int  `json:"id"`
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	groupMutationMu.Lock()
	defer groupMutationMu.Unlock()
	previousGroups := allGroupsByID()
	if err := op.ChannelEnabled(request.ID, request.Enabled, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	refreshChangedChannelGroups(request.ID, previousGroups, allGroupsByID())
	resp.Success(c, nil)
}

func deleteChannel(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	groupMutationMu.Lock()
	defer groupMutationMu.Unlock()
	previousGroups := allGroupsByID()
	if err := op.ChannelDel(id, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	refreshChangedChannelGroups(id, previousGroups, allGroupsByID())
	if err := op.LLMCleanupGhosts(c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, nil)
}

type channelGroupMember struct {
	itemID   int
	grantID  int
	priority int
}

func allGroupsByID() map[int]model.Group {
	groups := make(map[int]model.Group)
	for _, group := range op.GroupList() {
		groups[group.ID] = group
	}
	return groups
}

func sameChannelExceptCooldownRules(left, right model.ChannelDetail) bool {
	left.CooldownRules, right.CooldownRules = nil, nil
	return reflect.DeepEqual(left, right)
}

func refreshChangedChannelGroups(channelID int, previous, current map[int]model.Group, preserveRoute ...bool) {
	groupIDs := make(map[int]struct{}, len(previous)+len(current))
	for groupID := range previous {
		groupIDs[groupID] = struct{}{}
	}
	for groupID := range current {
		groupIDs[groupID] = struct{}{}
	}
	for groupID := range groupIDs {
		oldGroup, hadOld := previous[groupID]
		newGroup, hasNew := current[groupID]
		oldMembers := channelMembers(oldGroup, channelID)
		newMembers := channelMembers(newGroup, channelID)
		if len(oldMembers) == 0 && len(newMembers) == 0 {
			continue
		}
		if !hasNew {
			relay.ResetRouteState(groupID)
			if hadOld {
				publishGroupEvent(groupEvent{Name: "deleted", Data: groupID})
			}
			continue
		}
		if !hadOld || !slices.Equal(oldMembers, newMembers) {
			relay.ResetRouteStateForGroup(newGroup)
		} else if len(preserveRoute) == 0 || !preserveRoute[0] {
			relay.NotifyRouteStateChanged(newGroup)
		}
		publishGroupSnapshot(newGroup)
	}
}

func channelMembers(group model.Group, channelID int) []channelGroupMember {
	var members []channelGroupMember
	for _, item := range group.Items {
		if item.ChannelID == channelID {
			members = append(members, channelGroupMember{itemID: item.ID, grantID: item.ChannelGrantID, priority: item.Priority})
		}
	}
	return members
}

// addChannelModelPrices 为渠道模型匹配校准价格，并批量写入尚不存在的价格记录。
func addChannelModelPrices(modelNames []string, ctx context.Context) error {
	seen := make(map[string]struct{}, len(modelNames))
	llmInfos := make([]model.LLMInfo, 0, len(modelNames))
	for _, modelName := range modelNames {
		modelName = strings.ToLower(modelName)
		if _, ok := seen[modelName]; ok {
			continue
		}
		seen[modelName] = struct{}{}
		llmInfo := model.LLMInfo{Name: modelName}
		if modelPrice := price.GetLLMPrice(modelName); modelPrice != nil {
			llmInfo.LLMPrice = *modelPrice
		}
		llmInfos = append(llmInfos, llmInfo)
	}
	return op.LLMBatchCreate(llmInfos, ctx)
}

// fetchModel 按提交的渠道配置与凭据拉取上游模型列表, 并按过滤表达式筛选后返回。
// 同时探测 OpenAI 与 Anthropic 两侧, 谁返回了哪些模型, 就给对应协议位打勾: 协议支持由探测结果决定, 无需用户声明。
// OpenAI 侧记为 Responses 而不是 Chat: Chat Completions 已被官方标记弃用, 新渠道应默认走 Responses,
// 仍需 Chat 的渠道由用户在界面上手动勾选。两侧的 /models 地址与认证形态不同, 故必须分别探测:
// 单协议上游只有一侧会成功, "哪侧成功" 本身就是协议支持的证据。
// 只有两侧都失败才算失败; 一侧失败属正常情况, 单协议上游本就只有一侧讲得通, 按成功那侧的结果返回。
func fetchModel(c *gin.Context) {
	var request model.ChannelFetchModelRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	ctx := c.Request.Context()
	// 探测收的是尚未落库的提交配置, 不经 normalizeChannelConfig, 故在此自行去空白;
	// 其中只有地址是硬需求: 渠道尚未命名时也可试拉, 故名称不在此校验。
	target := request.Channel
	target.AllowedProtocols = model.NormalizeProtocols(target.AllowedProtocols)
	target.BaseURL = strings.TrimSpace(target.BaseURL)
	target.ChannelProxy = strings.TrimSpace(target.ChannelProxy)
	target.MatchRegex = strings.TrimSpace(target.MatchRegex)
	if target.AllowedProtocols&^model.ProtocolAll != 0 {
		resp.Error(c, http.StatusBadRequest, fmt.Errorf("%w: %d contains undefined bits", op.ErrInvalidChannelAllowedProtocols, target.AllowedProtocols).Error())
		return
	}
	if target.BaseURL == "" {
		resp.Error(c, http.StatusBadRequest, "channel base url is required")
		return
	}
	probe, err := op.ChannelFetchModels(ctx, target, request.Key)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, op.ErrInvalidChannelModelFilter) || errors.Is(err, op.ErrInvalidChannelAllowedProtocols) {
			status = http.StatusBadRequest
		}
		resp.Error(c, status, err.Error())
		return
	}
	openAIEnabled := target.AllowedProtocols&(model.ProtocolOpenAIChatCompletion|model.ProtocolOpenAIResponse) != 0

	anthropicEnabled := target.AllowedProtocols&model.ProtocolAnthropicMessage != 0
	openAISuccess := openAIEnabled && probe.OpenAIError == ""
	anthropicSuccess := anthropicEnabled && probe.AnthropicError == ""
	if (openAIEnabled || anthropicEnabled) && !openAISuccess && !anthropicSuccess {
		message := ""
		if openAIEnabled {
			message = fmt.Sprintf("openai: %s", probe.OpenAIError)
		}
		if anthropicEnabled {
			if message != "" {
				message += "; "
			}
			message += fmt.Sprintf("anthropic: %s", probe.AnthropicError)
		}
		resp.Error(c, http.StatusBadGateway, message)
		return
	}
	resp.Success(c, probe.Models)
}

// refreshChannelModels 持久化刷新已保存渠道的模型和授权; 任一 Key 探测失败时仍以结果体返回逐 Key 诊断。
func refreshChannelModels(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	var request map[string]json.RawMessage
	if err := c.ShouldBindJSON(&request); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	var options model.ChannelModelRefreshOptions
	if value := request["auto_add_groups"]; len(value) > 0 {
		if err := json.Unmarshal(value, &options.AutoAddGroups); err != nil {
			resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
			return
		}
	}
	// jsoniter 会将 RawMessage 的 null 解为 nil，用键是否存在区分省略与 null。
	if value, present := request["enabled_only"]; present {
		switch strings.TrimSpace(string(value)) {
		case "true":
			options.EnabledOnly = true
		case "false":
			options.EnabledOnly = false
		default:
			resp.Error(c, http.StatusBadRequest, "enabled_only must be a boolean")
			return
		}
	}
	detail, err := op.ChannelDetailGet(id)
	if err != nil && !options.EnabledOnly {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	if err == nil {
		detail.AllowedProtocols = model.NormalizeProtocols(detail.AllowedProtocols)
		if (!options.EnabledOnly || detail.Enabled) && detail.AllowedProtocols&^model.ProtocolAll != 0 {
			resp.Error(c, http.StatusBadRequest, fmt.Errorf("%w: %d contains undefined bits", op.ErrInvalidChannelAllowedProtocols, detail.AllowedProtocols).Error())
			return
		}
	}

	result, err := op.RefreshChannelModels(c.Request.Context(), id, options)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, op.ErrInvalidChannelModelFilter) || errors.Is(err, op.ErrInvalidChannelAllowedProtocols) {
			status = http.StatusBadRequest
		} else if errors.Is(err, op.ErrChannelRefreshStale) {
			status = http.StatusConflict
		}
		resp.Error(c, status, err.Error())
		return
	}
	if !result.Success && result.Error == op.ErrChannelRefreshInProgress.Error() {
		resp.Error(c, http.StatusConflict, result.Error)
		return
	}
	resp.Success(c, result)
}
