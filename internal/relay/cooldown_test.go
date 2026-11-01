package relay

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
)

type cooldownTestClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*cooldownTestTimer
}

type cooldownTestTimer struct {
	clock  *cooldownTestClock
	at     time.Time
	run    func()
	active bool
}

func newCooldownTestClock() *cooldownTestClock {
	return &cooldownTestClock{now: time.Unix(1700000000, 123456789)}
}

func (c *cooldownTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *cooldownTestClock) AfterFunc(delay time.Duration, run func()) CooldownTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &cooldownTestTimer{clock: c, at: c.now.Add(delay), run: run, active: true}
	c.timers = append(c.timers, timer)
	return timer
}

func (timer *cooldownTestTimer) Stop() bool {
	timer.clock.mu.Lock()
	defer timer.clock.mu.Unlock()
	wasActive := timer.active
	timer.active = false
	return wasActive
}

func (c *cooldownTestClock) move(delay time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(delay)
	c.mu.Unlock()
}

func (c *cooldownTestClock) advance(delay time.Duration) {
	c.move(delay)
	for {
		c.mu.Lock()
		var due *cooldownTestTimer
		for _, timer := range c.timers {
			if timer.active && !timer.at.After(c.now) && (due == nil || timer.at.Before(due.at)) {
				due = timer
			}
		}
		if due != nil {
			due.active = false
		}
		c.mu.Unlock()
		if due == nil {
			return
		}
		due.run()
	}
}

func (c *cooldownTestClock) activeTimers() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for _, timer := range c.timers {
		if timer.active {
			count++
		}
	}
	return count
}

func configureCooldownTest(t *testing.T, engine *CooldownEngine, channelID int, generation uint64, rules ...model.CooldownRule) {
	t.Helper()
	notify, err := engine.Configure(channelID, generation, rules)
	if err != nil {
		t.Fatal(err)
	}
	if notify != nil {
		notify()
	}
}

func captureCooldownTest(t *testing.T, engine *CooldownEngine, channelID int, generation uint64, upstreamModel string) CooldownToken {
	t.Helper()
	token, ok := engine.Capture(channelID, generation, upstreamModel)
	if !ok {
		t.Fatal("Capture rejected current configuration")
	}
	return token
}

func failureForCooldownTest(t *testing.T, token CooldownToken, protocol model.Protocol, status int, body string) UpstreamFailure {
	t.Helper()
	identity := UpstreamFailure{ChannelID: token.channelID, UpstreamModel: token.modelName, Generation: token.generation, Protocol: protocol, HTTPStatus: status}
	failure, ok := extractUpstreamFailure(identity, []byte(body), "", false, "application/json")
	if !ok {
		t.Fatal("fixture did not describe an upstream failure")
	}
	return failure
}

const cooldownFailureBody = `{"error":{"code":"quota_code","type":"quota_error","message":"Fixture QUOTA reached"}}`

func assertCooldownSignal(t *testing.T, changed <-chan struct{}, want bool) {
	t.Helper()
	select {
	case <-changed:
		if !want {
			t.Fatal("unexpected change notification")
		}
	default:
		if want {
			t.Fatal("change notification was lost")
		}
	}
}

