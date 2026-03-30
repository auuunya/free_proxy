package proxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type service struct {
	reader              *bufio.Reader
	out                 io.Writer
	client              *http.Client
	fetchLimit          chan struct{}
	fetchRetryCount     int
	fetchRetryDelay     time.Duration
	validateConcurrency int
	regionConcurrency   int
	regionLookupURL     string
}

type siteFetchResult struct {
	index   int
	name    string
	proxies []ProxyRecord
	stats   siteStats
	err     error
}

type pageFetchResult struct {
	page       int
	proxies    []ProxyRecord
	retryCount int
	err        error
}

type siteStats struct {
	name             string
	pages            int
	fetchedPages     int
	failedPages      int
	fetchedProxies   int
	fetchRetries     int
	validatedSuccess int
}

// Run starts the proxy workflow in non-interactive mode.
func Run(ctx context.Context, out io.Writer, options Options) error {
	normalized, err := options.normalized()
	if err != nil {
		return err
	}

	s := newService(nil, out, normalized)
	s.printBanner()

	sources, err := loadSiteConfigs(normalized.SourceConfigPath)
	if err != nil {
		return err
	}

	proxies, stats, err := s.collectUniqueProxies(ctx, sources)
	if err != nil {
		return err
	}
	if len(proxies) == 0 {
		s.printSourceStats(stats)
		s.printf("未能获取到任何代理，程序即将退出。\n")
		return nil
	}

	selected, err := selectProxyCount(proxies, normalized.Count)
	if err != nil {
		return err
	}
	return s.execute(ctx, selected, normalized, stats)
}

// RunInteractive preserves the original prompt-driven flow for local terminal use.
func RunInteractive(ctx context.Context, in io.Reader, out io.Writer) error {
	options, err := (Options{RequestTimeout: defaultRequestTimeout}).normalized()
	if err != nil {
		return err
	}

	s := newService(in, out, options)
	s.printBanner()

	proxies, stats, err := s.collectUniqueProxies(ctx, append([]siteConfig(nil), defaultSiteConfigs...))
	if err != nil {
		return err
	}
	if len(proxies) == 0 {
		s.printSourceStats(stats)
		s.printf("未能获取到任何代理，程序即将退出。\n")
		return nil
	}

	selected, err := s.promptProxyCount(proxies)
	if err != nil {
		return err
	}

	unfilteredFormat, err := s.promptChoice("请选择未过滤代理 IP 的输出格式 (json/txt): ", []string{"json", "txt"})
	if err != nil {
		return err
	}
	checkRegion, err := s.promptBool("是否需要检测代理 IP 的地区信息？(y/n, 默认n): ", false)
	if err != nil {
		return err
	}
	checkTypeHTTPS, err := s.promptBool("是否需要检测代理类型和HTTPS支持？(y/n, 默认y): ", true)
	if err != nil {
		return err
	}

	validFormat, err := s.promptChoice("请选择有效代理 IP 的输出格式 (json/txt): ", []string{"json", "txt"})
	if err != nil {
		return err
	}

	onlyIP := false
	if validFormat == "txt" {
		onlyIP, err = s.promptBool("是否仅输出代理 IP (ip:port)，每行一个？(y/n, 默认n): ", false)
		if err != nil {
			return err
		}
	}

	return s.execute(ctx, selected, Options{
		UnfilteredFormat:    unfilteredFormat,
		ValidFormat:         validFormat,
		OutputDir:           ".",
		CheckRegion:         checkRegion,
		CheckTypeHTTPS:      checkTypeHTTPS,
		OnlyIP:              onlyIP,
		RequestTimeout:      options.RequestTimeout,
		FetchConcurrency:    options.FetchConcurrency,
		ValidateConcurrency: options.ValidateConcurrency,
		RegionConcurrency:   options.RegionConcurrency,
		FetchRetryCount:     options.FetchRetryCount,
		FetchRetryDelay:     options.FetchRetryDelay,
		RegionLookupURL:     options.RegionLookupURL,
	}, stats)
}

