package model

import (
	"errors"
	"fmt"
	"strings"
)

var ErrInvalidGroupConfig = errors.New("invalid group configuration")

// 分组选择上游成员的模式。
type GroupMode string

const (
	GroupModeManual   GroupMode = "manual"   // 只使用人工选中的成员。
	GroupModeFailover GroupMode = "failover" // 按成员排序选择并在失败时切换。
)

// 分组模型名自动匹配模式。
type GroupModelMatchMode string

const (
	GroupModelMatchModeKeyword GroupModelMatchMode = "keyword" // 分组名作为关键词, 匹配包含该关键词的模型。
	GroupModelMatchModeExact   GroupModelMatchMode = "exact"   // 去首尾空格后匹配完整模型名。
)

// NormalizeGroupModelMatchMode 为旧分组和旧备份补齐匹配模式。
func NormalizeGroupModelMatchMode(mode GroupModelMatchMode) GroupModelMatchMode {
	if mode == "" {
		return GroupModelMatchModeKeyword
	}
	return mode
}

// ValidateGroupModelMatchMode 校验分组模型名自动匹配模式。
func ValidateGroupModelMatchMode(mode GroupModelMatchMode) error {
	if mode != GroupModelMatchModeKeyword && mode != GroupModelMatchModeExact {
		return fmt.Errorf("%w: unsupported model match mode", ErrInvalidGroupConfig)
	}
	return nil
}

// MatchGroupModelName 按分组配置判断模型是否匹配, 供编辑器和渠道刷新成员同步共用。
func MatchGroupModelName(mode GroupModelMatchMode, groupName, modelName string) bool {
	if strings.TrimSpace(groupName) == "" {
		return false
	}
	switch mode {
	case GroupModelMatchModeKeyword:
		return strings.Contains(strings.ToLower(modelName), strings.ToLower(strings.TrimSpace(groupName)))
	case GroupModelMatchModeExact:
		return strings.EqualFold(strings.TrimSpace(modelName), strings.TrimSpace(groupName))
	default:
		return false
	}
}

// 同一成员相邻两次尝试之间的等待策略。
type RetryMode string

const (
	RetryModeFixed       RetryMode = "fixed"       // 每次重试都等待固定间隔。
	RetryModeExponential RetryMode = "exponential" // 每次重试的等待按固定倍数递增, 直到最大间隔。
)

// 指数退避默认从 1 秒开始, 最大间隔默认 600 秒; 两者均可按需配置。
const (
	RetryInitialIntervalSecondsDefault = 1
	RetryMaxIntervalSecondsDefault     = 600
)

// 分组 Relay 的持久化配置，数据库中以 JSON 存储。
type GroupRelayConfig struct {
	MemberMaxAttempts                     int       `json:"member_max_attempts"`                        // 故障转移成员包含首次请求的总尝试次数。
	MemberRetryIntervalSeconds            int       `json:"member_retry_interval_seconds"`              // 固定重试间隔秒数。
	MemberNonStreamResponseTimeoutSeconds int       `json:"member_non_stream_response_timeout_seconds"` // 完整非流式响应超时秒数。
	MemberStreamFirstEventTimeoutSeconds  int       `json:"member_stream_first_event_timeout_seconds"`  // 流式首事件超时秒数。
	MemberCooldownSeconds                 int       `json:"member_cooldown_seconds"`                    // 故障转移成员冷却秒数。
	MemberAffinitySeconds                 int       `json:"member_affinity_seconds"`                    // 旧 JSON 兼容字段, 有效值和新保存值恒为 0。
	RetryMode                             RetryMode `json:"retry_mode"`                                 // 固定或指数重试。
	RetryInitialIntervalSeconds           int       `json:"retry_initial_interval_seconds"`             // 指数重试初始间隔秒数。
	RetryMaxIntervalSeconds               int       `json:"retry_max_interval_seconds"`                 // 指数重试间隔上限秒数。
	ManualMaxAttempts                     int       `json:"manual_max_attempts"`                        // 手动总尝试次数, 0 表示不限额。
}