func TestCooldownMatching(t *testing.T) {
	for _, test := range []struct {
		name, code, keyword, message string
		want                         bool
	}{
		{name: "HTTP", code: "429", want: true},
		{name: "code", code: "quota_code", want: true},
		{name: "type", code: "quota_error", want: true},
		{name: "keyword", keyword: "quota", want: true},
		{name: "AND", code: "429", keyword: "fixture", want: true},
		{name: "trim", code: " \t429\n", keyword: " QuOtA \t", want: true},
		{name: "wrong-code-AND", code: "500", keyword: "quota"},
		{name: "wrong-keyword-AND", code: "429", keyword: "another"},
		{name: "case-sensitive-code", code: "QUOTA_CODE"},
		{name: "partial-code", code: "quota"},
		{name: "no-regex", keyword: "quota.*reached"},
		{name: "no-wildcard", keyword: "quot?"},
		{name: "ASCII-only", keyword: "ä", message: "Ä"},
		{name: "non-ASCII-exact", keyword: "额度", message: "Fixture 额度耗尽", want: true},
		{name: "missing-code", code: "response_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			clock := newCooldownTestClock()
			engine := NewCooldownEngine(clock, nil)
			defer engine.Close()
			configureCooldownTest(t, engine, 301, 1, model.CooldownRule{ErrorCode: test.code, Keyword: test.keyword, CooldownSeconds: 30, Scope: model.CooldownScopeModel})
			token := captureCooldownTest(t, engine, 301, 1, "fixture-model-a")
			failure := failureForCooldownTest(t, token, model.ProtocolOpenAIChatCompletion, 429, cooldownFailureBody)
			if test.message != "" {
				failure.Message = test.message
			}
			observation := engine.Observe(301, token.modelName)
			if got := engine.Record(token, failure); got != test.want {
				t.Fatalf("Record = %t, want %t", got, test.want)
			}
			assertCooldownSignal(t, observation.Changed, test.want)
			if got := len(engine.Snapshot(301)); (got == 1) != test.want {
				t.Fatalf("active hits = %d, match = %t", got, test.want)
			}
		})
	}
}

func TestCooldownScopesAndRuleDeadlines(t *testing.T) {
	clock := newCooldownTestClock()
	engine := NewCooldownEngine(clock, nil)
	defer engine.Close()
	rules := []model.CooldownRule{
		{ErrorCode: "429", CooldownSeconds: 10, Scope: model.CooldownScopeModel},
		{Keyword: "quota", CooldownSeconds: 30, Scope: model.CooldownScopeModel},
		{ErrorCode: "quota_error", CooldownSeconds: 20, Scope: model.CooldownScopeChannel},
	}
	configureCooldownTest(t, engine, 301, 1, rules...)
	configureCooldownTest(t, engine, 302, 2, rules...)
	token := captureCooldownTest(t, engine, 301, 1, "fixture-model-a")
	failure := failureForCooldownTest(t, token, model.ProtocolOpenAIChatCompletion, 429, cooldownFailureBody)
	start := clock.Now()
	if !engine.Record(token, failure) {
		t.Fatal("matching rules were not recorded")
	}
	a, b := engine.Observe(301, "fixture-model-a"), engine.Observe(301, "fixture-model-b")
	if !a.Until.Equal(start.Add(30*time.Second)) || len(a.Hits) != 3 ||
		!b.Until.Equal(start.Add(20*time.Second)) || len(b.Hits) != 1 || b.Hits[0].ModelName != "" {
		t.Fatalf("scope/max deadline mismatch: a=%+v b=%+v", a, b)
	}
	if !engine.Observe(302, "fixture-model-a").Until.IsZero() {
		t.Fatal("same model on another channel was cooled")
	}
	if got := engine.Observe(301, "Fixture-model-a"); len(got.Hits) != 1 {
		t.Fatal("model identity was case-folded")
	}
	// Separate keys, grants and groups all capture the actual channel/model.
	// Each protocol still carries its own failure identity; none becomes a key.
	for index, protocol := range []model.Protocol{model.ProtocolOpenAIChatCompletion, model.ProtocolOpenAIResponse, model.ProtocolAnthropicMessage} {
		grant := model.ChannelGrant{ID: 401 + index, ChannelKeyID: 201 + index,
			ChannelModel: &model.ChannelModel{ID: 101, ChannelID: 301, Name: "fixture-model-a"}, Protocols: protocol}
		group := model.Group{ID: 501 + index}
		shared := captureCooldownTest(t, engine, grant.ChannelModel.ChannelID, 1, grant.ChannelModel.Name)
		if got := engine.Observe(shared.channelID, shared.modelName); !got.Until.Equal(a.Until) {
			t.Fatalf("group %d/key %d bypassed shared state", group.ID, grant.ChannelKeyID)
		}
		if engine.Record(shared, failureForCooldownTest(t, shared, protocol, 429, cooldownFailureBody)) {
			t.Fatal("same-time failure extended or duplicated a target")
		}
	}
	// Consumers receive deep value copies, including the rule values.
	a.Hits[0].Rule.ErrorCode = "mutated"
	snapshot := engine.Snapshot(301)
	snapshot[0].Until = 0
	if engine.Snapshot(301)[0].Rule.ErrorCode != "429" || engine.Snapshot(301)[0].Until == 0 {
		t.Fatal("snapshot mutation changed the engine")
	}
	clock.advance(20 * time.Second)
	if len(engine.Observe(301, "fixture-model-a").Hits) != 1 || !engine.Observe(301, "fixture-model-b").Until.IsZero() {
		t.Fatal("independent rule deadlines were merged or added")
	}
}

