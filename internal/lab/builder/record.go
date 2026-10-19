package builder

import (
	"encoding/json"
	"fmt"
	"unicode"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
	ss "github.com/injoyai/strategy-tail/strategies/sell"
)

// record.go 回测原因记录（设计文档 §3 原因采集）。
//
// 原则：包装器只转发一次求值并顺带登记评估帧，不改变短路顺序、
// 不重复求值、不改变成交结果——记录是纯旁路（需求 R07/R15）。
// 组合子按具体类型 sb.And/sb.Or、ss.And/ss.Or 识别；其余实现一律
// 视为叶子规则（sb.Strategy 包装器实现 Children() 但不是组合器）。

// 角色常量别名（lab 层组装路径用；与 catalog.go 内部常量对齐）。
const (
	RoleBuy        = roleBuy  // 买入条件
	RoleSellSignal = roleSell // 策略卖出信号
	RoleRisk       = roleRisk // 独立风控
	RoleSell       = "sell"   // 卖出侧根容器（默认 Seller 或合并卖出组合）
)

// RuleDesc 单条规则静态描述。Path 为 Builder 节点路径（如 buy.children[1]、
// risks[0]、sellSignal.children[2]）；Op 仅组节点非空（and|or）。
type RuleDesc struct {
	Path   string         `json:"path"`
	Role   string         `json:"role"`
	Name   string         `json:"name"`
	Params map[string]any `json:"params,omitempty"`
	Desc   string         `json:"desc,omitempty"`
	Op     string         `json:"op,omitempty"`
}

// evalFrame 单次求值中的一帧：children 为本次求值实际执行的子帧
// （失败短路/Or 提前返回的分支不会产生帧，天然剪枝）。
type evalFrame struct {
	desc     RuleDesc
	result   bool
	orGroup  bool
	children []evalFrame
}

// RecordContext 收集一个变体的规则描述与最近一次求值帧。
// 回测引擎在单 goroutine 内串行求值同一 Buyer/Seller，无需加锁。
type RecordContext struct {
	rules  []RuleDesc
	seen   map[string]bool
	frames []evalFrame
}

// NewRecordContext 创建空的记录上下文。
func NewRecordContext() *RecordContext {
	return &RecordContext{seen: map[string]bool{}}
}

// register 按路径去重登记规则描述（子规则先于组节点注册，与包装顺序一致）。
func (rc *RecordContext) register(desc RuleDesc) {
	if rc.seen[desc.Path] {
		return
	}
	rc.seen[desc.Path] = true
	rc.rules = append(rc.rules, desc)
}

// Rules 返回规则描述副本（注册顺序）。
func (rc *RecordContext) Rules() []RuleDesc {
	return append([]RuleDesc(nil), rc.rules...)
}

// reset 清空帧栈（根入口每次求值前调用；分钟循环复用同一缓冲）。
func (rc *RecordContext) reset() { rc.frames = rc.frames[:0] }

// RecordedBuyer 携带原因记录的 Buyer 标记接口（接线处据此取上下文）。
type RecordedBuyer interface {
	core.Buyer
	RecordContext() *RecordContext
}

// RecordedSeller 携带原因记录的 Seller 标记接口。
type RecordedSeller interface {
	core.Seller
	RecordContext() *RecordContext
}

// recordBuyer 包装 Buyer：inner 恰好求值一次，求值前后压/弹评估帧。
// root 为根入口：每次 Buy 前清空帧栈。
type recordBuyer struct {
	inner   core.Buyer
	rc      *RecordContext
	desc    RuleDesc
	orGroup bool
	root    bool
}

func (w *recordBuyer) Name() string { return w.inner.Name() }

func (w *recordBuyer) Buy(code string, dks extend.Klines) bool {
	if w.root {
		w.rc.reset()
	}
	mark := len(w.rc.frames)
	result := w.inner.Buy(code, dks)
	// 拷贝子帧，防止后续 append 覆盖底层切片导致历史帧内容漂移。
	kids := append([]evalFrame(nil), w.rc.frames[mark:]...)
	w.rc.frames = append(w.rc.frames[:mark], evalFrame{desc: w.desc, result: result, orGroup: w.orGroup, children: kids})
	return result
}

func (w *recordBuyer) RecordContext() *RecordContext { return w.rc }

// recordSeller 包装 Seller，结构与 recordBuyer 一致。
type recordSeller struct {
	inner   core.Seller
	rc      *RecordContext
	desc    RuleDesc
	orGroup bool
	root    bool
}

func (w *recordSeller) Name() string { return w.inner.Name() }

