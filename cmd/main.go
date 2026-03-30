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
		interactive         = flag.Bool("interactive", false, "run the prompt-driven workflow")
		count               = flag.Int("count", 0, "maximum number of proxies to keep before validation; 0 means all")
		validCount          = flag.Int("valid-count", 0, "stop validation after this many usable proxies are found; 0 means all")
		unfilteredFormat    = flag.String("unfiltered-format", "json", "output format for the unfiltered proxy list: json or txt")
		validFormat         = flag.String("valid-format", "json", "output format for the validated proxy list: json or txt")
		outputDir           = flag.String("output-dir", ".", "directory used to store generated output files")
		checkRegion         = flag.Bool("check-region", false, "resolve region metadata for validated proxies")
		checkTypeHTTPS      = flag.Bool("check-type-https", true, "detect proxy protocol and HTTPS support during validation")
		onlyIP              = flag.Bool("only-ip", false, "when valid-format=txt, write one ip:port entry per line")
		requestTimeout      = flag.Duration("timeout", 20*time.Second, "request timeout used for fetch and region lookups")
		fetchConcurrency    = flag.Int("fetch-concurrency", 8, "maximum concurrent fetch requests")
		validateConcurrency = flag.Int("validate-concurrency", 100, "maximum concurrent proxy validations")
		fetchRetries        = flag.Int("fetch-retries", 3, "maximum fetch attempts per source page")
		fetchRetryDelay     = flag.Duration("fetch-retry-delay", 500*time.Millisecond, "base delay between fetch retries")
		sourceConfig        = flag.String("source-config", "", "optional JSON file used to override the built-in source list")
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
