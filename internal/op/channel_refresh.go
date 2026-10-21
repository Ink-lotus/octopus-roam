package op

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/dlclark/regexp2"
	"golang.org/x/net/proxy"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrInvalidChannelModelFilter = errors.New("invalid channel model filter")
var ErrInvalidChannelAllowedProtocols = errors.New("invalid channel allowed protocols")
var ErrChannelRefreshStale = errors.New("channel changed while refresh was in progress")
var ErrChannelRefreshInProgress = errors.New("channel refresh already in progress")

// 每个 Key 的两侧探测和全部分页共用期限，避免慢响应长期占用刷新任务。
const channelModelProbeTimeout = 30 * time.Second

var channelModelRefreshLockState = struct {
	sync.Mutex
	locks map[int]*channelModelRefreshLock
}{locks: make(map[int]*channelModelRefreshLock)}

// channelRefreshCommitMu 只覆盖渠道写事务和缓存提交, 不覆盖上游网络探测。
// 渠道编辑与刷新在同一提交锁下串行, 避免编辑在刷新复核后插入而被旧探测结果覆盖。
var channelRefreshCommitMu sync.Mutex

type channelModelRefreshLock struct {
	running bool
	refs    int
}

type channelRefreshGrantKey struct {
	modelName string
	keyName   string
}

type channelRefreshGrantSnapshot struct {
	protocols model.Protocol
}

// ChannelFetchModels 探测一份凭据可用的 OpenAI 与 Anthropic 模型, 并应用渠道和全局过滤。
// 单侧失败是单协议上游的正常情况; 两侧错误由调用方按 Key 汇总处理。
func ChannelFetchModels(ctx context.Context, target model.ChannelConfig, key string) (model.ChannelModelFetchResult, error) {
	client, cleanup, err := channelRefreshClient(target)
	if err != nil {
		return model.ChannelModelFetchResult{}, err
	}
	if cleanup != nil {
		defer cleanup()
	}
	return ChannelFetchModelsWithClient(ctx, target, key, client)
}