func TestCooldownConcurrencyOnlyExtends(t *testing.T) {
	clock := newCooldownTestClock()
	var notifications atomic.Int32
	var engine *CooldownEngine
	engine = NewCooldownEngine(clock, func(id int) {
		_ = engine.Snapshot(id) // Callback must never hold the engine mutex.
		notifications.Add(1)
	})
	defer engine.Close()
	configureCooldownTest(t, engine, 301, 1, model.CooldownRule{ErrorCode: "429", CooldownSeconds: 60, Scope: model.CooldownScopeChannel})
	token := captureCooldownTest(t, engine, 301, 1, "fixture-model-a")
	failure := failureForCooldownTest(t, token, model.ProtocolOpenAIChatCompletion, 429, cooldownFailureBody)
	engine.Record(token, failure)
	start := clock.Now()
	clock.advance(10 * time.Second)
	ready, release := make(chan struct{}, 32), make(chan struct{})
	var writes atomic.Int32
	var workers sync.WaitGroup
	for range 32 {
		workers.Go(func() {
			ready <- struct{}{}
			<-release
			if engine.Record(token, failure) {
				writes.Add(1)
			}
			_ = engine.Observe(301, "fixture-model-b")
			_ = engine.Snapshot(301)
		})
	}
	for range 32 {
		<-ready
	}
	close(release)
	workers.Wait()
	if writes.Load() != 1 || notifications.Load() != 3 || !engine.Observe(301, "fixture-model-b").Until.Equal(start.Add(70*time.Second)) {
		t.Fatalf("concurrent extension: writes=%d notifications=%d", writes.Load(), notifications.Load())
	}
	clock.move(-5 * time.Second)
	if engine.Record(token, failure) || !engine.Observe(301, token.modelName).Until.Equal(start.Add(70*time.Second)) {
		t.Fatal("an earlier candidate deadline shortened the active state")
	}
	if engine.Record(token, UpstreamFailure{}) {
		t.Fatal("success/no failure changed cooldown")
	}
	if notifications.Load() != 3 || clock.activeTimers() != 1 {
		t.Fatal("no-op record emitted notifications or duplicated timers")
	}
}

