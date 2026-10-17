package main

import (
	"fmt"
	"sort"
	"strings"

	common "github.com/injoyai/strategy-tail"
)

func resolveCodes(raw string, all bool) ([]string, error) {
	if all {
		if err := common.Initialize(); err != nil {
			return nil, err
		}
		codes := append([]string(nil), common.GetAllCodes()...)
		sort.Strings(codes)
		return codes, nil
	}
	seen := make(map[string]struct{})
	var codes []string
	for _, rawCode := range strings.Split(raw, ",") {
		code, err := canonicalCode(rawCode)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		codes = append(codes, code)
	}
	if len(codes) == 0 {
		return nil, fmt.Errorf("valuation-sync: codes 为空")
	}
	sort.Strings(codes)
	return codes, nil
}

func canonicalCode(raw string) (string, error) {
	code := strings.ToLower(strings.TrimSpace(raw))
	if len(code) == 8 && (strings.HasPrefix(code, "sh") || strings.HasPrefix(code, "sz") || strings.HasPrefix(code, "bj")) {
		return code, nil
	}
	if len(code) == 9 && code[6] == '.' {
		suffix := code[7:]
		if suffix == "sh" || suffix == "sz" || suffix == "bj" {
			return suffix + code[:6], nil
		}
	}
	if len(code) == 6 {
		switch code[0] {
		case '6':
			return "sh" + code, nil
		case '0', '3':
			return "sz" + code, nil
		case '4', '8', '9':
			return "bj" + code, nil
		}
	}
	return "", fmt.Errorf("valuation-sync: 股票代码无效 %q", raw)
}
