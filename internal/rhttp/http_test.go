package rhttp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestProxyForURLCapturedDestinationAndReuse(t *testing.T) {
	proxy := func(body string) *httptest.Server {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Host != "fixture.invalid" {
				t.Error("proxy did not receive the synthetic target")
			}
			_, _ = io.WriteString(w, body)
		}))
		t.Cleanup(server.Close)
		return server
	}
	first, second := proxy("first"), proxy("second")
	client, err := ProxyForURL(first.URL)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			copy, err := ProxyForURL(first.URL)
			if err != nil || copy != client {
				t.Error("unchanged captured URL lost shared pool")
			}
		}()
	}
	wg.Wait()
	newClient, err := ProxyForURL(second.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		client *http.Client
		want   string
	}{{client, "first"}, {newClient, "second"}} {
		response, err := tc.client.Get("http://fixture.invalid/v1/fixture")
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || string(body) != tc.want {
			t.Fatalf("captured proxy destination=%q want=%q error=%v", body, tc.want, err)
		}
		tc.client.CloseIdleConnections()
	}
	for _, value := range []string{"", "invalid://fixture"} {
		if _, err := ProxyForURL(value); err == nil {
			t.Fatal("invalid captured proxy accepted")
		}
	}
}