// ChannelFetchModelsWithClient 为 HTTP 处理器和测试提供客户端注入入口。
func ChannelFetchModelsWithClient(ctx context.Context, target model.ChannelConfig, key string, client *http.Client) (model.ChannelModelFetchResult, error) {
	ctx, cancel := context.WithTimeout(ctx, channelModelProbeTimeout)
	defer cancel()
	target.BaseURL = strings.TrimSpace(target.BaseURL)
	target.ChannelProxy = strings.TrimSpace(target.ChannelProxy)
	target.MatchRegex = strings.TrimSpace(target.MatchRegex)
	if target.BaseURL == "" {
		return model.ChannelModelFetchResult{}, fmt.Errorf("channel base url is required")
	}
	target.AllowedProtocols = model.NormalizeProtocols(target.AllowedProtocols)
	if target.AllowedProtocols&^model.ProtocolAll != 0 {
		return model.ChannelModelFetchResult{}, fmt.Errorf("%w: %d contains undefined bits", ErrInvalidChannelAllowedProtocols, target.AllowedProtocols)
	}

	var channelFilter *regexp2.Regexp
	if target.MatchRegex != "" {
		compiled, err := regexp2.Compile(target.MatchRegex, regexp2.ECMAScript)
		if err != nil {
			return model.ChannelModelFetchResult{}, fmt.Errorf("%w: %v", ErrInvalidChannelModelFilter, err)
		}
		channelFilter = compiled
	}
	globalFilterText, _ := SettingGetString(model.SettingKeyModelFilter)
	var globalFilter *regexp2.Regexp
	if globalFilterText != "" {
		compiled, err := regexp2.Compile(globalFilterText, regexp2.ECMAScript)
		if err != nil {
			return model.ChannelModelFetchResult{}, fmt.Errorf("%w: %v", ErrInvalidChannelModelFilter, err)
		}
		globalFilter = compiled
	}

	if client == nil {
		client = http.DefaultClient
	}

	var openaiModels, anthropicModels []string
	var openaiErr, anthropicErr error
	var wg sync.WaitGroup
	openAIAllowed := target.AllowedProtocols & (model.ProtocolOpenAIChatCompletion | model.ProtocolOpenAIResponse)
	openAIModelPath := target.OpenAIResponsePath
	if target.AllowedProtocols&model.ProtocolOpenAIResponse == 0 {
		openAIModelPath = target.OpenAIChatCompletionPath
	}
	if openAIAllowed != 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			openaiModels, openaiErr = fetchOpenAIChannelModels(client, ctx, target, key, channelModelsURL(target.BaseURL, openAIModelPath))
		}()
	}
	if target.AllowedProtocols&model.ProtocolAnthropicMessage != 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			anthropicModels, anthropicErr = fetchAnthropicChannelModels(client, ctx, target, key, channelModelsURL(target.BaseURL, target.AnthropicMessagePath))
		}()
	}
	wg.Wait()

	result := model.ChannelModelFetchResult{
		Models:         make([]model.ChannelFetchModel, 0),
		OpenAIError:    errorText(openaiErr),
		AnthropicError: errorText(anthropicErr),
	}
	protocolsByModel := make(map[string]model.Protocol, len(openaiModels)+len(anthropicModels))
	order := make([]string, 0, len(openaiModels)+len(anthropicModels))
	addModels := func(names []string, protocol model.Protocol) error {
		for _, name := range names {
			if channelFilter != nil {
				matched, err := channelFilter.MatchString(name)
				if err != nil {
					return fmt.Errorf("%w: %v", ErrInvalidChannelModelFilter, err)
				}
				if !matched {
					continue
				}
			}
			if globalFilter != nil {
				matched, err := globalFilter.MatchString(name)
				if err != nil {
					return fmt.Errorf("%w: %v", ErrInvalidChannelModelFilter, err)
				}
				if !matched {
					continue
				}
			}
			if _, exists := protocolsByModel[name]; !exists {
				order = append(order, name)
			}
			protocolsByModel[name] |= protocol
		}
		return nil
	}
	if openAIAllowed != 0 && openaiErr == nil {
		// OpenAI 的模型列表由 Chat 与 Responses 共用。为保持旧渠道兼容,
		// 两者同时启用时沿用 Responses 标记；仅启用 Chat 时标记为 Chat。
		openAIProtocol := model.ProtocolOpenAIResponse
		if target.AllowedProtocols&model.ProtocolOpenAIResponse == 0 {
			openAIProtocol = model.ProtocolOpenAIChatCompletion
		}
		if err := addModels(openaiModels, openAIProtocol); err != nil {
			return model.ChannelModelFetchResult{}, err
		}
	}
	if target.AllowedProtocols&model.ProtocolAnthropicMessage != 0 && anthropicErr == nil {
		if err := addModels(anthropicModels, model.ProtocolAnthropicMessage); err != nil {
			return model.ChannelModelFetchResult{}, err
		}
	}
	for _, name := range order {
		result.Models = append(result.Models, model.ChannelFetchModel{Name: name, Protocols: protocolsByModel[name]})
	}
	return result, nil
}

// RefreshChannelModels 拉取全部启用凭据并原子替换该渠道的模型和授权。
// 已有授权对应的协议探测失败时不写数据库, 也不刷新缓存。
func RefreshChannelModels(ctx context.Context, channelID int, options model.ChannelModelRefreshOptions) (model.ChannelModelRefreshResult, error) {
	return refreshChannelModelsWithClient(ctx, channelID, options, nil)
}

// RefreshChannelModelsWithClient 为 HTTP 处理器和测试提供客户端注入入口。
func RefreshChannelModelsWithClient(ctx context.Context, channelID int, options model.ChannelModelRefreshOptions, client *http.Client) (model.ChannelModelRefreshResult, error) {
	return refreshChannelModelsWithClient(ctx, channelID, options, client)
}

