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
		s.printf("No proxies were collected. Exiting.\n")
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
		s.printf("No proxies were collected. Exiting.\n")
		return nil
	}

	selected, err := s.promptProxyCount(proxies)
	if err != nil {
		return err
	}

	unfilteredFormat, err := s.promptChoice("Select the output format for unfiltered proxies (json/txt): ", []string{"json", "txt"})
	if err != nil {
		return err
	}
	checkRegion, err := s.promptBool("Look up region metadata for validated proxies? (y/n, default n): ", false)
	if err != nil {
		return err
	}
	checkTypeHTTPS, err := s.promptBool("Detect proxy type and HTTPS support? (y/n, default y): ", true)
	if err != nil {
		return err
	}

	validFormat, err := s.promptChoice("Select the output format for valid proxies (json/txt): ", []string{"json", "txt"})
	if err != nil {
		return err
	}

	onlyIP := false
	if validFormat == "txt" {
		onlyIP, err = s.promptBool("Write only proxy addresses (ip:port), one per line? (y/n, default n): ", false)
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
	s.printf("Multi-source Proxy Collector v3.0 (Go concurrency edition)\n")
	s.printf("%s\n", strings.Repeat("=", 50))
}

func (s *service) collectUniqueProxies(ctx context.Context, sources []siteConfig) ([]ProxyRecord, []siteStats, error) {
	s.printf("\nCollecting proxies from sources concurrently...\n")

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
			s.printf("Source %s failed during collection: %v\n", result.name, result.err)
			continue
		}
		if len(result.proxies) == 0 {
			s.printf("Source %s returned no proxies.\n", result.name)
			continue
		}
		s.printf("Collected %d raw proxies from %s\n", len(result.proxies), result.name)
		allProxies = append(allProxies, result.proxies...)
	}

	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	uniqueProxies := sortProxyRecords(dedupeProxies(allProxies))
	s.printf("\nCollected %d unique proxies in total\n", len(uniqueProxies))
	return uniqueProxies, stats, nil
}

func (s *service) execute(ctx context.Context, selected []ProxyRecord, options Options, stats []siteStats) error {
	selected = sortProxyRecords(selected)
	s.printf("Selected %d proxies for processing.\n", len(selected))

	currentTime := time.Now().Format("20060102150405")
	unfilteredFilename := filepath.Join(options.OutputDir, fmt.Sprintf("unfiltered_proxies_%s.%s", currentTime, options.UnfilteredFormat))
	if err := saveProxies(selected, options.UnfilteredFormat, unfilteredFilename, false); err != nil {
		return err
	}
	s.printf("Saved proxies to %s\n", unfilteredFilename)

	s.printf("\nValidating proxies concurrently with goroutines...\n")
	validProxies := s.validateProxies(ctx, selected, options.CheckTypeHTTPS, options.TargetValidCount, stats)
	s.printf("Validation complete. %d proxies are usable.\n", len(validProxies))
	s.printSourceStats(stats)
	if len(validProxies) == 0 {
		s.printf("No usable proxies were found. Exiting.\n")
		return nil
	}

	if options.CheckRegion {
		s.printf("\nLooking up region metadata for valid proxies...\n")
		s.addRegions(ctx, validProxies)
	}

	validProxies = sortProxyRecords(validProxies)
	if options.TargetValidCount > 0 && len(validProxies) > options.TargetValidCount {
		validProxies = validProxies[:options.TargetValidCount]
	}

	s.printf("\nFinished. Found %d valid proxies.\n", len(validProxies))
	validFilename := filepath.Join(options.OutputDir, fmt.Sprintf("valid_proxies_%s.%s", currentTime, options.ValidFormat))
	if err := saveProxies(validProxies, options.ValidFormat, validFilename, options.OnlyIP); err != nil {
		return err
	}
	s.printf("Saved proxies to %s\n", validFilename)
	s.printf("\nProxy collection and validation completed.\n")
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
			s.printf("%s page %d failed: %v\n", cfg.Name, result.page, result.err)
			continue
		}
		stats.fetchedPages++
		stats.fetchedProxies += len(result.proxies)
		all = append(all, result.proxies...)
		s.printf("Collected %d proxies from %s page %d.\n", len(result.proxies), cfg.Name, result.page)
	}
	return all, stats, nil
}

func (s *service) fetchSitePage(ctx context.Context, cfg siteConfig, page int) pageFetchResult {
	pageURL := strings.ReplaceAll(cfg.URL, "{page}", strconv.Itoa(page))
	s.printf("Fetching %s page %d: %s\n", cfg.Name, page, pageURL)

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
			return pageFetchResult{page: page, retryCount: retries, err: fmt.Errorf("invalid JSON response format: %w", err)}
		}
	case siteKindWeb:
		proxies, err = parseWebProxyPage(string(body), cfg.WebParser)
		if err != nil {
			return pageFetchResult{page: page, retryCount: retries, err: fmt.Errorf("page parsing failed: %w", err)}
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
	s.printf("\nSource summary:\n")
	for _, stat := range stats {
		successRate := 0.0
		if stat.pages > 0 {
			successRate = float64(stat.fetchedPages) / float64(stat.pages) * 100
		}
		s.printf("- %s: fetched=%d, retries=%d, page success rate=%.0f%%, validated=%d\n", stat.name, stat.fetchedProxies, stat.fetchRetries, successRate, stat.validatedSuccess)
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
