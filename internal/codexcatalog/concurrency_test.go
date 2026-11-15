package codexcatalog

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	end := time.Now().Add(3 * time.Second)
	for !condition() {
		if time.Now().After(end) {
			t.Fatal("bounded condition did not complete")
		}
		time.Sleep(time.Millisecond)
	}
}

func emptyResponse() *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"models":[]}`))}
}

func TestCoalescingAndOneWaiterCancellation(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	service := New(func() (http.RoundTripper, error) {
		return roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if calls.Add(1) == 1 {
				close(started)
			}
			select {
			case <-release:
				return emptyResponse(), nil
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		}), nil
	})
	defer service.Close()
	firstCtx, cancelFirst := context.WithCancel(t.Context())
	defer cancelFirst()
	first := make(chan error, 1)
	go func() { _, err := service.Get(firstCtx, "0.169.0"); first <- err }()
	<-started
	const followers = 20
	results := make(chan error, followers)
	for range followers {
		go func() {
			result, err := service.Get(t.Context(), "0.169.0")
			if err == nil && result.Cache != "coalesced" {
				err = fmt.Errorf("expected coalesced result")
			}
			results <- err
		}()
	}
	waitFor(t, func() bool {
		service.mu.Lock()
		defer service.mu.Unlock()
		return service.flights["0.169.0"].waiters == followers+1
	})
	cancelFirst()
	requireStatus(t, <-first, 408)
	close(release)
	for range followers {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("same-version requests were not merged")
	}
}

func TestGlobalActiveQueueAndPerVersionWaiterLimits(t *testing.T) {
	var active, peak, calls atomic.Int32
	service := New(func() (http.RoundTripper, error) {
		return roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls.Add(1)
			n := active.Add(1)
			defer active.Add(-1)
			for old := peak.Load(); n > old; old = peak.Load() {
				if peak.CompareAndSwap(old, n) {
					break
				}
			}
			<-r.Context().Done()
			return nil, r.Context().Err()
		}), nil
	})
	defer service.Close()
	total := MaxActiveFetches + MaxQueuedVersions
	results := make(chan error, total+MaxWaitersPerVersion-1)
	for index := range total {
		go func(version string) { _, err := service.Get(t.Context(), version); results <- err }(fmt.Sprintf("1.0.%d", index))
	}
	waitFor(t, func() bool {
		service.mu.Lock()
		defer service.mu.Unlock()
		return len(service.flights) == total && service.queued == MaxQueuedVersions && calls.Load() == MaxActiveFetches
	})
	_, err := service.Get(t.Context(), "9.9.9")
	requireStatus(t, err, 503)
	for range MaxWaitersPerVersion - 1 {
		go func() {
			_, err := service.Get(t.Context(), "1.0.0")
			results <- err
		}()
	}
	waitFor(t, func() bool {
		service.mu.Lock()
		defer service.mu.Unlock()
		return service.flights["1.0.0"].waiters == MaxWaitersPerVersion
	})
	_, err = service.Get(t.Context(), "1.0.0")
	requireStatus(t, err, 503)
	if peak.Load() != MaxActiveFetches || calls.Load() != MaxActiveFetches {
		t.Fatal("outbound cap exceeded")
	}
	service.Close()
	for range total + MaxWaitersPerVersion - 1 {
		requireStatus(t, <-results, 503)
	}
	waitFor(t, func() bool {
		service.mu.Lock()
		defer service.mu.Unlock()
		return len(service.flights) == 0 && service.queued == 0 && len(service.active) == 0
	})
}

func TestLastWaiterCancellationStopsFetchAndReleasesVersion(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	service := New(func() (http.RoundTripper, error) {
		return roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if calls.Add(1) == 1 {
				close(started)
				<-r.Context().Done()
				close(stopped)
				return nil, r.Context().Err()
			}
			return emptyResponse(), nil
		}), nil
	})
	defer service.Close()
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() { _, err := service.Get(ctx, "1.2.3"); result <- err }()
	<-started
	cancel()
	requireStatus(t, <-result, 408)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("abandoned HTTP was not canceled")
	}
	waitFor(t, func() bool { service.mu.Lock(); defer service.mu.Unlock(); return len(service.flights) == 0 })
	if _, err := service.Get(t.Context(), "1.2.3"); err != nil || calls.Load() != 2 {
		t.Fatal("canceled version could not refresh")
	}
}

func TestQueueTimeConsumesTheFetchBudget(t *testing.T) {
	started := make(chan time.Duration, 2)
	service := newService(func() (http.RoundTripper, error) {
		return roundTripFunc(func(r *http.Request) (*http.Response, error) {
			deadline, _ := r.Context().Deadline()
			started <- time.Until(deadline)
			<-r.Context().Done()
			return nil, r.Context().Err()
		}), nil
	}, limits{timeout: 300 * time.Millisecond, active: 1, queued: 1, waiters: 4, capacity: 2})
	defer service.Close()
	first := make(chan error, 1)
	go func() { _, err := service.Get(t.Context(), "1.0.0"); first <- err }()
	<-started
	second := make(chan error, 1)
	go func() { _, err := service.Get(t.Context(), "1.0.1"); second <- err }()
	waitFor(t, func() bool { service.mu.Lock(); defer service.mu.Unlock(); return service.queued == 1 })
	requireStatus(t, <-first, 504)
	requireStatus(t, <-second, 504)
	select {
	case remaining := <-started:
		if remaining > 150*time.Millisecond {
			t.Fatalf("queue restarted the budget: %s", remaining)
		}
	default: // Expiring while queued is also a valid bounded timeout.
	}
	waitFor(t, func() bool { service.mu.Lock(); defer service.mu.Unlock(); return len(service.flights) == 0 })
}