func (w *recordSeller) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	if w.root {
		w.rc.reset()
	}
	mark := len(w.rc.frames)
	result := w.inner.Sell(code, dks, buy)
	kids := append([]evalFrame(nil), w.rc.frames[mark:]...)
	w.rc.frames = append(w.rc.frames[:mark], evalFrame{desc: w.desc, result: result, orGroup: w.orGroup, children: kids})
	return result
}

func (w *recordSeller) RecordContext() *RecordContext { return w.rc }

// WrapBuyerRule 把单个 Buyer 包装为记录规则（nil 原样返回）。
// orGroup 标记 Or 组（提取成功路径时只取首个为真分支）；root 标记根入口。
func (rc *RecordContext) WrapBuyerRule(inner core.Buyer, desc RuleDesc, orGroup, root bool) core.Buyer {
	if inner == nil {
		return nil
	}
	rc.register(desc)
	return &recordBuyer{inner: inner, rc: rc, desc: desc, orGroup: orGroup, root: root}
}

// WrapSellerRule 把单个 Seller 包装为记录规则（nil 原样返回）。
func (rc *RecordContext) WrapSellerRule(inner core.Seller, desc RuleDesc, orGroup, root bool) core.Seller {
	if inner == nil {
		return nil
	}
	rc.register(desc)
	return &recordSeller{inner: inner, rc: rc, desc: desc, orGroup: orGroup, root: root}
}

// WrapBuyerGroup 把已包装的成员组成 And 根容器（inner = sb.And(members)），
// 供因子组合/预设变体装配记录根；成员求值顺序与组合顺序一致。
func (rc *RecordContext) WrapBuyerGroup(members []core.Buyer, path, role string, root bool) core.Buyer {
	filtered := make(sb.And, 0, len(members))
	for _, m := range members {
		if m != nil {
			filtered = append(filtered, m)
		}
	}
	desc := RuleDesc{Path: path, Role: role, Name: filtered.Name(), Op: "and"}
	rc.register(desc)
	return &recordBuyer{inner: filtered, rc: rc, desc: desc, root: root}
}

// WrapBuyerTree 递归包装已构建的 Buyer 树：And/Or 组节点重建子切片逐层包装
// （子路径 %s.children[%d]），其余实现视为叶子规则。只按具体类型识别组合子。
func (rc *RecordContext) WrapBuyerTree(inner core.Buyer, path, role string, root bool) core.Buyer {
	if inner == nil {
		return nil
	}
	switch node := inner.(type) {
	case sb.And:
		children := make(sb.And, 0, len(node))
		for _, child := range node {
			if child == nil {
				continue
			}
			children = append(children, rc.WrapBuyerTree(child, fmt.Sprintf("%s.children[%d]", path, len(children)), role, false))
		}
		desc := RuleDesc{Path: path, Role: role, Name: node.Name(), Op: "and"}
		rc.register(desc)
		return &recordBuyer{inner: children, rc: rc, desc: desc, root: root}
	case sb.Or:
		children := make(sb.Or, 0, len(node))
		for _, child := range node {
			if child == nil {
				continue
			}
			children = append(children, rc.WrapBuyerTree(child, fmt.Sprintf("%s.children[%d]", path, len(children)), role, false))
		}
		desc := RuleDesc{Path: path, Role: role, Name: node.Name(), Op: "or"}
		rc.register(desc)
		return &recordBuyer{inner: children, rc: rc, desc: desc, orGroup: true, root: root}
	default:
		return rc.WrapBuyerRule(inner, leafBuyerRule(inner, path, role), false, root)
	}
}

// WrapSellerTree 递归包装已构建的 Seller 树（sell.And/sell.Or 无 Children()
// 方法，同样只按具体类型识别）。
func (rc *RecordContext) WrapSellerTree(inner core.Seller, path, role string, root bool) core.Seller {
	if inner == nil {
		return nil
	}
	switch node := inner.(type) {
	case ss.And:
		children := make(ss.And, 0, len(node))
		for _, child := range node {
			if child == nil {
				continue
			}
			children = append(children, rc.WrapSellerTree(child, fmt.Sprintf("%s.children[%d]", path, len(children)), role, false))
		}
		desc := RuleDesc{Path: path, Role: role, Name: node.Name(), Op: "and"}
		rc.register(desc)
		return &recordSeller{inner: children, rc: rc, desc: desc, root: root}
	case ss.Or:
		children := make(ss.Or, 0, len(node))
		for _, child := range node {
			if child == nil {
				continue
			}
			children = append(children, rc.WrapSellerTree(child, fmt.Sprintf("%s.children[%d]", path, len(children)), role, false))
		}
		desc := RuleDesc{Path: path, Role: role, Name: node.Name(), Op: "or"}
		rc.register(desc)
		return &recordSeller{inner: children, rc: rc, desc: desc, orGroup: true, root: root}
	default:
		return rc.WrapSellerRule(inner, leafSellerRule(inner, path, role), false, root)
	}
}

