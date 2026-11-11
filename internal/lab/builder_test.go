package lab

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/injoyai/strategy-tail/core"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
	ss "github.com/injoyai/strategy-tail/strategies/sell"
)

// builder_test.go 搭建器后端测试（设计文档 §8 验收场景）：
// 目录/工厂一致性、条件树校验（角色/深度/节点数/参数）、组合语义
//（风控先求值、单规则展平、空 Or 恒 false）、策略库存储与 REST API 契约。

// ---- 测试辅助 ----

// bCond 构造条件节点（测试内简写）。
func bCond(id string, params map[string]any) *BuilderNode {
	return &BuilderNode{Type: "condition", ComponentID: id, Params: params}
}

// bGroup 构造分组节点。
func bGroup(op string, children ...*BuilderNode) *BuilderNode {
	return &BuilderNode{Type: "group", Op: op, Children: children}
}

// baseSpec 合法最小 spec：全部样本买入 + MACD 反转卖出。
func baseSpec() BuilderSpec {
	return BuilderSpec{
		Version:    builderSpecV1,
		Name:       "测试策略",
		Buy:        bGroup("and", bCond("buy_all", nil)),
		SellSignal: bGroup("or", bCond("sell_macd_reversal", map[string]any{"lookback": 10.0})),
	}
}

// nestedGroups 构造 depth 层嵌套 and 分组，最内层放单个条件。
func nestedGroups(depth int) *BuilderNode {
	leaf := bCond("buy_all", nil)
	node := leaf
	for i := 0; i < depth; i++ {
		node = bGroup("and", node)
	}
	return node
}

// ---- 目录与工厂一致性 ----

// ---- 条件树校验 ----

// TestBuilderSpecValidateMode 运行/保存两种校验模式（验收场景：空侧拒绝、草稿允许）。
func TestBuilderSpecValidateMode(t *testing.T) {
	spec := baseSpec()
	if err := spec.Validate("run"); err != nil {
		t.Fatalf("合法 spec run 校验失败: %v", err)
	}
	if err := spec.Validate("save"); err != nil {
		t.Fatalf("合法 spec save 校验失败: %v", err)
	}

	// 版本与名称
	bad := baseSpec()
	bad.Version = 2
	if err := bad.Validate("run"); err == nil || !strings.Contains(err.Error(), "版本") {
		t.Fatalf("未知版本应被拒绝: %v", err)
	}
	bad = baseSpec()
	bad.Name = " "
	if err := bad.Validate("run"); err == nil || !strings.Contains(err.Error(), "名称") {
		t.Fatalf("空名称应被拒绝: %v", err)
	}

	// 空买入：run 拒绝、save 允许（草稿）
	bad = baseSpec()
	bad.Buy = nil
	if err := bad.Validate("run"); err == nil || !strings.Contains(err.Error(), "买入条件为空") {
		t.Fatalf("空买入 run 应拒绝: %v", err)
	}
	if err := bad.Validate("save"); err != nil {
		t.Fatalf("空买入 save（草稿）应允许: %v", err)
	}

	// 卖出侧全空：run 拒绝、save 允许
	bad = baseSpec()
	bad.SellSignal = nil
	if err := bad.Validate("run"); err == nil || !strings.Contains(err.Error(), "卖出侧为空") {
		t.Fatalf("卖出侧全空 run 应拒绝: %v", err)
	}
	if err := bad.Validate("save"); err != nil {
		t.Fatalf("卖出侧全空 save 应允许: %v", err)
	}

	// 显式持有至期末：run 通过
	ok := baseSpec()
	ok.SellSignal = nil
	ok.HoldToEnd = true
	if err := ok.Validate("run"); err != nil {
		t.Fatalf("持有至期末 run 应通过: %v", err)
	}

	// 仅独立风控：run 通过
	ok = baseSpec()
	ok.SellSignal = nil
	ok.Risks = []*BuilderNode{bCond("risk_fixed_stop", map[string]any{"pct": 5.0})}
	if err := ok.Validate("run"); err != nil {
		t.Fatalf("仅独立风控 run 应通过: %v", err)
	}
}