func TestCooldownConfigurationGenerations(t *testing.T) {
	clock := newCooldownTestClock()
	engine := NewCooldownEngine(clock, nil)
	defer engine.Close()
	rules := []model.CooldownRule{{ErrorCode: "429", CooldownSeconds: 20, Scope: model.CooldownScopeModel}}
	configureCooldownTest(t, engine, 301, 10, rules...)
	tokenA := captureCooldownTest(t, engine, 301, 10, "fixture-model-a")
	failureA := failureForCooldownTest(t, tokenA, model.ProtocolOpenAIChatCompletion, 429, cooldownFailureBody)
	engine.Record(tokenA, failureA)
	original := engine.Snapshot(301)
	rules[0].CooldownSeconds = 99
	if engine.Snapshot(301)[0].Rule.CooldownSeconds != 20 {
		t.Fatal("Configure retained the caller's rule slice")
	}
	// Metadata changes keep generation and normalized rules. The op fingerprint
	// deciding which fields advance generation is explicitly W06's boundary.
	notify, err := engine.Configure(301, 10, []model.CooldownRule{{ErrorCode: " 429 ", CooldownSeconds: 20, Scope: model.CooldownScopeModel}})
	if err != nil || notify != nil || !reflect.DeepEqual(original, engine.Snapshot(301)) {
		t.Fatal("same generation/configuration cleared live state")
	}
	for _, generation := range []uint64{9, 10} {
		if _, err := engine.Configure(301, generation, rules); !errors.Is(err, ErrCooldownGeneration) {
			t.Fatal("stale/reused generation accepted a changed configuration")
		}
	}
	if _, err := engine.Configure(301, 11, []model.CooldownRule{{CooldownSeconds: 20, Scope: model.CooldownScopeModel}}); err == nil {
		t.Fatal("invalid configuration accepted")
	}
	if !reflect.DeepEqual(original, engine.Snapshot(301)) {
		t.Fatal("failed Configure mutated runtime state")
	}
	oldObservation := engine.Observe(301, tokenA.modelName)
	// Simulate changed outbound credentials with otherwise identical rules.
	notify, err = engine.Configure(301, 11, []model.CooldownRule{original[0].Rule})
	if err != nil || notify == nil || len(engine.Snapshot(301)) != 0 {
		t.Fatal("new outbound generation did not clear old state")
	}
	assertCooldownSignal(t, oldObservation.Changed, false)
	afterPublish := engine.Observe(301, tokenA.modelName)
	notify()
	notify() // Idempotent dispatch permits common cleanup paths.
	assertCooldownSignal(t, oldObservation.Changed, true)
	assertCooldownSignal(t, afterPublish.Changed, false)
	if _, ok := engine.Capture(301, 10, tokenA.modelName); ok {
		t.Fatal("old configuration could capture a new attempt")
	}
	lateReady, lateRelease, lateResult := make(chan struct{}), make(chan struct{}), make(chan bool)
	go func() {
		close(lateReady)
		<-lateRelease
		lateResult <- engine.Record(tokenA, failureA)
	}()
	<-lateReady
	configureCooldownTest(t, engine, 301, 12, original[0].Rule) // A -> B -> A still has a new generation.
	close(lateRelease)
	if <-lateResult || len(engine.Snapshot(301)) != 0 {
		t.Fatal("late A result survived the ABA configuration change")
	}
	tokenNew := captureCooldownTest(t, engine, 301, 12, tokenA.modelName)
	failureNew := failureForCooldownTest(t, tokenNew, model.ProtocolOpenAIChatCompletion, 429, cooldownFailureBody)
	if !engine.Record(tokenNew, failureNew) {
		t.Fatal("new attempt was rejected")
	}
	configureCooldownTest(t, engine, 301, 13) // Explicit rule clear.
	if len(engine.Snapshot(301)) != 0 || engine.Record(tokenNew, failureNew) || clock.activeTimers() != 0 {
		t.Fatal("rule clear left state or accepted an old result")
	}
	emptyToken := captureCooldownTest(t, engine, 301, 13, tokenA.modelName)
	if engine.Record(emptyToken, failureForCooldownTest(t, emptyToken, model.ProtocolOpenAIChatCompletion, 429, cooldownFailureBody)) {
		t.Fatal("default-empty rules created a hidden cooldown")
	}
	notify, err = engine.Remove(301, 14)
	if err != nil {
		t.Fatal(err)
	}
	notify()
	if _, ok := engine.Capture(301, 14, tokenA.modelName); ok || engine.Record(emptyToken, failureNew) {
		t.Fatal("deleted channel accepted an attempt")
	}
	if _, err := engine.Configure(301, 14, rules); !errors.Is(err, ErrCooldownGeneration) {
		t.Fatal("deleted generation resurrected its channel")
	}
	configureCooldownTest(t, engine, 301, 15, rules...)
	if engine.Record(tokenA, failureA) || engine.Record(tokenNew, failureNew) || len(engine.Snapshot(301)) != 0 {
		t.Fatal("ID reuse accepted a pre-deletion token")
	}
}

