package lab

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/lab/builder"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
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

// FactorFilterSpec 保留共享因子条件的序列化契约。
type FactorFilterSpec = builder.FactorFilterSpec

// ExitSpec 卖出规则：复用持有天数、止盈、止损。
// TakeProfit/StopLoss 为原始比例（0.10=10%），0=不启用。
type ExitSpec struct {
	HoldingDays int     `json:"holdingDays"`
	TakeProfit  float64 `json:"takeProfit"` // 原始比例，0.10 = 10%
	StopLoss    float64 `json:"stopLoss"`   // 原始比例
}

// StrategyRunSpec 运行范围：字段与 RunConfig 对应，转换后由其校验。
type StrategyRunSpec struct {
	StartYear   int            `json:"startYear"`
	EndYear     int            `json:"endYear"`
	SampleMode  string         `json:"sampleMode"`            // all|random|codes
	SampleSize  int            `json:"sampleSize,omitempty"`  // random 模式
	SampleCodes []string       `json:"sampleCodes,omitempty"` // codes 模式
	Account     *AccountConfig `json:"account,omitempty"`
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
		if err := ft.Validate(); err != nil {
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
		Account:     s.Run.Account,
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
		flt, err := s.FactorFilters[i].Build()
		if err != nil {
			return nil, err
		}
		filters = append(filters, flt)
		descs = append(descs, s.FactorFilters[i].Describe())
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

// toBuyers A因子过滤 值切片 → Buyer 接口切片（And 需要接口元素）。
func toBuyers(filters []sb.A因子过滤) []core.Buyer {
	out := make([]core.Buyer, 0, len(filters))
	for i := range filters {
		out = append(out, filters[i])
	}
	return out
}

// RecordVariants 在 Variants 合同之上为每个变体接入原因记录（R07/R15）：
// 每个变体独立 RecordContext；包装器只旁路登记评估帧，Buyer 的求值
// 次数、短路顺序与结果和 Variants() 完全一致。基准变体根路径 buy；
// 单条件与组合增强变体为 And 组（buy.children[0]=基础、
// buy.children[i+1]=第 i 项因子过滤）。变体名称逐一取 Variants() 原名。
func (s StrategySpec) RecordVariants() ([]core.Variant, error) {
	plain, err := s.Variants()
	if err != nil {
		return nil, err
	}
	base, err := s.baseBuyer()
	if err != nil {
		return nil, err
	}
	out := make([]core.Variant, 0, len(plain))
	rc := builder.NewRecordContext()
	out = append(out, core.Variant{
		Name:  plain[0].Name,
		Buyer: rc.WrapBuyerTree(base, "buy", builder.RoleBuy, true),
	})
	for i := range s.FactorFilters {
		flt, err := s.FactorFilters[i].Build()
		if err != nil {
			return nil, err
		}
		rc := builder.NewRecordContext()
		members := []core.Buyer{
			rc.WrapBuyerTree(base, "buy.children[0]", builder.RoleBuy, false),
			rc.WrapBuyerRule(flt, builder.NewFactorRule(&s.FactorFilters[i], "buy.children[1]", builder.RoleBuy), false, false),
		}
		out = append(out, core.Variant{
			Name:  plain[i+1].Name,
			Buyer: rc.WrapBuyerGroup(members, "buy", builder.RoleBuy, true),
		})
	}
	if len(s.FactorFilters) <= 1 {
		return out, nil
	}
	rc = builder.NewRecordContext()
	members := make([]core.Buyer, 0, len(s.FactorFilters)+1)
	members = append(members, rc.WrapBuyerTree(base, "buy.children[0]", builder.RoleBuy, false))
	for i := range s.FactorFilters {
		flt, err := s.FactorFilters[i].Build()
		if err != nil {
			return nil, err
		}
		members = append(members, rc.WrapBuyerRule(flt, builder.NewFactorRule(
			&s.FactorFilters[i], fmt.Sprintf("buy.children[%d]", i+1), builder.RoleBuy), false, false))
	}
	out = append(out, core.Variant{
		Name:  plain[len(plain)-1].Name,
		Buyer: rc.WrapBuyerGroup(members, "buy", builder.RoleBuy, true),
	})
	return out, nil
}
