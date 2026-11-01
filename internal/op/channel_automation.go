package op

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/utils/cache"
	"gorm.io/gorm/clause"
)

var channelRefreshRecords = cache.New[int, model.ChannelRefreshRecord](16)
var channelRefreshActivity = struct {
	sync.Mutex
	running map[int]time.Time
}{running: make(map[int]time.Time)}
var channelRefreshObservers = struct {
	sync.Mutex
	fns         []func(int, []model.Group)
	maintenance func(context.Context, []string) []string
}{}

// SetChannelRefreshMaintenance 由管理端启动代码登记共享的模型价格维护步骤。
func SetChannelRefreshMaintenance(fn func(context.Context, []string) []string) {
	channelRefreshObservers.Lock()
	defer channelRefreshObservers.Unlock()
	channelRefreshObservers.maintenance = fn
}

// OnChannelModelsRefreshed 在服务启动阶段登记；回调发生在事务及 op 写锁释放后。
func OnChannelModelsRefreshed(fn func(int, []model.Group)) {
	channelRefreshObservers.Lock()
	defer channelRefreshObservers.Unlock()
	channelRefreshObservers.fns = append(channelRefreshObservers.fns, fn)
}

func ChannelAutoUpdateEligible(id int) bool {
	enabled, _ := SettingGetBool(model.SettingKeyChannelAutoUpdateEnabled)
	channel, err := ChannelGet(id)
	return enabled && err == nil && channel.Enabled && channel.FollowsGlobalAutoUpdate()
}

