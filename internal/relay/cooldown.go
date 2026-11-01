package relay

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/model"
)

var (
	ErrCooldownGeneration = errors.New("stale or reused cooldown generation")
	ErrCooldownClosed     = errors.New("cooldown engine is closed")
)

// CooldownClock 的回调必须异步执行，不能在 AfterFunc 内同步调用。
// 测试时钟同样先释放自己的锁，再执行到期回调。
type CooldownClock interface {
	Now() time.Time
	AfterFunc(time.Duration, func()) CooldownTimer
}

type CooldownTimer interface{ Stop() bool }

type systemCooldownClock struct{}

func (systemCooldownClock) Now() time.Time { return time.Now() }
func (systemCooldownClock) AfterFunc(d time.Duration, f func()) CooldownTimer {
	return time.AfterFunc(d, f)
}

// CooldownHit 只投影命中规则，不包含错误正文、凭据或运行代次。
// Until 为展示所需的 Unix 毫秒；引擎内部保留完整 time.Time 精度。
type CooldownHit struct {
	ChannelID int                 `json:"channel_id"`
	Scope     model.CooldownScope `json:"scope"`
	ModelName string              `json:"model_name,omitempty"`
	Until     int64               `json:"until"`
	RuleIndex int                 `json:"rule_index"`
	Rule      model.CooldownRule  `json:"rule"`
}

type CooldownObservation struct {
	Until      time.Time
	Hits       []CooldownHit
	Changed    <-chan struct{}
	Generation uint64
	Configured bool
}

// CooldownToken 只能由 Capture 创建。注册对象和引擎身份防止删除、ABA 或
// 新引擎重用数值代次后接受旧请求；令牌不持有凭据或可变规则切片。
type CooldownToken struct {
	engine     *CooldownEngine
	channel    *cooldownChannel
	channelID  int
	generation uint64
	modelName  string
}

type cooldownKey struct {
	ruleIndex int
	modelName string
}

type cooldownEntry struct {
	hit   CooldownHit
	until time.Time
}

type cooldownChannel struct {
	generation uint64
	configured bool
	rules      []model.CooldownRule
	hits       map[cooldownKey]cooldownEntry
	changed    chan struct{}
	timer      CooldownTimer
	timerEpoch uint64
}

// CooldownEngine 仅管理进程内的用户规则状态，与分组成员健康和数据库独立。
// 所有回调均在引擎锁外执行。W06 才注册生产配置及接通 Record/选路/通知。
type CooldownEngine struct {
	mu       sync.Mutex
	clock    CooldownClock
	channels map[int]*cooldownChannel
	onChange func(int)
	closed   bool
	stopped  chan struct{}
}

func NewCooldownEngine(clock CooldownClock, onChange func(channelID int)) *CooldownEngine {
	if clock == nil {
		clock = systemCooldownClock{}
	}
	return &CooldownEngine{clock: clock, channels: make(map[int]*cooldownChannel), onChange: onChange, stopped: make(chan struct{})}
}

// Configure 安装已经提交的配置。新代次必须递增；同代次同规则是幂等操作。
// 返回的非 nil 通知函数必须在调用者的配置发布锁等外层锁释放后调用一次。
// 状态及订阅通道在本方法内原子替换，延后通知不会遗漏后来的观察者。
func (e *CooldownEngine) Configure(channelID int, generation uint64, rules []model.CooldownRule) (notify func(), err error) {
	if channelID <= 0 || generation == 0 {
		return nil, ErrCooldownGeneration
	}
	rules, err = model.NormalizeCooldownRules(rules)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, ErrCooldownClosed
	}
	previous := e.channels[channelID]
	if previous != nil && generation <= previous.generation {
		if generation == previous.generation && previous.configured && slices.Equal(previous.rules, rules) {
			return nil, nil
		}
		return nil, ErrCooldownGeneration
	}
	channel := &cooldownChannel{generation: generation, configured: true, rules: rules,
		hits: make(map[cooldownKey]cooldownEntry), changed: make(chan struct{})}
	if previous != nil {
		e.stopTimerLocked(previous)
		channel.changed = previous.changed
	}
	e.channels[channelID] = channel
	return e.notificationLocked(channelID, channel), nil
}