func refreshChannelModelsOnce(ctx context.Context, channelID int, options model.ChannelModelRefreshOptions, client *http.Client) (model.ChannelModelRefreshResult, error) {
	result := model.ChannelModelRefreshResult{
		ChannelID:     channelID,
		AddedModels:   make([]string, 0),
		RemovedModels: make([]string, 0),
		KeyResults:    make([]model.ChannelModelRefreshKeyResult, 0),
	}
	channel, ok := channelCache.Get(channelID)
	if !ok {
		return result, fmt.Errorf("channel not found")
	}
	channel.AllowedProtocols = model.NormalizeProtocols(channel.AllowedProtocols)
	if channel.AllowedProtocols&^model.ProtocolAll != 0 {
		return result, fmt.Errorf("%w: %d contains undefined bits", ErrInvalidChannelAllowedProtocols, channel.AllowedProtocols)
	}

	keys := make([]model.ChannelKey, 0)
	keySnapshot := make(map[int]model.ChannelKeyConfig)
	for _, channelKey := range channelKeyCache.GetAll() {
		if channelKey.ChannelID == channelID {
			keySnapshot[channelKey.ID] = channelKey.ChannelKeyConfig
			if channelKey.Enabled {
				keys = append(keys, channelKey)
			}
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Name < keys[j].Name })
	if len(keys) == 0 {
		result.Error = "channel has no enabled keys"
		return result, nil
	}
	grantSnapshot, err := loadChannelRefreshGrantSnapshot(ctx, channelID)
	if err != nil {
		return result, err
	}
	if client == nil {
		var cleanup func()
		var err error
		client, cleanup, err = channelRefreshClient(channel.ChannelConfig)
		if err != nil {
			return result, err
		}
		if cleanup != nil {
			defer cleanup()
		}
	}

	modelNames := make([]string, 0)
	modelSeen := make(map[string]struct{})
	grantIndex := make(map[string]int)
	grants := make([]model.ChannelGrantConfig, 0)
	failed := false
	for _, channelKey := range keys {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		probe, err := ChannelFetchModelsWithClient(ctx, channel.ChannelConfig, channelKey.Key, client)
		if err != nil {
			return result, err
		}
		openAIEnabled := channel.AllowedProtocols&(model.ProtocolOpenAIChatCompletion|model.ProtocolOpenAIResponse) != 0
		anthropicEnabled := channel.AllowedProtocols&model.ProtocolAnthropicMessage != 0
		openAIFailed := openAIEnabled && probe.OpenAIError != ""
		anthropicFailed := anthropicEnabled && probe.AnthropicError != ""
		openAIHasGrant := false
		anthropicHasGrant := false
		for grantKey, grant := range grantSnapshot {
			if grantKey.keyName != channelKey.Name {
				continue
			}
			protocols := model.EffectiveProtocols(channel.AllowedProtocols, grant.protocols)
			openAIHasGrant = openAIHasGrant || protocols&(model.ProtocolOpenAIChatCompletion|model.ProtocolOpenAIResponse) != 0
			anthropicHasGrant = anthropicHasGrant || protocols&model.ProtocolAnthropicMessage != 0
		}
		keyFailed := !(openAIEnabled && !openAIFailed || anthropicEnabled && !anthropicFailed) ||
			openAIFailed && openAIHasGrant || anthropicFailed && anthropicHasGrant
		keyErrors := make([]string, 0, 2)
		if openAIFailed {
			keyErrors = append(keyErrors, "openai: "+probe.OpenAIError)
		}
		if anthropicFailed {
			keyErrors = append(keyErrors, "anthropic: "+probe.AnthropicError)
		}
		keyResult := model.ChannelModelRefreshKeyResult{
			KeyName:      channelKey.Name,
			Success:      !keyFailed,
			ModelCount:   len(probe.Models),
			AddedCount:   new(int),
			RemovedCount: new(int),
			Error:        strings.Join(keyErrors, "; "),
		}
		if keyFailed {
			failed = true
		}
		result.KeyResults = append(result.KeyResults, keyResult)
		if !keyResult.Success {
			continue
		}
		for _, fetched := range probe.Models {
			if _, exists := modelSeen[fetched.Name]; !exists {
				modelSeen[fetched.Name] = struct{}{}
				modelNames = append(modelNames, fetched.Name)
			}
			grantKey := fetched.Name + "\x00" + channelKey.Name
			openAIProtocols := fetched.Protocols & (model.ProtocolOpenAIChatCompletion | model.ProtocolOpenAIResponse)
			anthropicProtocols := fetched.Protocols & model.ProtocolAnthropicMessage
			if existing, ok := grantSnapshot[channelRefreshGrantKey{modelName: fetched.Name, keyName: channelKey.Name}]; ok {
				// 每个协议族独立同步: 只有该侧仍返回模型时才保留原有手动位,
				// 侧别探测没有列出模型时清掉该侧位; 现有授权不自动启用新协议。
				if openAIProtocols != 0 {
					openAIProtocols = existing.protocols & (model.ProtocolOpenAIChatCompletion | model.ProtocolOpenAIResponse)
				}
				if anthropicProtocols != 0 {
					anthropicProtocols = existing.protocols & model.ProtocolAnthropicMessage
				}
			}
			protocols := openAIProtocols | anthropicProtocols
			if protocols == 0 {
				continue
			}
			if index, exists := grantIndex[grantKey]; exists {
				grants[index].Protocols |= protocols
				continue
			}
			grantIndex[grantKey] = len(grants)
			grants = append(grants, model.ChannelGrantConfig{
				ModelName: fetched.Name,
				KeyName:   channelKey.Name,
				Protocols: protocols,
			})
		}
	}
	if failed {
		result.Error = "one or more enabled keys failed; previous models preserved"
		return result, nil
	}

	channelRefreshCommitMu.Lock()
	defer channelRefreshCommitMu.Unlock()
	// 渠道刷新会删除失效授权并可能追加分组成员。与分组手动编辑共用这把锁,
	// 让成员同步、事务提交和缓存重载之间不会插入另一笔分组写入。
	groupWriteMu.Lock()
	defer groupWriteMu.Unlock()
	oldNames := make(map[string]struct{})
	if err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if options.Automatic && !ChannelAutoUpdateEligible(channelID) {
			return ErrChannelRefreshStale
		}
		if err := verifyChannelRefreshSnapshot(tx, channelID, channel.ChannelConfig, keySnapshot, grantSnapshot); err != nil {
			return err
		}
		oldGrantIDs, err := channelGrantIDsInTx(tx, channelID)
		if err != nil {
			return err
		}
		var existing []model.ChannelModel
		if err := tx.Where("channel_id = ?", channelID).Find(&existing).Error; err != nil {
			return fmt.Errorf("failed to load channel models: %w", err)
		}
		for _, current := range existing {
			oldNames[current.Name] = struct{}{}
		}
		if err := syncChannelModels(tx, channelID, modelNames); err != nil {
			return err
		}
		if err := syncChannelGrants(tx, channelID, grants); err != nil {
			return err
		}
		return syncRefreshedChannelGroups(tx, channelID, options.AutoAddGroups, oldGrantIDs)
	}); err != nil {
		return result, err
	}

	// 仅在事务提交后统计各 Key 的授权变化，失败或并发冲突保留零变更。
	keyResultsByName := make(map[string]*model.ChannelModelRefreshKeyResult, len(result.KeyResults))
	for i := range result.KeyResults {
		keyResultsByName[result.KeyResults[i].KeyName] = &result.KeyResults[i]
	}
	for _, grant := range grants {
		if _, exists := grantSnapshot[channelRefreshGrantKey{modelName: grant.ModelName, keyName: grant.KeyName}]; !exists {
			(*keyResultsByName[grant.KeyName].AddedCount)++
		}
	}
	for grantKey := range grantSnapshot {
		if _, exists := grantIndex[grantKey.modelName+"\x00"+grantKey.keyName]; !exists {
			if keyResult, enabled := keyResultsByName[grantKey.keyName]; enabled {
				(*keyResult.RemovedCount)++
			}
		}
	}

	newNames := make(map[string]struct{}, len(modelNames))
	for _, name := range modelNames {
		newNames[name] = struct{}{}
		if _, exists := oldNames[name]; !exists {
			result.AddedModels = append(result.AddedModels, name)
		}
	}
	for name := range oldNames {
		if _, exists := newNames[name]; !exists {
			result.RemovedModels = append(result.RemovedModels, name)
		}
	}
	sort.Strings(result.AddedModels)
	sort.Strings(result.RemovedModels)
	result.ZeroModels = len(modelNames) == 0
	result.Success = true

	if err := reloadChannelChildren(ctx, channelID); err != nil {
		return result, err
	}
	if err := groupRefreshCacheLocked(ctx); err != nil {
		return result, fmt.Errorf("failed to refresh groups: %w", err)
	}
	return result, nil
}