// TestBuilderSpecValidateTree 结构与角色校验（验收场景 5：风控组件进卖出信号树被拒）。
func TestBuilderSpecValidateTree(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*BuilderSpec)
		wantErr string
	}{
		{"空分组", func(s *BuilderSpec) { s.Buy = bGroup("and") }, "空条件分组"},
		{"分组缺 op", func(s *BuilderSpec) {
			s.Buy = &BuilderNode{Type: "group", Children: []*BuilderNode{bCond("buy_all", nil)}}
		}, "组合方式无效"},
		{"分组带组件", func(s *BuilderSpec) {
			g := bGroup("and", bCond("buy_all", nil))
			g.ComponentID = "buy_all"
			s.Buy = g
		}, "不应携带组件标识"},
		{"条件带子节点", func(s *BuilderSpec) {
			c := bCond("buy_all", nil)
			c.Children = []*BuilderNode{bCond("buy_etf", nil)}
			s.Buy = bGroup("and", c)
		}, "不应包含子节点"},
		{"未知节点类型", func(s *BuilderSpec) { s.Buy = &BuilderNode{Type: "xor"} }, "类型无效"},
		{"未知组件", func(s *BuilderSpec) { s.Buy = bGroup("and", bCond("buy_nope", nil)) }, "未知组件"},
		{"风控组件进买入树", func(s *BuilderSpec) { s.Buy = bGroup("and", bCond("risk_fixed_stop", nil)) }, "独立风控"},
		{"风控组件进卖出信号树", func(s *BuilderSpec) {
			s.SellSignal = bGroup("or", bCond("risk_takeprofit_stoploss", nil))
		}, "独立风控"},
		{"卖出组件进买入树", func(s *BuilderSpec) { s.Buy = bGroup("and", bCond("sell_rsi_overbought", nil)) }, "策略卖出信号"},
		{"未知参数键", func(s *BuilderSpec) {
			s.Buy = bGroup("and", bCond("buy_all", map[string]any{"bogus": 1.0}))
		}, "不支持参数"},
		{"整数参数传小数", func(s *BuilderSpec) {
			s.Buy = bGroup("and", bCond("buy_price_above_ma", map[string]any{"period": 5.5}))
		}, "须为整数"},
		{"参数超上限", func(s *BuilderSpec) {
			s.Buy = bGroup("and", bCond("buy_ma_up", map[string]any{"period": 30.0, "minSlope": 20.0}))
		}, "不能大于"},
		{"参数低于下限", func(s *BuilderSpec) {
			s.Buy = bGroup("and", bCond("buy_price_above_ma", map[string]any{"period": 0.0}))
		}, "不能小于"},
		{"文本参数格式不符", func(s *BuilderSpec) {
			s.Risks = []*BuilderNode{bCond("risk_holding_days_tail", map[string]any{"days": 1.0, "time": "25:00:00"})}
		}, "格式无效"},
		{"整数列表参数类型不符", func(s *BuilderSpec) {
			s.Buy = bGroup("and", bCond("buy_ma_bull", map[string]any{"periods": "5,10"}))
		}, "须为数组"},
		{"深度超限", func(s *BuilderSpec) { s.Buy = nestedGroups(9) }, "嵌套超过"},
		{"节点数超限", func(s *BuilderSpec) {
			children := make([]*BuilderNode, 0, 64)
			for i := 0; i < 64; i++ {
				children = append(children, bCond("buy_all", nil))
			}
			s.Buy = bGroup("and", children...) // 1 + 64 = 65 节点
		}, "节点数超过"},
	}
	for _, tc := range cases {
		spec := baseSpec()
		tc.mutate(&spec)
		err := spec.Validate("run")
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Fatalf("%s: 期望错误含 %q，实际: %v", tc.name, tc.wantErr, err)
		}
	}

	// 边界：8 层嵌套与 64 节点恰好通过
	ok := baseSpec()
	ok.Buy = nestedGroups(8)
	if err := ok.Validate("run"); err != nil {
		t.Fatalf("8 层嵌套应通过: %v", err)
	}
	children := make([]*BuilderNode, 0, 63)
	for i := 0; i < 63; i++ {
		children = append(children, bCond("buy_all", nil))
	}
	ok = baseSpec()
	ok.SellSignal = nil // 节点计数跨整棵树（含卖出侧），用持有至期末隔离
	ok.HoldToEnd = true
	ok.Buy = bGroup("and", children...) // 1 + 63 = 64 节点
	if err := ok.Validate("run"); err != nil {
		t.Fatalf("64 节点应通过: %v", err)
	}
}

