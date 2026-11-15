package codexcatalog

import (
	"compress/gzip"
	"compress/zlib"
	"container/list"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	FetchTimeout         = 5 * time.Second
	MaxCacheAge          = 5 * time.Minute
	MaxCachedVersions    = 32
	MaxActiveFetches     = 4
	MaxQueuedVersions    = 8
	MaxWaitersPerVersion = 32
)

// TransportProvider resolves the current application proxy without changing a shared Client.
type TransportProvider func() (http.RoundTripper, error)

type limits struct {
	timeout                           time.Duration
	active, queued, waiters, capacity int
}

type cacheEntry struct {
	version  string
	snapshot *Snapshot
	expires  time.Time
}

type flight struct {
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	waiters int
	queued  bool
	result  *Snapshot
	err     error
}

// Result distinguishes a cached response from a newly fetched or coalesced response.
type Result struct {
	Snapshot *Snapshot
	Cache    string
}

// Service owns a bounded cache and bounded, cancellable refresh tasks.
// Construct a new Service on configuration initialization; Close cancels the old tasks.
type Service struct {
	mu        sync.Mutex
	transport TransportProvider
	limits    limits
	now       func() time.Time
	ctx       context.Context
	cancel    context.CancelFunc
	closed    bool
	cache     map[string]*list.Element
	lru       *list.List
	flights   map[string]*flight
	active    chan struct{}
	queued    int
}

func New(transport TransportProvider) *Service {
	return newService(transport, limits{FetchTimeout, MaxActiveFetches, MaxQueuedVersions, MaxWaitersPerVersion, MaxCachedVersions})
}

func newService(transport TransportProvider, budget limits) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{transport: transport, limits: budget, now: time.Now, ctx: ctx, cancel: cancel,
		cache: make(map[string]*list.Element), lru: list.New(),
		flights: make(map[string]*flight), active: make(chan struct{}, budget.active)}
}

func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.cancel()
	clear(s.cache)
	s.lru.Init()
}

func (s *Service) Get(ctx context.Context, version string) (Result, error) {
	if !ValidVersion(version) {
		return Result{}, failure(400, "invalid_client_version")
	}
	if ctx.Err() != nil {
		return Result{}, failure(408, "catalog_request_canceled")
	}
	if s.transport == nil {
		return Result{}, failure(503, "catalog_transport_unavailable")
	}
	// Validate the selected proxy even for a hit; missing proxy never becomes a direct request.
	transport, err := s.transport()
	if err != nil || transport == nil {
		return Result{}, failure(503, "catalog_transport_unavailable")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return Result{}, failure(503, "catalog_closed")
	}
	if cached, ok := s.cache[version]; ok {
		value := cached.Value.(*cacheEntry)
		if s.now().Before(value.expires) {
			s.lru.MoveToFront(cached)
			s.mu.Unlock()
			return Result{value.snapshot, "hit"}, nil
		}
	}
	task, shared := s.flights[version]
	if shared {
		if task.waiters >= s.limits.waiters || task.ctx.Err() != nil {
			s.mu.Unlock()
			return Result{}, failure(503, "catalog_busy")
		}
	} else {
		if len(s.flights) >= s.limits.active+s.limits.queued {
			s.mu.Unlock()
			return Result{}, failure(503, "catalog_busy")
		}
		queued := false
		select {
		case s.active <- struct{}{}:
		default:
			if s.queued >= s.limits.queued {
				s.mu.Unlock()
				return Result{}, failure(503, "catalog_busy")
			}
			queued = true
			s.queued++
		}
		taskCtx, cancel := context.WithTimeout(s.ctx, s.limits.timeout)
		task = &flight{ctx: taskCtx, cancel: cancel, done: make(chan struct{}), queued: queued}
		s.flights[version] = task
		go s.run(version, transport, task)
	}
	task.waiters++
	s.mu.Unlock()
	defer s.leave(task)
	select {
	case <-ctx.Done():
		return Result{}, failure(408, "catalog_request_canceled")
	case <-task.done:
		if ctx.Err() != nil {
			return Result{}, failure(408, "catalog_request_canceled")
		}
		state := "miss"
		if shared {
			state = "coalesced"
		}
		return Result{task.result, state}, task.err
	}
}

func (s *Service) leave(task *flight) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task.waiters--
	if task.waiters == 0 {
		task.cancel()
	}
}

func (s *Service) run(version string, transport http.RoundTripper, task *flight) {
	active := !task.queued
	if task.queued {
		select {
		case s.active <- struct{}{}:
			active = true
		case <-task.ctx.Done():
		}
		s.mu.Lock()
		s.queued--
		s.mu.Unlock()
	}
	var snapshot *Snapshot
	var expires time.Time
	var err error
	if task.ctx.Err() != nil {
		err = contextFailure(task.ctx)
	} else {
		snapshot, expires, err = s.fetch(task.ctx, transport, version)
	}
	if active {
		<-s.active
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if task.ctx.Err() != nil {
		snapshot, err = nil, contextFailure(task.ctx)
	}
	if err == nil && !s.closed {
		// A successful no-store/expired result also removes an earlier entity.
		if previous, ok := s.cache[version]; ok {
			s.lru.Remove(previous)
			delete(s.cache, version)
		}
		if s.now().Before(expires) {
			s.cache[version] = s.lru.PushFront(&cacheEntry{version, snapshot, expires})
			for s.lru.Len() > s.limits.capacity {
				old := s.lru.Back()
				delete(s.cache, old.Value.(*cacheEntry).version)
				s.lru.Remove(old)
			}
		}
	}
	task.result, task.err = snapshot, err
	delete(s.flights, version)
	task.cancel()
	close(task.done)
}

func contextFailure(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return failure(http.StatusGatewayTimeout, "catalog_timeout")
	}
	return failure(http.StatusServiceUnavailable, "catalog_canceled")
}

