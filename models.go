package proxy

import (
	"crypto/rand"
	"fmt"
	"net"
	"strings"
	"time"
)

const (
	defaultConcurrentLimit  = 100
	defaultRegionAPIQPS     = 100
	defaultRequestTimeout   = 20 * time.Second
	defaultFetchConcurrency = 8
	defaultFetchRetryCount  = 3
	defaultFetchRetryDelay  = 500 * time.Millisecond
	defaultRegionLookupURL  = "https://apimobile.meituan.com/locate/v2/ip/loc?rgeo=true&ip=%s"
)

var (
	httpProbeURL  = "http://httpbin.org/ip"
	httpsProbeURL = "https://httpbin.org/ip"
)

var userAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_4) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0.6834.160 Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.6778.204 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:134.0) Gecko/20100101 Firefox/134.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 14.4; rv:133.0) Gecko/20100101 Firefox/133.0",
	"Mozilla/5.0 (X11; Linux x86_64; rv:132.0) Gecko/20100101 Firefox/132.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_4) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.3 Safari/605.1.15",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Edg/132.0.2957.140 Chrome/132.0.6834.160 Safari/537.36",
	"Mozilla/5.0 (iPhone; CPU iPhone OS 18_3 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.3 Mobile/15E148 Safari/604.1",
	"Mozilla/5.0 (Linux; Android 15; Pixel 9 Pro) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.6943.54 Mobile Safari/537.36",
}

// ProxyRecord is the normalized proxy representation shared by collectors,
// validators, and CLI output writers.
type ProxyRecord struct {
	IP          string `json:"ip"`
	Port        string `json:"port"`
	Type        string `json:"type,omitempty"`
	HTTPS       string `json:"https,omitempty"`
	Region      string `json:"region,omitempty"`
	ValidatedOK bool   `json:"validated_ok,omitempty"`
	sourceName  string
}

// Address returns the socket address in ip:port form.
func (p ProxyRecord) Address() string {
	return net.JoinHostPort(strings.TrimSpace(p.IP), strings.TrimSpace(p.Port))
}

// RequestProfile describes request-side metadata that can be attached to
// another payload for observability or auditing.
type RequestProfile struct {
	UserAgent string
	Proxy     *ProxyRecord
}

// Options controls the non-interactive runner and CLI execution mode.
type Options struct {
	Count               int
	TargetValidCount    int
	UnfilteredFormat    string
	ValidFormat         string
	OutputDir           string
	CheckRegion         bool
	CheckTypeHTTPS      bool
	OnlyIP              bool
	RequestTimeout      time.Duration
	FetchConcurrency    int
	ValidateConcurrency int
	RegionConcurrency   int
	FetchRetryCount     int
	FetchRetryDelay     time.Duration
	RegionLookupURL     string
	SourceConfigPath    string
}

func (o Options) normalized() (Options, error) {
	if o.UnfilteredFormat == "" {
		o.UnfilteredFormat = "json"
	}
	if o.ValidFormat == "" {
		o.ValidFormat = "json"
	}
	o.UnfilteredFormat = strings.ToLower(strings.TrimSpace(o.UnfilteredFormat))
	o.ValidFormat = strings.ToLower(strings.TrimSpace(o.ValidFormat))

	if err := validateFormat(o.UnfilteredFormat); err != nil {
		return Options{}, fmt.Errorf("invalid unfiltered format: %w", err)
	}
	if err := validateFormat(o.ValidFormat); err != nil {
		return Options{}, fmt.Errorf("invalid valid format: %w", err)
	}
	if o.Count < 0 {
		return Options{}, fmt.Errorf("count must be >= 0")
	}
	if o.TargetValidCount < 0 {
		return Options{}, fmt.Errorf("target valid count must be >= 0")
	}
	if o.OutputDir == "" {
		o.OutputDir = "."
	}
	if o.RequestTimeout <= 0 {
		o.RequestTimeout = defaultRequestTimeout
	}
	if o.FetchConcurrency <= 0 {
		o.FetchConcurrency = defaultFetchConcurrency
	}
	if o.ValidateConcurrency <= 0 {
		o.ValidateConcurrency = defaultConcurrentLimit
	}
	if o.RegionConcurrency <= 0 {
		o.RegionConcurrency = defaultConcurrentLimit
	}
	if o.FetchRetryCount <= 0 {
		o.FetchRetryCount = defaultFetchRetryCount
	}
	if o.FetchRetryDelay <= 0 {
		o.FetchRetryDelay = defaultFetchRetryDelay
	}
	if o.RegionLookupURL == "" {
		o.RegionLookupURL = defaultRegionLookupURL
	}
	return o, nil
}

func validateFormat(formatType string) error {
	switch formatType {
	case "json", "txt":
		return nil
	default:
		return fmt.Errorf("unsupported format %q", formatType)
	}
}

func randomUserAgent() string {
	if len(userAgents) == 0 {
		return "Mozilla/5.0"
	}
	var b [1]byte
	if _, err := rand.Read(b[:]); err != nil {
		return userAgents[0]
	}
	return userAgents[int(b[0])%len(userAgents)]
}