// ---- 组合语义 ----

// TestBuilderCombinatorSemantics 分组 → And/Or 组件组合；风控先于信号（验收场景 3/4）。
func TestBuilderCombinatorSemantics(t *testing.T) {
	// and/or 嵌套结构
	spec := BuilderSpec{
		Version: builderSpecV1, Name: "组合",
		Buy: bGroup("and",
			bCond("buy_price", map[string]any{"max": 120.0}),
			bGroup("or", bCond("buy_board_cyb", nil), bCond("buy_board_kcb", nil)),
		),
		SellSignal: bGroup("or", bCond("sell_macd_reversal", map[string]any{"lookback": 10.0})),
	}
	if err := spec.Validate("run"); err != nil {
		t.Fatalf("组合 spec 校验失败: %v", err)
	}
	buyer, err := spec.Buyer()
	if err != nil {
		t.Fatal(err)
	}
	and, ok := buyer.(sb.And)
	if !ok || len(and) != 2 {
		t.Fatalf("顶层 and 组应为 sb.And 且 2 个子条件: %#v", buyer)
	}
	if _, ok := and[1].(sb.Or); !ok {
		t.Fatalf("嵌套 or 组应为 sb.Or: %#v", and[1])
	}

	// 风控先求值：sell.Or{风控..., 策略卖出信号}
	risk := BuilderSpec{
		Version: builderSpecV1, Name: "风控组合",
		Buy:        bGroup("and", bCond("buy_all", nil)),
		SellSignal: bGroup("or", bCond("sell_macd_reversal", map[string]any{"lookback": 10.0})),
		Risks: []*BuilderNode{
			bCond("risk_fixed_stop", map[string]any{"pct": 5.0}),
			bCond("risk_holding_days", map[string]any{"days": 5.0}),
		},
	}
	seller, err := risk.Seller()
	if err != nil {
		t.Fatal(err)
	}
	or, ok := seller.(ss.Or)
	if !ok || len(or) != 3 {
		t.Fatalf("风控+信号应为 ss.Or 且 3 条: %#v", seller)
	}
	if _, ok := or[0].(ss.A固定止损); !ok {
		t.Fatalf("风控应排在最前: %#v", or[0])
	}
	if _, ok := or[2].(ss.Or); !ok {
		t.Fatalf("策略卖出信号应排最后: %#v", or[2])
	}

	// 单规则展平：仅信号 → 信号树自身（MACD 模板等价依赖）
	onlySignal := baseSpec()
	sp, err := onlySignal.Seller()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := sp.(ss.Or); !ok {
		t.Fatalf("单卖出信号应展平为该信号树自身: %#v", sp)
	}

	// 仅持有至期末 → 空 Or 恒 false
	hold := baseSpec()
	hold.SellSignal = nil
	hold.HoldToEnd = true
	sp, err = hold.Seller()
	if err != nil {
		t.Fatal(err)
	}
	emptyOr, ok := sp.(ss.Or)
	if !ok || len(emptyOr) != 0 {
		t.Fatalf("持有至期末应为空 ss.Or: %#v", sp)
	}
	if emptyOr.Sell("sh600000", nil, core.Buy{}) {
		t.Fatal("空 ss.Or 应恒 false")
	}
}

// ---- 策略库存储 ----

