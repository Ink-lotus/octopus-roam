package op

import "sync"

// 广播先取通知句柄再读配置，更新不会因无接收者而丢失。
type configSignal struct {
	mu      sync.Mutex
	changed chan struct{}
}

func (s *configSignal) watch() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.changed == nil {
		s.changed = make(chan struct{})
	}
	return s.changed
}

func (s *configSignal) notify() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.changed != nil {
		close(s.changed)
	}
	s.changed = make(chan struct{})
}

var channelConfigSignal, automationSettingsSignal configSignal

func ChannelConfigChanged() <-chan struct{}      { return channelConfigSignal.watch() }
func AutomationSettingsChanged() <-chan struct{} { return automationSettingsSignal.watch() }
func notifyChannelConfigChanged()                { channelConfigSignal.notify() }