// NewFactorRule 由因子过滤声明构建叶子规则描述：参数直接取声明
// （不对构建出的 A因子过滤 做结构体反射，接口字段不可序列化）。
func NewFactorRule(ft *FactorFilterSpec, path, role string) RuleDesc {
	desc := RuleDesc{
		Path: path,
		Role: role,
		Name: ft.Describe(),
		Params: map[string]any{
			"kind":     ft.Kind,
			"days":     ft.Days,
			"operator": ft.Operator,
		},
	}
	if ft.FactorVersion != 0 {
		desc.Params["factorVersion"] = ft.FactorVersion
	}
	if ft.Min != nil {
		desc.Params["min"] = *ft.Min
	}
	if ft.Max != nil {
		desc.Params["max"] = *ft.Max
	}
	return desc
}

// conditionRule 条件节点规则描述：参数按声明原样拷贝（缺省参数不填目录
// 默认值，与工厂零值语义一致），Desc 取目录条目说明，Name 由调用方填
// 组件实例名。
func conditionRule(n *BuilderNode, path, role string) RuleDesc {
	desc := RuleDesc{Path: path, Role: role, Params: paramsCopy(n.Params)}
	if entry, ok := builderComponent(n.ComponentID); ok {
		desc.Desc = entry.Desc
	}
	return desc
}

// leafBuyerRule 非组合 Buyer 的叶子规则描述（参数取可 JSON 序列化的结构体
// 字段，键首字母转小写；失败或为空则省略）。
func leafBuyerRule(inner core.Buyer, path, role string) RuleDesc {
	desc := RuleDesc{Path: path, Role: role, Name: inner.Name()}
	if p := structParams(inner); p != nil {
		desc.Params = p
	}
	return desc
}

// leafSellerRule 非组合 Seller 的叶子规则描述。
func leafSellerRule(inner core.Seller, path, role string) RuleDesc {
	desc := RuleDesc{Path: path, Role: role, Name: inner.Name()}
	if p := structParams(inner); p != nil {
		desc.Params = p
	}
	return desc
}

// structParams 把结构体字段序列化为参数 map（仅展示用途，不参与求值）；
// 序列化失败或结果为空返回 nil。
func structParams(v any) map[string]any {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, val := range m {
		r := []rune(k)
		r[0] = unicode.ToLower(r[0])
		out[string(r)] = val
	}
	return out
}

// paramsCopy 浅拷贝参数 map，避免记录描述与请求声明共享底层 map；nil 原样。
func paramsCopy(params map[string]any) map[string]any {
	if params == nil {
		return nil
	}
	out := make(map[string]any, len(params))
	for k, v := range params {
		out[k] = v
	}
	return out
}

// ExtractSuccessPath 返回最近一次求值的根到叶命中路径（RuleDesc.Path 序列）。
// 根失败返回 nil；Or 组成功只取首个为真子分支；未执行分支无帧自然剪枝。
// 调用方在真实成交时提取后必须立即拷贝使用——下一次求值会复用帧缓冲。
func (rc *RecordContext) ExtractSuccessPath() []string {
	if len(rc.frames) == 0 {
		return nil
	}
	root := rc.frames[len(rc.frames)-1]
	if !root.result {
		return nil
	}
	var path []string
	var walk func(f evalFrame)
	walk = func(f evalFrame) {
		path = append(path, f.desc.Path)
		if f.orGroup {
			for _, kid := range f.children {
				if kid.result {
					walk(kid)
					break
				}
			}
			return
		}
		for _, kid := range f.children {
			walk(kid)
		}
	}
	walk(root)
	return path
}

// RecordBuyer 构建带原因记录的买入树（错误信息与 Buyer() 一致）。
func (s *BuilderSpec) RecordBuyer(rc *RecordContext) (core.Buyer, error) {
	if s.Buy == nil {
		return nil, fmt.Errorf("买入条件为空")
	}
	return s.Buy.recordBuyerNode(rc, "buy", roleBuy, true)
}