// Remove 使用新的删除代次保留轻量墓碑，不保留规则、命中或活动计时器。
// 墓碑防止旧 Configure 和迟到 Record 复活状态；通知约定同 Configure。
func (e *CooldownEngine) Remove(channelID int, generation uint64) (notify func(), err error) {
	if channelID <= 0 || generation == 0 {
		return nil, ErrCooldownGeneration
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, ErrCooldownClosed
	}
	previous := e.channels[channelID]
	if previous != nil && generation <= previous.generation {
		if generation == previous.generation && !previous.configured {
			return nil, nil
		}
		return nil, ErrCooldownGeneration
	}
	channel := &cooldownChannel{generation: generation, changed: make(chan struct{})}
	if previous != nil {
		e.stopTimerLocked(previous)
		channel.changed = previous.changed
	}
	e.channels[channelID] = channel
	return e.notificationLocked(channelID, channel), nil
}

func (e *CooldownEngine) Capture(channelID int, generation uint64, upstreamModel string) (CooldownToken, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	channel := e.channels[channelID]
	if e.closed || channel == nil || !channel.configured || channel.generation != generation || upstreamModel == "" {
		return CooldownToken{}, false
	}
	return CooldownToken{engine: e, channel: channel, channelID: channelID, generation: generation, modelName: upstreamModel}, true
}

// Observe 在同一锁内取得截止时间、命中副本和 changed；读取不运行回调，
// 也不进入 op/route。到期项立即从视图排除，计时器负责实际清理与通知。
func (e *CooldownEngine) Observe(channelID int, upstreamModel string) CooldownObservation {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return CooldownObservation{Hits: []CooldownHit{}, Changed: e.stopped}
	}
	channel := e.channels[channelID]
	if channel == nil {
		channel = &cooldownChannel{changed: make(chan struct{})}
		e.channels[channelID] = channel
	}
	observation := CooldownObservation{Hits: []CooldownHit{}, Changed: channel.changed,
		Generation: channel.generation, Configured: channel.configured}
	now := e.clock.Now()
	for _, entry := range channel.hits {
		if !entry.until.After(now) || (entry.hit.Scope == model.CooldownScopeModel && entry.hit.ModelName != upstreamModel) {
			continue
		}
		observation.Hits = append(observation.Hits, entry.hit)
		if entry.until.After(observation.Until) {
			observation.Until = entry.until
		}
	}
	sortCooldownHits(observation.Hits)
	return observation
}

func (e *CooldownEngine) Snapshot(channelID int) []CooldownHit {
	e.mu.Lock()
	defer e.mu.Unlock()
	hits := []CooldownHit{}
	if channel := e.channels[channelID]; !e.closed && channel != nil {
		now := e.clock.Now()
		for _, entry := range channel.hits {
			if entry.until.After(now) {
				hits = append(hits, entry.hit)
			}
		}
	}
	sortCooldownHits(hits)
	return hits
}

// Record 只接受由实际上游失败生成且与令牌完全对应的快照。一次失败的多条
// 规则共用 Now，每条自身截止只延长；不读 Retry-After，也没有成功清除入口。
// 调用者必须先释放 op/route 锁；本方法在解锁后同步通知。
func (e *CooldownEngine) Record(token CooldownToken, failure UpstreamFailure) bool {
	if token.engine != e || !failure.eligible() || token.channelID != failure.ChannelID ||
		token.generation != failure.Generation || token.modelName != failure.UpstreamModel {
		return false
	}
	e.mu.Lock()
	channel := e.channels[token.channelID]
	if e.closed || channel == nil || !channel.configured || channel != token.channel || channel.generation != token.generation {
		e.mu.Unlock()
		return false
	}
	now := e.clock.Now()
	changed := false
	for index, rule := range channel.rules {
		if !matchesCooldownRule(rule, failure) {
			continue
		}
		key := cooldownKey{ruleIndex: index}
		if rule.Scope == model.CooldownScopeModel {
			key.modelName = token.modelName
		}
		until := now.Add(time.Duration(rule.CooldownSeconds) * time.Second)
		if previous, ok := channel.hits[key]; ok && !until.After(previous.until) {
			continue
		}
		channel.hits[key] = cooldownEntry{until: until, hit: CooldownHit{ChannelID: token.channelID,
			Scope: rule.Scope, ModelName: key.modelName, Until: until.UnixMilli(), RuleIndex: index, Rule: rule}}
		changed = true
	}
	var notify func()
	if changed {
		e.scheduleLocked(token.channelID, channel, now)
		notify = e.notificationLocked(token.channelID, channel)
	}
	e.mu.Unlock()
	if notify != nil {
		notify()
	}
	return changed
}

