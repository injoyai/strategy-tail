package lab

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
	f "github.com/injoyai/strategy-tail/strategies/factor"
)

// strategy_spec.go 简单模式的声明式策略配置（实施文档 §4.2）。
//
// 职责边界：Validate 只管声明式合同（version/可选 preset/名称/N 项因子过滤）；
// RunConfig 只做字段转换，运行范围与卖出规则的最终合法性留给现有 RunConfig；
// Variants 从可选预设 Builder 取基础 Buyer；未选预设时以 A全部 作为透明
// 对照基准，买入只由逐项 A因子过滤决定。
// 不给 core.Buyer 增加序列化能力，接口实例不写入报告。

// strategySpecVersion 首版配置版本；升级时显式迁移。
const strategySpecVersion = 1

// noBound A因子过滤 的单边无界值（与 A因子过滤 注释一致：不用 0 表示无限制）。
const noBound = 1e9

// FactorFilterSpec 单项因子区间过滤。Min/Max 用指针保留 null 与 0 的
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
// FactorFilters 数量不限（可为 0，仅运行基础策略对照），全部以 AND 追加到基础 Buyer。
type StrategySpec struct {
	Version       int                `json:"version"`
	Name          string             `json:"name"`
	BasePresetID  string             `json:"basePresetId"` // 空值=不使用预设，仅因子条件
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
	if strings.TrimSpace(s.BasePresetID) != "" {
		if _, err := BuildPresetBuyer(s.BasePresetID); err != nil {
			return err
		}
	}
	return s.validateFilters()
}

// validateFilters 因子过滤声明校验：数量不限、可为 0（2026-09-19 用户裁决
// 取消 1～4 项限制）、kind 可注册、days>0、
// kind+days 不重复、operator 枚举、阈值有限且区间不反向。
func (s StrategySpec) validateFilters() error {
	seen := make(map[string]bool, len(s.FactorFilters))
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

// validate 单项过滤校验：kind 可注册、days>0、因子版本 fail-closed、
// operator 枚举、阈值有限且区间不反向。
func (ft *FactorFilterSpec) validate() error {
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
// 否则由后端生成可读名称（预设名或“仅因子条件” + 条件数）。
func (s StrategySpec) DisplayName() string {
	if name := strings.TrimSpace(s.Name); name != "" {
		return name
	}
	if strings.TrimSpace(s.BasePresetID) == "" {
		return fmt.Sprintf("仅因子条件 · %d 条", len(s.FactorFilters))
	}
	return fmt.Sprintf("%s · %d 条因子过滤", s.presetName(), len(s.FactorFilters))
}

// presetName 预设展示名；空值表示不使用预设，未知 ID 回退为 ID 本身。
func (s StrategySpec) presetName() string {
	if strings.TrimSpace(s.BasePresetID) == "" {
		return "全部样本"
	}
	for _, p := range PresetInfos() {
		if p.ID == s.BasePresetID {
			return p.Name
		}
	}
	return s.BasePresetID
}

// baseBuyer 构建简单模式的基础 Buyer。空预设使用 A全部，使“仅因子条件”
// 与既有 And 组合和 Comparison 基准合同保持一致，同时不引入隐式筛选规则。
func (s StrategySpec) baseBuyer() (core.Buyer, error) {
	if strings.TrimSpace(s.BasePresetID) == "" {
		return sb.A全部{}, nil
	}
	return BuildPresetBuyer(s.BasePresetID)
}

// Variants 构建报告对比合同约定的 core.Variant 序列：
// 基准 · 预设名/全部样本 → 单条件 1..N（与 FactorFilters 顺序一一对应，
// 只追加单个过滤用于解释）→ 组合增强 · 预设名 · N 个条件
// （And{基础, filter1..filterN}）。N≥2 时共 N+2 个；N=1 时组合增强
// 与单条件 1 完全等价、N=0 时无单条件，均不重复生成组合增强
// （共 N+1 个，N=0 即只有基准 1 个）。
// 调用前必须先通过 Validate。
func (s StrategySpec) Variants() ([]core.Variant, error) {
	base, err := s.baseBuyer()
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
	if len(s.FactorFilters) <= 1 {
		// N=1：单条件 1 = And{基础, filter1}，即组合增强本身；N=0：无单条件。
		// 两者都不再生成组合增强（N=0 时 vs 只有基准）。
		return vs, nil
	}
	combined := make(sb.And, 0, len(filters)+1)
	combined = append(combined, base)
	combined = append(combined, toBuyers(filters)...)
	combinedName := "组合增强 · " + presetName + " · " + strings.Join(descs, "、")
	if strings.TrimSpace(s.BasePresetID) == "" {
		combinedName = "因子组合 · " + strings.Join(descs, "、")
	}
	vs = append(vs, core.Variant{
		Name:  combinedName,
		Buyer: combined,
	})
	return vs, nil
}

// build 把操作符映射为 A因子过滤：gte→[min,+∞)、lte→(-∞,max]、
// between→[min,max]；单边用 ±1e9（0 是合法因子值，不用 0 表示无界）。
// 构建前重复执行版本校验（fail-closed，不允许绕过 validate 直接 build）。
func (ft *FactorFilterSpec) build() (sb.A因子过滤, error) {
	fct := f.BuildWithData(ft.Kind, ft.Days, common.ResearchData)
	if fct == nil {
		return sb.A因子过滤{}, fmt.Errorf("未知因子类型: %s", ft.Kind)
	}
	if err := ft.validateVersion(); err != nil {
		return sb.A因子过滤{}, err
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

// toBuyers A因子过滤 值切片 → Buyer 接口切片（And 需要接口元素）。
func toBuyers(filters []sb.A因子过滤) []core.Buyer {
	out := make([]core.Buyer, 0, len(filters))
	for i := range filters {
		out = append(out, filters[i])
	}
	return out
}
