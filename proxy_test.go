package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseLineProxyList(t *testing.T) {
	input := "1.1.1.1:80\n2.2.2.2:8080\ninvalid\n"
	got := parseLineProxyList(input, "HTTP", "Unchecked")
	if len(got) != 2 {
		t.Fatalf("expected 2 proxies, got %d", len(got))
	}
	if got[0].IP != "1.1.1.1" || got[0].Port != "80" {
		t.Fatalf("unexpected first proxy: %+v", got[0])
	}
}

func TestParseFreeProxyList(t *testing.T) {
	html := `
<table class="table table-striped table-bordered">
  <tbody>
    <tr>
      <td>8.8.8.8</td><td>8080</td><td>US</td><td>anonymous</td><td>socks5</td><td>google</td><td>yes</td>
    </tr>
  </tbody>
</table>`
	got := parseFreeProxyList(html)
	if len(got) != 1 {
		t.Fatalf("expected 1 proxy, got %d", len(got))
	}
	if got[0].Type != "SOCKS5" || got[0].HTTPS != "Supported" {
		t.Fatalf("unexpected parsed proxy: %+v", got[0])
	}
}

func TestParseProxyListPlus(t *testing.T) {
	html := `
<table class="bg">
  <tr class="cells">
    <td>1</td><td>9.9.9.9</td><td>3128</td><td>x</td><td>y</td><td>yes</td><td>http</td>
  </tr>
</table>`
	got := parseProxyListPlus(html)
	if len(got) != 1 {
		t.Fatalf("expected 1 proxy, got %d", len(got))
	}
	if got[0].IP != "9.9.9.9" || got[0].Port != "3128" || got[0].Type != "HTTP" {
		t.Fatalf("unexpected parsed proxy: %+v", got[0])
	}
}

func TestParse89IP(t *testing.T) {
	html := `
<table class="layui-table">
  <tbody>
    <tr><td>3.3.3.3</td><td>9999</td></tr>
  </tbody>
</table>`
	got := parse89IP(html)
	if len(got) != 1 {
		t.Fatalf("expected 1 proxy, got %d", len(got))
	}
	if got[0].IP != "3.3.3.3" || got[0].Port != "9999" || got[0].Type != "HTTP" {
		t.Fatalf("unexpected parsed proxy: %+v", got[0])
	}
}

func TestRandomUserAgent(t *testing.T) {
	got := randomUserAgent()
	if got == "" {
		t.Fatal("expected non-empty user agent")
	}

	found := false
	for _, item := range userAgents {
		if item == got {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("unexpected user agent: %s", got)
	}
}

func TestMergeRequestMetadata(t *testing.T) {
	profile := RequestProfile{
		UserAgent: "ua-test",
		Proxy: &ProxyRecord{
			IP:    "1.2.3.4",
			Port:  "8080",
			Type:  "HTTP",
			HTTPS: "Supported",
		},
	}

	merged := MergeRequestMetadata(map[string]any{"topics": []string{"cve"}}, profile)
	requestValue, ok := merged["request"].(map[string]any)
	if !ok {
		t.Fatalf("expected request metadata map, got %#v", merged["request"])
	}
	if requestValue["proxy_ip"] != "1.2.3.4" {
		t.Fatalf("unexpected proxy ip: %#v", requestValue["proxy_ip"])
	}
	if requestValue["user_agent"] != "ua-test" {
		t.Fatalf("unexpected user agent: %#v", requestValue["user_agent"])
	}
	if merged["topics"] == nil {
		t.Fatal("expected existing metadata to be preserved")
	}
}

func TestOptionsNormalizedAppliesDefaults(t *testing.T) {
	normalized, err := (Options{}).normalized()
	if err != nil {
		t.Fatalf("normalized returned error: %v", err)
	}
	if normalized.RequestTimeout != defaultRequestTimeout {
		t.Fatalf("unexpected timeout: %v", normalized.RequestTimeout)
	}
	if normalized.FetchConcurrency != defaultFetchConcurrency {
		t.Fatalf("unexpected fetch concurrency: %d", normalized.FetchConcurrency)
	}
	if normalized.ValidateConcurrency != defaultConcurrentLimit {
		t.Fatalf("unexpected validate concurrency: %d", normalized.ValidateConcurrency)
	}
	if normalized.RegionConcurrency != defaultConcurrentLimit {
		t.Fatalf("unexpected region concurrency: %d", normalized.RegionConcurrency)
	}
	if normalized.FetchRetryCount != defaultFetchRetryCount {
		t.Fatalf("unexpected fetch retry count: %d", normalized.FetchRetryCount)
	}
	if normalized.FetchRetryDelay != defaultFetchRetryDelay {
		t.Fatalf("unexpected fetch retry delay: %v", normalized.FetchRetryDelay)
	}
	if normalized.RegionLookupURL != defaultRegionLookupURL {
		t.Fatalf("unexpected region lookup url: %s", normalized.RegionLookupURL)
	}
}

func TestLoadSiteConfigsFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sources.json")
	content := `[{"name":"custom","url":"https://example.com/{page}","pages":2,"kind":"api_text","default_type":"HTTP","default_https":"Supported"}]`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	configs, err := loadSiteConfigs(path)
	if err != nil {
		t.Fatalf("loadSiteConfigs returned error: %v", err)
	}
	if len(configs) != 1 || configs[0].Name != "custom" || configs[0].Pages != 2 {
		t.Fatalf("unexpected configs: %+v", configs)
	}
}