// TestBuilderStoreCRUD 创建/读取/更新/复制与版本冲突（验收场景 6/7）。
func TestBuilderStoreCRUD(t *testing.T) {
	st := NewBuilderStore(t.TempDir())

	created, err := st.Create(baseSpec())
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if created.Version != 1 || !strings.HasPrefix(created.ID, "st_") {
		t.Fatalf("新策略应为版本 1 且 ID 前缀 st_: %+v", created)
	}

	got, err := st.Get(created.ID)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if !reflect.DeepEqual(got.Spec, baseSpec()) {
		t.Fatalf("往返 spec 不一致: %#v", got.Spec)
	}

	// 更新：版本 +1
	updated := baseSpec()
	updated.Name = "改名"
	st2, err := st.Update(created.ID, updated, 1)
	if err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	if st2.Version != 2 || st2.Name != "改名" {
		t.Fatalf("更新后版本/名称不符: %+v", st2)
	}

	// 版本冲突：旧版本号拒绝
	if _, err := st.Update(created.ID, updated, 1); err != errBuilderVersionConflict {
		t.Fatalf("旧版本号应触发冲突: %v", err)
	}
	// 冲突后库中内容不变
	got, _ = st.Get(created.ID)
	if got.Version != 2 || got.Name != "改名" {
		t.Fatalf("冲突保存不应改变库中策略: %+v", got)
	}

	// 复制：新 ID/版本 1/源不变
	copySpec := got.Spec
	copySpec.Name = "副本"
	copied, err := st.Create(copySpec)
	if err != nil {
		t.Fatalf("复制失败: %v", err)
	}
	if copied.ID == created.ID || copied.Version != 1 {
		t.Fatalf("复制应为新身份: %+v", copied)
	}
	src, _ := st.Get(created.ID)
	if src.Version != 2 || src.Name != "改名" {
		t.Fatalf("复制后源策略不变: %+v", src)
	}

	// 列表含两条
	list, err := st.List()
	if err != nil || len(list) != 2 {
		t.Fatalf("列表应含 2 条: %v %d", err, len(list))
	}

	// 非法 ID 拒绝
	if _, err := st.Get("../../etc/passwd"); err == nil {
		t.Fatal("非法 ID 应被拒绝")
	}
	// 不存在
	if _, err := st.Get("st_20990101T000000000Z_00000000"); err != errBuilderNotFound {
		t.Fatalf("不存在应返回 errBuilderNotFound: %v", err)
	}

	// 草稿空树可保存
	draft := baseSpec()
	draft.Buy = nil
	draft.SellSignal = nil
	if _, err := st.Create(draft); err != nil {
		t.Fatalf("空树草稿应可保存: %v", err)
	}
}