func matchesCooldownRule(rule model.CooldownRule, failure UpstreamFailure) bool {
	code, keyword := strings.TrimSpace(rule.ErrorCode), strings.TrimSpace(rule.Keyword)
	if code == "" && keyword == "" {
		return false
	}
	if code != "" && code != failure.Code && code != failure.Type &&
		(failure.HTTPStatus == 0 || code != strconv.Itoa(failure.HTTPStatus)) {
		return false
	}
	return keyword == "" || strings.Contains(asciiLower(failure.Message), asciiLower(keyword))
}

func asciiLower(value string) string {
	result := []byte(value)
	for index, char := range result {
		if char >= 'A' && char <= 'Z' {
			result[index] = char + ('a' - 'A')
		}
	}
	return string(result)
}

func sortCooldownHits(hits []CooldownHit) {
	slices.SortFunc(hits, func(a, b CooldownHit) int {
		if a.RuleIndex != b.RuleIndex {
			if a.RuleIndex < b.RuleIndex {
				return -1
			}
			return 1
		}
		return strings.Compare(a.ModelName, b.ModelName)
	})
}

func (e *CooldownEngine) notificationLocked(channelID int, channel *cooldownChannel) func() {
	previous := channel.changed
	channel.changed = make(chan struct{})
	var once sync.Once
	return func() {
		once.Do(func() {
			close(previous)
			if e.onChange != nil {
				e.onChange(channelID)
			}
		})
	}
}

func (e *CooldownEngine) stopTimerLocked(channel *cooldownChannel) {
	channel.timerEpoch++
	if channel.timer != nil {
		channel.timer.Stop()
		channel.timer = nil
	}
}

func (e *CooldownEngine) scheduleLocked(channelID int, channel *cooldownChannel, now time.Time) {
	e.stopTimerLocked(channel)
	var earliest time.Time
	for _, entry := range channel.hits {
		if earliest.IsZero() || entry.until.Before(earliest) {
			earliest = entry.until
		}
	}
	if earliest.IsZero() {
		return
	}
	epoch := channel.timerEpoch
	channel.timer = e.clock.AfterFunc(max(earliest.Sub(now), 0), func() {
		e.expire(channelID, channel, epoch)
	})
}

func (e *CooldownEngine) expire(channelID int, channel *cooldownChannel, epoch uint64) {
	e.mu.Lock()
	if e.closed || e.channels[channelID] != channel || channel.timerEpoch != epoch {
		e.mu.Unlock()
		return
	}
	now := e.clock.Now()
	changed := false
	for key, entry := range channel.hits {
		if !entry.until.After(now) {
			delete(channel.hits, key)
			changed = true
		}
	}
	e.scheduleLocked(channelID, channel, now)
	var notify func()
	if changed {
		notify = e.notificationLocked(channelID, channel)
	}
	e.mu.Unlock()
	if notify != nil {
		notify()
	}
}

// Close 停止本实例计时器并使所有令牌失效；不影响持久化配置。
func (e *CooldownEngine) Close() {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.closed = true
	var notifications []func()
	for channelID, channel := range e.channels {
		e.stopTimerLocked(channel)
		notifications = append(notifications, e.notificationLocked(channelID, channel))
	}
	clear(e.channels)
	e.mu.Unlock()
	close(e.stopped)
	for _, notify := range notifications {
		notify()
	}
}