// DefaultGroupRelayConfig 返回新分组使用的 Relay 默认配置。
func DefaultGroupRelayConfig() GroupRelayConfig {
	return GroupRelayConfig{
		MemberMaxAttempts:                     2,
		MemberRetryIntervalSeconds:            3,
		MemberNonStreamResponseTimeoutSeconds: 120,
		MemberStreamFirstEventTimeoutSeconds:  30,
		MemberCooldownSeconds:                 60,
		MemberAffinitySeconds:                 0,
		RetryMode:                             RetryModeExponential,
		RetryInitialIntervalSeconds:           RetryInitialIntervalSecondsDefault,
		RetryMaxIntervalSeconds:               RetryMaxIntervalSecondsDefault,
		ManualMaxAttempts:                     0,
	}
}

// NormalizeGroupRelayConfig 补齐分组 Relay 配置中的空值。
// 新增字段一并补默认: 旧分组缺少重试模式时回落固定模式, 保持它们原有的重试行为。
func NormalizeGroupRelayConfig(config *GroupRelayConfig) {
	defaults := DefaultGroupRelayConfig()
	if *config == (GroupRelayConfig{}) {
		*config = defaults
		return
	}
	// 手动上限 0 是合法值(不限额), 故只把负数归零。
	if config.ManualMaxAttempts < 0 {
		config.ManualMaxAttempts = 0
	}
	// 未知的重试模式取值一律回落固定模式, 避免持久化数据里的旧值或错值改变重试行为。
	if config.RetryMode != RetryModeFixed && config.RetryMode != RetryModeExponential {
		config.RetryMode = RetryModeFixed
	}
	// 600 秒只用于缺省回填; 初始间隔不超过用户配置的最大间隔。
	// 秒数转换及指数翻倍的溢出保护由实际计算等待时间的 RetryDelay 处理。
	if config.RetryMaxIntervalSeconds < 1 {
		config.RetryMaxIntervalSeconds = RetryMaxIntervalSecondsDefault
	}
	if config.RetryInitialIntervalSeconds < RetryInitialIntervalSecondsDefault {
		config.RetryInitialIntervalSeconds = RetryInitialIntervalSecondsDefault
	}
	if config.RetryInitialIntervalSeconds > config.RetryMaxIntervalSeconds {
		config.RetryInitialIntervalSeconds = config.RetryMaxIntervalSeconds
	}
	if config.MemberMaxAttempts < 1 {
		config.MemberMaxAttempts = defaults.MemberMaxAttempts
	}
	if config.MemberRetryIntervalSeconds < 1 {
		config.MemberRetryIntervalSeconds = defaults.MemberRetryIntervalSeconds
	}
	if config.MemberNonStreamResponseTimeoutSeconds < 1 {
		config.MemberNonStreamResponseTimeoutSeconds = defaults.MemberNonStreamResponseTimeoutSeconds
	}
	if config.MemberStreamFirstEventTimeoutSeconds < 1 {
		config.MemberStreamFirstEventTimeoutSeconds = defaults.MemberStreamFirstEventTimeoutSeconds
	}
	if config.MemberCooldownSeconds < 1 {
		config.MemberCooldownSeconds = defaults.MemberCooldownSeconds
	}
	config.MemberAffinitySeconds = 0
}

func ValidateGroupMode(mode GroupMode) error {
	if mode != GroupModeManual && mode != GroupModeFailover {
		return fmt.Errorf("%w: unsupported group mode", ErrInvalidGroupConfig)
	}
	return nil
}