func TestCooldownExpiryAndNotifications(t *testing.T) {
	clock := newCooldownTestClock()
	engine := NewCooldownEngine(clock, nil)
	defer engine.Close()
	beforeConfiguration := engine.Observe(301, "fixture-model-a")
	configureCooldownTest(t, engine, 301, 1,
		model.CooldownRule{ErrorCode: "429", CooldownSeconds: 5, Scope: model.CooldownScopeModel},
		model.CooldownRule{Keyword: "quota", CooldownSeconds: 8, Scope: model.CooldownScopeChannel})
	assertCooldownSignal(t, beforeConfiguration.Changed, true)
	token := captureCooldownTest(t, engine, 301, 1, "fixture-model-a")
	failure := failureForCooldownTest(t, token, model.ProtocolOpenAIChatCompletion, 429, cooldownFailureBody)
	engine.Record(token, failure)
	beforeExpiry := engine.Observe(301, token.modelName)
	clock.move(5 * time.Second) // A delayed timer must not expose expired hits.
	if got := engine.Observe(301, token.modelName); len(got.Hits) != 1 || got.Hits[0].Scope != model.CooldownScopeChannel {
		t.Fatal("Observe retained an expired rule")
	}
	clock.advance(0)
	assertCooldownSignal(t, beforeExpiry.Changed, true)
	if len(engine.channels[301].hits) != 1 || clock.activeTimers() != 1 {
		t.Fatal("timer did not remove its expired entry and schedule the next")
	}
	nextExpiry := engine.Observe(301, token.modelName)
	clock.advance(3 * time.Second)
	assertCooldownSignal(t, nextExpiry.Changed, true)
	if len(engine.Snapshot(301)) != 0 || len(engine.channels[301].hits) != 0 || clock.activeTimers() != 0 {
		t.Fatal("expiry left active state or a background timer")
	}
	if !engine.Record(token, failure) {
		t.Fatal("a later natural failure could not begin a new cooldown")
	}
	// A callback already dispatched by a real timer may arrive after Stop.
	clock.mu.Lock()
	oldCallback := clock.timers[len(clock.timers)-1].run
	clock.mu.Unlock()
	configureCooldownTest(t, engine, 301, 2, model.CooldownRule{ErrorCode: "429", CooldownSeconds: 60, Scope: model.CooldownScopeModel})
	newToken := captureCooldownTest(t, engine, 301, 2, token.modelName)
	engine.Record(newToken, failureForCooldownTest(t, newToken, model.ProtocolOpenAIChatCompletion, 429, cooldownFailureBody))
	current := engine.Observe(301, token.modelName)
	oldCallback()
	assertCooldownSignal(t, current.Changed, false)
	if !engine.Observe(301, token.modelName).Until.Equal(current.Until) || clock.activeTimers() != 1 {
		t.Fatal("stopped timer callback changed the new generation")
	}
	engine.Close()
	assertCooldownSignal(t, current.Changed, true)
	if clock.activeTimers() != 0 || engine.Record(newToken, failure) {
		t.Fatal("closed engine accepted a result or retained a timer")
	}
}

