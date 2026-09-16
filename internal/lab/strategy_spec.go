package lab

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/injoyai/strategy-tail/core"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
	f "github.com/injoyai/strategy-tail/strategies/factor"
)

// strategy_spec.go 简单模式的声明式策略配置（实施文档 §4.2）。
//
// 职责边界：Validate 只管声明式合同（version/preset/名称/N 项因子过滤）；
// RunConfig 只做字段转换，运行范围与卖出规则的最终合法性留给现有 RunConfig；
// Variants 从预设 Builder 取基础 Buyer，逐项组合 A因子过滤。
// 不给 core.Buyer 增加序列化能力，接口实例不写入报告。

// strategySpecVersion 首版配置版本；升级时显式迁移。
const strategySpecVersion = 1

// noBound A因子过滤 的单边无界值（与 A因子过滤 注释一致：不用 0 表示无限制）。
const noBound = 1e9

// FactorFilterSpec 单项因子区间过滤。Min/Max 用指针保留 null 与 0 的
// 区别；0 是合法因子值。gte 只要求 Min，lte 只要求 Max，
// between 同时要求 Min/Max 且 Min <= Max。
type FactorFilterSpec struct {
	Kind     string   `json:"kind"`
	Days     int      `json:"days"`
	Operator string   `json:"operator"` // gte | lte | between
	Min      *float64 `json:"min,omitempty"`
	Max      *float64 `json:"max,omitempty"`
}

// ExitSpec 卖出规则：复用持有天数、止盈、止损。
// TakeProfit/StopLoss 为原始比例（0.10=10%），0=不启用。
type ExitSpec struct {
	HoldingDays int     `json:"holdingDays"`
	TakeProfit  float64 `json:"takeProfit"` // 原始比例，0.10 = 10%
	StopLoss    float64 `json:"stopLoss"`   // 原始比例
}

// StrategyRunSpec 运行范围：字段与 RunConfig 对应，转换后由其校验。
type StrategyRunSpec struct {
	StartYear   int      `json:"startYear"`
	EndYear     int      `json:"endYear"`
	SampleMode  string   `json:"sampleMode"`            // all|random|codes
	SampleSize  int      `json:"sampleSize,omitempty"`  // random 模式
	SampleCodes []string `json:"sampleCodes,omitempty"` // codes 模式
}

// StrategySpec 简单策略配置快照（API 提交与报告保存共用）。
// FactorFilters 1～4 项，全部以 AND 追加到基础 Buyer。
type StrategySpec struct {
	Version       int                `json:"version"`
	Name          string             `json:"name"`
	BasePresetID  string             `json:"basePresetId"`
	FactorFilters []FactorFilterSpec `json:"factorFilters"`
	Exit          ExitSpec           `json:"exit"`
	Run           StrategyRunSpec    `json:"run"`
}

// Validate 声明式合同校验（不含运行范围与卖出规则，那由 RunConfig 负责）。
func (s StrategySpec) Validate() error {
	if s.Version != strategySpecVersion {
		return fmt.Errorf("配置版本无效: %d（应为 %d）", s.Version, strategySpecVersion)
	}
	if name := strings.TrimSpace(s.Name); name != "" && utf8.RuneCountInString(name) > 80 {
		return fmt.Errorf("策略名称过长: %d 个字符（上限 80）", utf8.RuneCountInString(name))
	}
	if _, err := BuildPresetBuyer(s.BasePresetID); err != nil {
		return err
	}
	return s.validateFilters()
}

// validateFilters 因子过滤声明校验：1～4 项（2026-09-16 由 2～4 放宽，
// 用户裁决）、kind 可注册、days>0、
// kind+days 不重复、operator 枚举、阈值有限且区间不反向。
func (s StrategySpec) validateFilters() error {
	n := len(s.FactorFilters)
	if n < 1 || n > 4 {
		return fmt.Errorf("因子过滤条件需要 1～4 项，当前 %d 项", n)
	}
	seen := make(map[string]bool, n)
	for i := range s.FactorFilters {
		ft := &s.FactorFilters[i]
		if err := ft.validate(); err != nil {
			return fmt.Errorf("第 %d 项因子过滤: %w", i+1, err)
		}
		key := fmt.Sprintf("%s:%d", ft.Kind, ft.Days)
		if seen[key] {
			return fmt.Errorf("第 %d 项因子过滤与之前条件重复: %s（相同 kind+days 只能出现一次）", i+1, key)
		}
		seen[key] = true
	}
	return nil
}