func loadChannelRefreshGrantSnapshot(ctx context.Context, channelID int) (map[channelRefreshGrantKey]channelRefreshGrantSnapshot, error) {
	type grantRow struct {
		ModelName string         `gorm:"column:model_name"`
		KeyName   string         `gorm:"column:key_name"`
		Protocols model.Protocol `gorm:"column:protocols"`
	}
	var rows []grantRow
	if err := db.GetDB().WithContext(ctx).Table("channel_grants AS grants").
		Select("models.name AS model_name, credentials.name AS key_name, grants.protocols AS protocols").
		Joins("JOIN channel_models AS models ON models.id = grants.channel_model_id").
		Joins("JOIN channel_keys AS credentials ON credentials.id = grants.channel_key_id").
		Where("models.channel_id = ?", channelID).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to load existing channel grants: %w", err)
	}
	snapshot := make(map[channelRefreshGrantKey]channelRefreshGrantSnapshot, len(rows))
	for _, row := range rows {
		snapshot[channelRefreshGrantKey{modelName: row.ModelName, keyName: row.KeyName}] = channelRefreshGrantSnapshot{protocols: row.Protocols}
	}
	return snapshot, nil
}

func verifyChannelRefreshGrantSnapshot(tx *gorm.DB, channelID int, expected map[channelRefreshGrantKey]channelRefreshGrantSnapshot) error {
	type grantRow struct {
		ModelName string         `gorm:"column:model_name"`
		KeyName   string         `gorm:"column:key_name"`
		Protocols model.Protocol `gorm:"column:protocols"`
	}
	var rows []grantRow
	if err := tx.Table("channel_grants AS grants").
		Select("models.name AS model_name, credentials.name AS key_name, grants.protocols AS protocols").
		Joins("JOIN channel_models AS models ON models.id = grants.channel_model_id").
		Joins("JOIN channel_keys AS credentials ON credentials.id = grants.channel_key_id").
		Where("models.channel_id = ?", channelID).Scan(&rows).Error; err != nil {
		return fmt.Errorf("failed to verify existing channel grants: %w", err)
	}
	if len(rows) != len(expected) {
		return ErrChannelRefreshStale
	}
	for _, row := range rows {
		key := channelRefreshGrantKey{modelName: row.ModelName, keyName: row.KeyName}
		grant, ok := expected[key]
		if !ok || grant.protocols != row.Protocols {
			return ErrChannelRefreshStale
		}
	}
	return nil
}

