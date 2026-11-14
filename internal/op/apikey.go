package op

import (
	"context"
	"fmt"
	"sort"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/utils/cache"
)

var apiKeyCache = cache.New[int, model.APIKey](16)
var apiKeyIDMap = cache.New[string, int](16)

func APIKeyCreate(key *model.APIKey, ctx context.Context) error {
	backupAuxMu.Lock()
	defer backupAuxMu.Unlock()
	if backupUnavailable.Load() {
		return ErrBackupCommitted
	}
	if err := db.GetDB().WithContext(ctx).Create(key).Error; err != nil {
		return fmt.Errorf("failed to create API key: %w", err)
	}
	apiKeyCache.Set(key.ID, *key)
	apiKeyIDMap.Set(key.APIKey, key.ID)
	return nil
}

func APIKeyUpdate(key *model.APIKey, ctx context.Context) error {
	backupAuxMu.Lock()
	defer backupAuxMu.Unlock()
	if backupUnavailable.Load() {
		return ErrBackupCommitted
	}
	existing, ok := apiKeyCache.Get(key.ID)
	if !ok {
		return fmt.Errorf("API key not found")
	}
	if key.APIKey == "" {
		key.APIKey = existing.APIKey
	}
	if err := db.GetDB().WithContext(ctx).Save(key).Error; err != nil {
		return fmt.Errorf("failed to update API key: %w", err)
	}
	if key.APIKey != existing.APIKey {
		apiKeyIDMap.Del(existing.APIKey)
		apiKeyIDMap.Set(key.APIKey, key.ID)
	}
	apiKeyCache.Set(key.ID, *key)
	return nil
}

// APIKeyList 返回全部 API Key, 按主键升序定序。
// 设置页不提供排序开关, 而缓存遍历顺序随机, 故顺序须由此处定稿。
func APIKeyList(ctx context.Context) ([]model.APIKey, error) {
	backupCacheMu.RLock()
	defer backupCacheMu.RUnlock()
	keys := make([]model.APIKey, 0, apiKeyCache.Len())
	for _, apiKey := range apiKeyCache.GetAll() {
		keys = append(keys, apiKey)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].ID < keys[j].ID })
	return keys, nil
}

func APIKeyGet(id int, ctx context.Context) (model.APIKey, error) {
	backupCacheMu.RLock()
	defer backupCacheMu.RUnlock()
	apiKey, ok := apiKeyCache.Get(id)
	if !ok {
		return model.APIKey{}, fmt.Errorf("API key not found")
	}
	return apiKey, nil
}

func APIKeyGetByAPIKey(apiKey string, ctx context.Context) (model.APIKey, error) {
	backupCacheMu.RLock()
	defer backupCacheMu.RUnlock()
	id, ok := apiKeyIDMap.Get(apiKey)
	if !ok {
		return model.APIKey{}, fmt.Errorf("API key not found")
	}
	key, ok := apiKeyCache.Get(id)
	if !ok {
		return model.APIKey{}, fmt.Errorf("API key not found")
	}
	return key, nil
}

func APIKeyDelete(id int, ctx context.Context) error {
	backupAuxMu.Lock()
	defer backupAuxMu.Unlock()
	if backupUnavailable.Load() {
		return ErrBackupCommitted
	}
	k := model.APIKey{
		ID: id,
	}
	if err := StatsAPIKeyDel(id); err != nil {
		return fmt.Errorf("failed to delete stats API key: %v", err)
	}
	result := db.GetDB().WithContext(ctx).Delete(&k)
	if result.RowsAffected == 0 {
		return fmt.Errorf("API key not found")
	}
	if result.Error != nil {
		return fmt.Errorf("failed to delete API key: %w", result.Error)
	}
	apiKeyCache.Del(k.ID)
	apiKeyIDMap.Del(k.APIKey)
	return nil
}

func apiKeyRefreshCache(ctx context.Context) error {
	apiKeys := []model.APIKey{}
	if err := db.GetDB().WithContext(ctx).Find(&apiKeys).Error; err != nil {
		return err
	}
	for _, apiKey := range apiKeys {
		apiKeyCache.Set(apiKey.ID, apiKey)
		apiKeyIDMap.Set(apiKey.APIKey, apiKey.ID)
	}
	return nil
}
