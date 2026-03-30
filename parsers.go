package proxy

import (
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strings"
)

var (
	tableRegexp     = regexp.MustCompile(`(?is)<table\b([^>]*)>(.*?)</table>`)
	rowRegexp       = regexp.MustCompile(`(?is)<tr\b([^>]*)>(.*?)</tr>`)
	cellRegexp      = regexp.MustCompile(`(?is)<t[dh]\b[^>]*>(.*?)</t[dh]>`)
	classAttrRegexp = regexp.MustCompile(`(?is)\bclass\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	tagRegexp       = regexp.MustCompile(`(?is)<[^>]+>`)
	spaceRegexp     = regexp.MustCompile(`\s+`)
)

func parseLineProxyList(body, defaultType, defaultHTTPS string) []ProxyRecord {
	lines := strings.Split(body, "\n")
	proxies := make([]ProxyRecord, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, ":") {
			continue
		}

		parts := strings.Split(line, ":")
		if len(parts) < 2 {
			continue
		}

		proxies = append(proxies, ProxyRecord{
			IP:    strings.TrimSpace(parts[0]),
			Port:  strings.TrimSpace(parts[1]),
			Type:  defaultType,
			HTTPS: defaultHTTPS,
		})
	}
	return proxies
}

func parseGeonode(body []byte) ([]ProxyRecord, error) {
	var payload struct {
		Data []struct {
			IP        string      `json:"ip"`
			Port      interface{} `json:"port"`
			Protocols []string    `json:"protocols"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}

	proxies := make([]ProxyRecord, 0, len(payload.Data))
	for _, item := range payload.Data {
		proxyType := "Unchecked"
		httpsSupport := "Unsupported"
		if len(item.Protocols) > 0 {
			proxyType = strings.ToUpper(item.Protocols[0])
		}
		for _, protocol := range item.Protocols {
			if strings.EqualFold(protocol, "https") {
				httpsSupport = "Supported"
				break
			}
		}
		proxies = append(proxies, ProxyRecord{
			IP:    strings.TrimSpace(item.IP),
			Port:  strings.TrimSpace(fmt.Sprint(item.Port)),
			Type:  proxyType,
			HTTPS: httpsSupport,
		})
	}
	return proxies, nil
}

func parseWebProxyPage(body string, parser webParser) ([]ProxyRecord, error) {
	switch parser {
	case parserFreeProxyList:
		return parseFreeProxyList(body), nil
	case parserProxyListPlus:
		return parseProxyListPlus(body), nil
	case parser89IP:
		return parse89IP(body), nil
	case parserSSLProxies:
		return parseSSLProxies(body), nil
	default:
		return nil, fmt.Errorf("unknown parser: %s", parser)
	}
}

func parseFreeProxyList(body string) []ProxyRecord {
	rows := extractRowsFromTableClass(body, "table")
	proxies := make([]ProxyRecord, 0, len(rows))
	for _, row := range rows {
		if len(row) < 7 {
			continue
		}

		proxyType := "HTTP"
		if strings.Contains(strings.ToLower(row[4]), "socks") {
			proxyType = "SOCKS5"
		}

		httpsSupport := "Unsupported"
		if strings.Contains(strings.ToLower(row[6]), "yes") {
			httpsSupport = "Supported"
		}

		proxies = append(proxies, ProxyRecord{
			IP:    row[0],
			Port:  row[1],
			Type:  proxyType,
			HTTPS: httpsSupport,
		})
	}
	return proxies
}

func parseProxyListPlus(body string) []ProxyRecord {
	rows := extractRowsByClass(body, "cells")
	proxies := make([]ProxyRecord, 0, len(rows))
	for _, row := range rows {
		if len(row) < 7 {
			continue
		}

		httpsSupport := "Unsupported"
		if strings.Contains(strings.ToLower(row[5]), "yes") {
			httpsSupport = "Supported"
		}

		proxies = append(proxies, ProxyRecord{
			IP:    row[1],
			Port:  row[2],
			Type:  strings.ToUpper(row[6]),
			HTTPS: httpsSupport,
		})
	}
	return proxies
}