func channelGrantIDsInTx(tx *gorm.DB, channelID int) (map[int]struct{}, error) {
	var grants []model.ChannelGrant
	models := tx.Model(&model.ChannelModel{}).Select("id").Where("channel_id = ?", channelID)
	if err := tx.Where("channel_model_id IN (?)", models).Find(&grants).Error; err != nil {
		return nil, fmt.Errorf("failed to load existing channel grants: %w", err)
	}
	ids := make(map[int]struct{}, len(grants))
	for _, grant := range grants {
		ids[grant.ID] = struct{}{}
	}
	return ids, nil
}

// syncRefreshedChannelGroups 在刷新事务中同步现有分组成员。
// 授权删除造成的失效成员由此显式清理; 启用自动添加时补齐所有匹配的当前授权。
func syncRefreshedChannelGroups(tx *gorm.DB, channelID int, autoAdd bool, oldGrantIDs map[int]struct{}) error {
	currentGrantIDs, err := channelGrantIDsInTx(tx, channelID)
	if err != nil {
		return err
	}
	removedGrantIDs := make([]int, 0)
	for grantID := range oldGrantIDs {
		if _, exists := currentGrantIDs[grantID]; !exists {
			removedGrantIDs = append(removedGrantIDs, grantID)
		}
	}
	if len(removedGrantIDs) > 0 {
		if err := clearActiveItems(tx, removedGrantIDs); err != nil {
			return err
		}
		if err := tx.Where("channel_grant_id IN ?", removedGrantIDs).Delete(&model.GroupItem{}).Error; err != nil {
			return fmt.Errorf("failed to delete stale refreshed group items: %w", err)
		}
	}
	if !autoAdd {
		return nil
	}
	return appendMatchingChannelGroups(tx, channelID)
}

