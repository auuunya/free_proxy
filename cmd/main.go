package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	proxy "github.com/auuunya/free_proxy"
)

func main() {
	var (
		interactive         = flag.Bool("interactive", false, "run the interactive workflow")
		count               = flag.Int("count", 0, "number of collected proxies to send into validation; 0 uses all collected proxies")
		validCount          = flag.Int("valid-count", 0, "stop validation after finding this many usable proxies; 0 validates all candidates")
		unfilteredFormat    = flag.String("unfiltered-format", "json", "output format for the unfiltered proxy list (json or txt)")
		validFormat         = flag.String("valid-format", "json", "output format for the validated proxy list (json or txt)")
		outputDir           = flag.String("output-dir", ".", "directory where output files are written")
		checkRegion         = flag.Bool("check-region", false, "look up region metadata for validated proxies")
		checkTypeHTTPS      = flag.Bool("check-type-https", true, "detect proxy protocol and HTTPS support during validation")
		onlyIP              = flag.Bool("only-ip", false, "when valid-format=txt, write only ip:port entries, one per line")
		requestTimeout      = flag.Duration("timeout", 20*time.Second, "request timeout for source fetches and region lookups")
		fetchConcurrency    = flag.Int("fetch-concurrency", 8, "maximum number of source fetch requests running at the same time")
		validateConcurrency = flag.Int("validate-concurrency", 100, "maximum number of proxy validation checks running at the same time")
		fetchRetries        = flag.Int("fetch-retries", 3, "maximum number of attempts for each source page fetch")
		fetchRetryDelay     = flag.Duration("fetch-retry-delay", 500*time.Millisecond, "base backoff delay between fetch retries")
		sourceConfig        = flag.String("source-config", "", "path to a JSON file that overrides the built-in source list")
	)
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	if *interactive || flag.NFlag() == 0 {
		err = proxy.RunInteractive(ctx, os.Stdin, os.Stdout)
	} else {
		err = proxy.Run(ctx, os.Stdout, proxy.Options{
			Count:               *count,
			TargetValidCount:    *validCount,
			UnfilteredFormat:    *unfilteredFormat,
			ValidFormat:         *validFormat,
			OutputDir:           *outputDir,
			CheckRegion:         *checkRegion,
			CheckTypeHTTPS:      *checkTypeHTTPS,
			OnlyIP:              *onlyIP,
			RequestTimeout:      *requestTimeout,
			FetchConcurrency:    *fetchConcurrency,
			ValidateConcurrency: *validateConcurrency,
			FetchRetryCount:     *fetchRetries,
			FetchRetryDelay:     *fetchRetryDelay,
			SourceConfigPath:    *sourceConfig,
		})
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
