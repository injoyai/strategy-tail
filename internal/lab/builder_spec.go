package lab

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/injoyai/strategy-tail/core"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
	ss "github.com/injoyai/strategy-tail/strategies/sell"
)

// builder_spec.go 搭建器条件树定义、校验与构建（设计文档 §4）。
//
// 买入树复用 buy.And/buy.Or，策略卖出信号树复用 sell.And/sell.Or，
// 独立风控与信号组合为 sell.Or{独立风控..., 策略卖出信号}（风控先求值）。
// 空分组任何场景都拒绝；未知组件/角色不匹配/非法参数显式报错。

// 条件树规模边界（§6：写入与回测入口验证规则树规模及深度）。
const (
	builderMaxDepth = 8  // 分组嵌套最大深度
	builderMaxNodes = 64 // 整棵树（含风控列表）最大节点数
)

// BuilderNode 条件树节点。
// type=group 时 Op（and|or）+ Children；type=condition 时 ComponentID + Params。
// type=factor 时 Factor 复用因子组合的声明；仅用于买入与策略卖出信号。
type BuilderNode struct {
	Type        string            `json:"type"`
	Op          string            `json:"op,omitempty"`
	ComponentID string            `json:"componentId,omitempty"`
	Params      map[string]any    `json:"params,omitempty"`
	Factor      *FactorFilterSpec `json:"factor,omitempty"`
	Children    []*BuilderNode    `json:"children,omitempty"`
}

// BuilderSpec 搭建器策略定义（Lab 自身版本化契约，独立于简单模式 StrategySpec）。
type BuilderSpec struct {
	Version    int            `json:"version"`
	Name       string         `json:"name"`
	Buy        *BuilderNode   `json:"buy,omitempty"`
	SellSignal *BuilderNode   `json:"sellSignal,omitempty"`
	HoldToEnd  bool           `json:"holdToEnd,omitempty"`
	Risks      []*BuilderNode `json:"risks,omitempty"`
}

// validateName 策略名称检查（与简单模式 StrategySpec 一致的长度语义）。
func validateBuilderName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("策略名称不能为空")
	}
	if len([]rune(name)) > 80 {
		return fmt.Errorf("策略名称不能超过 80 字")
	}
	return nil
}

// countNodes 统计节点数并校验深度（返回节点总数）。
func countNodes(n *BuilderNode, depth int) (int, error) {
	if n == nil {
		return 0, nil
	}
	if depth > builderMaxDepth {
		return 0, fmt.Errorf("条件分组嵌套超过 %d 层", builderMaxDepth)
	}
	count := 1
	for _, child := range n.Children {
		c, err := countNodes(child, depth+1)
		if err != nil {
			return 0, err
		}
		count += c
	}
	return count, nil
}

