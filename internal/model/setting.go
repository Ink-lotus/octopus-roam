package model

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/dlclark/regexp2"
)

type SettingKey string

const (
	SettingKeyProxyURL                        SettingKey = "proxy_url"
	SettingKeyStatsSaveInterval               SettingKey = "stats_save_interval"        // 将统计信息写入数据库的周期(分钟)
	SettingKeyModelInfoUpdateInterval         SettingKey = "model_info_update_interval" // 模型信息更新间隔(小时)
	SettingKeyCORSAllowOrigins                SettingKey = "cors_allow_origins"         // 跨域白名单(逗号分隔, 如 "example.com,example2.com"). 为空不允许跨域, "*"允许所有
	SettingKeyModelFilter                     SettingKey = "model_filter"               // 渠道获取模型时的全局过滤表达式; 留空表示不过滤
	SettingKeyWebhookManualFailureThreshold   SettingKey = "webhook_manual_failure_threshold"
	SettingKeyWebhookFailoverChannelThreshold SettingKey = "webhook_failover_channel_threshold"
	SettingKeyGroupRouteDefaults              SettingKey = "group_route_defaults"
	SettingKeyChannelAutoUpdateEnabled        SettingKey = "channel_auto_update_enabled"
	SettingKeyChannelAutoUpdateInterval       SettingKey = "channel_auto_update_interval_minutes"
	SettingKeyChannelAutoAddGroups            SettingKey = "channel_auto_update_auto_add_groups"
)

type Setting struct {
	Key   SettingKey `json:"key" gorm:"primaryKey"`
	Value string     `json:"value" gorm:"not null"`
}

func DefaultSettings() []Setting {
	routeDefaults, _ := json.Marshal(DefaultGroupRelayConfig())
	return []Setting{
		{Key: SettingKeyProxyURL, Value: ""},
		{Key: SettingKeyStatsSaveInterval, Value: "10"},       // 默认10分钟保存一次统计信息
		{Key: SettingKeyCORSAllowOrigins, Value: ""},          // CORS 默认不允许跨域，设置为 "*" 才允许所有来源
		{Key: SettingKeyModelInfoUpdateInterval, Value: "24"}, // 默认24小时更新一次模型信息
		{Key: SettingKeyModelFilter, Value: ""},               // 默认不过滤模型
		{Key: SettingKeyWebhookManualFailureThreshold, Value: "10"},
		{Key: SettingKeyWebhookFailoverChannelThreshold, Value: "3"},
		{Key: SettingKeyGroupRouteDefaults, Value: string(routeDefaults)},
		{Key: SettingKeyChannelAutoUpdateEnabled, Value: "false"},
		{Key: SettingKeyChannelAutoUpdateInterval, Value: "1440"},
		{Key: SettingKeyChannelAutoAddGroups, Value: "false"},
	}
}

func (s *Setting) Validate() error {
	switch s.Key {
	case SettingKeyChannelAutoUpdateEnabled, SettingKeyChannelAutoAddGroups:
		if s.Value != "true" && s.Value != "false" {
			return fmt.Errorf("channel automation switch must be true or false")
		}
		return nil
	case SettingKeyChannelAutoUpdateInterval:
		value, err := strconv.Atoi(s.Value)
		if err != nil || value < 1 || value > 10080 {
			return fmt.Errorf("channel auto update interval must be between 1 and 10080 minutes")
		}
		return nil
	case SettingKeyModelInfoUpdateInterval:
		_, err := strconv.Atoi(s.Value)
		if err != nil {
			return fmt.Errorf("model info update interval must be an integer")
		}
		return nil
	case SettingKeyWebhookManualFailureThreshold, SettingKeyWebhookFailoverChannelThreshold:
		value, err := strconv.Atoi(s.Value)
		if err != nil || value < 1 {
			return fmt.Errorf("webhook threshold must be a positive integer")
		}
		return nil
	case SettingKeyModelFilter:
		if s.Value == "" {
			return nil
		}
		// 与渠道侧一致用 ECMAScript 方言校验, 避免设置能存但探测时编译失败。
		if _, err := regexp2.Compile(s.Value, regexp2.ECMAScript); err != nil {
			return fmt.Errorf("model filter regex is invalid: %w", err)
		}
		return nil
	case SettingKeyProxyURL:
		if s.Value == "" {
			return nil
		}
		parsedURL, err := url.Parse(s.Value)
		if err != nil {
			return fmt.Errorf("proxy URL is invalid: %w", err)
		}
		validSchemes := map[string]bool{
			"http":    true,
			"https":   true,
			"socks5":  true,
			"socks5h": true,
		}
		if !validSchemes[parsedURL.Scheme] {
			return fmt.Errorf("proxy URL scheme must be http, https, socks5, or socks5h")
		}
		if parsedURL.Host == "" {
			return fmt.Errorf("proxy URL must have a host")
		}
		return nil
	}

	return nil
}
