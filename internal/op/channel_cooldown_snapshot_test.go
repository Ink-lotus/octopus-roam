package op

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/model"
)

func newCooldownSnapshotFixture(t *testing.T) (int, int) {
	t.Helper()
	detail, err := ChannelCreate(&model.ChannelDetail{ChannelConfig: model.ChannelConfig{
		Name: t.Name(), Enabled: true, BaseURL: "http://127.0.0.1/fixture-a", CustomHeader: []model.CustomHeader{{HeaderKey: "X-Fixture", HeaderValue: "a"}},
		CooldownRules: []model.CooldownRule{{ErrorCode: "429", CooldownSeconds: 60, Scope: model.CooldownScopeModel}},
	}, Keys: []model.ChannelKeyConfig{{Name: "a", Key: "fixture-a", Enabled: true}, {Name: "b", Key: "fixture-b", Enabled: true}},
		Models: []string{"fixture-model-a"}, Grants: []model.ChannelGrantConfig{{ModelName: "fixture-model-a", KeyName: "a", Protocols: model.ProtocolAll}}}, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ChannelDel(detail.ID, context.Background()) })
	for _, grant := range ChannelGrantCandidates() {
		if grant.ChannelID == detail.ID {
			return detail.ID, grant.ID
		}
	}
	t.Fatal("fixture grant missing")
	return 0, 0
}

func TestChannelCooldownSnapshotIdentity(t *testing.T) {
	cases := []struct {
		name    string
		changes bool
		mutate  func(*model.ChannelDetail)
	}{
		{"name", false, func(d *model.ChannelDetail) { d.Name += " renamed" }},
		{"capacity", false, func(d *model.ChannelDetail) { d.MaxConcurrency = 3 }},
		{"automatic-preference", false, func(d *model.ChannelDetail) { v := false; d.FollowGlobalAutoUpdate = &v }},
		{"model-filter", false, func(d *model.ChannelDetail) { d.MatchRegex = "fixture" }},
		{"enabled", false, func(d *model.ChannelDetail) { d.Enabled = false }},
		{"discovered-model", false, func(d *model.ChannelDetail) { d.Models = append(d.Models, "fixture-model-b") }},
		{"discovered-protocol", false, func(d *model.ChannelDetail) { d.Grants[0].Protocols = model.ProtocolOpenAIChatCompletion }},
		{"normalized-rule", false, func(d *model.ChannelDetail) { d.CooldownRules[0].ErrorCode = " 429 " }},
		{"rule", true, func(d *model.ChannelDetail) { d.CooldownRules[0].CooldownSeconds++ }},
		{"clear", true, func(d *model.ChannelDetail) { d.CooldownRules = []model.CooldownRule{} }},
		{"base-url", true, func(d *model.ChannelDetail) { d.BaseURL += "/changed" }},
		{"chat-path", true, func(d *model.ChannelDetail) { d.OpenAIChatCompletionPath = "/changed" }},
		{"response-path", true, func(d *model.ChannelDetail) { d.OpenAIResponsePath = "/changed" }},
		{"anthropic-path", true, func(d *model.ChannelDetail) { d.AnthropicMessagePath = "/changed" }},
		{"dialect", true, func(d *model.ChannelDetail) { d.Dialect = "fixture-dialect" }},
		{"allowed-protocols", true, func(d *model.ChannelDetail) { d.AllowedProtocols = model.ProtocolOpenAIChatCompletion }},
		{"proxy-switch", true, func(d *model.ChannelDetail) { d.Proxy = true }},
		{"dedicated-proxy", true, func(d *model.ChannelDetail) { d.ChannelProxy = "http://127.0.0.1:18999" }},
		{"header", true, func(d *model.ChannelDetail) { d.CustomHeader[0].HeaderValue = "changed" }},
		{"parameters", true, func(d *model.ChannelDetail) { d.ParamOverride = `{"temperature":0}` }},
		{"credential-value", true, func(d *model.ChannelDetail) { d.Keys[0].Key = "fixture-changed" }},
		{"credential-enabled", true, func(d *model.ChannelDetail) { d.Keys[1].Enabled = false }},
		{"credential-composition", true, func(d *model.ChannelDetail) { d.Keys = d.Keys[:1] }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, grantID := newCooldownSnapshotFixture(t)
			before, err := ChannelRelaySnapshot(grantID)
			if err != nil {
				t.Fatal(err)
			}
			detail, _ := ChannelDetailGet(id)
			tc.mutate(&detail)
			if _, err := ChannelUpdateWithOptions(&detail, model.ChannelSaveOptions{CooldownRulesPresent: true}, t.Context()); err != nil {
				t.Fatal(err)
			}
			after, err := ChannelRelaySnapshot(grantID)
			if err != nil || (after.Generation != before.Generation) != tc.changes {
				t.Fatalf("generation change=%t want=%t error=%v", after.Generation != before.Generation, tc.changes, err)
			}
			if after.Generation == 0 || (tc.changes && after.Generation <= before.Generation) {
				t.Fatal("generation is not monotonic")
			}
		})
	}
}