// RecordSeller 构建带原因记录的卖出组合，分组语义与 Seller() 完全一致
// （0→空 Or；1→单规则提升为根入口；N→Or 组合包装为根，风控先求值）。
func (s *BuilderSpec) RecordSeller(rc *RecordContext) (core.Seller, error) {
	sellers := []core.Seller(nil)
	for i, risk := range s.Risks {
		seller, err := risk.recordSellerNode(rc, fmt.Sprintf("risks[%d]", i), roleRisk, false)
		if err != nil {
			return nil, fmt.Errorf("独立风控: %w", err)
		}
		sellers = append(sellers, seller)
	}
	if s.SellSignal != nil {
		seller, err := s.SellSignal.recordSellerNode(rc, "sellSignal", roleSell, false)
		if err != nil {
			return nil, fmt.Errorf("策略卖出信号: %w", err)
		}
		sellers = append(sellers, seller)
	}
	switch len(sellers) {
	case 0:
		return ss.Or{}, nil
	case 1:
		// 单规则直接提升为根入口，不双重包装（与 Seller() 的展平行为对齐）。
		if w, ok := sellers[0].(*recordSeller); ok {
			w.root = true
		}
		return sellers[0], nil
	default:
		desc := RuleDesc{Path: "sell", Role: RoleSell, Name: "任一满足", Op: "or"}
		rc.register(desc)
		return &recordSeller{inner: ss.Or(sellers), rc: rc, desc: desc, orGroup: true, root: true}, nil
	}
}

// recordBuyerNode 复刻 BuilderNode.Buyer 的构建语义，逐层插入记录包装。
func (n *BuilderNode) recordBuyerNode(rc *RecordContext, path, role string, root bool) (core.Buyer, error) {
	if n.Type == "factor" {
		if n.Factor == nil {
			return nil, fmt.Errorf("因子节点缺少因子定义")
		}
		buyer, err := n.Factor.Build()
		if err != nil {
			return nil, err
		}
		return rc.WrapBuyerRule(buyer, NewFactorRule(n.Factor, path, role), false, root), nil
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
		rule := conditionRule(n, path, role)
		rule.Name = buyer.Name()
		return rc.WrapBuyerRule(buyer, rule, false, root), nil
	}
	children := make(sb.And, 0, len(n.Children))
	for i, child := range n.Children {
		b, err := child.recordBuyerNode(rc, fmt.Sprintf("%s.children[%d]", path, i), role, false)
		if err != nil {
			return nil, err
		}
		children = append(children, b)
	}
	if n.Op == "or" {
		orChildren := sb.Or(children)
		desc := RuleDesc{Path: path, Role: role, Name: orChildren.Name(), Op: "or"}
		rc.register(desc)
		return &recordBuyer{inner: orChildren, rc: rc, desc: desc, orGroup: true, root: root}, nil
	}
	desc := RuleDesc{Path: path, Role: role, Name: children.Name(), Op: "and"}
	rc.register(desc)
	return &recordBuyer{inner: children, rc: rc, desc: desc, root: root}, nil
}

// recordSellerNode 复刻 BuilderNode.Seller 的构建语义，逐层插入记录包装。
func (n *BuilderNode) recordSellerNode(rc *RecordContext, path, role string, root bool) (core.Seller, error) {
	if n.Type == "factor" {
		if n.Factor == nil {
			return nil, fmt.Errorf("因子节点缺少因子定义")
		}
		filter, err := n.Factor.Build()
		if err != nil {
			return nil, err
		}
		return rc.WrapSellerRule(ss.A因子过滤{A因子过滤: filter}, NewFactorRule(n.Factor, path, role), false, root), nil
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
		rule := conditionRule(n, path, role)
		rule.Name = seller.Name()
		return rc.WrapSellerRule(seller, rule, false, root), nil
	}
	children := make(ss.And, 0, len(n.Children))
	for i, child := range n.Children {
		s, err := child.recordSellerNode(rc, fmt.Sprintf("%s.children[%d]", path, i), role, false)
		if err != nil {
			return nil, err
		}
		children = append(children, s)
	}
	if n.Op == "or" {
		orChildren := ss.Or(children)
		desc := RuleDesc{Path: path, Role: role, Name: orChildren.Name(), Op: "or"}
		rc.register(desc)
		return &recordSeller{inner: orChildren, rc: rc, desc: desc, orGroup: true, root: root}, nil
	}
	desc := RuleDesc{Path: path, Role: role, Name: children.Name(), Op: "and"}
	rc.register(desc)
	return &recordSeller{inner: children, rc: rc, desc: desc, root: root}, nil
}