func TestCooldownRejectsUnrelatedFailures(t *testing.T) {
	engine := NewCooldownEngine(newCooldownTestClock(), nil)
	defer engine.Close()
	configureCooldownTest(t, engine, 301, 1, model.CooldownRule{Keyword: "quota", CooldownSeconds: 1, Scope: model.CooldownScopeChannel})
	token := captureCooldownTest(t, engine, 301, 1, "fixture-model-a")
	baseline := failureForCooldownTest(t, token, model.ProtocolOpenAIChatCompletion, 429, cooldownFailureBody)
	for _, alter := range []func(*UpstreamFailure){
		func(f *UpstreamFailure) { f.confirmed = false },
		func(f *UpstreamFailure) { f.Source = "local_validation" },
		func(f *UpstreamFailure) { f.Source = "model_refresh" },
		func(f *UpstreamFailure) { f.Source = "client_cancelled" },
		func(f *UpstreamFailure) { f.Generation++ },
		func(f *UpstreamFailure) { f.ChannelID++ },
		func(f *UpstreamFailure) { f.UpstreamModel = "public-group-name" },
		func(f *UpstreamFailure) { f.Protocol = model.ProtocolAll },
		func(f *UpstreamFailure) { f.HTTPStatus = 200 },
	} {
		failure := baseline
		alter(&failure)
		if engine.Record(token, failure) {
			t.Fatalf("unrelated failure was accepted: %+v", failure)
		}
	}
	if len(engine.Snapshot(301)) != 0 {
		t.Fatal("rejected failures left state")
	}
}

func TestCooldownDurationBounds(t *testing.T) {
	for _, seconds := range []int64{1, model.MaxCooldownSeconds} {
		t.Run(time.Duration(seconds*time.Second.Nanoseconds()).String(), func(t *testing.T) {
			clock := newCooldownTestClock()
			engine := NewCooldownEngine(clock, nil)
			defer engine.Close()
			configureCooldownTest(t, engine, 301, 1, model.CooldownRule{ErrorCode: "429", CooldownSeconds: seconds, Scope: model.CooldownScopeModel})
			token := captureCooldownTest(t, engine, 301, 1, "fixture-model-a")
			duration := time.Duration(seconds) * time.Second
			start := clock.Now()
			engine.Record(token, failureForCooldownTest(t, token, model.ProtocolOpenAIChatCompletion, 429, cooldownFailureBody))
			if !engine.Observe(301, token.modelName).Until.Equal(start.Add(duration)) {
				t.Fatal("seconds conversion lost precision or overflowed")
			}
			clock.advance(duration - time.Nanosecond)
			if len(engine.Snapshot(301)) != 1 {
				t.Fatal("cooldown expired before its exact deadline")
			}
			clock.advance(time.Nanosecond)
			if len(engine.Snapshot(301)) != 0 {
				t.Fatal("cooldown outlived its exact deadline")
			}
		})
	}
}

func TestCooldownConcurrentConfigurationAndTimers(t *testing.T) {
	clock := newCooldownTestClock()
	var engine *CooldownEngine
	engine = NewCooldownEngine(clock, func(id int) { _ = engine.Snapshot(id) })
	defer engine.Close()
	rule := model.CooldownRule{ErrorCode: "429", CooldownSeconds: 1, Scope: model.CooldownScopeChannel}
	configureCooldownTest(t, engine, 301, 1, rule)
	oldToken := captureCooldownTest(t, engine, 301, 1, "fixture-model-a")
	oldFailure := failureForCooldownTest(t, oldToken, model.ProtocolOpenAIChatCompletion, 429, cooldownFailureBody)
	ready, release := make(chan struct{}, 4), make(chan struct{})
	var workers sync.WaitGroup
	jobs := []func(){
		func() {
			for generation := uint64(2); generation <= 40; generation++ {
				configureCooldownTest(t, engine, 301, generation, rule)
				token := captureCooldownTest(t, engine, 301, generation, "fixture-model-a")
				engine.Record(token, failureForCooldownTest(t, token, model.ProtocolOpenAIChatCompletion, 429, cooldownFailureBody))
			}
		},
		func() {
			for range 256 {
				engine.Record(oldToken, oldFailure)
			}
		},
		func() {
			for range 256 {
				_ = engine.Observe(301, "fixture-model-b")
				snapshot := engine.Snapshot(301)
				if len(snapshot) > 0 {
					snapshot[0].Rule.ErrorCode = "mutated copy"
				}
			}
		},
		func() {
			for range 256 {
				clock.advance(10 * time.Millisecond)
			}
		},
	}
	for _, job := range jobs {
		workers.Go(func() { ready <- struct{}{}; <-release; job() })
	}
	for range jobs {
		<-ready
	}
	close(release)
	workers.Wait()
	if observation := engine.Observe(301, "fixture-model-a"); observation.Generation != 40 || !observation.Configured {
		t.Fatal("concurrent operations rolled back configuration generation")
	}
	notify, err := engine.Remove(301, 41)
	if err != nil {
		t.Fatal(err)
	}
	notify()
	clock.advance(2 * time.Second)
	if engine.Record(oldToken, oldFailure) || len(engine.Snapshot(301)) != 0 || clock.activeTimers() != 0 {
		t.Fatal("concurrent timer/result revived a removed channel")
	}
}

