package researchdata

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// 股票池模式（与研究协议 UniverseSpec.Mode 对齐）。
const (
	UniverseModeCurrentStatic        = "current_static"        // 当前静态列表：存在生存者偏差，证据等级上限 exploratory
	UniverseModeHistoricalMembership = "historical_membership" // 历史成员关系：按交易日回放
	UniverseModeCodes                = "codes"                 // 指定代码个案研究
)

// 股票池成员关系 PIT 质量等级。verified 表示成员记录具备可靠可知时间且数据
// 来源经过确认；unverified 是缺省降级，只支撑探索分析，不参加严格晋级。
const (
	UniversePITVerified   = "verified"
	UniversePITUnverified = "unverified"
)

// Universe 历史成员只读接口，供应商无关。
//
// 合同：
//   - Codes(asOf)：asOf 当日（按日期粒度）仍在市的成员代码，升序去重；
//   - CodesBetween(start, end)：区间内任一天曾是成员的代码并集，供加载层覆盖
//     已退市、今天不在静态列表中的证券；每日截面仍必须按 Contains 过滤当日
//     成员，不得把区间并集直接当成每天的截面；
//   - Contains(code, asOf)：该代码在 asOf 日是否为有效成员；
//   - Snapshot：不可变质量快照（来源、版本、覆盖范围、PIT 等级），随报告冻结。
type Universe interface {
	Codes(asOf time.Time) ([]string, error)
	CodesBetween(start, end time.Time) ([]string, error)
	Contains(code string, asOf time.Time) (bool, error)
	Snapshot() UniverseSnapshot
}

// UniverseSnapshot 股票池质量快照：报告必须保存来源、版本、覆盖日期和质量
// 等级，用于判断证据等级。没有理想数据源时可以运行探索，但不得静默升级。
type UniverseSnapshot struct {
	ID              string `json:"id"`
	Version         string `json:"version,omitempty"`
	Source          string `json:"source"`
	Mode            string `json:"mode"`
	CoverageStart   string `json:"coverageStart,omitempty"` // YYYY-MM-DD；静态池无历史覆盖时为空
	CoverageEnd     string `json:"coverageEnd,omitempty"`
	IncludeDelisted bool   `json:"includeDelisted"`
	MembershipPIT   string `json:"membershipPit"`
	Size            int    `json:"size"` // 静态池为当前代码数；历史池为成员记录数
}

// Validate 拒绝无法定性来源或质量等级的快照。
func (s UniverseSnapshot) Validate() error {
	if strings.TrimSpace(s.ID) == "" {
		return fmt.Errorf("researchdata: universe snapshot id is empty")
	}
	if strings.TrimSpace(s.Source) == "" {
		return fmt.Errorf("researchdata: universe %q source is empty", s.ID)
	}
	switch s.Mode {
	case UniverseModeCurrentStatic, UniverseModeHistoricalMembership, UniverseModeCodes:
	default:
		return fmt.Errorf("researchdata: universe %q mode is invalid: %q", s.ID, s.Mode)
	}
	switch s.MembershipPIT {
	case UniversePITVerified, UniversePITUnverified:
	default:
		return fmt.Errorf("researchdata: universe %q membership PIT is invalid: %q", s.ID, s.MembershipPIT)
	}
	return nil
}

// dedupeSorted 升序去重，返回新切片。
func dedupeSorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	k := 0
	for i, v := range out {
		if i == 0 || v != out[i-1] {
			out[k] = v
			k++
		}
	}
	return out[:k]
}

// StaticUniverse 当前静态股票池：codes 惰性取自调用方提供的函数（避免包
// 初始化顺序依赖），任何时点的 Codes/Contains 都按"当前"列表回答——这正是
// 生存者偏差的来源。MembershipPIT 恒为 unverified，只能支撑 exploratory
// 证据等级。任务编排方必须在任务开始时取快照固定成员，不得在运行期间依赖
// 实时列表。
type StaticUniverse struct {
	id     string
	source string
	codes  func() []string
}

// StaticUniverseConfig 静态股票池配置。Codes 为惰性求值的当前代码列表，nil
// 视为空列表。
type StaticUniverseConfig struct {
	ID     string
	Source string
	Codes  func() []string
}

// NewStaticUniverse 创建当前静态股票池；ID/Source 缺省填充，保证 Snapshot
// 始终可校验。
func NewStaticUniverse(cfg StaticUniverseConfig) *StaticUniverse {
	if strings.TrimSpace(cfg.ID) == "" {
		cfg.ID = "current_static"
	}
	if strings.TrimSpace(cfg.Source) == "" {
		cfg.Source = "unknown"
	}
	return &StaticUniverse{id: cfg.ID, source: cfg.Source, codes: cfg.Codes}
}

func (u *StaticUniverse) list() []string {
	if u.codes == nil {
		return nil
	}
	return u.codes()
}

// Codes 静态池与时间无关：任何 asOf 都返回当前列表（升序去重）。
func (u *StaticUniverse) Codes(asOf time.Time) ([]string, error) {
	return dedupeSorted(u.list()), nil
}

// CodesBetween 静态池无历史概念，区间并集即当前列表。
func (u *StaticUniverse) CodesBetween(start, end time.Time) ([]string, error) {
	return u.Codes(end)
}

// Contains 按当前列表判断，与 asOf 无关。
func (u *StaticUniverse) Contains(code string, asOf time.Time) (bool, error) {
	for _, c := range u.list() {
		if c == code {
			return true, nil
		}
	}
	return false, nil
}

// Snapshot 静态池快照：无历史覆盖日期、不含退市、PIT 恒为 unverified。
// Size 按去重后的当前代码数计。
func (u *StaticUniverse) Snapshot() UniverseSnapshot {
	return UniverseSnapshot{
		ID:              u.id,
		Source:          u.source,
		Mode:            UniverseModeCurrentStatic,
		IncludeDelisted: false,
		MembershipPIT:   UniversePITUnverified,
		Size:            len(dedupeSorted(u.list())),
	}
}