func newService(in io.Reader, out io.Writer, options Options) *service {
	var reader *bufio.Reader
	if in != nil {
		reader = bufio.NewReader(in)
	}
	if out == nil {
		out = io.Discard
	}
	return &service{
		reader:              reader,
		out:                 out,
		client:              &http.Client{Timeout: options.RequestTimeout},
		fetchLimit:          make(chan struct{}, options.FetchConcurrency),
		fetchRetryCount:     options.FetchRetryCount,
		fetchRetryDelay:     options.FetchRetryDelay,
		validateConcurrency: options.ValidateConcurrency,
		regionConcurrency:   options.RegionConcurrency,
		regionLookupURL:     options.RegionLookupURL,
	}
}

func (s *service) printBanner() {
	s.printf("%s\n", strings.Repeat("=", 50))
	s.printf("多源代理 IP 爬虫 v3.0 (Go 协程版)\n")
	s.printf("%s\n", strings.Repeat("=", 50))
}

func (s *service) collectUniqueProxies(ctx context.Context, sources []siteConfig) ([]ProxyRecord, []siteStats, error) {
	s.printf("\n正在并发爬取代理源...\n")

	results := make(chan siteFetchResult, len(sources))
	var wg sync.WaitGroup
	for index, cfg := range sources {
		index := index
		cfg := cfg
		wg.Add(1)
		go func() {
			defer wg.Done()
			siteProxies, stats, err := s.getProxiesFromSite(ctx, cfg)
			results <- siteFetchResult{
				index:   index,
				name:    cfg.Name,
				proxies: siteProxies,
				stats:   stats,
				err:     err,
			}
		}()
	}
	wg.Wait()
	close(results)

	collected := make([]siteFetchResult, 0, len(sources))
	for result := range results {
		collected = append(collected, result)
	}
	sort.Slice(collected, func(i, j int) bool {
		return collected[i].index < collected[j].index
	})

	var allProxies []ProxyRecord
	stats := make([]siteStats, 0, len(collected))
	for _, result := range collected {
		if errors.Is(result.err, context.Canceled) || errors.Is(result.err, context.DeadlineExceeded) {
			return nil, nil, result.err
		}
		stats = append(stats, result.stats)
		if result.err != nil {
			s.printf("站点 %s 爬取时发生异常: %v\n", result.name, result.err)
			continue
		}
		if len(result.proxies) == 0 {
			s.printf("站点 %s 未返回任何代理。\n", result.name)
			continue
		}
		s.printf("从 %s 获取到 %d 个原始代理\n", result.name, len(result.proxies))
		allProxies = append(allProxies, result.proxies...)
	}

	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	uniqueProxies := sortProxyRecords(dedupeProxies(allProxies))
	s.printf("\n共获取到 %d 个唯一代理\n", len(uniqueProxies))
	return uniqueProxies, stats, nil
}

func (s *service) execute(ctx context.Context, selected []ProxyRecord, options Options, stats []siteStats) error {
	selected = sortProxyRecords(selected)
	s.printf("已选择 %d 个代理进行后续处理。\n", len(selected))

	currentTime := time.Now().Format("20060102150405")
	unfilteredFilename := filepath.Join(options.OutputDir, fmt.Sprintf("unfiltered_proxies_%s.%s", currentTime, options.UnfilteredFormat))
	if err := saveProxies(selected, options.UnfilteredFormat, unfilteredFilename, false); err != nil {
		return err
	}
	s.printf("代理已保存到 %s\n", unfilteredFilename)

	s.printf("\n正在使用 goroutine 并发验证代理可用性...\n")
	validProxies := s.validateProxies(ctx, selected, options.CheckTypeHTTPS, options.TargetValidCount, stats)
	s.printf("验证完成，共有 %d 个代理可用。\n", len(validProxies))
	s.printSourceStats(stats)
	if len(validProxies) == 0 {
		s.printf("没有找到可用的代理，程序结束。\n")
		return nil
	}

	if options.CheckRegion {
		s.printf("\n正在为有效代理添加地区信息...\n")
		s.addRegions(ctx, validProxies)
	}

	validProxies = sortProxyRecords(validProxies)
	if options.TargetValidCount > 0 && len(validProxies) > options.TargetValidCount {
		validProxies = validProxies[:options.TargetValidCount]
	}

	s.printf("\n处理完成，找到 %d 个有效代理。\n", len(validProxies))
	validFilename := filepath.Join(options.OutputDir, fmt.Sprintf("valid_proxies_%s.%s", currentTime, options.ValidFormat))
	if err := saveProxies(validProxies, options.ValidFormat, validFilename, options.OnlyIP); err != nil {
		return err
	}
	s.printf("代理已保存到 %s\n", validFilename)
	s.printf("\n代理爬取和验证任务完成。\n")
	return nil
}

