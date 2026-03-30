package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type regionLimiter struct {
	ticker *time.Ticker
}

func newRegionLimiter(qps int) *regionLimiter {
	if qps <= 0 {
		return nil
	}

	interval := time.Second / time.Duration(qps)
	if interval <= 0 {
		interval = time.Millisecond
	}
	return &regionLimiter{ticker: time.NewTicker(interval)}
}

func (l *regionLimiter) Wait(ctx context.Context) error {
	if l == nil || l.ticker == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-l.ticker.C:
		return nil
	}
}

func (l *regionLimiter) Stop() {
	if l != nil && l.ticker != nil {
		l.ticker.Stop()
	}
}

func (s *service) fetch(ctx context.Context, rawURL string) ([]byte, error) {
	select {
	case s.fetchLimit <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-s.fetchLimit }()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", randomUserAgent())
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected status: %s", resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func (s *service) fetchWithRetry(ctx context.Context, rawURL string) ([]byte, int, error) {
	attempts := s.fetchRetryCount
	if attempts <= 0 {
		attempts = 1
	}

	delay := s.fetchRetryDelay
	if delay <= 0 {
		delay = defaultFetchRetryDelay
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		body, err := s.fetch(ctx, rawURL)
		if err == nil {
			return body, attempt - 1, nil
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, attempt - 1, err
		}

		lastErr = err
		if attempt == attempts {
			break
		}

		wait := delay * time.Duration(1<<(attempt-1))
		s.printf("抓取 %s 失败，准备重试 (%d/%d): %v\n", rawURL, attempt, attempts, err)
		if err := sleepContext(ctx, wait); err != nil {
			return nil, attempt - 1, err
		}
	}

	if lastErr == nil {
		lastErr = errors.New("unknown fetch failure")
	}
	return nil, attempts - 1, fmt.Errorf("fetch failed after %d attempts: %w", attempts, lastErr)
}

func (s *service) validateProxies(ctx context.Context, proxies []ProxyRecord, checkTypeHTTPS bool, targetValidCount int, stats []siteStats) []ProxyRecord {
	validateCtx := ctx
	cancel := func() {}
	if targetValidCount > 0 {
		validateCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	sem := make(chan struct{}, s.validateConcurrency)
	results := make(chan ProxyRecord, len(proxies))
	var wg sync.WaitGroup
	var successCount atomic.Int32

	for _, proxyItem := range proxies {
		proxyItem := proxyItem
		if targetValidCount > 0 && int(successCount.Load()) >= targetValidCount {
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-validateCtx.Done():
				return
			}
			defer func() { <-sem }()

			validated, ok := s.validateProxy(validateCtx, proxyItem, checkTypeHTTPS)
			if !ok {
				return
			}
			current := successCount.Add(1)
			results <- validated
			if targetValidCount > 0 && int(current) >= targetValidCount {
				cancel()
			}
		}()
	}

	wg.Wait()
	close(results)

	valid := make([]ProxyRecord, 0, len(results))
	for item := range results {
		valid = append(valid, item)
		for i := range stats {
			if stats[i].name == item.sourceName {
				stats[i].validatedSuccess++
				break
			}
		}
	}
	valid = sortProxyRecords(valid)
	if targetValidCount > 0 && len(valid) > targetValidCount {
		valid = valid[:targetValidCount]
	}
	return valid
}