// appendMatchingChannelGroups 供渠道保存和刷新共用，在同一事务中补齐已有分组。
// 调用方必须持有 groupWriteMu，已有成员及其优先级保持不变。
func appendMatchingChannelGroups(tx *gorm.DB, channelID int) error {
	var channel model.Channel
	if err := tx.Select("enabled").First(&channel, channelID).Error; err != nil {
		return fmt.Errorf("failed to load refreshed channel status: %w", err)
	}
	if !channel.Enabled {
		return nil
	}

	var channelModels []model.ChannelModel
	if err := tx.Where("channel_id = ?", channelID).Find(&channelModels).Error; err != nil {
		return fmt.Errorf("failed to load refreshed channel models for groups: %w", err)
	}
	modelNames := make(map[int]string, len(channelModels))
	for _, channelModel := range channelModels {
		modelNames[channelModel.ID] = channelModel.Name
	}
	var grants []model.ChannelGrant
	models := tx.Model(&model.ChannelModel{}).Select("id").Where("channel_id = ?", channelID)
	keys := tx.Model(&model.ChannelKey{}).Select("id").Where("channel_id = ? AND enabled = ?", channelID, true).
		Where(clause.Neq{Column: "key", Value: ""})
	if err := tx.Where("channel_model_id IN (?) AND channel_key_id IN (?)", models, keys).
		Order("id ASC").Find(&grants).Error; err != nil {
		return fmt.Errorf("failed to load refreshed grants for groups: %w", err)
	}
	if len(grants) == 0 {
		return nil
	}

	var groups []model.Group
	if err := tx.Preload("Items").Find(&groups).Error; err != nil {
		return fmt.Errorf("failed to load groups for refreshed models: %w", err)
	}
	for _, group := range groups {
		matchMode := model.NormalizeGroupModelMatchMode(group.ModelMatchMode)
		existingGrantIDs := make(map[int]struct{}, len(group.Items))
		priority := 0
		for _, item := range group.Items {
			existingGrantIDs[item.ChannelGrantID] = struct{}{}
			if item.Priority > priority {
				priority = item.Priority
			}
		}
		for _, grant := range grants {
			if _, exists := existingGrantIDs[grant.ID]; exists {
				continue
			}
			modelName := modelNames[grant.ChannelModelID]
			if !model.MatchGroupModelName(matchMode, group.Name, modelName) {
				continue
			}
			priority++
			item := model.GroupItem{GroupID: group.ID, ChannelGrantID: grant.ID, Priority: priority}
			if err := tx.Create(&item).Error; err != nil {
				return fmt.Errorf("failed to append refreshed group item: %w", err)
			}
			existingGrantIDs[grant.ID] = struct{}{}
		}
	}
	return nil
}

