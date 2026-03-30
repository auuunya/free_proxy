package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

func (s *service) promptProxyCount(proxies []ProxyRecord) ([]ProxyRecord, error) {
	for {
		line, err := s.prompt(fmt.Sprintf("请输入你需要的代理数量 (可用: %d, 直接回车代表全部): ", len(proxies)))
		if err != nil {
			return nil, err
		}
		if line == "" {
			return append([]ProxyRecord(nil), proxies...), nil
		}

		count, err := strconv.Atoi(line)
		if err != nil {
			s.printf("输入无效，请输入一个有效的整数或直接回车。\n")
			continue
		}
		selected, err := selectProxyCount(proxies, count)
		if err != nil {
			s.printf("%v\n", err)
			continue
		}
		return selected, nil
	}
}

func (s *service) promptChoice(message string, choices []string) (string, error) {
	valid := make(map[string]struct{}, len(choices))
	for _, choice := range choices {
		valid[choice] = struct{}{}
	}

	for {
		line, err := s.prompt(message)
		if err != nil {
			return "", err
		}
		if _, ok := valid[line]; ok {
			return line, nil
		}
		s.printf("输入无效，请输入 %s。\n", strings.Join(choices, " 或 "))
	}
}

func (s *service) promptBool(message string, defaultYes bool) (bool, error) {
	for {
		line, err := s.prompt(message)
		if err != nil {
			return false, err
		}
		switch line {
		case "":
			return defaultYes, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		default:
			s.printf("输入无效，请输入 y 或 n。\n")
		}
	}
}

func (s *service) prompt(message string) (string, error) {
	if s.reader == nil {
		return "", errors.New("interactive input is not configured")
	}

	s.printf("%s", message)
	line, err := s.reader.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			return strings.TrimSpace(line), nil
		}
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func (s *service) printf(format string, args ...any) {
	fmt.Fprintf(s.out, format, args...)
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
