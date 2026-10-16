package common

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/injoyai/strategy-tail/researchdata"
)

// LocalCodes 从本地日线库枚举市场代码，不依赖当前在市列表。
func (m Market) LocalCodes() ([]string, error) {
	if Pull == nil {
		return nil, fmt.Errorf("本地行情读取器未初始化")
	}
	all, err := Pull.LocalStockCodes()
	if err != nil || m == MarketAll {
		return all, err
	}
	codes := make([]string, 0, len(all))
	for _, code := range all {
		for _, prefix := range m.Prefixes() {
			if strings.HasPrefix(code, prefix) {
				codes = append(codes, code)
				break
			}
		}
	}
	if len(codes) == 0 {
		return nil, fmt.Errorf("本地日线库没有%s股票数据", m)
	}
	return codes, nil
}

// localUniverse 包含本地保留的退市代码；没有逐日成员关系，仍只用于探索。
type localUniverse struct{}

func (localUniverse) Codes(_ time.Time) ([]string, error) {
	return MarketAll.LocalCodes()
}

func (u localUniverse) CodesBetween(_, end time.Time) ([]string, error) {
	return u.Codes(end)
}

func (u localUniverse) Contains(code string, asOf time.Time) (bool, error) {
	codes, err := u.Codes(asOf)
	return slices.Contains(codes, code), err
}

func (u localUniverse) Snapshot() researchdata.UniverseSnapshot {
	// 快照接口没有错误返回值；实际读取失败由 Codes/Contains 传递给执行器。
	codes, _ := u.Codes(time.Time{})
	return researchdata.UniverseSnapshot{
		ID:              "local_klines",
		Source:          "tdx_local",
		Mode:            researchdata.UniverseModeCurrentStatic,
		IncludeDelisted: true,
		MembershipPIT:   researchdata.UniversePITUnverified,
		Size:            len(codes),
	}
}
