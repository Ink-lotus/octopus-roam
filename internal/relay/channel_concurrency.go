package relay

import (
	"container/list"
	"context"
	"errors"
	"sync"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
)

var errChannelCapacityChanged = errors.New("channel or route changed while waiting for capacity")
var errChannelCapacityBusy = errors.New("channel capacity is occupied")

type channelCapacity struct {
	active int
	queue  list.List // 每个等待者持有独立的、容量为 1 的通知通道。
}

var channelCapacities = struct {
	sync.Mutex
	entries map[int]*channelCapacity
}{entries: make(map[int]*channelCapacity)}

func ChannelConcurrencyStats(id int) model.ChannelConcurrencyStatus {
	channelCapacities.Lock()
	defer channelCapacities.Unlock()
	if entry := channelCapacities.entries[id]; entry != nil {
		return model.ChannelConcurrencyStatus{Active: entry.active, Waiting: entry.queue.Len()}
	}
	return model.ChannelConcurrencyStatus{}
}

// 额度按渠道共享，FIFO 队列只通过释放、配置通知或取消唤醒。
// 即使当前不限流也计入 active，使运行中调低上限时不会额外放行。
func acquireChannelCapacity(ctx context.Context, id int, routeChanged <-chan struct{}, onWait ...func()) (func(), error) {
	return acquireChannelCapacityMode(ctx, id, routeChanged, true, onWait...)
}

// 恢复探测不能在持有分组选路权时排队，也不能越过已经排队的普通请求。
func tryAcquireChannelCapacity(ctx context.Context, id int, routeChanged <-chan struct{}) (func(), error) {
	return acquireChannelCapacityMode(ctx, id, routeChanged, false)
}

func acquireChannelCapacityMode(ctx context.Context, id int, routeChanged <-chan struct{}, wait bool, onWait ...func()) (func(), error) {
	channelCapacities.Lock()
	entry := channelCapacities.entries[id]
	if entry == nil {
		entry = &channelCapacity{}
		channelCapacities.entries[id] = entry
	}
	ready := make(chan struct{}, 1)
	position := entry.queue.PushBack(ready)
	remove := func() {
		wasFirst := entry.queue.Front() == position
		entry.queue.Remove(position)
		if wasFirst {
			signalChannelCapacity(id, entry)
		}
	}
	for {
		configChanged := op.ChannelConfigChanged()
		channel, err := op.ChannelGet(id)
		if ctx.Err() != nil {
			remove()
			channelCapacities.Unlock()
			return nil, ctx.Err()
		}
		select {
		case <-routeChanged:
			remove()
			channelCapacities.Unlock()
			return nil, errChannelCapacityChanged
		default:
		}
		if err != nil || !channel.Enabled {
			remove()
			channelCapacities.Unlock()
			return nil, errChannelCapacityChanged
		}
		if entry.queue.Front() == position && (channel.MaxConcurrency == 0 || entry.active < channel.MaxConcurrency) {
			entry.active++
			remove()
			channelCapacities.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() {
					channelCapacities.Lock()
					defer channelCapacities.Unlock()
					entry.active--
					signalChannelCapacity(id, entry)
				})
			}, nil
		}
		if !wait {
			remove()
			channelCapacities.Unlock()
			return nil, errChannelCapacityBusy
		}
		channelCapacities.Unlock()
		if len(onWait) > 0 {
			onWait[0]()
			onWait = nil
		}
		select {
		case <-ctx.Done():
		case <-routeChanged:
		case <-configChanged:
		case <-ready:
		}
		channelCapacities.Lock()
	}
}

// 调用方持有 channelCapacities 锁；空条目立即回收，释放不会遗留墓碑。
func signalChannelCapacity(id int, entry *channelCapacity) {
	if first := entry.queue.Front(); first != nil {
		select {
		case first.Value.(chan struct{}) <- struct{}{}:
		default:
		}
	}
	if entry.active == 0 && entry.queue.Len() == 0 {
		delete(channelCapacities.entries, id)
	}
}