func (s *service) getProxiesFromSite(ctx context.Context, cfg siteConfig) ([]ProxyRecord, siteStats, error) {
	pageCount := max(1, cfg.Pages)
	results := make(chan pageFetchResult, pageCount)
	var wg sync.WaitGroup
	for page := 1; page <= pageCount; page++ {
		page := page
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- s.fetchSitePage(ctx, cfg, page)
		}()
	}
	wg.Wait()
	close(results)

	pages := make([]pageFetchResult, 0, pageCount)
	for result := range results {
		pages = append(pages, result)
	}
	sort.Slice(pages, func(i, j int) bool {
		return pages[i].page < pages[j].page
	})

	stats := siteStats{name: cfg.Name, pages: pageCount}
	all := make([]ProxyRecord, 0)
	for _, result := range pages {
		if ctx.Err() != nil {
			return all, stats, ctx.Err()
		}
		stats.fetchRetries += result.retryCount
		if result.err != nil {
			stats.failedPages++
			s.printf("%s 第 %d 页爬取失败: %v\n", cfg.Name, result.page, result.err)
			continue
		}
		stats.fetchedPages++
		stats.fetchedProxies += len(result.proxies)
		all = append(all, result.proxies...)
		s.printf("从 %s 第 %d 页获取到 %d 个代理。\n", cfg.Name, result.page, len(result.proxies))
	}
	return all, stats, nil
}

func (s *service) fetchSitePage(ctx context.Context, cfg siteConfig, page int) pageFetchResult {
	pageURL := strings.ReplaceAll(cfg.URL, "{page}", strconv.Itoa(page))
	s.printf("正在爬取 %s 第 %d 页: %s\n", cfg.Name, page, pageURL)

	body, retries, err := s.fetchWithRetry(ctx, pageURL)
	if err != nil {
		return pageFetchResult{page: page, retryCount: retries, err: err}
	}

	var proxies []ProxyRecord
	switch cfg.Kind {
	case siteKindAPIText:
		proxies = parseLineProxyList(string(body), cfg.DefaultTyp, cfg.DefaultSSL)
	case siteKindAPIJSON:
		proxies, err = parseGeonode(body)
		if err != nil {
			return pageFetchResult{page: page, retryCount: retries, err: fmt.Errorf("返回的 JSON 数据格式有误: %w", err)}
		}
	case siteKindWeb:
		proxies, err = parseWebProxyPage(string(body), cfg.WebParser)
		if err != nil {
			return pageFetchResult{page: page, retryCount: retries, err: fmt.Errorf("页面解析失败: %w", err)}
		}
	}

	sanitized := sanitizeProxies(proxies)
	for i := range sanitized {
		sanitized[i].sourceName = cfg.Name
	}
	return pageFetchResult{page: page, proxies: sanitized, retryCount: retries}
}

func (s *service) printSourceStats(stats []siteStats) {
	if len(stats) == 0 {
		return
	}
	s.printf("\n源站汇总:\n")
	for _, stat := range stats {
		successRate := 0.0
		if stat.pages > 0 {
			successRate = float64(stat.fetchedPages) / float64(stat.pages) * 100
		}
		s.printf("- %s: 抓取=%d, 重试=%d, 页面成功率=%.0f%%, 验证通过=%d\n", stat.name, stat.fetchedProxies, stat.fetchRetries, successRate, stat.validatedSuccess)
	}
}

func selectProxyCount(proxies []ProxyRecord, count int) ([]ProxyRecord, error) {
	if count == 0 {
		return append([]ProxyRecord(nil), proxies...), nil
	}
	if count < 0 {
		return nil, fmt.Errorf("count must be >= 0")
	}
	if count > len(proxies) {
		return nil, fmt.Errorf("requested %d proxies, but only %d are available", count, len(proxies))
	}
	return append([]ProxyRecord(nil), proxies[:count]...), nil
}