func (s *service) validateProxy(ctx context.Context, proxyItem ProxyRecord, checkTypeHTTPS bool) (ProxyRecord, bool) {
	address := proxyItem.Address()
	initialType := strings.ToLower(strings.TrimSpace(proxyItem.Type))
	detectedType := "未知"
	httpsSupport := "不支持"

	order := []string{"http"}
	switch initialType {
	case "socks5":
		order = []string{"socks5"}
	case "https":
		order = []string{"https", "http"}
	case "http":
		order = []string{"http", "https"}
	default:
		if checkTypeHTTPS {
			order = []string{"http", "https", "socks5"}
		}
	}

	for _, mode := range order {
		switch mode {
		case "http":
			if s.testHTTPProxy(ctx, address, httpProbeURL) {
				detectedType = "HTTP"
				if checkTypeHTTPS && s.testHTTPProxy(ctx, address, httpsProbeURL) {
					httpsSupport = "支持"
					if initialType == "https" {
						detectedType = "HTTPS"
					}
				}
				proxyItem.ValidatedOK = true
				proxyItem.Type = detectedType
				proxyItem.HTTPS = httpsSupport
				s.printf("代理 %s (%s) 验证通过，HTTPS: %s\n", address, detectedType, httpsSupport)
				return proxyItem, true
			}
		case "https":
			if checkTypeHTTPS && s.testHTTPProxy(ctx, address, httpsProbeURL) {
				proxyItem.ValidatedOK = true
				proxyItem.Type = "HTTPS"
				proxyItem.HTTPS = "支持"
				s.printf("代理 %s (HTTPS) 验证通过\n", address)
				return proxyItem, true
			}
		case "socks5":
			if s.testSOCKS5Proxy(ctx, address, false) {
				detectedType = "SOCKS5"
				if checkTypeHTTPS && s.testSOCKS5Proxy(ctx, address, true) {
					httpsSupport = "支持"
				}
				proxyItem.ValidatedOK = true
				proxyItem.Type = detectedType
				proxyItem.HTTPS = httpsSupport
				s.printf("代理 %s (SOCKS5) 验证通过，HTTPS: %s\n", address, httpsSupport)
				return proxyItem, true
			}
			if initialType == "socks5" {
				s.printf("代理 %s (SOCKS5) 验证失败\n", address)
				return ProxyRecord{}, false
			}
		}
	}

	s.printf("代理 %s 验证失败\n", address)
	return ProxyRecord{}, false
}

func (s *service) testHTTPProxy(ctx context.Context, proxyAddress, target string) bool {
	proxyURL, err := url.Parse("http://" + proxyAddress)
	if err != nil {
		return false
	}

	transport := &http.Transport{
		Proxy:               http.ProxyURL(proxyURL),
		TLSHandshakeTimeout: 5 * time.Second,
		DisableKeepAlives:   true,
	}
	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: transport,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", randomUserAgent())
	req.Header.Set("Accept", "*/*")

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
}

func (s *service) testSOCKS5Proxy(ctx context.Context, proxyAddress string, useTLS bool) bool {
	targetHost := "httpbin.org"
	targetAddr := "httpbin.org:80"
	if useTLS {
		targetAddr = "httpbin.org:443"
	}

	conn, err := dialSOCKS5(ctx, proxyAddress, targetAddr)
	if err != nil {
		return false
	}
	defer conn.Close()

	deadline := time.Now().Add(5 * time.Second)
	if err := conn.SetDeadline(deadline); err != nil {
		return false
	}

	var rw io.ReadWriter = conn
	if useTLS {
		tlsConn := tls.Client(conn, &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: targetHost,
		})
		if err := tlsConn.Handshake(); err != nil {
			return false
		}
		rw = tlsConn
	}

	request := fmt.Sprintf("GET /ip HTTP/1.1\r\nHost: %s\r\nUser-Agent: %s\r\nConnection: close\r\n\r\n", targetHost, randomUserAgent())
	if _, err := io.WriteString(rw, request); err != nil {
		return false
	}

	reader := bufio.NewReader(rw)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	return strings.Contains(statusLine, " 200 ")
}

