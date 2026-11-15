package codexcatalog

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fixtureService(t *testing.T, handler http.HandlerFunc) *Service {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{Proxy: nil}
	t.Cleanup(transport.CloseIdleConnections)
	service := New(func() (http.RoundTripper, error) {
		return roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Scheme != "https" || request.URL.Host != "raw.githubusercontent.com" ||
				request.URL.RawQuery != "" || request.Method != "GET" {
				t.Error("request left the fixed anonymous source contract")
			}
			if request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" ||
				request.Header.Get("X-Client-Private") != "" || len(request.Header) != 2 ||
				request.Header.Get("Accept") != "application/json" || request.Header.Get("User-Agent") == "" {
				t.Error("outbound headers were not generated independently")
			}
			clone := request.Clone(request.Context())
			clone.URL.Scheme, clone.URL.Host = target.Scheme, target.Host
			return transport.RoundTrip(clone)
		}), nil
	})
	t.Cleanup(service.Close)
	return service
}

func requireStatus(t *testing.T, err error, status int) {
	t.Helper()
	var classified *Error
	if !errors.As(err, &classified) || classified.Status != status {
		t.Fatalf("error=%v want status=%d", err, status)
	}
	if strings.Contains(err.Error(), "private-fixture") {
		t.Fatal("transport details leaked")
	}
}

func TestDifferentVersionBodiesPathsAndCacheIsolation(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	first := `{"models":[{"slug":"main","auto_review_model_override":null}],"version_fixture":"first"}`
	second := `{"models":[{"slug":"other","auto_review_model_override":"official"}],"version_fixture":"second"}`
	service := fixtureService(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Cache-Control", "max-age=300")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		switch r.URL.Path {
		case "/openai/codex/rust-v0.169.0/codex-rs/models-manager/models.json":
			io.WriteString(w, first)
		case "/openai/codex/rust-v12.34.56/codex-rs/models-manager/models.json":
			io.WriteString(w, second)
		default:
			t.Error("incorrect version path")
			w.WriteHeader(404)
		}
	})
	a, err := service.Get(t.Context(), "0.169.0")
	if err != nil {
		t.Fatal(err)
	}
	b, err := service.Get(t.Context(), "12.34.56")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ version, body, digest string }{
		{"0.169.0", first, a.Snapshot.SHA256()}, {"12.34.56", second, b.Snapshot.SHA256()},
	} {
		hit, err := service.Get(t.Context(), test.version)
		if err != nil || hit.Cache != "hit" || string(hit.Snapshot.raw) != test.body || hit.Snapshot.SHA256() != test.digest {
			t.Fatal("version cache identity was lost")
		}
	}
	if a.Snapshot.SHA256() == b.Snapshot.SHA256() {
		t.Fatal("fixtures must have different contents")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 2 {
		t.Fatalf("expected exactly two distinct-version fetches: %v", paths)
	}
}

func TestCacheExpiryRefreshFailureAndChangedDigest(t *testing.T) {
	var now atomic.Int64
	now.Store(time.Now().UTC().Truncate(time.Second).UnixNano())
	var status atomic.Int32
	status.Store(200)
	var revision atomic.Int32
	var calls atomic.Int32
	service := fixtureService(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Date", time.Unix(0, now.Load()).UTC().Format(http.TimeFormat))
		w.Header().Set("Cache-Control", "max-age=10")
		w.WriteHeader(int(status.Load()))
		fmt.Fprintf(w, `{"models":[{"slug":"main"}],"revision":%d}`, revision.Load())
	})
	service.now = func() time.Time { return time.Unix(0, now.Load()) }
	first, err := service.Get(t.Context(), "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	now.Add(int64(11 * time.Second))
	for _, code := range []int{404, 429, 500, 401, 403, 302, 304} {
		status.Store(int32(code))
		result, err := service.Get(t.Context(), "1.2.3")
		want := 502
		if code == 404 {
			want = 404
		}
		if code == 429 {
			want = 503
		}
		requireStatus(t, err, want)
		if result.Snapshot != nil {
			t.Fatal("stale catalog returned on error")
		}
	}
	status.Store(200)
	revision.Store(2)
	refreshed, err := service.Get(t.Context(), "1.2.3")
	if err != nil || refreshed.Snapshot.SHA256() == first.Snapshot.SHA256() {
		t.Fatal("changed version content was not rebound")
	}
	hit, err := service.Get(t.Context(), "1.2.3")
	if err != nil || hit.Cache != "hit" || calls.Load() != 9 {
		t.Fatalf("refresh cache or calls: %d %v", calls.Load(), err)
	}
}

func TestUncacheableResponsesAndLegalEmptyCatalog(t *testing.T) {
	for _, policy := range []string{"no-store", "no-cache", "no-cache=\"Set-Cookie\"", "must-revalidate", "proxy-revalidate", "private", "max-age=0", "max-age=broken", "max-age=300, max-age=400", "vary-star"} {
		t.Run(policy, func(t *testing.T) {
			var calls atomic.Int32
			service := fixtureService(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if policy == "vary-star" {
					w.Header().Set("Vary", "*")
				} else {
					w.Header().Set("Cache-Control", policy)
				}
				io.WriteString(w, `{"models":[]}`)
			})
			for range 2 {
				result, err := service.Get(t.Context(), "2.3.4")
				if err != nil || result.Snapshot.ModelCount() != 0 {
					t.Fatal("valid empty catalog failed")
				}
			}
			if calls.Load() != 2 {
				t.Fatal("response requiring a fresh GET was cached")
			}
			service.mu.Lock()
			n := len(service.cache)
			service.mu.Unlock()
			if n != 0 {
				t.Fatal("uncacheable body retained")
			}
		})
	}
}

