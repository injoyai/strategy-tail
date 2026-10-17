package builder

import (
	"fmt"
	"math"

	common "github.com/injoyai/strategy-tail"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
	f "github.com/injoyai/strategy-tail/strategies/factor"
)

// noBound A因子过滤 的单边无界值（与 A因子过滤 注释一致：不用 0 表示无限制）。
const noBound = 1e9

// FactorFilterSpec 因子组合与自由搭建共用的单项因子条件。Min/Max 用指针保留 null 与 0 的
// 区别；0 是合法因子值。gte 只要求 Min，lte 只要求 Max，
// between 同时要求 Min/Max 且 Min <= Max。
// FactorVersion 因子实现版本：0=旧配置兼容（按当前注册版本运行）；
// >0 严格模式，必须等于当前注册版本（候选库加入策略时必须写正版本）。
type FactorFilterSpec struct {
	Kind          string   `json:"kind"`
	Days          int      `json:"days"`
	FactorVersion int      `json:"factorVersion,omitempty"`
	Operator      string   `json:"operator"` // gte | lte | between
	Min           *float64 `json:"min,omitempty"`
	Max           *float64 `json:"max,omitempty"`
}

// Validate 单项过滤校验：kind 可注册、days>0、因子版本 fail-closed、
// operator 枚举、阈值有限且区间不反向。
func (ft *FactorFilterSpec) Validate() error {
	if f.BuildContext(ft.Kind, ft.Days) == nil {
		return fmt.Errorf("未知因子类型: %s", ft.Kind)
	}
	if ft.Days <= 0 {
		return fmt.Errorf("回看天数无效: %d（应为正整数）", ft.Days)
	}
	if err := ft.validateVersion(); err != nil {
		return err
	}
	finite := func(v *float64, name string) error {
		if v == nil {
			return fmt.Errorf("%s 缺失", name)
		}
		if math.IsNaN(*v) || math.IsInf(*v, 0) {
			return fmt.Errorf("%s 必须是有限数", name)
		}
		return nil
	}
	switch ft.Operator {
	case "gte":
		return finite(ft.Min, "区间下限 min")
	case "lte":
		return finite(ft.Max, "区间上限 max")
	case "between":
		if err := finite(ft.Min, "区间下限 min"); err != nil {
			return err
		}
		if err := finite(ft.Max, "区间上限 max"); err != nil {
			return err
		}
		if *ft.Min > *ft.Max {
			return fmt.Errorf("区间下限 %v 不能大于上限 %v", *ft.Min, *ft.Max)
		}
		return nil
	default:
		return fmt.Errorf("过滤操作符无效: %s（应为 gte/lte/between）", ft.Operator)
	}
}

// validateVersion 因子实现版本 fail-closed 校验：0=旧请求兼容（按当前注册
// 版本运行）；<0 拒绝；>0 必须等于注册表当前版本，否则返回可读错误。
// build() 前必须经过此校验，禁止绕过。
func (ft *FactorFilterSpec) validateVersion() error {
	if ft.FactorVersion < 0 {
		return fmt.Errorf("因子实现版本无效: %d（不能为负）", ft.FactorVersion)
	}
	if ft.FactorVersion == 0 {
		return nil
	}
	entry, ok := f.Catalog(ft.Kind)
	if !ok {
		return fmt.Errorf("未知因子类型: %s", ft.Kind)
	}
	if ft.FactorVersion != entry.ImplementationVersion {
		return fmt.Errorf("因子实现版本不匹配: %s 当前版本为 %d，请求 %d（请重新分析后保存）",
			ft.Kind, entry.ImplementationVersion, ft.FactorVersion)
	}
	return nil
}

// Build 把操作符映射为 A因子过滤：gte→[min,+∞)、lte→(-∞,max]、
// between→[min,max]；单边用 ±1e9（0 是合法因子值，不用 0 表示无界）。
// 构建前完整校验声明（fail-closed，不允许绕过 validate 直接 build）。
func (ft *FactorFilterSpec) Build() (sb.A因子过滤, error) {
	if err := ft.Validate(); err != nil {
		return sb.A因子过滤{}, err
	}
	fct := f.BuildWithData(ft.Kind, ft.Days, common.ResearchData)
	if fct == nil {
		return sb.A因子过滤{}, fmt.Errorf("未知因子类型: %s", ft.Kind)
	}
	flt := sb.A因子过滤{Factor: fct}
	switch ft.Operator {
	case "gte":
		flt.Min, flt.Max = *ft.Min, noBound
	case "lte":
		flt.Min, flt.Max = -noBound, *ft.Max
	case "between":
		flt.Min, flt.Max = *ft.Min, *ft.Max
	default:
		return sb.A因子过滤{}, fmt.Errorf("过滤操作符无效: %s", ft.Operator)
	}
	return flt, nil
}

// Describe 单条件的人类可读描述（因子名称与区间）。
func (ft *FactorFilterSpec) Describe() string {
	name := f.BuildContext(ft.Kind, ft.Days).Name()
	switch ft.Operator {
	case "gte":
		return fmt.Sprintf("%s≥%.4g", name, *ft.Min)
	case "lte":
		return fmt.Sprintf("%s≤%.4g", name, *ft.Max)
	case "between":
		return fmt.Sprintf("%s∈[%.4g,%.4g]", name, *ft.Min, *ft.Max)
	default:
		return name
	}
}