// ValidateGroupRelayConfig 校验提交的公共与当前模式字段; 零值可由旧 JSON 补默认。
// 不使用字段级 binding 校验, 避免非当前模式的历史参数阻止保存。
func ValidateGroupRelayConfig(config GroupRelayConfig, mode GroupMode) error {
	if err := ValidateGroupMode(mode); err != nil {
		return err
	}
	if config.RetryMode != "" && config.RetryMode != RetryModeFixed && config.RetryMode != RetryModeExponential {
		return fmt.Errorf("%w: unsupported retry mode", ErrInvalidGroupConfig)
	}
	if config.MemberRetryIntervalSeconds < 0 || config.MemberNonStreamResponseTimeoutSeconds < 0 || config.MemberStreamFirstEventTimeoutSeconds < 0 {
		return fmt.Errorf("%w: retry interval and response timeouts cannot be negative", ErrInvalidGroupConfig)
	}
	if config.RetryInitialIntervalSeconds < 0 || config.RetryMaxIntervalSeconds < 0 {
		return fmt.Errorf("%w: exponential retry intervals cannot be negative", ErrInvalidGroupConfig)
	}
	if mode == GroupModeManual && config.ManualMaxAttempts < 0 {
		return fmt.Errorf("%w: manual attempts cannot be negative", ErrInvalidGroupConfig)
	}
	if mode == GroupModeFailover && (config.MemberMaxAttempts < 0 || config.MemberCooldownSeconds < 0) {
		return fmt.Errorf("%w: failover attempts and cooldown cannot be negative", ErrInvalidGroupConfig)
	}
	return nil
}

// NormalizeGroupRelayConfigForMode 补齐公共与当前模式参数, 保留另一模式的已有参数供切换使用。
func NormalizeGroupRelayConfigForMode(config *GroupRelayConfig, mode GroupMode) {
	original := *config
	NormalizeGroupRelayConfig(config)
	if original == (GroupRelayConfig{}) {
		return
	}
	if mode == GroupModeManual {
		config.MemberMaxAttempts = original.MemberMaxAttempts
		config.MemberCooldownSeconds = original.MemberCooldownSeconds
	} else if mode == GroupModeFailover {
		config.ManualMaxAttempts = original.ManualMaxAttempts
	}
}

// EffectiveGroupRelayConfig 供路由版本比较使用, 不生效字段不触发重置。
func EffectiveGroupRelayConfig(config GroupRelayConfig, mode GroupMode) GroupRelayConfig {
	NormalizeGroupRelayConfigForMode(&config, mode)
	if mode == GroupModeManual {
		config.MemberMaxAttempts = 0
		config.MemberCooldownSeconds = 0
	} else if mode == GroupModeFailover {
		config.ManualMaxAttempts = 0
	}
	return config
}

// 客户端模型名称及其可手动选择或故障转移的上游分组。
type Group struct {
	ID             int                 `json:"id" gorm:"primaryKey"`                                                          // 分组主键。
	Name           string              `json:"name" gorm:"unique;not null"`                                                   // 客户端请求使用的模型名称。
	Mode           GroupMode           `json:"mode" gorm:"not null;default:manual" binding:"omitempty,oneof=manual failover"` // 选择成员的模式。
	ModelMatchMode GroupModelMatchMode `json:"model_match_mode" gorm:"not null;default:keyword"`                              // 自动添加模型时使用的匹配模式。
	ActiveItemID   int                 `json:"active_item_id" gorm:"not null;default:0"`                                      // 手动模式指定的成员, 故障转移模式忽略该值, 0 表示未指定; 写入侧字段, 读取一律用响应中的 runtime.current_item_id, 出 JSON 仅为让备份转储带上它。
	RelayConfig    GroupRelayConfig    `json:"relay_config" gorm:"serializer:json"`                                           // 该分组的 Relay 路由配置。
	Items          []GroupItem         `json:"items" gorm:"foreignKey:GroupID;constraint:OnDelete:CASCADE"`                   // 该分组可手动选择或故障转移的分组项; 读取时恒为数组, 空集合也给出以免各消费方各自兜底。
}

