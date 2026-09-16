package lab

import (
	"testing"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
)

// fakeFac 测试用常数因子。
type fakeFac struct{ name string }

func (f fakeFac) Name() string                        { return f.name }
func (f fakeFac) Value(string, extend.Klines) float64 { return 0 }

// fakeComposite 测试用组合买家（core.CompositeBuyer）。
type fakeComposite struct{ kids []core.Buyer }

func (fakeComposite) Name() string                   { return "组合" }
func (fakeComposite) Buy(string, extend.Klines) bool { return false }
func (f fakeComposite) Children() []core.Buyer       { return f.kids }

// fakePlain 非 TopN 的普通买家。
type fakePlain struct{}

func (fakePlain) Name() string                   { return "plain" }
func (fakePlain) Buy(string, extend.Klines) bool { return false }

// TestCollectTopN 递归收集：值/指针双断言、去重保序、无效项跳过。
func TestCollectTopN(t *testing.T) {
	fa := fakeFac{name: "动量"}
	variants := []core.Variant{
		// 指针型（&字面量路径）
		{Name: "a", Buyer: &sb.A因子TopN{Factor: fa, N: 5, Asc: false}},
		// 值型（脚本复合字面量退化路径）：同 key 去重；asc 方向保留
		{Name: "b", Buyer: fakeComposite{kids: []core.Buyer{
			sb.A因子TopN{Factor: fa, N: 3, Asc: false},
			sb.A因子TopN{Factor: fa, N: 1, Asc: true},
		}}},
		// 无效项跳过、非 TopN 买家忽略
		{Name: "c", Buyer: fakeComposite{kids: []core.Buyer{
			sb.A因子TopN{N: 1},
			sb.A因子TopN{Factor: fa, N: 0, Asc: true},
			fakePlain{},
		}}},
	}

	reqs := collectTopN(variants)
	if len(reqs) != 2 {
		t.Fatalf("去重后应剩 2 个请求，得 %d: %+v", len(reqs), reqs)
	}
	if reqs[0].key != core.TopNKey("动量", false) || reqs[0].asc {
		t.Fatalf("reqs[0] 应为 动量|desc: %+v", reqs[0])
	}
	if reqs[1].key != core.TopNKey("动量", true) || !reqs[1].asc {
		t.Fatalf("reqs[1] 应为 动量|asc: %+v", reqs[1])
	}
}