func verifyChannelRefreshSnapshot(tx *gorm.DB, channelID int, config model.ChannelConfig, keys map[int]model.ChannelKeyConfig, grants map[channelRefreshGrantKey]channelRefreshGrantSnapshot) error {
	var current model.Channel
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, channelID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrChannelRefreshStale
		}
		return err
	}
	if current.CustomHeader == nil {
		current.CustomHeader = []model.CustomHeader{}
	}
	current.AllowedProtocols = model.NormalizeProtocols(current.AllowedProtocols)
	currentFollow, expectedFollow := current.FollowsGlobalAutoUpdate(), config.FollowsGlobalAutoUpdate()
	current.FollowGlobalAutoUpdate, config.FollowGlobalAutoUpdate = &currentFollow, &expectedFollow
	if config.CustomHeader == nil {
		config.CustomHeader = []model.CustomHeader{}
	}
	if !reflect.DeepEqual(current.ChannelConfig, config) {
		return ErrChannelRefreshStale
	}
	var currentKeys []model.ChannelKey
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("channel_id = ?", channelID).Find(&currentKeys).Error; err != nil {
		return err
	}
	if len(currentKeys) != len(keys) {
		return ErrChannelRefreshStale
	}
	for _, currentKey := range currentKeys {
		snapshotKey, ok := keys[currentKey.ID]
		if !ok || !reflect.DeepEqual(snapshotKey, currentKey.ChannelKeyConfig) {
			return ErrChannelRefreshStale
		}
	}
	return verifyChannelRefreshGrantSnapshot(tx, channelID, grants)
}

func lockChannelModelRefresh(channelID int) (func(), bool) {
	channelModelRefreshLockState.Lock()
	lock := channelModelRefreshLockState.locks[channelID]
	if lock == nil {
		lock = &channelModelRefreshLock{}
		channelModelRefreshLockState.locks[channelID] = lock
	}
	lock.refs++
	if lock.running {
		lock.refs--
		if lock.refs == 0 {
			delete(channelModelRefreshLockState.locks, channelID)
		}
		channelModelRefreshLockState.Unlock()
		return nil, false
	}
	lock.running = true
	channelModelRefreshLockState.Unlock()
	return func() {
		channelModelRefreshLockState.Lock()
		lock.running = false
		lock.refs--
		if lock.refs == 0 {
			delete(channelModelRefreshLockState.locks, channelID)
		}
		channelModelRefreshLockState.Unlock()
	}, true
}

func fetchOpenAIChannelModels(httpClient *http.Client, ctx context.Context, target model.ChannelConfig, key, url string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	for _, header := range target.CustomHeader {
		if header.HeaderKey != "" {
			req.Header.Set(header.HeaderKey, header.HeaderValue)
		}
	}
	response, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	result, err := decodeChannelModelList[model.OpenAIModelList](response)
	if err != nil {
		return nil, err
	}
	models := make([]string, 0, len(result.Data))
	for _, channelModel := range result.Data {
		models = append(models, channelModel.ID)
	}
	return models, nil
}

