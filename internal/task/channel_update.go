package task

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
)

type channelUpdateConfig struct {
	enabled  bool
	interval time.Duration
	autoAdd  bool
}

// 独立调度循环避免任务重叠，设置广播可靠地重设计时器。
type channelUpdater struct {
	config          func() channelUpdateConfig
	settingsChanged func() <-chan struct{}
	channelsChanged func() <-chan struct{}
	ids             func() []int
	eligible        func(int) bool
	refresh         func(context.Context, int, model.ChannelModelRefreshOptions) (model.ChannelModelRefreshResult, error)
	backoff         time.Duration
	cancel          context.CancelFunc
	done            chan struct{}
	closeOnce       sync.Once
}

func startChannelUpdater(parent context.Context) *channelUpdater {
	u := &channelUpdater{
		config: func() channelUpdateConfig {
			enabled, _ := op.SettingGetBool(model.SettingKeyChannelAutoUpdateEnabled)
			minutes, err := op.SettingGetInt(model.SettingKeyChannelAutoUpdateInterval)
			if err != nil || minutes < 1 || minutes > 10080 {
				enabled = false
				minutes = 1440
			}
			autoAdd, _ := op.SettingGetBool(model.SettingKeyChannelAutoAddGroups)
			return channelUpdateConfig{enabled, time.Duration(minutes) * time.Minute, autoAdd}
		},
		settingsChanged: op.AutomationSettingsChanged, channelsChanged: op.ChannelConfigChanged,
		ids: op.ChannelAutoUpdateIDs, eligible: op.ChannelAutoUpdateEligible,
		refresh: op.RefreshChannelModels, backoff: 30 * time.Second,
	}
	u.start(parent)
	return u
}

func (u *channelUpdater) start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	u.cancel, u.done = cancel, make(chan struct{})
	go func() { defer close(u.done); u.run(ctx) }()
}

func (u *channelUpdater) Close() {
	u.closeOnce.Do(u.cancel)
	<-u.done
}

func (u *channelUpdater) run(ctx context.Context) {
	changed := u.settingsChanged()
	config := u.config()
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	var tick <-chan time.Time
	reset := func() {
		timer.Stop()
		tick = nil
		if config.enabled && config.interval > 0 {
			timer.Reset(config.interval)
			tick = timer.C
		}
	}
	reset()
	var finished chan struct{}
	var cancelRun context.CancelFunc
	defer func() {
		if cancelRun != nil {
			cancelRun()
			<-finished
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-changed:
			changed = u.settingsChanged()
			config = u.config()
			if cancelRun != nil {
				cancelRun()
			}
			reset()
		case <-tick:
			if finished == nil {
				runCtx, cancel := context.WithCancel(ctx)
				cancelRun, finished = cancel, make(chan struct{})
				done := finished
				current := config
				go func() { defer close(done); u.batch(runCtx, current) }()
			}
			reset()
		case <-finished:
			cancelRun()
			cancelRun, finished = nil, nil
		}
	}
}

func (u *channelUpdater) batch(ctx context.Context, config channelUpdateConfig) {
	for _, id := range u.ids() {
		if ctx.Err() != nil {
			return
		}
		if !u.eligible(id) {
			continue
		}
		u.channel(ctx, id, config)
	}
}

func (u *channelUpdater) channel(parent context.Context, id int, config channelUpdateConfig) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		for {
			changed := u.channelsChanged()
			if !u.eligible(id) {
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-changed:
			}
		}
	}()
	defer func() { cancel(); <-watchDone }()
	for attempt := 0; attempt < 3; attempt++ {
		if ctx.Err() != nil || !u.eligible(id) {
			return
		}
		result, err := u.refresh(ctx, id, model.ChannelModelRefreshOptions{AutoAddGroups: config.autoAdd, Automatic: true})
		if result.Success || ctx.Err() != nil || errors.Is(err, op.ErrChannelRefreshStale) ||
			errors.Is(err, op.ErrInvalidChannelModelFilter) || errors.Is(err, op.ErrInvalidChannelAllowedProtocols) ||
			result.Error == op.ErrChannelRefreshInProgress.Error() || len(result.KeyResults) == 0 || attempt == 2 {
			return
		}
		timer := time.NewTimer(u.backoff * time.Duration(1<<attempt))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