// validate 单项过滤校验：kind 可注册、days>0、operator 枚举、
// 阈值有限且区间不反向。
func (ft *FactorFilterSpec) validate() error {
	if f.Build(ft.Kind, ft.Days) == nil {
		return fmt.Errorf("未知因子类型: %s", ft.Kind)
	}
	if ft.Days <= 0 {
		return fmt.Errorf("回看天数无效: %d（应为正整数）", ft.Days)
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

// RunConfig 字段转换（不做业务校验）。
// TakeProfit/StopLoss 按原值传递（0.10=10%），不二次换算。
func (s StrategySpec) RunConfig() RunConfig {
	return RunConfig{
		StartYear:   s.Run.StartYear,
		EndYear:     s.Run.EndYear,
		SampleMode:  s.Run.SampleMode,
		SampleSize:  s.Run.SampleSize,
		SampleCodes: s.Run.SampleCodes,
		HoldingDays: s.Exit.HoldingDays,
		TakeProfit:  s.Exit.TakeProfit,
		StopLoss:    s.Exit.StopLoss,
		ScriptName:  "simple",
	}
}

// DisplayName 报告与响应使用的策略名称：trim 后非空用原值，
// 否则由后端生成可读名称（预设名 + 条件数）。
func (s StrategySpec) DisplayName() string {
	if name := strings.TrimSpace(s.Name); name != "" {
		return name
	}
	return fmt.Sprintf("%s · %d 条因子过滤", s.presetName(), len(s.FactorFilters))
}

// presetName 预设展示名；未知 ID 回退为 ID 本身。
func (s StrategySpec) presetName() string {
	for _, p := range PresetInfos() {
		if p.ID == s.BasePresetID {
			return p.Name
		}
	}
	return s.BasePresetID
}

// Variants 构建报告对比合同约定的 core.Variant 序列：
// 基准 · 预设名 → 单条件 1..N（与 FactorFilters 顺序一一对应，
// 只追加单个过滤用于解释）→ 组合增强 · 预设名 · N 个条件
// （And{基础, filter1..filterN}）。N≥2 时共 N+2 个；N=1 时组合增强
// 与单条件 1 完全等价，不再重复生成，共 N+1=2 个（省一次回测），
// 对照摘要的组合字段由 buildComparison 指向单条件变体。
// 调用前必须先通过 Validate。
func (s StrategySpec) Variants() ([]core.Variant, error) {
	base, err := BuildPresetBuyer(s.BasePresetID)
	if err != nil {
		return nil, err
	}
	presetName := s.presetName()
	filters := make([]sb.A因子过滤, 0, len(s.FactorFilters))
	descs := make([]string, 0, len(s.FactorFilters))
	for i := range s.FactorFilters {
		flt, err := s.FactorFilters[i].build()
		if err != nil {
			return nil, err
		}
		filters = append(filters, flt)
		descs = append(descs, s.FactorFilters[i].describe())
	}
	vs := make([]core.Variant, 0, len(s.FactorFilters)+2)
	vs = append(vs, core.Variant{Name: "基准 · " + presetName, Buyer: base})
	for i := range filters {
		vs = append(vs, core.Variant{
			Name:  fmt.Sprintf("单条件 %d · %s", i+1, descs[i]),
			Buyer: sb.And{base, filters[i]},
		})
	}
	if len(s.FactorFilters) == 1 {
		// 单条件 1 = And{基础, filter1}，即组合增强本身，不再重复跑
		return vs, nil
	}
	combined := make(sb.And, 0, len(filters)+1)
	combined = append(combined, base)
	combined = append(combined, toBuyers(filters)...)
	vs = append(vs, core.Variant{
		Name:  "组合增强 · " + presetName + " · " + strings.Join(descs, "、"),
		Buyer: combined,
	})
	return vs, nil
}

// build 把操作符映射为 A因子过滤：gte→[min,+∞)、lte→(-∞,max]、
// between→[min,max]；单边用 ±1e9（0 是合法因子值，不用 0 表示无界）。
func (ft *FactorFilterSpec) build() (sb.A因子过滤, error) {
	fct := f.Build(ft.Kind, ft.Days)
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

// describe 单条件的人类可读描述（因子名称与区间）。
func (ft *FactorFilterSpec) describe() string {
	name := f.Build(ft.Kind, ft.Days).Name()
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

// toBuyers A因子过滤 值切片 → Buyer 接口切片（And 需要接口元素）。
func toBuyers(filters []sb.A因子过滤) []core.Buyer {
	out := make([]core.Buyer, 0, len(filters))
	for i := range filters {
		out = append(out, filters[i])
	}
	return out
}