func TestCacheCapacityLRUAndConfigurationReinitialization(t *testing.T) {
	var calls atomic.Int32
	service := fixtureService(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, `{"models":[]}`)
	})
	for i := range MaxCachedVersions {
		if _, err := service.Get(t.Context(), fmt.Sprintf("1.0.%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.Get(t.Context(), "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(t.Context(), "1.0.32"); err != nil {
		t.Fatal(err)
	}
	hit, err := service.Get(t.Context(), "1.0.0")
	if err != nil || hit.Cache != "hit" {
		t.Fatal("recent entry evicted")
	}
	miss, err := service.Get(t.Context(), "1.0.1")
	if err != nil || miss.Cache != "miss" || calls.Load() != 34 {
		t.Fatal("LRU limit was not enforced")
	}
	service.mu.Lock()
	count := len(service.cache)
	service.mu.Unlock()
	if count != MaxCachedVersions {
		t.Fatal("cache capacity")
	}
	service.Close()
	_, err = service.Get(t.Context(), "1.0.0")
	requireStatus(t, err, 503)
	restarted := New(service.transport)
	defer restarted.Close()
	again, err := restarted.Get(t.Context(), "1.0.0")
	if err != nil || again.Cache != "miss" || calls.Load() != 35 {
		t.Fatal("new configuration reused old cache")
	}
}

func TestUpstreamFailuresRedirectsAndDecompressedSize(t *testing.T) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	writer.Write([]byte(`{"models":[]}` + strings.Repeat(" ", MaxBytes)))
	writer.Close()
	for _, test := range []struct {
		name           string
		status         int
		body, encoding string
		want           int
	}{
		{"not-json", 200, "private-fixture-error", "", 502},
		{"invalid-entry", 200, `{"models":[{"slug":"x"},{"slug":"x"}]}`, "", 502},
		{"oversize", 200, strings.Repeat("x", MaxBytes+1), "", 502},
		{"gzip-limit", 200, compressed.String(), "gzip", 502},
		{"bad-encoding", 200, "private-fixture", "br", 502},
		{"redirect", 302, "private-fixture", "", 502},
		{"no-entity-304", 304, "", "", 502},
		{"missing", 404, "private-fixture", "", 404},
		{"limited", 429, "private-fixture", "", 503},
		{"unauthorized", 401, "private-fixture", "", 502},
		{"forbidden", 403, "private-fixture", "", 502},
		{"upstream", 503, "private-fixture", "", 502},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			service := fixtureService(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if test.encoding != "" {
					w.Header().Set("Content-Encoding", test.encoding)
				}
				if test.status == 302 {
					w.Header().Set("Location", "https://other.invalid/redirect")
				}
				w.WriteHeader(test.status)
				io.WriteString(w, test.body)
			})
			_, err := service.Get(t.Context(), "0.169.0")
			requireStatus(t, err, test.want)
			if calls.Load() != 1 {
				t.Fatal("request was retried or redirected")
			}
			service.mu.Lock()
			retained := len(service.cache)
			service.mu.Unlock()
			if retained != 0 {
				t.Fatal("failed body cached")
			}
		})
	}
}

func TestProxyResolutionAndCanceledRequestsFailBeforeHTTP(t *testing.T) {
	var calls atomic.Int32
	var unavailable atomic.Bool
	service := New(func() (http.RoundTripper, error) {
		if unavailable.Load() {
			return nil, errors.New("private-fixture-proxy")
		}
		return roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls.Add(1)
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"models":[]}`))}, nil
		}), nil
	})
	defer service.Close()
	_, err := service.Get(t.Context(), "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	unavailable.Store(true)
	_, err = service.Get(t.Context(), "1.2.3")
	requireStatus(t, err, 503)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = service.Get(ctx, "1.2.3")
	requireStatus(t, err, 408)
	_, err = service.Get(t.Context(), "../main")
	requireStatus(t, err, 400)
	if calls.Load() != 1 {
		t.Fatal("configuration or input failure performed HTTP")
	}
}

func TestCacheControlRemainingFreshness(t *testing.T) {
	now := time.Date(2026, 11, 15, 8, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name   string
		header http.Header
		want   time.Duration
	}{
		{"default", http.Header{}, 5 * time.Minute},
		{"cap", http.Header{"Cache-Control": {"max-age=3600"}}, 5 * time.Minute},
		{"age", http.Header{"Cache-Control": {"max-age=100"}, "Age": {"30"}}, 68 * time.Second},
		{"date", http.Header{"Cache-Control": {"max-age=100"}, "Date": {now.Add(-40 * time.Second).Format(http.TimeFormat)}}, 60 * time.Second},
		{"expired", http.Header{"Cache-Control": {"max-age=100"}, "Age": {"200"}}, 0},
		{"expires", http.Header{"Date": {now.Format(http.TimeFormat)}, "Expires": {now.Add(time.Minute).Format(http.TimeFormat)}}, 58 * time.Second},
		{"bad-expires", http.Header{"Expires": {"bad"}}, 0},
		{"past-expires-large-age", http.Header{"Date": {now.Format(http.TimeFormat)}, "Expires": {now.AddDate(-100, 0, 0).Format(http.TimeFormat)}, "Age": {"9000000000"}}, 0},
		{"shared", http.Header{"Cache-Control": {"max-age=300, s-maxage=40"}}, 38 * time.Second},
		{"age-overflow", http.Header{"Cache-Control": {"max-age=300"}, "Age": {"999999999999999999999"}}, 0},
		{"wildcard", http.Header{"Vary": {"Accept, *"}}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := freshness(test.header, now.Add(-2*time.Second), now); got != test.want {
				t.Fatalf("freshness=%s want=%s", got, test.want)
			}
		})
	}
}