func (s *Service) fetch(ctx context.Context, transport http.RoundTripper, version string) (*Snapshot, time.Time, error) {
	url, _ := SourceURL(version)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, time.Time{}, failure(502, "catalog_request_failed")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "octopus-roam-codex-catalog/1")
	client := &http.Client{Transport: transport, Timeout: s.limits.timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	started := s.now()
	response, err := client.Do(request)
	if err != nil {
		var networkError net.Error
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || (errors.As(err, &networkError) && networkError.Timeout()) {
			return nil, time.Time{}, failure(504, "catalog_timeout")
		}
		if ctx.Err() != nil {
			return nil, time.Time{}, contextFailure(ctx)
		}
		return nil, time.Time{}, failure(502, "catalog_upstream_unavailable")
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, time.Time{}, failure(404, "catalog_version_unavailable")
	case http.StatusTooManyRequests:
		return nil, time.Time{}, failure(503, "catalog_upstream_rate_limited")
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, time.Time{}, failure(502, "catalog_upstream_access_failed")
	default:
		return nil, time.Time{}, failure(502, "catalog_upstream_status")
	}
	reader, err := decodedBody(response)
	if err != nil {
		return nil, time.Time{}, failure(502, "invalid_catalog_encoding")
	}
	if reader != response.Body {
		defer reader.Close()
	}
	raw, err := io.ReadAll(io.LimitReader(reader, MaxBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, time.Time{}, contextFailure(ctx)
		}
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			return nil, time.Time{}, failure(504, "catalog_timeout")
		}
		return nil, time.Time{}, failure(502, "catalog_body_failed")
	}
	snapshot, err := parseSnapshot(raw, version)
	if err != nil {
		return nil, time.Time{}, err
	}
	received := s.now()
	return snapshot, received.Add(freshness(response.Header, started, received)), nil
}

func decodedBody(response *http.Response) (io.ReadCloser, error) {
	if response.Uncompressed {
		return response.Body, nil
	}
	switch strings.ToLower(strings.TrimSpace(response.Header.Get("Content-Encoding"))) {
	case "", "identity":
		return response.Body, nil
	case "gzip":
		return gzip.NewReader(response.Body)
	case "deflate":
		return zlib.NewReader(response.Body)
	default:
		return nil, errors.New("unsupported encoding")
	}
}

// freshness is conservative for shared caching. No-cache/must-revalidate responses
// are fetched unconditionally; private, no-store and Vary:* responses are not retained.
func freshness(header http.Header, started, received time.Time) time.Duration {
	for _, line := range header.Values("Vary") {
		for _, field := range strings.Split(line, ",") {
			if strings.TrimSpace(field) == "*" {
				return 0
			}
		}
	}
	maxAge, sharedAge := time.Duration(-1), time.Duration(-1)
	for _, line := range header.Values("Cache-Control") {
		for _, directive := range strings.Split(line, ",") {
			name, value, hasValue := strings.Cut(strings.TrimSpace(directive), "=")
			switch strings.ToLower(name) {
			case "no-store", "no-cache", "must-revalidate", "proxy-revalidate", "private":
				return 0
			case "max-age", "s-maxage":
				age, valid := cacheSeconds(strings.Trim(value, "\""))
				if !hasValue || !valid {
					return 0
				}
				target := &maxAge
				if strings.EqualFold(name, "s-maxage") {
					target = &sharedAge
				}
				if *target >= 0 && *target != age {
					return 0
				}
				*target = age
			}
		}
	}
	lifetime := maxAge
	if sharedAge >= 0 {
		lifetime = sharedAge
	}
	date, dateErr := http.ParseTime(header.Get("Date"))
	if lifetime < 0 {
		if expires := header.Get("Expires"); expires != "" {
			expiry, err := http.ParseTime(expires)
			if err != nil {
				return 0
			}
			reference := received
			if dateErr == nil {
				reference = date
			}
			lifetime = expiry.Sub(reference)
		} else {
			return MaxCacheAge
		}
	}
	apparentAge := time.Duration(0)
	if dateErr == nil && received.After(date) {
		apparentAge = received.Sub(date)
	}
	age := time.Duration(0)
	if value := header.Get("Age"); value != "" {
		var valid bool
		age, valid = cacheSeconds(value)
		if !valid {
			return 0
		}
	}
	delay := received.Sub(started)
	if delay > 0 {
		if age > time.Duration(1<<63-1)-delay {
			return 0
		}
		age += delay
	}
	if apparentAge > age {
		age = apparentAge
	}
	// Compare before subtracting so an expired Expires plus a large Age cannot overflow.
	if lifetime <= age {
		return 0
	}
	remaining := lifetime - age
	if remaining > MaxCacheAge {
		return MaxCacheAge
	}
	return remaining
}

func cacheSeconds(value string) (time.Duration, bool) {
	if value == "" {
		return 0, false
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return 0, false
		}
	}
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || seconds > int64((1<<63-1)/time.Second)-1 {
		return 0, false
	}
	return time.Duration(seconds) * time.Second, true
}