// validateNode 校验单个节点（role 为该节点所在位置要求的组件角色）。
func validateNode(n *BuilderNode, role string, depth int, counter *int) error {
	if n == nil {
		return fmt.Errorf("条件节点不能为空")
	}
	*counter++
	if *counter > builderMaxNodes {
		return fmt.Errorf("条件节点数超过 %d，请拆分策略", builderMaxNodes)
	}
	switch n.Type {
	case "group":
		if n.Factor != nil {
			return fmt.Errorf("分组节点不应携带因子定义")
		}
		if n.Op != "and" && n.Op != "or" {
			return fmt.Errorf("分组组合方式无效: %q（须为 and 或 or）", n.Op)
		}
		if len(n.Children) == 0 {
			return fmt.Errorf("存在空条件分组：空分组不能作为有效规则，请添加条件或删除该分组")
		}
		if depth >= builderMaxDepth {
			return fmt.Errorf("条件分组嵌套超过 %d 层", builderMaxDepth)
		}
		if n.ComponentID != "" {
			return fmt.Errorf("分组节点不应携带组件标识")
		}
		for _, child := range n.Children {
			if err := validateNode(child, role, depth+1, counter); err != nil {
				return err
			}
		}
	case "condition":
		if n.Factor != nil {
			return fmt.Errorf("组件条件不应携带因子定义")
		}
		if n.ComponentID == "" {
			return fmt.Errorf("条件节点缺少组件标识")
		}
		if len(n.Children) != 0 {
			return fmt.Errorf("条件节点不应包含子节点")
		}
		entry, ok := builderComponent(n.ComponentID)
		if !ok {
			return fmt.Errorf("未知组件: %s", n.ComponentID)
		}
		if entry.Role != role {
			return fmt.Errorf("组件 %s 的角色是 %s，不能放在%s中",
				n.ComponentID, builderRoleName(entry.Role), builderRoleName(role))
		}
		if err := validateParams(entry, n.Params); err != nil {
			return err
		}
	case "factor":
		if role != roleBuy && role != roleSell {
			return fmt.Errorf("因子条件仅用于买入或策略卖出信号，不能作为独立风控")
		}
		if n.ComponentID != "" || len(n.Params) != 0 || n.Op != "" || len(n.Children) != 0 {
			return fmt.Errorf("因子节点不应携带组件、参数或分组字段")
		}
		if n.Factor == nil {
			return fmt.Errorf("因子节点缺少因子定义")
		}
		if err := n.Factor.validate(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("条件节点类型无效: %q", n.Type)
	}
	return nil
}

// builderRoleName 角色的中文名（错误提示用）。
func builderRoleName(role string) string {
	switch role {
	case roleBuy:
		return "买入条件"
	case roleSell:
		return "策略卖出信号"
	case roleRisk:
		return "独立风控"
	}
	return role
}

// validateParams 按 ParamDef 校验参数：未知键拒绝，类型/范围/格式不符报错。
func validateParams(entry ComponentEntry, params map[string]any) error {
	defs := map[string]ParamDef{}
	for _, def := range entry.Params {
		defs[def.Key] = def
	}
	for key := range params {
		if _, ok := defs[key]; !ok {
			return fmt.Errorf("组件 %s 不支持参数 %s", entry.ID, key)
		}
	}
	for _, def := range entry.Params {
		v, present := params[def.Key]
		if !present || v == nil {
			continue // 缺省走组件零值语义（0=不限/组件默认）
		}
		switch def.Type {
		case "int":
			f, ok := v.(float64)
			if !ok || f != float64(int(f)) {
				return fmt.Errorf("组件 %s 参数 %s 须为整数", entry.ID, def.Key)
			}
			if err := checkParamRange(entry, def, f); err != nil {
				return err
			}
		case "float", "price", "ratio", "percent":
			f, ok := v.(float64)
			if !ok {
				return fmt.Errorf("组件 %s 参数 %s 须为数字", entry.ID, def.Key)
			}
			if err := checkParamRange(entry, def, f); err != nil {
				return err
			}
		case "text":
			s, ok := v.(string)
			if !ok {
				return fmt.Errorf("组件 %s 参数 %s 须为文本", entry.ID, def.Key)
			}
			if def.Pattern != "" && !regexp.MustCompile(def.Pattern).MatchString(s) {
				return fmt.Errorf("组件 %s 参数 %s 格式无效（应为 %s）", entry.ID, def.Key, def.Desc)
			}
		case "intList":
			arr, ok := v.([]any)
			if !ok {
				return fmt.Errorf("组件 %s 参数 %s 须为数组", entry.ID, def.Key)
			}
			if len(arr) == 1 || len(arr) > builderMaxNodes {
				return fmt.Errorf("组件 %s 参数 %s 须包含 2～%d 个周期，或留空使用默认值", entry.ID, def.Key, builderMaxNodes)
			}
			for _, item := range arr {
				f, ok := item.(float64)
				if !ok || f != float64(int(f)) {
					return fmt.Errorf("组件 %s 参数 %s 须为整数数组", entry.ID, def.Key)
				}
				if err := checkParamRange(entry, def, f); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("组件 %s 参数 %s 类型未知", entry.ID, def.Key)
		}
	}
	return nil
}

// checkParamRange 数值范围检查（Min/Max 为界面单位，ratio 参数即百分数）。
func checkParamRange(entry ComponentEntry, def ParamDef, v float64) error {
	if def.Min != nil && v < *def.Min {
		return fmt.Errorf("组件 %s 参数 %s 不能小于 %g", entry.ID, def.Key, *def.Min)
	}
	if def.Max != nil && v > *def.Max {
		return fmt.Errorf("组件 %s 参数 %s 不能大于 %g", entry.ID, def.Key, *def.Max)
	}
	return nil
}

// Validate 校验整棵定义（mode=run 要求买入树与卖出侧非空；mode=save 允许空树草稿）。
func (s *BuilderSpec) validate(mode string) error {
	if s.Version != builderSpecV1 {
		return fmt.Errorf("不支持的策略定义版本: %d（当前版本 %d）", s.Version, builderSpecV1)
	}
	if err := validateBuilderName(s.Name); err != nil {
		return err
	}
	nodes := 0
	if s.Buy != nil {
		if err := validateNode(s.Buy, roleBuy, 0, &nodes); err != nil {
			return fmt.Errorf("买入条件: %w", err)
		}
	} else if mode == "run" {
		return fmt.Errorf("买入条件为空：请至少添加一个买入条件（或显式选择「全部样本买入」）")
	}
	if s.SellSignal != nil {
		if err := validateNode(s.SellSignal, roleSell, 0, &nodes); err != nil {
			return fmt.Errorf("策略卖出信号: %w", err)
		}
	}
	for _, risk := range s.Risks {
		if risk != nil && risk.Type != "condition" {
			return fmt.Errorf("独立风控只接受单个风控条件，请将每条规则分别添加到风控列表")
		}
		if err := validateNode(risk, roleRisk, 0, &nodes); err != nil {
			return fmt.Errorf("独立风控: %w", err)
		}
	}
	if mode == "run" {
		// 卖出侧：信号树 / 独立风控 / 显式持有至期末，三者必有其一。
		if s.SellSignal == nil && len(s.Risks) == 0 && !s.HoldToEnd {
			return fmt.Errorf("卖出侧为空：请添加策略卖出信号、独立风控，或显式选择「持有至期末」")
		}
	}
	return nil
}

// Buyer 构建买入条件树（spec 未通过 validate 时可能返回错误）。
func (s *BuilderSpec) Buyer() (core.Buyer, error) {
	if s.Buy == nil {
		return nil, fmt.Errorf("买入条件为空")
	}
	return s.Buy.buyer()
}

// Seller 构建卖出组合（§6.3）：有独立风控时为 sell.Or{独立风控..., 策略卖出信号}
// （风控先求值）；单规则时直接返回该规则，避免无谓嵌套（MACD 模板与
// macd_backtest seller() 构造逐字段等价依赖此行为）。
// 无任何卖出规则（显式持有至期末）返回空 ss.Or{}——引擎恒 false，
// 期末卖出由核心引擎以 Virtual 标记估值，不伪装成真实成交。
func (s *BuilderSpec) Seller() (core.Seller, error) {
	sellers := []core.Seller(nil)
	for _, risk := range s.Risks {
		seller, err := risk.seller()
		if err != nil {
			return nil, fmt.Errorf("独立风控: %w", err)
		}
		sellers = append(sellers, seller)
	}
	if s.SellSignal != nil {
		seller, err := s.SellSignal.seller()
		if err != nil {
			return nil, fmt.Errorf("策略卖出信号: %w", err)
		}
		sellers = append(sellers, seller)
	}
	switch len(sellers) {
	case 0:
		return ss.Or{}, nil
	case 1:
		return sellers[0], nil
	default:
		return ss.Or(sellers), nil
	}
}

// buyer 递归构建买入树。
func (n *BuilderNode) buyer() (core.Buyer, error) {
	if n.Type == "factor" {
		if n.Factor == nil {
			return nil, fmt.Errorf("因子节点缺少因子定义")
		}
		return n.Factor.build()
	}
	if n.Type == "condition" {
		factory, ok := buyFactories[n.ComponentID]
		if !ok {
			return nil, fmt.Errorf("组件 %s 未接入买入工厂", n.ComponentID)
		}
		buyer, err := factory(n.Params)
		if err != nil {
			return nil, fmt.Errorf("组件 %s: %w", n.ComponentID, err)
		}
		return buyer, nil
	}
	children := make(sb.And, 0, len(n.Children))
	for _, child := range n.Children {
		b, err := child.buyer()
		if err != nil {
			return nil, err
		}
		children = append(children, b)
	}
	if n.Op == "or" {
		return sb.Or(children), nil
	}
	return children, nil
}

// seller 递归构建卖出树。
func (n *BuilderNode) seller() (core.Seller, error) {
	if n.Type == "factor" {
		if n.Factor == nil {
			return nil, fmt.Errorf("因子节点缺少因子定义")
		}
		filter, err := n.Factor.build()
		if err != nil {
			return nil, err
		}
		return ss.A因子过滤{A因子过滤: filter}, nil
	}
	if n.Type == "condition" {
		factory, ok := sellFactories[n.ComponentID]
		if !ok {
			return nil, fmt.Errorf("组件 %s 未接入卖出工厂", n.ComponentID)
		}
		seller, err := factory(n.Params)
		if err != nil {
			return nil, fmt.Errorf("组件 %s: %w", n.ComponentID, err)
		}
		return seller, nil
	}
	children := make(ss.And, 0, len(n.Children))
	for _, child := range n.Children {
		s, err := child.seller()
		if err != nil {
			return nil, err
		}
		children = append(children, s)
	}
	if n.Op == "or" {
		return ss.Or(children), nil
	}
	return children, nil
}