func fetchAnthropicChannelModels(httpClient *http.Client, ctx context.Context, target model.ChannelConfig, key, url string) ([]string, error) {
	var allModels []string
	var afterID string
	seenCursors := make(map[string]struct{})
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Api-Key", key)
		req.Header.Set("Anthropic-Version", "2023-06-01")
		for _, header := range target.CustomHeader {
			if header.HeaderKey != "" {
				req.Header.Set(header.HeaderKey, header.HeaderValue)
			}
		}
		if afterID != "" {
			query := req.URL.Query()
			query.Set("after_id", afterID)
			req.URL.RawQuery = query.Encode()
		}
		response, err := httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		result, err := decodeChannelModelList[model.AnthropicModelList](response)
		if err != nil {
			return nil, err
		}
		for _, channelModel := range result.Data {
			allModels = append(allModels, channelModel.ID)
		}
		if !result.HasMore {
			break
		}
		if result.LastID == "" {
			return nil, fmt.Errorf("upstream model pagination is missing last_id")
		}
		if _, seen := seenCursors[result.LastID]; seen {
			return nil, fmt.Errorf("upstream model pagination repeated last_id")
		}
		seenCursors[result.LastID] = struct{}{}
		afterID = result.LastID
	}
	return allModels, nil
}

func decodeChannelModelList[T any](response *http.Response) (T, error) {
	defer response.Body.Close()
	var result T
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, err := io.ReadAll(io.LimitReader(response.Body, 512))
		if err != nil {
			return result, fmt.Errorf("upstream %s", response.Status)
		}
		return result, fmt.Errorf("upstream %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	decoder := json.NewDecoder(response.Body)
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return result, err
	}
	var envelope struct {
		Data *[]struct {
			ID string `json:"id"`
		} `json:"data"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return result, fmt.Errorf("invalid upstream model list: %w", err)
	}
	if envelope.Data == nil || (len(envelope.Error) > 0 && string(envelope.Error) != "null") {
		return result, fmt.Errorf("invalid upstream model list: expected data array without error")
	}
	for _, entry := range *envelope.Data {
		if strings.TrimSpace(entry.ID) == "" {
			return result, fmt.Errorf("invalid upstream model list: model id is empty")
		}
	}
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF {
		return result, fmt.Errorf("invalid upstream model list: trailing content")
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, err
	}
	return result, nil
}

func channelModelsURL(baseURL, protocolPath string) string {
	parent := path.Dir(strings.TrimRight(protocolPath, "/"))
	if strings.HasSuffix(parent, "/chat") {
		parent = path.Dir(parent)
	}
	if parent == "." || parent == "/" {
		parent = ""
	}
	return strings.TrimRight(baseURL, "/") + parent + "/models"
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// channelRefreshClient 构造公共刷新服务使用的上游客户端。处理器可传入已构造的客户端,
// 自动刷新等调用方则由服务按渠道配置自行选择直连、应用代理或渠道专用代理。
func channelRefreshClient(config model.ChannelConfig) (*http.Client, func(), error) {
	proxyURL := ""
	if config.Proxy {
		if config.ChannelProxy != "" {
			proxyURL = config.ChannelProxy
		} else {
			var err error
			proxyURL, err = SettingGetString(model.SettingKeyProxyURL)
			if err != nil {
				return nil, nil, err
			}
			if proxyURL == "" {
				return nil, nil, fmt.Errorf("proxy url is empty")
			}
		}
	}
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, nil, fmt.Errorf("default transport is not *http.Transport")
	}
	cloned := transport.Clone()
	if !config.Proxy {
		cloned.Proxy = nil
		client := &http.Client{Transport: cloned}
		return client, client.CloseIdleConnections, nil
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid proxy url: %w", err)
	}
	switch parsed.Scheme {
	case "http", "https":
		cloned.Proxy = http.ProxyURL(parsed)
	case "socks5", "socks5h":
		socksDialer, err := proxy.FromURL(parsed, &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second})
		if err != nil {
			return nil, nil, fmt.Errorf("invalid socks proxy: %w", err)
		}
		contextDialer, ok := socksDialer.(proxy.ContextDialer)
		if !ok {
			return nil, nil, fmt.Errorf("socks proxy dialer does not support context")
		}
		cloned.Proxy = nil
		cloned.DialContext = contextDialer.DialContext
	default:
		return nil, nil, fmt.Errorf("unsupported proxy scheme: %s", parsed.Scheme)
	}
	client := &http.Client{Transport: cloned}
	return client, client.CloseIdleConnections, nil
}
