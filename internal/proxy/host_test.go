package proxy_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nano-step/nano-brain-observe/internal/proxy"
)

func TestHostAndOriginGuard(t *testing.T) {
	var hits atomic.Int32
	var upstreamHost string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		upstreamHost = r.Host
		w.WriteHeader(http.StatusNoContent)
	}))
	defer up.Close()
	u, _ := url.Parse(up.URL)
	allowed, err := proxy.ParseAllowedHosts("host.docker.internal,192.168.1.5")
	if err != nil {
		t.Fatal(err)
	}
	s := &memSink{}
	px := httptest.NewServer(proxy.New(proxy.Options{Upstream: u, Sink: s, AllowedHosts: allowed}))
	defer px.Close()

	tests := []struct {
		name, host, origin, path string
		want                     int
	}{
		{"loopback", "localhost:3101", "", "/", 204},
		{"127 subnet", "127.10.2.3:3101", "", "/mcp", 204},
		{"ipv6", "[::1]:3101", "http://[::1]:3101", "/sse", 204},
		{"docker allowed", "HOST.DOCKER.INTERNAL:3101", "http://host.docker.internal:3101", "/", 204},
		{"allowed IP", "192.168.1.5:3101", "", "/", 204},
		{"hostile", "evil.example:3101", "", "/", 403},
		{"suffix", "localhost.evil:3101", "", "/", 403},
		{"wildcard", "*:3101", "", "/", 403},
		{"invalid host", "localhost:bad", "", "/", 403},
		{"null origin", "localhost:3101", "null", "/", 403},
		{"cross host", "localhost:3101", "http://evil.example:3101", "/", 403},
		{"cross port", "localhost:3101", "http://localhost:3102", "/", 403},
		{"cross scheme", "localhost:3101", "https://localhost:3101", "/", 403},
		{"origin path", "localhost:3101", "http://localhost:3101/path", "/", 403},
		{"malformed origin", "localhost:3101", "http://localhost:bad", "/", 403},
		{"streaming guarded", "evil.example:3101", "", "/mcp", 403},
		{"sse guarded", "evil.example:3101", "", "/sse", 403},
	}
	recorded := 0
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s.wait(t, recorded)
			before := hits.Load()
			s.mu.Lock()
			records := len(s.recs)
			s.mu.Unlock()
			req, err := http.NewRequest(http.MethodGet, px.URL+tt.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Host = tt.host
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			req.Header.Set("X-Forwarded-Host", "evil.example")
			req.Header.Set("X-Forwarded-Proto", "https")
			resp, err := px.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode != tt.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.want)
			}
			if tt.want == 403 {
				if got := hits.Load(); got != before {
					t.Errorf("upstream hits = %d, want %d", got, before)
				}
				s.mu.Lock()
				got := len(s.recs)
				s.mu.Unlock()
				if got != records {
					t.Errorf("sink records = %d, want %d", got, records)
				}
			} else if upstreamHost != u.Host {
				t.Errorf("upstream Host = %q, want %q", upstreamHost, u.Host)
			}
			if tt.want != 403 && tt.path != "/mcp" && tt.path != "/sse" {
				recorded++
			}
		})
	}
	s.wait(t, recorded)
	for _, host := range []string{"", "[::1%25eth0]:3101"} {
		req := httptest.NewRequest(http.MethodGet, px.URL+"/", nil)
		req.Host = host
		w := httptest.NewRecorder()
		proxy.New(proxy.Options{Upstream: u, Sink: s}).ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("Host %q status = %d, want 403", host, w.Code)
		}
	}
}

func TestParseAllowedHosts(t *testing.T) {
	for _, raw := range []string{"*", "*.example.com", ".example.com", "a/b", "a:123", "a,", "a,,b", "fe80::1%eth0", "[::1]:123", "a..b", "127.0.0.999"} {
		if _, err := proxy.ParseAllowedHosts(raw); err == nil {
			t.Errorf("ParseAllowedHosts(%q) accepted invalid input", raw)
		}
	}
	hosts, err := proxy.ParseAllowedHosts("localhost,::1,[2001:db8::1],host.docker.internal")
	if err != nil || strings.Join(hosts, ",") != "localhost,::1,2001:db8::1,host.docker.internal" {
		t.Errorf("ParseAllowedHosts = %q, %v", hosts, err)
	}
}