func TestCooldownRuntimeNotPersisted(t *testing.T) {
	grantID := newTestChannel(t, testName(t, "cooldown-persistence"), "http://example.invalid", "fixture-model-a")
	channelID := channelIDOfGrant(t, grantID)
	detail, err := op.ChannelDetailGet(channelID)
	if err != nil {
		t.Fatal(err)
	}
	detail.CooldownRules = []model.CooldownRule{{ErrorCode: "429", CooldownSeconds: 60, Scope: model.CooldownScopeModel}}
	if _, err := op.ChannelUpdateWithOptions(&detail, model.ChannelSaveOptions{CooldownRulesPresent: true}, t.Context()); err != nil {
		t.Fatal(err)
	}
	readBackupChannel := func() []byte {
		var stored model.Channel
		if err := db.GetDB().First(&stored, channelID).Error; err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(model.DBDump{Channels: []model.Channel{stored}})
		if err != nil {
			t.Fatal(err)
		}
		dump, err := op.DBExportAll(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		backup, err := json.Marshal(dump)
		if err != nil {
			t.Fatal(err)
		}
		for _, runtimeField := range []string{`"rule_cooldowns":`, `"rule_cooldown_version":`, `"generation":`} {
			if bytes.Contains(backup, []byte(runtimeField)) {
				t.Fatalf("runtime field entered the real backup export: %s", runtimeField)
			}
		}
		found := false
		for _, channel := range dump.Channels {
			if channel.ID == channelID {
				found = slices.Equal(channel.CooldownRules, detail.CooldownRules)
			}
		}
		if !found {
			t.Fatal("W04 rules missing from the real backup export")
		}
		return encoded
	}
	before := readBackupChannel()
	clock := newCooldownTestClock()
	engine := NewCooldownEngine(clock, nil)
	configureCooldownTest(t, engine, channelID, 42, detail.CooldownRules...)
	token := captureCooldownTest(t, engine, channelID, 42, "fixture-model-a")
	failure := failureForCooldownTest(t, token, model.ProtocolOpenAIChatCompletion, 429, cooldownFailureBody)
	if !engine.Record(token, failure) || len(engine.Snapshot(channelID)) != 1 {
		t.Fatal("fixture did not create live state")
	}
	if !bytes.Equal(before, readBackupChannel()) {
		t.Fatal("runtime state changed database/backup channel bytes")
	}
	engine.Close()
	if err := op.InitCache(); err != nil {
		t.Fatal(err)
	}
	stored, err := op.ChannelGet(channelID)
	if err != nil || !slices.Equal(stored.CooldownRules, detail.CooldownRules) {
		t.Fatalf("W04 configuration did not survive reload: %v", err)
	}
	restarted := NewCooldownEngine(newCooldownTestClock(), nil)
	defer restarted.Close()
	configureCooldownTest(t, restarted, channelID, 42, stored.CooldownRules...)
	if len(restarted.Snapshot(channelID)) != 0 || restarted.Record(token, failure) || !bytes.Equal(before, readBackupChannel()) {
		t.Fatal("restart restored runtime state or accepted a token from another engine")
	}
}
