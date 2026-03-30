package proxy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func saveProxies(proxies []ProxyRecord, formatType, filename string, onlyIP bool) error {
	if len(proxies) == 0 {
		return nil
	}
	if err := validateFormat(formatType); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		return err
	}

	ordered := sortProxyRecords(proxies)

	switch formatType {
	case "json":
		data, err := json.MarshalIndent(ordered, "", "    ")
		if err != nil {
			return err
		}
		return os.WriteFile(filename, data, 0o644)
	case "txt":
		var builder strings.Builder
		if onlyIP {
			for _, proxyItem := range ordered {
				builder.WriteString(proxyItem.Address())
				builder.WriteString("\n")
			}
		} else {
			builder.WriteString("Region | Type | HTTPS | Proxy Address\n")
			builder.WriteString(strings.Repeat("-", 60))
			builder.WriteString("\n")
			for _, proxyItem := range ordered {
				region := proxyItem.Region
				if region == "" {
					region = "Unknown"
				}
				proxyType := proxyItem.Type
				if proxyType == "" {
					proxyType = "Unknown"
				}
				httpsSupport := proxyItem.HTTPS
				if httpsSupport == "" {
					httpsSupport = "Unknown"
				}
				builder.WriteString(fmt.Sprintf("%s | %s | %s | %s\n", region, proxyType, httpsSupport, proxyItem.Address()))
			}
		}
		return os.WriteFile(filename, []byte(builder.String()), 0o644)
	default:
		return fmt.Errorf("unsupported format: %s", formatType)
	}
}

func sortProxyRecords(proxies []ProxyRecord) []ProxyRecord {
	ordered := append([]ProxyRecord(nil), proxies...)
	sort.Slice(ordered, func(i, j int) bool {
		left := ordered[i]
		right := ordered[j]
		if left.IP != right.IP {
			return left.IP < right.IP
		}
		if left.Port != right.Port {
			return left.Port < right.Port
		}
		if left.Type != right.Type {
			return left.Type < right.Type
		}
		if left.HTTPS != right.HTTPS {
			return left.HTTPS < right.HTTPS
		}
		if left.Region != right.Region {
			return left.Region < right.Region
		}
		return left.sourceName < right.sourceName
	})
	return ordered
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
