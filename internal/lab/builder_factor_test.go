package lab

import (
	"encoding/json"
	"math"
	"net/http"
	"reflect"
	"testing"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

func factorNode(ft FactorFilterSpec) *BuilderNode {
	return &BuilderNode{Type: "factor", Factor: &ft}
}

func TestBuilderFactorConditionsShareStrategyRules(t *testing.T) {
	for _, ft := range []FactorFilterSpec{
		{Kind: "momentum", Days: 2, FactorVersion: 1, Operator: "gte", Min: fptr(0)},
		{Kind: "momentum", Days: 2, FactorVersion: 1, Operator: "lte", Max: fptr(0)},
		{Kind: "momentum", Days: 2, FactorVersion: 1, Operator: "between", Min: fptr(-0.02), Max: fptr(0.02)},
	} {
		t.Run(ft.Operator, func(t *testing.T) {
			spec := BuilderSpec{Version: 1, Name: "统一因子规则", Buy: factorNode(ft), SellSignal: factorNode(ft)}
			if err := spec.validate("run"); err != nil {
				t.Fatal(err)
			}
			buyer, err := spec.Buyer()
			if err != nil {
				t.Fatal(err)
			}
			seller, err := spec.Seller()
			if err != nil {
				t.Fatal(err)
			}
			variants, err := (StrategySpec{Version: 1, FactorFilters: []FactorFilterSpec{ft}}).Variants()
			if err != nil {
				t.Fatal(err)
			}
			bars := conditionBaselineBars(false)
			for end := 0; end <= len(bars); end++ {
				want := variants[1].Buyer.Buy("sh600000", bars[:end])
				if buyer.Buy("sh600000", bars[:end]) != want || seller.Sell("sh600000", bars[:end], core.Buy{Price: 999}) != want {
					t.Fatalf("第%d个前缀的因子条件不一致", end)
				}
			}
			if buyer.Name() != seller.Name() {
				t.Fatal("买卖条件名称应来自相同因子声明")
			}
		})
	}
}

func TestBuilderFactorValidation(t *testing.T) {
	valid := FactorFilterSpec{Kind: "momentum", Days: 2, FactorVersion: 1, Operator: "gte", Min: fptr(0)}
	for _, tt := range []struct {
		name   string
		mutate func(*BuilderSpec)
	}{
		{"缺定义", func(s *BuilderSpec) { s.Buy.Factor = nil }},
		{"未知因子", func(s *BuilderSpec) { s.Buy.Factor.Kind = "missing" }},
		{"旧实现版本", func(s *BuilderSpec) { s.Buy.Factor.FactorVersion = 999 }},
		{"缺阈值", func(s *BuilderSpec) { s.Buy.Factor.Min = nil }},
		{"无效阈值", func(s *BuilderSpec) { s.Buy.Factor.Min = fptr(math.NaN()) }},
		{"无效窗口", func(s *BuilderSpec) { s.Buy.Factor.Days = 0 }},
		{"混合组件", func(s *BuilderSpec) { s.Buy.ComponentID = "buy_all" }},
		{"混合参数", func(s *BuilderSpec) { s.Buy.Params = map[string]any{"period": 20.0} }},
		{"混合分组", func(s *BuilderSpec) { s.Buy.Children = []*BuilderNode{bCond("buy_all", nil)} }},
		{"组件夹带因子", func(s *BuilderSpec) { s.Buy.Type = "condition"; s.Buy.ComponentID = "buy_all" }},
		{"分组夹带因子", func(s *BuilderSpec) { s.Buy = bGroup("and", s.Buy); s.Buy.Factor = &valid }},
		{"风控角色", func(s *BuilderSpec) { s.Risks = []*BuilderNode{factorNode(valid)} }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			spec := BuilderSpec{Version: 1, Name: "测试", Buy: factorNode(valid), HoldToEnd: true}
			tt.mutate(&spec)
			for _, mode := range []string{"save", "run"} {
				if err := spec.validate(mode); err == nil {
					t.Fatal("必须拒绝无效因子节点")
				}
			}
		})
	}
	// 直接构建也校验阈值，不能因为绕过 Validate 而触发空指针或执行旧版本。
	invalid := valid
	invalid.Min = nil
	if _, err := factorNode(invalid).buyer(); err == nil {
		t.Fatal("直接构建必须校验阈值")
	}
	if _, err := (&BuilderNode{Type: "factor"}).seller(); err == nil {
		t.Fatal("直接构建必须校验声明存在")
	}
}

func TestBuilderFactorTreeAndPersistence(t *testing.T) {
	ft := FactorFilterSpec{Kind: "momentum", Days: 2, FactorVersion: 1, Operator: "gte", Min: fptr(0)}
	spec := BuilderSpec{Version: 1, Name: "混合规则", Buy: bGroup("or", factorNode(ft), bCond("buy_all", nil)), HoldToEnd: true}
	store := NewBuilderStore(t.TempDir())
	st, err := store.Create(spec)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(st.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Spec, spec) {
		t.Fatalf("保存丢失因子节点: %#v", got.Spec)
	}
	buyer, err := got.Spec.Buyer()
	if err != nil {
		t.Fatal(err)
	}
	if !buyer.Buy("sh600000", nil) {
		t.Fatal("混合规则必须保留原 OR 与全部样本语义")
	}
	raw, err := json.Marshal(got.Spec)
	if err != nil {
		t.Fatal(err)
	}
	var back BuilderSpec
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, spec) {
		t.Fatal("因子零阈值在JSON往返中丢失")
	}
	old, _ := json.Marshal(bCond("buy_all", nil))
	if string(old) != `{"type":"condition","componentId":"buy_all"}` {
		t.Fatalf("旧节点格式变化: %s", old)
	}
}

func TestBuilderFactorRunKeepsFrozenSnapshot(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	old := common.Pull
	common.Pull = &extend.PullKline{Config: extend.PullKlineConfig{Dir: dir}}
	t.Cleanup(func() { common.Pull = old })
	writeFakeDayDB(t, dir, "sh600000", 12, time.Date(2025, 1, 2, 0, 0, 0, 0, time.Local))
	ft := FactorFilterSpec{Kind: "momentum", Days: 2, FactorVersion: 1, Operator: "gte", Min: fptr(0)}
	spec := BuilderSpec{Version: 1, Name: "因子信号", Buy: factorNode(ft), SellSignal: factorNode(ft)}
	server := NewServer()
	h := server.Handler()
	doReq(t, h, http.MethodPost, "/api/builder/strategies", map[string]any{"spec": spec}, http.StatusCreated)
	req := runReq(spec)
	req["startYear"], req["endYear"] = 2025, 2025
	doReq(t, h, http.MethodPost, "/api/builder/run", req, http.StatusOK)
	deadline := time.Now().Add(5 * time.Second)
	for server.runner.running.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if server.runner.Status()["state"] != "done" {
		t.Fatalf("回测失败: %v", server.runner.Status())
	}
	var report Report
	if err := json.Unmarshal(doReq(t, h, http.MethodGet, "/api/report/latest", nil, http.StatusOK), &report); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.Config.Builder, &spec) || len(report.Variants[0].Trades) == 0 {
		t.Fatal("报告丢失因子声明或没有执行成交")
	}
	*spec.Buy.Factor.Min = 0.5
	var persisted Report
	if err := json.Unmarshal(doReq(t, h, http.MethodGet, "/api/report/"+report.RunID(), nil, http.StatusOK), &persisted); err != nil {
		t.Fatal(err)
	}
	if *persisted.Config.Builder.Buy.Factor.Min != 0 {
		t.Fatal("编辑草稿改变了已保存的报告快照")
	}
}