func TestChannelCooldownSnapshotGlobalProxyAndFailures(t *testing.T) {
	id, grantID := newCooldownSnapshotFixture(t)
	original, _ := SettingGetString(model.SettingKeyProxyURL)
	t.Cleanup(func() { _ = SettingSetString(model.SettingKeyProxyURL, original) })
	before, _ := ChannelRelaySnapshot(grantID)
	if err := SettingSetString(model.SettingKeyProxyURL, "http://127.0.0.1:18001"); err != nil {
		t.Fatal(err)
	}
	after, _ := ChannelRelaySnapshot(grantID)
	if after.Generation != before.Generation {
		t.Fatal("global proxy changed a direct channel")
	}
	detail, _ := ChannelDetailGet(id)
	detail.Proxy = true
	if _, err := ChannelUpdate(&detail, t.Context()); err != nil {
		t.Fatal(err)
	}
	before, _ = ChannelRelaySnapshot(grantID)
	if err := SettingSetString(model.SettingKeyProxyURL, "http://127.0.0.1:18002"); err != nil {
		t.Fatal(err)
	}
	after, _ = ChannelRelaySnapshot(grantID)
	if after.Generation <= before.Generation || after.ProxyURL != "http://127.0.0.1:18002" {
		t.Fatal("effective global proxy was not captured atomically")
	}
	before = after
	if err := SettingSetString(model.SettingKeyProxyURL, "  http://127.0.0.1:18002  "); err != nil {
		t.Fatal(err)
	}
	if err := SettingSetString(model.SettingKeyProxyURL, "invalid://fixture"); err == nil {
		t.Fatal("invalid proxy accepted")
	}
	detail, _ = ChannelDetailGet(id)
	detail.CooldownRules[0].CooldownSeconds = 0
	if _, err := ChannelUpdateWithOptions(&detail, model.ChannelSaveOptions{CooldownRulesPresent: true}, t.Context()); err == nil {
		t.Fatal("invalid rule accepted")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	detail, _ = ChannelDetailGet(id)
	detail.Keys[0].Key = "fixture-failed-transaction"
	if _, err := ChannelUpdate(&detail, canceled); err == nil {
		t.Fatal("canceled transaction succeeded")
	}
	after, _ = ChannelRelaySnapshot(grantID)
	if after.Generation != before.Generation || after.Grant.ChannelKey.Key != before.Grant.ChannelKey.Key {
		t.Fatal("no-op/failed saves changed the published attempt")
	}
	detail, _ = ChannelDetailGet(id)
	detail.ChannelProxy = "http://127.0.0.1:18003"
	if _, err := ChannelUpdate(&detail, t.Context()); err != nil {
		t.Fatal(err)
	}
	before, _ = ChannelRelaySnapshot(grantID)
	if err := SettingSetString(model.SettingKeyProxyURL, "http://127.0.0.1:18004"); err != nil {
		t.Fatal(err)
	}
	after, _ = ChannelRelaySnapshot(grantID)
	if after.Generation != before.Generation || after.ProxyURL != detail.ChannelProxy {
		t.Fatal("global proxy changed a dedicated-proxy channel")
	}
	if err := InitCache(); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := ChannelRelaySnapshot(grantID)
	if reloaded.Generation != after.Generation {
		t.Fatal("unchanged reload changed generation")
	}
	if err := ChannelDel(id, t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := ChannelRelaySnapshot(grantID); err == nil {
		t.Fatal("deleted channel retained a sendable snapshot")
	}
}

type snapshotNotificationProbe struct {
	t     *testing.T
	calls atomic.Int32
}

func (p *snapshotNotificationProbe) Configure(int, uint64, []model.CooldownRule) (func(), error) {
	return p.notify, nil
}
func (p *snapshotNotificationProbe) Remove(int, uint64) (func(), error) { return p.notify, nil }
func (p *snapshotNotificationProbe) notify() {
	p.calls.Add(1)
	for _, lock := range []*sync.Mutex{&channelRefreshCommitMu, &groupWriteMu} {
		if !lock.TryLock() {
			p.t.Error("notification ran under op write lock")
			return
		}
		lock.Unlock()
	}
	if !channelCooldownSnapshots.TryLock() {
		p.t.Error("notification ran under publication lock")
		return
	}
	channelCooldownSnapshots.Unlock()
}

func TestChannelCooldownSnapshotNotificationsAfterUnlock(t *testing.T) {
	probe := &snapshotNotificationProbe{t: t}
	if err := RegisterChannelCooldownPublisher(probe); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = RegisterChannelCooldownPublisher(nil) })
	id, _ := newCooldownSnapshotFixture(t)
	detail, _ := ChannelDetailGet(id)
	detail.CooldownRules[0].CooldownSeconds++
	if _, err := ChannelUpdateWithOptions(&detail, model.ChannelSaveOptions{CooldownRulesPresent: true}, t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := ChannelDel(id, t.Context()); err != nil {
		t.Fatal(err)
	}
	if probe.calls.Load() < 3 {
		t.Fatal("missing deferred notifications")
	}
}

func TestChannelCooldownSnapshotConcurrentPublicationAndCopies(t *testing.T) {
	id, grantID := newCooldownSnapshotFixture(t)
	copy, _ := ChannelRelaySnapshot(grantID)
	copy.Channel.CooldownRules[0].CooldownSeconds = 999
	copy.Channel.CustomHeader[0].HeaderValue = "mutated"
	copy.Grant.ChannelKey.Key = "mutated"
	current, _ := ChannelRelaySnapshot(grantID)
	if current.Channel.CooldownRules[0].CooldownSeconds != 60 || current.Channel.CustomHeader[0].HeaderValue != "a" || current.Grant.ChannelKey.Key != "fixture-a" {
		t.Fatal("snapshot exposed mutable published data")
	}
	var wg sync.WaitGroup
	done := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			config, err := ChannelRelaySnapshot(grantID)
			if err != nil || !strings.HasSuffix(config.Channel.BaseURL, "/"+config.Grant.ChannelKey.Key) {
				t.Error("reader combined configuration and credential from different commits")
				return
			}
		}
	}()
	for i := range 20 {
		detail, _ := ChannelDetailGet(id)
		value := fmt.Sprintf("fixture-%d", i)
		detail.BaseURL, detail.Keys[0].Key = "http://127.0.0.1/"+value, value
		if _, err := ChannelUpdate(&detail, t.Context()); err != nil {
			t.Error(err)
			break
		}
	}
	close(done)
	wg.Wait()
}