func ChannelAutoUpdateIDs() []int {
	ids := make([]int, 0)
	for id := range channelCache.GetAll() {
		if ChannelAutoUpdateEligible(id) {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	return ids
}

func channelRefreshStartedAt(id int) *time.Time {
	channelRefreshActivity.Lock()
	defer channelRefreshActivity.Unlock()
	if started, ok := channelRefreshActivity.running[id]; ok {
		return &started
	}
	return nil
}

func ChannelRefreshSummary(id int) *model.ChannelRefreshSummary {
	started := channelRefreshStartedAt(id)
	status := &model.ChannelRefreshSummary{Running: started != nil, StartedAt: started}
	if record, ok := channelRefreshRecords.Get(id); ok {
		status.Last = &model.ChannelRefreshSummaryRecord{
			StartedAt: record.StartedAt, FinishedAt: record.FinishedAt,
			Source: record.Source, Status: record.Status,
			BeforeCount: record.BeforeCount, AfterCount: record.AfterCount,
			AddedCount: len(record.Result.AddedModels), RemovedCount: len(record.Result.RemovedModels),
			WarningCount: len(record.Result.Warnings),
		}
	}
	return status
}

func ChannelRefreshStatus(id int) *model.ChannelRefreshStatus {
	started := channelRefreshStartedAt(id)
	status := &model.ChannelRefreshStatus{Running: started != nil, StartedAt: started}
	if record, ok := channelRefreshRecords.Get(id); ok {
		// 仅复制可变字段，不在热查询里执行 JSON 往返。
		record.Result = cloneChannelRefreshResult(record.Result)
		status.Last = &record
	}
	return status
}

func cloneChannelRefreshResult(result model.ChannelModelRefreshResult) model.ChannelModelRefreshResult {
	result.AddedModels = slices.Clone(result.AddedModels)
	result.RemovedModels = slices.Clone(result.RemovedModels)
	result.KeyResults = slices.Clone(result.KeyResults)
	for i := range result.KeyResults {
		keyResult := &result.KeyResults[i]
		if keyResult.AddedCount != nil {
			count := *keyResult.AddedCount
			keyResult.AddedCount = &count
		}
		if keyResult.RemovedCount != nil {
			count := *keyResult.RemovedCount
			keyResult.RemovedCount = &count
		}
	}
	result.Warnings = slices.Clone(result.Warnings)
	return result
}

func loadChannelRefreshRecords(ctx context.Context) error {
	var records []model.ChannelRefreshRecord
	if err := db.GetDB().WithContext(ctx).Find(&records).Error; err != nil {
		return err
	}
	channelRefreshRecords.Clear()
	for _, record := range records {
		channelRefreshRecords.Set(record.ChannelID, record)
	}
	return nil
}

func refreshChannelModelsWithClient(ctx context.Context, id int, options model.ChannelModelRefreshOptions, client *http.Client) (result model.ChannelModelRefreshResult, err error) {
	result = model.ChannelModelRefreshResult{ChannelID: id, AddedModels: []string{}, RemovedModels: []string{}, KeyResults: []model.ChannelModelRefreshKeyResult{}}
	release, acquired := lockChannelModelRefresh(id)
	if !acquired {
		if options.EnabledOnly {
			reason := channelRefreshEnabledSkipReason(id)
			if reason == "" {
				reason = model.ChannelRefreshSkipInProgress
			}
			return skippedChannelModelRefresh(id, reason), nil
		}
		result.Error = ErrChannelRefreshInProgress.Error()
		return result, nil
	}
	defer release()
	if options.EnabledOnly {
		if reason := channelRefreshEnabledSkipReason(id); reason != "" {
			return skippedChannelModelRefresh(id, reason), nil
		}
	}
	detail, err := ChannelDetailGet(id)
	if err != nil {
		if options.EnabledOnly {
			if reason := channelRefreshEnabledSkipReason(id); reason != "" {
				return skippedChannelModelRefresh(id, reason), nil
			}
		}
		return result, err
	}
	if options.Automatic && !ChannelAutoUpdateEligible(id) {
		return result, ErrChannelRefreshStale
	}
	started := time.Now()
	channelRefreshActivity.Lock()
	channelRefreshActivity.running[id] = started
	channelRefreshActivity.Unlock()
	defer func() {
		channelRefreshActivity.Lock()
		delete(channelRefreshActivity.running, id)
		channelRefreshActivity.Unlock()
	}()
	before := GroupList()
	result, err = refreshChannelModelsOnce(ctx, id, options, client)
	if result.Skipped {
		// 跳过不是失败，不覆盖已有诊断；running 仍由本轮 defer 清理。
		return result, err
	}
	if err != nil {
		result.Error = err.Error()
	}
	if result.Success {
		channelRefreshObservers.Lock()
		observers := append([]func(int, []model.Group){}, channelRefreshObservers.fns...)
		maintenance := channelRefreshObservers.maintenance
		channelRefreshObservers.Unlock()
		for _, observer := range observers {
			observer(id, before)
		}
		if maintenance != nil {
			maintenanceCtx := ctx
			if options.Automatic {
				var cancel context.CancelFunc
				maintenanceCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
				defer cancel()
			}
			result.Warnings = append(result.Warnings, maintenance(maintenanceCtx, result.AddedModels)...)
		}
	}
	record := model.ChannelRefreshRecord{ChannelID: id, StartedAt: started, FinishedAt: time.Now(), Source: "manual", Status: "failed", BeforeCount: len(detail.Models), AfterCount: len(detail.Models), Result: result}
	if options.Automatic {
		record.Source = "automatic"
	}
	if result.Success {
		record.Status = "success"
		record.AfterCount += len(result.AddedModels) - len(result.RemovedModels)
	} else if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		record.Status = "canceled"
	}
	if e := saveChannelRefreshRecord(ctx, record); e != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("refresh status save: %v", e))
	}
	return result, err
}

func saveChannelRefreshRecord(ctx context.Context, record model.ChannelRefreshRecord) error {
	// 返回给调用方的切片/计数指针不能改变已保存的诊断快照。
	record.Result = cloneChannelRefreshResult(record.Result)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	channelRefreshCommitMu.Lock()
	defer channelRefreshCommitMu.Unlock()
	// 与渠道删除共用提交锁，删除完成后不再创建孤立诊断。
	if _, err := ChannelGet(record.ChannelID); err != nil {
		return nil
	}
	if err := db.GetDB().WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(&record).Error; err != nil {
		return err
	}
	channelRefreshRecords.Set(record.ChannelID, record)
	return nil
}
