package op

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Lock order: handler groupMutationMu -> channelRefreshCommitMu -> groupWriteMu
// -> backupAuxMu -> backupStatsMu -> backupCacheMu -> cooldown snapshot/engine.
// Ordinary stats operations take only backupStatsMu.RLock. Readers take only
// backupCacheMu.RLock and cache-local locks. No lock covers upstream network I/O.
var backupAuxMu sync.Mutex
var backupStatsMu sync.RWMutex
var backupCacheMu sync.RWMutex
var backupUnavailable atomic.Bool

func beginStatsOperation() (func(), error) {
	backupStatsMu.RLock()
	if backupUnavailable.Load() {
		backupStatsMu.RUnlock()
		return nil, ErrBackupCommitted
	}
	return backupStatsMu.RUnlock, nil
}

// Flush into the IMPORT transaction without draining dirty sets. Rejection or
// late write failure preserves both DB rows and unsaved in-memory increments.
// Incoming metrics replace covered keys. Do not first insert a covered key
// that was absent when planning: a newly initialized cache or first request
// can contain a statistics row that has never been persisted.
func flushBackupStats(tx *gorm.DB, incoming *model.DBDump) error {
	covered := map[string]map[any]bool{"id": {}, "date": {}, "hour": {}, "api_key_id": {}}
	for _, row := range incoming.StatsTotal {
		covered["id"][row.ID] = true
	}
	for _, row := range incoming.StatsDaily {
		covered["date"][row.Date] = true
	}
	for _, row := range incoming.StatsHourly {
		covered["hour"][row.Hour] = true
	}
	for _, row := range incoming.StatsAPIKey {
		covered["api_key_id"][row.APIKeyID] = true
	}
	put := func(row any, key string, value any) error {
		if covered[key][value] {
			return nil
		}
		var count int64
		query := tx.Model(row).Where(clause.Eq{Column: clause.Column{Name: key}, Value: value})
		if err := query.Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return tx.Omit(clause.Associations).Create(row).Error
		}
		return tx.Model(row).Where(clause.Eq{Column: clause.Column{Name: key}, Value: value}).Select("*").Omit(key, clause.Associations).Updates(row).Error
	}
	if statsTotalCache.ID != 0 {
		if err := put(&statsTotalCache, "id", statsTotalCache.ID); err != nil {
			return err
		}
	}
	if statsDailyCache.Date != "" {
		if err := put(&statsDailyCache, "date", statsDailyCache.Date); err != nil {
			return err
		}
	}
	for _, row := range statsHourlyCache {
		if row.Date != "" {
			if err := put(&row, "hour", row.Hour); err != nil {
				return err
			}
		}
	}
	for id := range statsAPIKeyCacheNeedUpdate {
		if row, ok := statsAPIKeyCache.Get(id); ok {
			if err := put(&row, "api_key_id", id); err != nil {
				return err
			}
		}
	}
	for id := range channelStatsNeedUpdate {
		if row, ok := channelCache.Get(id); ok {
			if err := tx.Model(&model.Channel{}).Where("id = ?", id).Select(backupMetricColumns).Updates(&row).Error; err != nil {
				return err
			}
		}
	}
	for id := range channelKeyStatsNeedUpdate {
		if row, ok := channelKeyCache.Get(id); ok {
			if err := tx.Model(&model.ChannelKey{}).Where("id = ?", id).Select(backupMetricColumns).Updates(&row).Error; err != nil {
				return err
			}
		}
	}
	for id := range channelModelStatsNeedUpdate {
		if row, ok := channelModelCache.Get(id); ok {
			if err := tx.Model(&model.ChannelModel{}).Where("id = ?", id).Select(backupMetricColumns).Updates(&row).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

// All fallible reads/validation finish in the transaction. Publication consumes
// that exact committed snapshot; it does not reload tables in separate windows.
func publishBackupCache(d *model.DBDump) (func(), error) {
	backupCacheMu.Lock()
	defer backupCacheMu.Unlock()
	configs := make([]*channelCooldownConfig, 0, len(d.Channels))
	for _, row := range d.Channels {
		configs = append(configs, makeChannelCooldownConfig(row, d.ChannelKeys, d.ChannelModels, d.ChannelGrants))
	}
	proxy := ""
	for _, s := range d.Settings {
		if s.Key == model.SettingKeyProxyURL {
			proxy = s.Value
		}
	}
	notify, err := publishChannelCooldownConfigs(configs, true, proxy)
	if err != nil {
		// An engine publisher may have applied part of its local state. Never
		// allow stale forwarding or later old-statistics writes over the commit.
		backupUnavailable.Store(true)
		return nil, ErrBackupCommitted
	}
	settingCache.Clear()
	for _, row := range model.DefaultSettings() {
		settingCache.Set(row.Key, row.Value)
	}
	for _, row := range d.Settings {
		if !model.IsRetiredSetting(row.Key) {
			settingCache.Set(row.Key, row.Value)
		}
	}
	channelCache.Clear()
	channelKeyCache.Clear()
	channelModelCache.Clear()
	channelGrantCache.Clear()
	for _, row := range d.Channels {
		channelCache.Set(row.ID, row)
	}
	for _, row := range d.ChannelKeys {
		channelKeyCache.Set(row.ID, row)
	}
	for _, row := range d.ChannelModels {
		channelModelCache.Set(row.ID, row)
	}
	for _, row := range d.ChannelGrants {
		channelGrantCache.Set(row.ID, row)
	}
	items := make(map[int][]model.GroupItem)
	for _, row := range d.GroupItems {
		items[row.GroupID] = append(items[row.GroupID], row)
	}
	previous := groupCache.GetAll()
	groupNameIndex.Clear()
	for _, row := range d.Groups {
		row.Items = items[row.ID]
		sortGroupItems(row.Items)
		setGroupCacheLocked(row)
		groupNameIndex.Set(row.Name, row.ID)
		delete(previous, row.ID)
	}
	for id := range previous {
		groupCache.Del(id)
	}
	categories := make([]model.GroupCategory, 0, len(d.GroupCategories))
	for _, row := range d.GroupCategories {
		name, key, _ := model.NormalizeGroupCategoryName(row.Name)
		categories = append(categories, model.GroupCategory{ID: row.ID, Name: name, NameKey: key})
	}
	setGroupCategoriesLocked(categories)
	apiKeyCache.Clear()
	apiKeyIDMap.Clear()
	llmModelCache.Clear()
	for _, row := range d.APIKeys {
		apiKeyCache.Set(row.ID, row)
		apiKeyIDMap.Set(row.APIKey, row.ID)
	}
	for _, row := range d.LLMInfos {
		llmModelCache.Set(row.Name, row.LLMPrice)
	}
	statsTotalCacheLock.Lock()
	statsTotalCache = model.StatsTotal{ID: 1}
	if len(d.StatsTotal) == 1 {
		statsTotalCache = d.StatsTotal[0]
	}
	statsTotalCacheLock.Unlock()
	statsDailyCacheLock.Lock()
	statsDailyCache = model.StatsDaily{Date: time.Now().Format("20060102")}
	for _, row := range d.StatsDaily {
		if row.Date == statsDailyCache.Date {
			statsDailyCache = row
		}
	}
	statsDailyCacheLock.Unlock()
	statsHourlyCacheLock.Lock()
	statsHourlyCache = [24]model.StatsHourly{}
	for _, row := range d.StatsHourly {
		statsHourlyCache[row.Hour] = row
	}
	statsHourlyCacheLock.Unlock()
	statsAPIKeyCache.Clear()
	for _, row := range d.StatsAPIKey {
		statsAPIKeyCache.Set(row.APIKeyID, row)
	}
	clear(channelStatsNeedUpdate)
	clear(channelKeyStatsNeedUpdate)
	clear(channelModelStatsNeedUpdate)
	clear(statsAPIKeyCacheNeedUpdate)
	backupUnavailable.Store(false)
	return notify, nil
}

// Retained for startup only. Import never calls InitCache or its writeful
// settings initialization, and therefore cannot recursively acquire op locks.
func finishBackupPublication(publish func()) {
	if publish != nil {
		publish()
	}
	notifyChannelConfigChanged()
	automationSettingsSignal.notify()
}