// TestBuilderStoreFailClosed 损坏文件与未知版本 fail closed。
func TestBuilderStoreFailClosed(t *testing.T) {
	root := t.TempDir()
	st := NewBuilderStore(root)

	created, err := st.Create(baseSpec())
	if err != nil {
		t.Fatal(err)
	}

	// 损坏 JSON：读取与列表都拒绝，不静默跳过
	if err := os.WriteFile(filepath.Join(root, created.ID+".json"), []byte("{bad json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Get(created.ID); err == nil {
		t.Fatal("损坏文件 Get 应失败")
	}
	if _, err := st.List(); err == nil {
		t.Fatal("损坏文件 List 应失败")
	}

	// spec 版本高于已知：明确拒绝
	future := BuilderStrategy{ID: created.ID, Name: "未来", Version: 1,
		Spec: BuilderSpec{Version: 99, Name: "未来"}}
	buf, _ := json.Marshal(future)
	if err := os.WriteFile(filepath.Join(root, created.ID+".json"), buf, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Get(created.ID); err == nil || !strings.Contains(err.Error(), "高于已知最高版本") {
		t.Fatalf("未知 spec 版本应明确拒绝: %v", err)
	}
}

// ---- REST API 契约 ----

// TestBuilderAPI 组件目录/策略库/运行入口的 API 契约（验收场景 5/8/11）。
func TestBuilderAPI(t *testing.T) {
	t.Chdir(t.TempDir()) // builderDataDir 相对路径落临时目录
	h := NewServer().Handler()

	// 组件目录
	res := doReq(t, h, http.MethodGet, "/api/builder/components", nil, http.StatusOK)
	var cat struct {
		Components  []ComponentEntry       `json:"components"`
		Unavailable []UnavailableComponent `json:"unavailable"`
	}
	if err := json.Unmarshal(res, &cat); err != nil {
		t.Fatal(err)
	}
	if len(cat.Components) == 0 || len(cat.Unavailable) == 0 {
		t.Fatalf("目录响应应含可运行与不可用清单: %d/%d", len(cat.Components), len(cat.Unavailable))
	}

	// 策略库 CRUD
	spec := baseSpec()
	res = doReq(t, h, http.MethodPost, "/api/builder/strategies", map[string]any{"spec": spec}, http.StatusCreated)
	var created struct {
		Strategy BuilderStrategy `json:"strategy"`
	}
	if err := json.Unmarshal(res, &created); err != nil {
		t.Fatal(err)
	}
	id := created.Strategy.ID
	if id == "" || created.Strategy.Version != 1 {
		t.Fatalf("创建响应异常: %+v", created.Strategy)
	}

	doReq(t, h, http.MethodGet, "/api/builder/strategies/"+id, nil, http.StatusOK)

	// 覆盖保存
	spec.Name = "改名"
	res = doReq(t, h, http.MethodPut, "/api/builder/strategies/"+id,
		map[string]any{"spec": spec, "expectedVersion": 1}, http.StatusOK)
	var updated struct {
		Strategy BuilderStrategy `json:"strategy"`
	}
	if err := json.Unmarshal(res, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Strategy.Version != 2 {
		t.Fatalf("保存后版本应为 2: %+v", updated.Strategy)
	}

	// 旧版本号 → 409
	doReq(t, h, http.MethodPut, "/api/builder/strategies/"+id,
		map[string]any{"spec": spec, "expectedVersion": 1}, http.StatusConflict)

	// 另存为使用新建接口提交当前草稿。
	spec.Name = "副本"
	doReq(t, h, http.MethodPost, "/api/builder/strategies",
		map[string]any{"spec": spec}, http.StatusCreated)
	res = doReq(t, h, http.MethodGet, "/api/builder/strategies", nil, http.StatusOK)
	var list struct {
		Strategies []BuilderStrategy `json:"strategies"`
	}
	if err := json.Unmarshal(res, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Strategies) != 2 {
		t.Fatalf("策略库应含 2 条: %d", len(list.Strategies))
	}

	// run 校验：风控组件放进策略卖出信号树 → 400
	bad := baseSpec()
	bad.SellSignal = bGroup("or", bCond("risk_takeprofit_stoploss", nil))
	res = doReq(t, h, http.MethodPost, "/api/builder/run",
		runReq(bad), http.StatusBadRequest)
	if !strings.Contains(string(res), "独立风控") {
		t.Fatalf("风控组件进卖出信号应报角色错误: %s", res)
	}

	// run 校验：空买入 → 400
	bad = baseSpec()
	bad.Buy = nil
	res = doReq(t, h, http.MethodPost, "/api/builder/run", runReq(bad), http.StatusBadRequest)
	if !strings.Contains(string(res), "买入条件为空") {
		t.Fatalf("空买入应报错: %s", res)
	}

	// run 校验：卖出侧全空 → 400
	bad = baseSpec()
	bad.SellSignal = nil
	res = doReq(t, h, http.MethodPost, "/api/builder/run", runReq(bad), http.StatusBadRequest)
	if !strings.Contains(string(res), "卖出侧为空") {
		t.Fatalf("卖出侧全空应报错: %s", res)
	}

	// run 校验：与持仓天数混用 → 400
	bad = baseSpec()
	req := runReq(bad)
	req["holdingDays"] = 5
	res = doReq(t, h, http.MethodPost, "/api/builder/run", req, http.StatusBadRequest)
	if !strings.Contains(string(res), "混入") {
		t.Fatalf("builder 与简单卖出参数混用应报错: %s", res)
	}

	// run 校验：未知字段 → 400（DisallowUnknownFields）
	req = runReq(baseSpec())
	req["bogus"] = 1
	doReq(t, h, http.MethodPost, "/api/builder/run", req, http.StatusBadRequest)

	// 合法请求：单变体入队成功（后台因无行情运行时失败，不影响响应契约）
	doReq(t, h, http.MethodPost, "/api/builder/run", runReq(baseSpec()), http.StatusOK)
}

// runReq 构造搭建器运行请求体（年度/样本默认合法）。
func runReq(spec BuilderSpec) map[string]any {
	return map[string]any{
		"startYear":   2023,
		"endYear":     2023,
		"sampleMode":  "codes",
		"sampleCodes": []string{"sh600000"},
		"builder":     spec,
	}
}
