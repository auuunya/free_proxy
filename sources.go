package proxy

import (
	"encoding/json"
	"fmt"
	"os"
)

type siteKind string

const (
	siteKindAPIText siteKind = "api_text"
	siteKindAPIJSON siteKind = "api_json"
	siteKindWeb     siteKind = "web"
)

type webParser string

const (
	parserFreeProxyList webParser = "freeproxylist"
	parserProxyListPlus webParser = "proxy_list_plus"
	parser89IP          webParser = "89ip"
	parserSSLProxies    webParser = "sslproxies"
)

type siteConfig struct {
	Name       string    `json:"name"`
	URL        string    `json:"url"`
	Pages      int       `json:"pages"`
	Kind       siteKind  `json:"kind"`
	WebParser  webParser `json:"web_parser,omitempty"`
	DefaultTyp string    `json:"default_type,omitempty"`
	DefaultSSL string    `json:"default_https,omitempty"`
}

var defaultSiteConfigs = []siteConfig{
	{
		Name:       "proxy_scdn",
		URL:        "https://proxy.scdn.io/text.php",
		Pages:      1,
		Kind:       siteKindAPIText,
		DefaultTyp: "待检测",
		DefaultSSL: "待检测",
	},
	{
		Name:      "freeproxylist",
		URL:       "https://free-proxy-list.net/",
		Pages:     1,
		Kind:      siteKindWeb,
		WebParser: parserFreeProxyList,
	},
	{
		Name:      "proxy_list_plus",
		URL:       "https://list.proxylistplus.com/Fresh-HTTP-Proxy-List-{page}",
		Pages:     5,
		Kind:      siteKindWeb,
		WebParser: parserProxyListPlus,
	},
	{
		Name:  "geonode",
		URL:   "https://proxylist.geonode.com/api/proxy-list?limit=100&page={page}",
		Pages: 5,
		Kind:  siteKindAPIJSON,
	},
	{
		Name:      "89ip",
		URL:       "https://www.89ip.cn/index_{page}.html",
		Pages:     5,
		Kind:      siteKindWeb,
		WebParser: parser89IP,
	},
	{
		Name:       "proxyscrape",
		URL:        "https://api.proxyscrape.com/v2/?request=getproxies&protocol=http&timeout=10000&country=all&ssl=all&anonymity=all",
		Pages:      1,
		Kind:       siteKindAPIText,
		DefaultTyp: "HTTP",
		DefaultSSL: "待检测",
	},
	{
		Name:      "sslproxies",
		URL:       "https://www.sslproxies.org/",
		Pages:     1,
		Kind:      siteKindWeb,
		WebParser: parserSSLProxies,
	},
}

var siteConfigs = append([]siteConfig(nil), defaultSiteConfigs...)

func loadSiteConfigs(path string) ([]siteConfig, error) {
	if path == "" {
		return append([]siteConfig(nil), defaultSiteConfigs...), nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var configs []siteConfig
	if err := json.Unmarshal(data, &configs); err != nil {
		return nil, fmt.Errorf("decode source config: %w", err)
	}
	if len(configs) == 0 {
		return nil, fmt.Errorf("source config is empty")
	}
	for i, cfg := range configs {
		if cfg.Name == "" {
			return nil, fmt.Errorf("source %d has empty name", i)
		}
		if cfg.URL == "" {
			return nil, fmt.Errorf("source %q has empty url", cfg.Name)
		}
		if cfg.Pages <= 0 {
			configs[i].Pages = 1
		}
	}
	return configs, nil
}