func TestFetchWithRetryEventuallySucceeds(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) < 3 {
			http.Error(w, "retry", http.StatusBadGateway)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	s := newService(nil, io.Discard, mustNormalizeOptions(t, Options{RequestTimeout: time.Second}))
	s.fetchRetryCount = 3
	s.fetchRetryDelay = time.Millisecond

	body, retries, err := s.fetchWithRetry(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("expected retry to succeed, got error: %v", err)
	}
	if string(body) != "ok" {
		t.Fatalf("unexpected body: %q", string(body))
	}
	if retries != 2 {
		t.Fatalf("expected 2 retries, got %d", retries)
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
}

func TestDetectRegionUsesConfiguredURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("ip") != "1.2.3.4" {
			t.Fatalf("unexpected ip query: %s", r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"data":{"rgeo":{"country":"CN","province":"ZJ","city":"HZ"}}}`)
	}))
	defer server.Close()

	s := newService(nil, io.Discard, mustNormalizeOptions(t, Options{RequestTimeout: time.Second, RegionLookupURL: server.URL + "?ip=%s"}))
	region, err := s.detectRegion(context.Background(), "1.2.3.4")
	if err != nil {
		t.Fatalf("detectRegion returned error: %v", err)
	}
	if region != "CN/ZJ/HZ" {
		t.Fatalf("unexpected region: %s", region)
	}
}

func TestSaveProxiesWritesSortedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxies.json")
	input := []ProxyRecord{{IP: "2.2.2.2", Port: "80"}, {IP: "1.1.1.1", Port: "80"}}
	if err := saveProxies(input, "json", path, false); err != nil {
		t.Fatalf("saveProxies returned error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var got []ProxyRecord
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if len(got) != 2 || got[0].IP != "1.1.1.1" || got[1].IP != "2.2.2.2" {
		t.Fatalf("unexpected output ordering: %+v", got)
	}
}

func TestCollectUniqueProxiesFetchesPagesConcurrently(t *testing.T) {
	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := concurrent.Add(1)
		defer concurrent.Add(-1)
		for {
			maxSeen := maxConcurrent.Load()
			if current <= maxSeen {
				break
			}
			if maxConcurrent.CompareAndSwap(maxSeen, current) {
				break
			}
		}

		time.Sleep(30 * time.Millisecond)
		page := strings.TrimPrefix(r.URL.Path, "/")
		_, _ = io.WriteString(w, "10.0.0."+page+":8080\n")
	}))
	defer server.Close()

	s := newService(nil, io.Discard, mustNormalizeOptions(t, Options{RequestTimeout: time.Second, FetchRetryCount: 1}))
	proxies, _, err := s.collectUniqueProxies(context.Background(), []siteConfig{{
		Name:       "test_source",
		URL:        server.URL + "/{page}",
		Pages:      3,
		Kind:       siteKindAPIText,
		DefaultTyp: "HTTP",
		DefaultSSL: "Unchecked",
	}})
	if err != nil {
		t.Fatalf("collectUniqueProxies returned error: %v", err)
	}
	if len(proxies) != 3 {
		t.Fatalf("expected 3 proxies, got %d", len(proxies))
	}
	if maxConcurrent.Load() < 2 {
		t.Fatalf("expected concurrent page fetches, max concurrency was %d", maxConcurrent.Load())
	}
}

func TestCollectUniqueProxiesContinuesWhenOneSourceTimesOut(t *testing.T) {
	successServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "10.0.0.1:8080\n")
	}))
	defer successServer.Close()

	slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(80 * time.Millisecond)
		_, _ = io.WriteString(w, "10.0.0.2:8080\n")
	}))
	defer slowServer.Close()

	s := newService(nil, io.Discard, mustNormalizeOptions(t, Options{RequestTimeout: 20 * time.Millisecond, FetchRetryCount: 1}))
	proxies, _, err := s.collectUniqueProxies(context.Background(), []siteConfig{
		{Name: "fast_source", URL: successServer.URL, Pages: 1, Kind: siteKindAPIText, DefaultTyp: "HTTP", DefaultSSL: "Unchecked"},
		{Name: "slow_source", URL: slowServer.URL, Pages: 1, Kind: siteKindAPIText, DefaultTyp: "HTTP", DefaultSSL: "Unchecked"},
	})
	if err != nil {
		t.Fatalf("collectUniqueProxies returned error: %v", err)
	}
	if len(proxies) != 1 {
		t.Fatalf("expected 1 proxy from the healthy source, got %d", len(proxies))
	}
	if proxies[0].Address() != "10.0.0.1:8080" {
		t.Fatalf("unexpected proxy: %+v", proxies[0])
	}
}

func TestCollectUniqueProxiesReturnsContextErrorWhenCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		_, _ = io.WriteString(w, "10.0.0.1:8080\n")
	}))
	defer server.Close()

	s := newService(nil, io.Discard, mustNormalizeOptions(t, Options{RequestTimeout: time.Second, FetchRetryCount: 1}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := s.collectUniqueProxies(ctx, []siteConfig{{Name: "test_source", URL: server.URL, Pages: 1, Kind: siteKindAPIText, DefaultTyp: "HTTP", DefaultSSL: "Unchecked"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation error, got %v", err)
	}
}

func TestValidateProxiesStopsAtTargetAndSorts(t *testing.T) {
	proxies := []ProxyRecord{
		{IP: "2.2.2.2", Port: "80", Type: "HTTP", sourceName: "b"},
		{IP: "1.1.1.1", Port: "80", Type: "HTTP", sourceName: "a"},
		{IP: "3.3.3.3", Port: "80", Type: "HTTP", sourceName: "a"},
	}
	got := sortProxyRecords(proxies)
	if len(got) != 3 {
		t.Fatalf("expected 3 proxies, got %d", len(got))
	}
	if got[0].IP != "1.1.1.1" || got[1].IP != "2.2.2.2" || got[2].IP != "3.3.3.3" {
		t.Fatalf("expected sorted output, got %+v", got)
	}
}

func mustNormalizeOptions(t *testing.T, options Options) Options {
	t.Helper()
	normalized, err := options.normalized()
	if err != nil {
		t.Fatalf("normalized returned error: %v", err)
	}
	return normalized
}
