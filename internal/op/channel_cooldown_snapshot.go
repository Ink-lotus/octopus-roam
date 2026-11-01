package op

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"gorm.io/gorm"
)

// ChannelCooldownPublisher is implemented by Relay. Configure/Remove may only
// take the engine's local lock; their returned notifications run after ALL op
// write locks have been released. op must not depend on Relay.
type ChannelCooldownPublisher interface {
	Configure(int, uint64, []model.CooldownRule) (func(), error)
	Remove(int, uint64) (func(), error)
}

// ChannelRelayConfig binds the actual outbound fields and credential to one
// committed generation. It is an internal value copy, never an API/backup DTO.
type ChannelRelayConfig struct {
	Channel    model.Channel
	Grant      model.ChannelGrant
	Generation uint64
	ProxyURL   string
}

type channelCooldownConfig struct {
	channel    model.Channel
	keys       map[int]model.ChannelKey
	models     map[int]model.ChannelModel
	grants     map[int]model.ChannelGrant
	generation uint64
	proxyURL   string
	identity   [32]byte
}

// Write order: channelRefreshCommitMu -> groupWriteMu (if needed) -> this
// publication lock -> engine lock. Readers take only this RLock, including
// member-health reads under routeMu. Neither publisher nor readers enter route.
var channelCooldownSnapshots = struct {
	sync.RWMutex
	sequence  uint64
	channels  map[int]*channelCooldownConfig
	grants    map[int]int
	publisher ChannelCooldownPublisher
}{channels: make(map[int]*channelCooldownConfig), grants: make(map[int]int)}

func runCooldownNotification(notify *func()) {
	if *notify != nil {
		(*notify)()
	}
}

// RegisterChannelCooldownPublisher also registers configurations loaded before
// Relay initialization. Replacing the publisher is useful for process-lifetime
// tests; generations remain monotonic and are never persisted.
func RegisterChannelCooldownPublisher(publisher ChannelCooldownPublisher) error {
	var notify func()
	defer runCooldownNotification(&notify)
	channelRefreshCommitMu.Lock()
	defer channelRefreshCommitMu.Unlock()
	channelCooldownSnapshots.Lock()
	defer channelCooldownSnapshots.Unlock()
	var notifications []func()
	if publisher != nil {
		for id, config := range channelCooldownSnapshots.channels {
			fn, err := publisher.Configure(id, config.generation, config.channel.CooldownRules)
			if err != nil {
				return err
			}
			if fn != nil {
				notifications = append(notifications, fn)
			}
		}
	}
	channelCooldownSnapshots.publisher = publisher
	notify = cooldownNotifications(notifications)
	return nil
}

func cloneRelayChannel(channel model.Channel) model.Channel {
	channel.Keys, channel.Models = nil, nil
	channel.StatsMetrics = model.StatsMetrics{}
	channel.CustomHeader = slices.Clone(channel.CustomHeader)
	channel.CooldownRules = model.CloneCooldownRules(channel.CooldownRules)
	if channel.FollowGlobalAutoUpdate != nil {
		follow := *channel.FollowGlobalAutoUpdate
		channel.FollowGlobalAutoUpdate = &follow
	}
	return channel
}

func makeChannelCooldownConfig(channel model.Channel, keys []model.ChannelKey, models []model.ChannelModel, grants []model.ChannelGrant) *channelCooldownConfig {
	config := &channelCooldownConfig{channel: cloneRelayChannel(channel), keys: make(map[int]model.ChannelKey),
		models: make(map[int]model.ChannelModel), grants: make(map[int]model.ChannelGrant)}
	for _, key := range keys {
		if key.ChannelID == channel.ID {
			key.StatsMetrics = model.StatsMetrics{}
			config.keys[key.ID] = key
		}
	}
	for _, m := range models {
		if m.ChannelID == channel.ID {
			m.StatsMetrics = model.StatsMetrics{}
			config.models[m.ID] = m
		}
	}
	for _, grant := range grants {
		if _, ok := config.models[grant.ChannelModelID]; ok {
			grant.ChannelKey, grant.ChannelModel = nil, nil
			config.grants[grant.ID] = grant
		}
	}
	return config
}

// Read the final transaction rows before committing. A failed transaction never
// publishes, and a later failure reloading display caches cannot retain old
// credentials or resurrect old cooldown results.
func loadChannelCooldownConfig(tx *gorm.DB, id int) (*channelCooldownConfig, error) {
	var channel model.Channel
	var keys []model.ChannelKey
	var models []model.ChannelModel
	var grants []model.ChannelGrant
	if err := tx.First(&channel, id).Error; err != nil {
		return nil, err
	}
	if err := tx.Where("channel_id = ?", id).Find(&keys).Error; err != nil {
		return nil, err
	}
	if err := tx.Where("channel_id = ?", id).Find(&models).Error; err != nil {
		return nil, err
	}
	if err := tx.Where("channel_model_id IN (?)", tx.Model(&model.ChannelModel{}).Select("id").Where("channel_id = ?", id)).Find(&grants).Error; err != nil {
		return nil, err
	}
	return makeChannelCooldownConfig(channel, keys, models, grants), nil
}

