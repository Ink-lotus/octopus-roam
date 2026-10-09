package task

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/model"
)

func TestChannelUpdaterHotSettingsAndSingleBatch(t *testing.T) {
	var mu sync.Mutex
	config := channelUpdateConfig{interval: time.Hour}
	changed := make(chan struct{})
	readConfig := func() channelUpdateConfig { mu.Lock(); defer mu.Unlock(); return config }
	watch := func() <-chan struct{} { mu.Lock(); defer mu.Unlock(); return changed }
	set := func(c channelUpdateConfig) {
		mu.Lock()
		defer mu.Unlock()
		config = c
		close(changed)
		changed = make(chan struct{})
	}
	started := make(chan model.ChannelModelRefreshOptions, 8)
	canceled := make(chan struct{}, 8)
	var active, peak atomic.Int32
	u := &channelUpdater{config: readConfig, settingsChanged: watch,
		channelsChanged: func() <-chan struct{} { return nil }, ids: func() []int { return []int{1} }, eligible: func(int) bool { return true }, backoff: time.Millisecond,
		refresh: func(ctx context.Context, _ int, options model.ChannelModelRefreshOptions) (model.ChannelModelRefreshResult, error) {
			n := active.Add(1)
			if n > peak.Load() {
				peak.Store(n)
			}
			defer active.Add(-1)
			started <- options
			<-ctx.Done()
			canceled <- struct{}{}
			return model.ChannelModelRefreshResult{}, ctx.Err()
		},
	}
	u.start(t.Context())
	defer u.Close()
	select {
	case <-started:
		t.Fatal("disabled task ran")
	case <-time.After(30 * time.Millisecond):
	}
	set(channelUpdateConfig{enabled: true, interval: 10 * time.Millisecond, autoAdd: true})
	select {
	case options := <-started:
		if !options.Automatic || !options.AutoAddGroups {
			t.Fatal(options)
		}
	case <-time.After(time.Second):
		t.Fatal("setting update did not wake task")
	}
	select {
	case <-started:
		t.Fatal("overlapping batch")
	case <-time.After(40 * time.Millisecond):
	}
	set(channelUpdateConfig{enabled: false, interval: 10 * time.Millisecond})
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("disable did not cancel")
	}
	select {
	case <-started:
		t.Fatal("disabled task restarted")
	case <-time.After(30 * time.Millisecond):
	}
	set(channelUpdateConfig{enabled: true, interval: 10 * time.Millisecond})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("task did not rejoin")
	}
	if peak.Load() != 1 {
		t.Fatalf("peak = %d", peak.Load())
	}
}

func TestChannelUpdaterBackoffAndExit(t *testing.T) {
	var calls atomic.Int32
	u := &channelUpdater{channelsChanged: func() <-chan struct{} { return nil }, eligible: func(int) bool { return true }, backoff: 15 * time.Millisecond,
		refresh: func(context.Context, int, model.ChannelModelRefreshOptions) (model.ChannelModelRefreshResult, error) {
			calls.Add(1)
			return model.ChannelModelRefreshResult{KeyResults: []model.ChannelModelRefreshKeyResult{{Success: false}}}, nil
		},
	}
	start := time.Now()
	u.channel(t.Context(), 1, channelUpdateConfig{})
	if calls.Load() != 3 || time.Since(start) < 3*u.backoff {
		t.Fatalf("retry calls=%d elapsed=%v", calls.Load(), time.Since(start))
	}
	u.refresh = func(context.Context, int, model.ChannelModelRefreshOptions) (model.ChannelModelRefreshResult, error) {
		calls.Add(1)
		return model.ChannelModelRefreshResult{Error: "channel refresh already in progress"}, nil
	}
	u.channel(t.Context(), 1, channelUpdateConfig{})
	if calls.Load() != 4 {
		t.Fatal("busy refresh retried")
	}
	var eligible atomic.Bool
	eligible.Store(true)
	changed := make(chan struct{})
	u.channelsChanged = func() <-chan struct{} { return changed }
	u.eligible = func(int) bool { return eligible.Load() }
	started, stopped := make(chan struct{}), make(chan struct{})
	u.refresh = func(ctx context.Context, _ int, _ model.ChannelModelRefreshOptions) (model.ChannelModelRefreshResult, error) {
		close(started)
		<-ctx.Done()
		return model.ChannelModelRefreshResult{}, ctx.Err()
	}
	go func() { defer close(stopped); u.channel(t.Context(), 1, channelUpdateConfig{}) }()
	<-started
	eligible.Store(false)
	close(changed)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("channel opt-out did not cancel")
	}
}