func parse89IP(body string) []ProxyRecord {
	rows := extractRowsFromTableClass(body, "layui-table")
	proxies := make([]ProxyRecord, 0, len(rows))
	for _, row := range rows {
		if len(row) < 2 {
			continue
		}
		proxies = append(proxies, ProxyRecord{
			IP:    row[0],
			Port:  row[1],
			Type:  "HTTP",
			HTTPS: "Unchecked",
		})
	}
	return proxies
}

func parseSSLProxies(body string) []ProxyRecord {
	rows := extractRowsFromTableClass(body, "table")
	proxies := make([]ProxyRecord, 0, len(rows))
	for _, row := range rows {
		if len(row) < 2 {
			continue
		}
		proxies = append(proxies, ProxyRecord{
			IP:    row[0],
			Port:  row[1],
			Type:  "HTTPS",
			HTTPS: "Supported",
		})
	}
	return proxies
}

func extractRowsFromTableClass(body, className string) [][]string {
	tables := tableRegexp.FindAllStringSubmatch(body, -1)
	for _, match := range tables {
		if !hasClass(match[1], className) {
			continue
		}
		rows := extractRows(match[2], "")
		if len(rows) > 0 {
			return rows
		}
	}
	return nil
}

func extractRowsByClass(body, className string) [][]string {
	return extractRows(body, className)
}

func extractRows(body, rowClass string) [][]string {
	matches := rowRegexp.FindAllStringSubmatch(body, -1)
	rows := make([][]string, 0, len(matches))
	for _, match := range matches {
		if rowClass != "" && !hasClass(match[1], rowClass) {
			continue
		}
		cells := cellRegexp.FindAllStringSubmatch(match[2], -1)
		if len(cells) == 0 {
			continue
		}
		row := make([]string, 0, len(cells))
		for _, cell := range cells {
			row = append(row, cleanHTMLText(cell[1]))
		}
		rows = append(rows, row)
	}
	return rows
}

func hasClass(attrs, className string) bool {
	match := classAttrRegexp.FindStringSubmatch(attrs)
	if len(match) < 3 {
		return false
	}

	classes := match[1]
	if classes == "" {
		classes = match[2]
	}
	for _, item := range strings.Fields(classes) {
		if item == className {
			return true
		}
	}
	return false
}

func cleanHTMLText(raw string) string {
	text := tagRegexp.ReplaceAllString(raw, " ")
	text = html.UnescapeString(text)
	text = strings.ReplaceAll(text, "\u00a0", " ")
	text = spaceRegexp.ReplaceAllString(text, " ")
	return strings.TrimSpace(text)
}

func sanitizeProxies(proxies []ProxyRecord) []ProxyRecord {
	valid := make([]ProxyRecord, 0, len(proxies))
	for _, proxyItem := range proxies {
		proxyItem.IP = strings.TrimSpace(proxyItem.IP)
		proxyItem.Port = strings.TrimSpace(proxyItem.Port)
		if proxyItem.IP == "" || proxyItem.Port == "" {
			continue
		}
		if proxyItem.Type == "" {
			proxyItem.Type = "Unchecked"
		}
		if proxyItem.HTTPS == "" {
			proxyItem.HTTPS = "Unchecked"
		}
		valid = append(valid, proxyItem)
	}
	return valid
}

func dedupeProxies(proxies []ProxyRecord) []ProxyRecord {
	seen := make(map[string]struct{}, len(proxies))
	unique := make([]ProxyRecord, 0, len(proxies))
	for _, proxyItem := range proxies {
		if proxyItem.IP == "" || proxyItem.Port == "" {
			continue
		}
		key := proxyItem.Address()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, proxyItem)
	}
	return unique
}