func channelCooldownIdentity(config *channelCooldownConfig, globalProxy string) [32]byte {
	c := config.channel.ChannelConfig
	c.Name, c.MatchRegex = "", ""
	c.Enabled, c.MaxConcurrency, c.FollowGlobalAutoUpdate = false, 0, nil
	config.proxyURL = ""
	if c.Proxy {
		config.proxyURL = c.ChannelProxy
		if config.proxyURL == "" {
			config.proxyURL = globalProxy
		}
	}
	type credential struct {
		ID      int
		Key     string
		Enabled bool
	}
	keys := make([]credential, 0, len(config.keys))
	for _, key := range config.keys {
		keys = append(keys, credential{key.ID, key.Key, key.Enabled})
	}
	slices.SortFunc(keys, func(a, b credential) int { return a.ID - b.ID })
	// Model/grant discovery, statistics, names, preferences, enabled and capacity
	// do not clear custom cooldowns. Only an in-memory digest contains secrets.
	data, _ := json.Marshal(struct {
		Config model.ChannelConfig
		Keys   []credential
		Proxy  string
	}{c, keys, config.proxyURL})
	return sha256.Sum256(data)
}

// The caller holds channelRefreshCommitMu and defers notify before acquiring
// its op locks. replace additionally removes channels missing during reload.
func publishChannelCooldownConfigs(configs []*channelCooldownConfig, replace bool, globalProxy string) (func(), error) {
	channelCooldownSnapshots.Lock()
	defer channelCooldownSnapshots.Unlock()
	channels := maps.Clone(channelCooldownSnapshots.channels)
	if replace {
		channels = make(map[int]*channelCooldownConfig, len(configs))
	}
	var notifications []func()
	for _, config := range configs {
		id := config.channel.ID
		config.identity = channelCooldownIdentity(config, globalProxy)
		previous := channelCooldownSnapshots.channels[id]
		if previous != nil && previous.identity == config.identity {
			config.generation = previous.generation
		} else {
			channelCooldownSnapshots.sequence++
			config.generation = channelCooldownSnapshots.sequence
		}
		if publisher := channelCooldownSnapshots.publisher; publisher != nil {
			fn, err := publisher.Configure(id, config.generation, config.channel.CooldownRules)
			if err != nil {
				return cooldownNotifications(notifications), err
			}
			if fn != nil {
				notifications = append(notifications, fn)
			}
		}
		channels[id] = config
	}
	if replace {
		for id := range channelCooldownSnapshots.channels {
			if _, exists := channels[id]; exists {
				continue
			}
			channelCooldownSnapshots.sequence++
			if publisher := channelCooldownSnapshots.publisher; publisher != nil {
				fn, err := publisher.Remove(id, channelCooldownSnapshots.sequence)
				if err != nil {
					return cooldownNotifications(notifications), err
				}
				if fn != nil {
					notifications = append(notifications, fn)
				}
			}
		}
	}
	grantChannels := make(map[int]int)
	for id, config := range channels {
		for grantID := range config.grants {
			grantChannels[grantID] = id
		}
	}
	channelCooldownSnapshots.channels, channelCooldownSnapshots.grants = channels, grantChannels
	return cooldownNotifications(notifications), nil
}

func cooldownNotifications(notifications []func()) func() {
	if len(notifications) == 0 {
		return nil
	}
	return func() {
		for _, notify := range notifications {
			notify()
		}
	}
}

func publishChannelCooldownConfig(config *channelCooldownConfig) (func(), error) {
	proxy, _ := SettingGetString(model.SettingKeyProxyURL)
	return publishChannelCooldownConfigs([]*channelCooldownConfig{config}, false, proxy)
}

func republishChannelCooldownConfigs(globalProxy string, deletedID int) (func(), error) {
	channelCooldownSnapshots.RLock()
	configs := make([]*channelCooldownConfig, 0, len(channelCooldownSnapshots.channels))
	for id, config := range channelCooldownSnapshots.channels {
		if id != deletedID {
			copy := *config // all nested maps/slices remain immutable
			configs = append(configs, &copy)
		}
	}
	channelCooldownSnapshots.RUnlock()
	return publishChannelCooldownConfigs(configs, true, globalProxy)
}

// ChannelRelaySnapshot never combines values from separate mutable caches.
// Grant/model discovery may publish a new snapshot with the SAME generation.
func ChannelRelaySnapshot(grantID int) (ChannelRelayConfig, error) {
	channelCooldownSnapshots.RLock()
	defer channelCooldownSnapshots.RUnlock()
	id := channelCooldownSnapshots.grants[grantID]
	config := channelCooldownSnapshots.channels[id]
	if config == nil {
		return ChannelRelayConfig{}, fmt.Errorf("channel grant not found")
	}
	grant, exists := config.grants[grantID]
	m, modelOK := config.models[grant.ChannelModelID]
	key, keyOK := config.keys[grant.ChannelKeyID]
	if !exists || !modelOK || !keyOK || !key.Enabled {
		return ChannelRelayConfig{}, fmt.Errorf("channel grant is unavailable")
	}
	grant.Protocols = model.EffectiveProtocols(config.channel.AllowedProtocols, grant.Protocols)
	if grant.Protocols == 0 {
		return ChannelRelayConfig{}, fmt.Errorf("channel grant has no allowed protocol")
	}
	grant.ChannelKey, grant.ChannelModel = &key, &m
	return ChannelRelayConfig{Channel: cloneRelayChannel(config.channel), Grant: grant,
		Generation: config.generation, ProxyURL: config.proxyURL}, nil
}