func dialSOCKS5(ctx context.Context, proxyAddress, targetAddress string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", proxyAddress)
	if err != nil {
		return nil, err
	}

	fail := func(err error) (net.Conn, error) {
		conn.Close()
		return nil, err
	}

	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return fail(err)
	}
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return fail(err)
	}

	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return fail(err)
	}
	if reply[0] != 0x05 || reply[1] != 0x00 {
		return fail(errors.New("socks5 auth negotiation failed"))
	}

	host, portStr, err := net.SplitHostPort(targetAddress)
	if err != nil {
		return fail(err)
	}
	portNum, err := strconv.Atoi(portStr)
	if err != nil {
		return fail(err)
	}

	request := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if ipv4 := ip.To4(); ipv4 != nil {
			request = append(request, 0x01)
			request = append(request, ipv4...)
		} else {
			request = append(request, 0x04)
			request = append(request, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return fail(errors.New("host too long"))
		}
		request = append(request, 0x03, byte(len(host)))
		request = append(request, host...)
	}
	request = append(request, byte(portNum>>8), byte(portNum))

	if _, err := conn.Write(request); err != nil {
		return fail(err)
	}

	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return fail(err)
	}
	if header[1] != 0x00 {
		return fail(fmt.Errorf("socks5 connect failed: %d", header[1]))
	}

	switch header[3] {
	case 0x01:
		if _, err := io.CopyN(io.Discard, conn, 6); err != nil {
			return fail(err)
		}
	case 0x03:
		size := make([]byte, 1)
		if _, err := io.ReadFull(conn, size); err != nil {
			return fail(err)
		}
		if _, err := io.CopyN(io.Discard, conn, int64(size[0])+2); err != nil {
			return fail(err)
		}
	case 0x04:
		if _, err := io.CopyN(io.Discard, conn, 18); err != nil {
			return fail(err)
		}
	default:
		return fail(errors.New("unknown socks5 address type"))
	}

	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

func (s *service) addRegions(ctx context.Context, proxies []ProxyRecord) {
	limiter := newRegionLimiter(defaultRegionAPIQPS)
	defer limiter.Stop()

	sem := make(chan struct{}, s.regionConcurrency)
	var wg sync.WaitGroup
	for index := range proxies {
		index := index
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			if err := limiter.Wait(ctx); err != nil {
				return
			}

			region, err := s.detectRegion(ctx, proxies[index].IP)
			address := proxies[index].Address()
			if err != nil {
				s.printf("检测代理 %s 地区信息时发生异常: %v\n", address, err)
				proxies[index].Region = "未知"
				return
			}
			proxies[index].Region = region
			s.printf("代理 %s 地区检测结果: %s\n", address, region)
		}()
	}
	wg.Wait()
}

func (s *service) detectRegion(ctx context.Context, ip string) (string, error) {
	apiURL := fmt.Sprintf(s.regionLookupURL, ip)
	var lastErr error

	for attempt := 1; attempt <= 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("User-Agent", randomUserAgent())
		req.Header.Set("Accept", "application/json")

		resp, err := s.client.Do(req)
		if err != nil {
			lastErr = err
		} else {
			if resp.StatusCode != http.StatusOK {
				lastErr = fmt.Errorf("unexpected status: %s", resp.Status)
			} else {
				var payload struct {
					Data struct {
						RGeo struct {
							Country  string `json:"country"`
							Province string `json:"province"`
							City     string `json:"city"`
						} `json:"rgeo"`
					} `json:"data"`
				}
				if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
					lastErr = err
				} else {
					ipRegion := payload.Data.RGeo
					if ipRegion.Country == "" {
						ipRegion.Country = "未知"
					}
					if ipRegion.Province == "" {
						ipRegion.Province = "未知"
					}
					if ipRegion.City == "" {
						ipRegion.City = "未知"
					}
					region := ipRegion.Country + "/" + ipRegion.Province + "/" + ipRegion.City
					resp.Body.Close()
					return region, nil
				}
			}
			resp.Body.Close()
		}

		s.printf("检测 IP %s 地区信息失败 (尝试 %d/3): %v\n", ip, attempt, lastErr)
		if attempt < 3 {
			if err := sleepContext(ctx, time.Second); err != nil {
				return "", err
			}
		}
	}

	if lastErr == nil {
		lastErr = errors.New("unknown region response")
	}
	return "未知", lastErr
}
