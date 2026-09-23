package relay

import (
	"maps"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// RouteState 是一个分组的进程内路由状态; 跨该分组的全部请求共享。
// 同时作为路由流的消息形状与分组读取响应中的 runtime 字段: 冷却, 探测与亲和都是本包路由算法的概念,
// 故状态形状由本包定义, 分组的持久化配置不含它; 内部标志未导出, 不会随消息出到 JSON。
// 两种模式共用 CurrentItemID: 手动模式下即人工指定的成员, 故障转移模式下由路由决定,
// 前端由此只读这一个字段即可知道当前承载请求的成员, 无需再按模式分支。
type RouteState struct {
	GroupID       int           `json:"group_id"`        // 状态所属的分组 ID, 供状态流按分组定位。
	CurrentItemID int           `json:"current_item_id"` // 当前承载请求的成员 ID, 0 表示尚未建立路由或未人工指定。
	ProbeItemID   int           `json:"probe_item_id"`   // 当前占用恢复探测的成员 ID, 同一分组同时只允许一个成员被探测; 手动模式恒为 0。
	AffinityUntil int64         `json:"affinity_until"`  // 兼容旧 API 的字段, 路由不再使用亲和, 恒为 0。
	Cooldowns     map[int]int64 `json:"cooldowns"`       // 失败成员 ID 对应的冷却截止 Unix 毫秒时间, 已到期的条目由前端按当前时间忽略。
}

// routeSelection 是一次选路的内部结果。Generation 将结果回报绑定到选路版本,
// ProbeToken 则将恢复探测绑定到唯一持有者, 防止旧请求释放后来取得的探测。
type routeSelection struct {
	Item       model.GroupItem
	Wait       bool
	WaitUntil  time.Time
	Generation uint64
	ProbeToken uint64
	Changed    <-chan struct{}
}

type routeRuntime struct {
	state      RouteState
	generation uint64
	probeToken uint64
	changed    chan struct{}
}

type routeConfigSignature struct {
	mode         model.GroupMode
	name         string
	activeItemID int
	config       model.GroupRelayConfig
	items        []routeMemberSignature
}

type routeMemberSignature struct {
	id        int
	grantID   int
	priority  int
	channelID int
	available bool
}

const (
	routeStreamBuffer             = 16                    // 单个路由流连接的非阻塞消息缓冲容量。
	routeConfigMismatchRetryDelay = 10 * time.Millisecond // 配置指纹短暂失配时限制重读频率。
	routeBlockRetention           = time.Minute           // 保留删除墓碑以拦截短暂持有旧快照的请求。
	routeBlockCleanupInterval     = time.Minute           // 限频扫描过期墓碑, 避免每次选路遍历整张表。
)

var (
	routeMu             sync.Mutex                           // routeMu 保护全部分组路由状态。
	routes              = make(map[int]*routeRuntime)        // routes 按分组 ID 保存路由状态。
	routeConfigs        = make(map[int]routeConfigSignature) // 最新配置指纹, 阻止重置后迟到的旧配置快照选路。
	routeBlocked        = make(map[int]time.Time)            // 已删除分组墓碑的过期时间, 阻止旧请求再次创建路由。
	routeBlockCleanupAt time.Time                            // 下次扫描过期墓碑的时间。
	routeStreams        = make(map[chan RouteState]struct{}) // 全部路由 SSE 连接。
	routeVersion        atomic.Uint64                        // 全局递增, Reset 后新路由也不会与旧版本发生 ABA。
	routeProbeID        atomic.Uint64                        // 全局递增的恢复探测持有者令牌。
)

// RouteStateOf 返回分组当前的实时路由状态, 供读取接口随分组一并返回。
// 手动模式没有进程内路由: 当前成员即人工指定的成员, 冷却与亲和均不适用, 故直接由分组配置得出。
func RouteStateOf(group model.Group) RouteState {
	if group.Mode == model.GroupModeManual {
		return RouteState{
			GroupID:       group.ID,
			CurrentItemID: group.ActiveItemID,
			Cooldowns:     map[int]int64{},
		}
	}

	routeMu.Lock()
	defer routeMu.Unlock()

	route := ensureRouteLocked(group)
	state := route.state
	state.Cooldowns = maps.Clone(route.state.Cooldowns)
	return state
}

// ResetRouteState 丢弃分组的进程内路由状态, 用于分组切换选择模式或被删除。
// 不丢弃的话冷却与亲和会在 failover 切到 manual 再切回来之后复活并继续影响选路, 分组删除后其状态也会永久残留。
func ResetRouteState(groupID int) {
	routeMu.Lock()
	defer routeMu.Unlock()
	now := time.Now()
	cleanupRouteBlockedLocked(now)

	if route := routes[groupID]; route != nil {
		closeRouteWaitersLocked(route)
		delete(routes, groupID)
	}
	delete(routeConfigs, groupID)
	routeBlocked[groupID] = now.Add(routeBlockRetention)
}

// SetRouteConfig 登记最新分组配置, 用于拦截重置后迟到的旧配置快照。
func SetRouteConfig(group model.Group) {
	routeMu.Lock()
	defer routeMu.Unlock()
	routeConfigs[group.ID] = routeConfigSignatureOf(group)
	delete(routeBlocked, group.ID)
}

// ResetRouteStateForGroup 清空旧状态并登记新配置, 原子地阻止旧配置快照在重置后创建新路由。
func ResetRouteStateForGroup(group model.Group) {
	routeMu.Lock()
	defer routeMu.Unlock()
	if route := routes[group.ID]; route != nil {
		closeRouteWaitersLocked(route)
		delete(routes, group.ID)
	}
	routeConfigs[group.ID] = routeConfigSignatureOf(group)
	delete(routeBlocked, group.ID)
}

// pickGroupItem 按分组模式选择目标成员, 或返回带有精确唤醒条件的等待结果。
// probeToken 非零时表示调用方仍持有同一恢复探测, 允许它在成员预算内继续重试。
func pickGroupItem(group model.Group, probeToken uint64) routeSelection {
	return pickGroupItemAt(group, probeToken, time.Now())
}

func pickGroupItemAt(group model.Group, probeToken uint64, now time.Time) routeSelection {
	routeMu.Lock()
	defer routeMu.Unlock()
	cleanupRouteBlockedLocked(now)
	if blockedUntil, blocked := routeBlocked[group.ID]; blocked {
		if now.Before(blockedUntil) {
			return staleRouteSelection(now)
		}
		delete(routeBlocked, group.ID)
	}
	actual := routeConfigSignatureOf(group)
	if expected, exists := routeConfigs[group.ID]; exists && !sameRouteConfig(expected, actual) {
		return staleRouteSelection(now)
	}
	if _, exists := routeConfigs[group.ID]; !exists {
		routeConfigs[group.ID] = actual
	}
	if group.Mode == model.GroupModeManual {
		for _, item := range group.Items {
			if item.ID == group.ActiveItemID && item.Available {
				return routeSelection{Item: item}
			}
		}
		return routeSelection{Wait: true}
	}

	route := ensureRouteLocked(group)

	// 当前成员健康时一直固定使用。仅在它已经从本轮可用成员集合中消失时重新选路。
	if route.state.CurrentItemID != 0 {
		if current, exists := availableItemOf(group, route.state.CurrentItemID); exists {
			return selectionOf(route, current, 0)
		}
		route.state.CurrentItemID = 0
		route.state.AffinityUntil = 0
		advanceRouteLocked(route)
		publishRouteLocked(route)
	}

	// 恢复探测由原持有者继续, 其失败预算与退避从该请求的首次探测尝试开始累计。
	if probeToken != 0 && route.probeToken == probeToken && route.state.ProbeItemID != 0 {
		if item, exists := availableItemOf(group, route.state.ProbeItemID); exists {
			return selectionOf(route, item, route.probeToken)
		}
	}
	if route.state.ProbeItemID != 0 {
		return routeSelection{Wait: true, Generation: route.generation, Changed: route.changed}
	}

	var earliest time.Time
	for _, item := range group.Items {
		if !item.Available {
			continue
		}
		deadlineMS, cooling := route.state.Cooldowns[item.ID]
		if cooling {
			deadline := time.UnixMilli(deadlineMS)
			if deadline.After(now) {
				if earliest.IsZero() || deadline.Before(earliest) {
					earliest = deadline
				}
				continue
			}
			// 冷却到期后只允许一个探测持有者; 同成员的重试由持有者令牌识别。
			route.state.ProbeItemID = item.ID
			route.probeToken = routeProbeID.Add(1)
			advanceRouteLocked(route)
			signalRouteLocked(route)
			publishRouteLocked(route)
			return selectionOf(route, item, route.probeToken)
		}

		route.state.CurrentItemID = item.ID
		route.state.AffinityUntil = 0
		signalRouteLocked(route)
		publishRouteLocked(route)
		return selectionOf(route, item, 0)
	}

	// 无可用成员和全员冷却都依赖状态通知或最早冷却截止时间唤醒。
	return routeSelection{
		Wait:       true,
		WaitUntil:  earliest,
		Generation: route.generation,
		Changed:    route.changed,
	}
}

// recordRouteSuccess 仅接受当前选路版本的回报; 恢复探测成功后将成员固定为当前成员。
func recordRouteSuccess(group model.Group, itemID int, generation, probeToken uint64) {
	if group.Mode == model.GroupModeManual {
		return
	}

	routeMu.Lock()
	defer routeMu.Unlock()

	route := routes[group.ID]
	if route == nil {
		return
	}
	if route.generation != generation {
		if probeToken != 0 && route.probeToken == probeToken && route.state.ProbeItemID == itemID {
			route.state.ProbeItemID = 0
			route.probeToken = 0
			advanceRouteLocked(route)
			signalRouteLocked(route)
			publishRouteLocked(route)
		}
		return
	}
	if probeToken != 0 {
		if route.probeToken != probeToken || route.state.ProbeItemID != itemID {
			return
		}
		route.state.ProbeItemID = 0
		route.probeToken = 0
		route.state.CurrentItemID = itemID
		delete(route.state.Cooldowns, itemID)
		advanceRouteLocked(route)
		signalRouteLocked(route)
		publishRouteLocked(route)
		return
	}
	if route.state.CurrentItemID != itemID {
		return
	}
}

// recordRouteFailure 上报一轮失败: 达到配置的总尝试次数后将成员打入冷却并让出当前路由。
// failures 为该成员在本请求内包含首次请求的连续失败次数, 由调用方累计。
func recordRouteFailure(group model.Group, itemID, failures int, generation, probeToken uint64) bool {
	return recordRouteFailureAt(group, itemID, failures, generation, probeToken, time.Now())
}

func recordRouteFailureAt(group model.Group, itemID, failures int, generation, probeToken uint64, now time.Time) bool {
	if group.Mode == model.GroupModeManual {
		return false
	}

	routeMu.Lock()
	defer routeMu.Unlock()

	route := routes[group.ID]
	if route == nil {
		return true // 已重置或被较新路由替换, 调用方应立即重新选路。
	}
	if route.generation != generation {
		if probeToken != 0 && route.probeToken == probeToken && route.state.ProbeItemID == itemID {
			route.state.ProbeItemID = 0
			route.probeToken = 0
			advanceRouteLocked(route)
			signalRouteLocked(route)
			publishRouteLocked(route)
		}
		return true // 旧回报不参与预算, 也不冷却新路由。
	}
	if probeToken != 0 {
		if route.probeToken != probeToken || route.state.ProbeItemID != itemID {
			return true
		}
	} else if route.state.CurrentItemID != itemID {
		return true
	}
	if failures < group.RelayConfig.MemberMaxAttempts {
		return false
	}

	deadline := now.Add(secondsToDuration(group.RelayConfig.MemberCooldownSeconds))
	route.state.Cooldowns[itemID] = deadline.UnixMilli()
	if probeToken != 0 {
		route.state.ProbeItemID = 0
		route.probeToken = 0
	}
	if route.state.CurrentItemID == itemID {
		route.state.CurrentItemID = 0
		route.state.AffinityUntil = 0
	}
	advanceRouteLocked(route)
	signalRouteLocked(route)
	publishRouteLocked(route)
	return true
}

// releaseRouteProbe 归还未产生成败结论的探测占用, 用于请求被人工中止或客户端断开。
func releaseRouteProbe(group model.Group, itemID int, generation, probeToken uint64) {
	if probeToken == 0 {
		return
	}
	routeMu.Lock()
	defer routeMu.Unlock()

	if route := routes[group.ID]; route != nil &&
		route.probeToken == probeToken && route.state.ProbeItemID == itemID {
		route.state.ProbeItemID = 0
		route.probeToken = 0
		advanceRouteLocked(route)
		signalRouteLocked(route)
		publishRouteLocked(route)
	}
}

// waitRoute 等待路由变化、最早冷却截止时间或请求取消, 不用固定间隔轮询冷却状态。
func waitRoute(ctxDone <-chan struct{}, selection routeSelection) bool {
	if selection.Changed == nil && selection.WaitUntil.IsZero() {
		return false
	}
	if selection.WaitUntil.IsZero() {
		select {
		case <-ctxDone:
			return false
		case <-selection.Changed:
			return true
		}
	}
	delay := time.Until(selection.WaitUntil)
	if delay <= 0 {
		// 截止时间在取选路结果后到期时立即重选; 重选会占用探测令牌或等待现有探测。
		select {
		case <-ctxDone:
			return false
		default:
			return true
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctxDone:
		return false
	case <-selection.Changed:
		return true
	case <-timer.C:
		return true
	}
}

// NotifyRouteStateChanged 使等待请求重读分组与成员可用性, 同时让旧请求结果失效。
// 当前成员和冷却保留; 分组自身的策略或成员变更应调用 ResetRouteState。
func NotifyRouteStateChanged(group model.Group) {
	if group.ID == 0 {
		return
	}
	routeMu.Lock()
	defer routeMu.Unlock()
	routeConfigs[group.ID] = routeConfigSignatureOf(group)
	delete(routeBlocked, group.ID)
	if route := routes[group.ID]; route != nil {
		if route.state.ProbeItemID != 0 {
			if _, available := availableItemOf(group, route.state.ProbeItemID); !available {
				route.state.ProbeItemID = 0
				route.probeToken = 0
			}
		}
		if route.state.CurrentItemID != 0 {
			if _, available := availableItemOf(group, route.state.CurrentItemID); !available {
				route.state.CurrentItemID = 0
				route.state.AffinityUntil = 0
			}
		}
		advanceRouteLocked(route)
		signalRouteLocked(route)
		publishRouteLocked(route)
	}
}

// ResetAllRouteStates 清除配置导入后的运行时状态并唤醒所有等待请求。
func ResetAllRouteStates() {
	routeMu.Lock()
	defer routeMu.Unlock()
	now := time.Now()
	cleanupRouteBlockedLocked(now)
	for groupID, route := range routes {
		closeRouteWaitersLocked(route)
		delete(routes, groupID)
		routeBlocked[groupID] = now.Add(routeBlockRetention)
	}
	for groupID := range routeConfigs {
		routeBlocked[groupID] = now.Add(routeBlockRetention)
	}
}

// SetAllRouteConfigs 为缓存整体刷新后的分组登记新配置, 同时保留已删除分组的墓碑。
func SetAllRouteConfigs(groups []model.Group) {
	routeMu.Lock()
	defer routeMu.Unlock()
	for _, group := range groups {
		routeConfigs[group.ID] = routeConfigSignatureOf(group)
		delete(routeBlocked, group.ID)
	}
}

// ensureRouteLocked 创建分组路由状态; 调用方必须持有锁。
func ensureRouteLocked(group model.Group) *routeRuntime {
	route := routes[group.ID]
	if route == nil {
		route = &routeRuntime{
			state:      RouteState{GroupID: group.ID, Cooldowns: make(map[int]int64)},
			generation: routeVersion.Add(1),
			changed:    make(chan struct{}),
		}
		routes[group.ID] = route
	}
	return route
}

func cleanupRouteBlockedLocked(now time.Time) {
	if now.Before(routeBlockCleanupAt) {
		return
	}
	for groupID, blockedUntil := range routeBlocked {
		if !now.Before(blockedUntil) {
			delete(routeBlocked, groupID)
		}
	}
	routeBlockCleanupAt = now.Add(routeBlockCleanupInterval)
}

func staleRouteSelection(now time.Time) routeSelection {
	return routeSelection{Wait: true, WaitUntil: now.Add(routeConfigMismatchRetryDelay)}
}

func routeConfigSignatureOf(group model.Group) routeConfigSignature {
	config := group.RelayConfig
	model.NormalizeGroupRelayConfig(&config)
	config.MemberAffinitySeconds = 0 // 兼容字段不再参与路由策略或版本校验。
	signature := routeConfigSignature{mode: group.Mode, name: group.Name, config: config}
	if group.Mode == model.GroupModeManual {
		signature.activeItemID = group.ActiveItemID
	}
	signature.items = make([]routeMemberSignature, 0, len(group.Items))
	for _, item := range group.Items {
		signature.items = append(signature.items, routeMemberSignature{
			id: item.ID, grantID: item.ChannelGrantID, priority: item.Priority, channelID: item.ChannelID, available: item.Available,
		})
	}
	return signature
}

func sameRouteConfig(left, right routeConfigSignature) bool {
	if left.mode != right.mode || left.name != right.name || left.activeItemID != right.activeItemID || left.config != right.config || len(left.items) != len(right.items) {
		return false
	}
	for i := range left.items {
		if left.items[i] != right.items[i] {
			return false
		}
	}
	return true
}

func availableItemOf(group model.Group, itemID int) (model.GroupItem, bool) {
	for _, item := range group.Items {
		if item.ID == itemID && item.Available {
			return item, true
		}
	}
	return model.GroupItem{}, false
}

func selectionOf(route *routeRuntime, item model.GroupItem, probeToken uint64) routeSelection {
	return routeSelection{
		Item: item, Generation: route.generation, ProbeToken: probeToken, Changed: route.changed,
	}
}

func advanceRouteLocked(route *routeRuntime) {
	route.generation = routeVersion.Add(1)
}

func signalRouteLocked(route *routeRuntime) {
	close(route.changed)
	route.changed = make(chan struct{})
}

func closeRouteWaitersLocked(route *routeRuntime) {
	close(route.changed)
}

// publishRouteLocked 非阻塞发布路由状态, 连接拥塞时关闭它并交给客户端重连获取全量快照; 冷却表按值复制以免前端读到后续变更; 调用方必须持有锁。
func publishRouteLocked(route *routeRuntime) {
	message := route.state
	message.Cooldowns = maps.Clone(route.state.Cooldowns)
	for stream := range routeStreams {
		select {
		case stream <- message:
		default:
			delete(routeStreams, stream)
			close(stream)
		}
	}
}

// OpenRouteStream 注册路由流连接, 返回后续增量通道。
// 不再返回快照: 分组读取接口已随分组带回当前路由状态, 前端由此拿到的初始值即全量, 连接只负责增量。
func OpenRouteStream() chan RouteState {
	routeMu.Lock()
	defer routeMu.Unlock()

	stream := make(chan RouteState, routeStreamBuffer)
	routeStreams[stream] = struct{}{}
	return stream
}

// CloseRouteStream 注销并关闭指定路由流连接。
func CloseRouteStream(stream chan RouteState) {
	routeMu.Lock()
	defer routeMu.Unlock()

	if _, exists := routeStreams[stream]; exists {
		delete(routeStreams, stream)
		close(stream)
	}
}