// 分组内一个可选择的渠道授权分组项。
// 读取时补齐授权两侧的名称, 所属渠道与可用性: 界面只需展示与排序, 由此无需再按主键回查渠道, 模型与凭据。
// 补齐的字段不含上游凭据本身, 转发所需的完整授权由 Relay 另行按主键取。
type GroupItem struct {
	ID             int           `json:"id" gorm:"primaryKey"`                                                         // 分组项主键。
	GroupID        int           `json:"group_id" gorm:"not null;index:idx_group_grant,unique"`                        // 所属分组 ID。
	ChannelGrantID int           `json:"channel_grant_id" gorm:"not null;index:idx_group_grant,unique"`                // 引用的渠道授权 ID。
	ChannelGrant   *ChannelGrant `json:"-" gorm:"foreignKey:ChannelGrantID;references:ID;constraint:OnDelete:CASCADE"` // 仅用于声明级联外键, 授权被删除时成员随之删除; 读取时不填充, 展示所需字段见下方。
	Priority       int           `json:"priority" gorm:"not null"`                                                     // Priority 决定界面展示和故障转移模式下的成员切换顺序。

	ChannelID   int      `json:"channel_id" gorm:"-"`   // 授权所属渠道 ID。
	ChannelName string   `json:"channel_name" gorm:"-"` // 授权所属渠道名称。
	ModelName   string   `json:"model_name" gorm:"-"`   // 授权引用的上游模型名称。
	KeyName     string   `json:"key_name" gorm:"-"`     // 授权引用的凭据名称。
	Protocols   Protocol `json:"protocols" gorm:"-"`    // 授权支持的协议位掩码。
	Available   bool     `json:"available" gorm:"-"`    // 渠道与凭据均启用且模型, 凭据均存在时为真; 为假表示该成员当前无法转发, 但仍需列出以便移除。
}

// 创建分组请求; 成员顺序即优先级顺序。
// 不收主键与当前成员: 分组主键由数据库分配, 当前成员在创建后另行指定。
type GroupCreateRequest struct {
	Name           string              `json:"name" binding:"required"`                        // 客户端请求使用的模型名称。
	Mode           GroupMode           `json:"mode" binding:"omitempty,oneof=manual failover"` // 选择成员的模式, 留空按手动。
	ModelMatchMode GroupModelMatchMode `json:"model_match_mode,omitempty"`                     // 自动添加模型的匹配模式, 留空使用旧关键词规则。
	RelayConfig    GroupRelayConfig    `json:"relay_config"`                                   // Relay 路由配置, 零值由后端补默认。
	Items          []GroupItemInput    `json:"items"`                                          // 初始成员集合。
}

// 分组普通配置, 成员和当前成员的变更请求; 分组主键走路径, 不进请求体。
// 当前成员是分组的一个普通可选字段, 与其余字段共用本请求: 它不需要独立的权限, 审计或并发粒度。
type GroupUpdateRequest struct {
	Name           *string              `json:"name,omitempty"`                                           // Name 仅在名称变更时发送。
	Mode           *GroupMode           `json:"mode,omitempty" binding:"omitempty,oneof=manual failover"` // Mode 仅在选择模式变更时发送。
	ModelMatchMode *GroupModelMatchMode `json:"model_match_mode,omitempty"`                               // ModelMatchMode 仅在匹配规则变更时发送。
	RelayConfig    *GroupRelayConfig    `json:"relay_config,omitempty"`                                   // RelayConfig 仅在 Relay 配置变更时发送完整配置。
	Items          *[]GroupItemInput    `json:"items,omitempty"`                                          // 新的成员集合, 整体替换; 提交顺序即优先级顺序。
	ActiveItemID   *int                 `json:"active_item_id,omitempty"`                                 // 手动模式指定的当前成员, 0 表示取消选择; 用指针以便与"未提交该字段"区分。
}

// 提交分组成员时按渠道授权主键引用。
// 授权在提交前已由渠道页面创建, 主键必然存在, 故成员无需按名称引用;
// 成员自身的主键不参与提交: 整体替换按授权主键匹配, 已有成员的主键与统计由后端保留。
type GroupItemInput struct {
	ChannelGrantID int `json:"channel_grant_id" binding:"required"` // 待引用的渠道授权 ID。
}
