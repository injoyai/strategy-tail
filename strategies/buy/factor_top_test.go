package buy

import (
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

func TestA因子TopN(t *testing.T) {
	defer core.ClearCrossSection()
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)
	ks := extend.Klines{mkKline(base, 0, 10)}
	day := core.DayOf(ks[len(ks)-1].Time)

	// 无快照 → 恒 false（契约：本策略只读不写快照）
	b := A因子TopN{Factor: &stubFactor{name: "f"}, N: 1, Asc: true}
	if b.Buy("sh600001", ks) {
		t.Fatal("无快照不应买入")
	}

	// 写入快照：升序名次 sh600001=1, sh600002=2 → N=1 只买名次 1
	key := core.TopNKey("f", true)
	core.SetCrossSection(day, key, []string{"sh600001", "sh600002"})

	if !b.Buy("sh600001", ks) {
		t.Fatal("名次1应买入")
	}
	if b.Buy("sh600002", ks) {
		t.Fatal("名次2不应买入")
	}

	// N=2 名次 2 也买
	if !(A因子TopN{Factor: &stubFactor{name: "f"}, N: 2, Asc: true}).Buy("sh600002", ks) {
		t.Fatal("N=2 名次2应买入")
	}

	// N=0 → 恒 false
	if (A因子TopN{Factor: &stubFactor{name: "f"}, Asc: true}).Buy("sh600001", ks) {
		t.Fatal("N=0 不应买入")
	}

	// key 按方向隔离：desc 方向无快照 → false
	if (A因子TopN{Factor: &stubFactor{name: "f"}, N: 1, Asc: false}).Buy("sh600001", ks) {
		t.Fatal("desc 方向无快照不应买入")
	}

	// 跨日期：快照只写了 day，隔日 K 线查询不到 → false
	ksTomorrow := extend.Klines{mkKline(base, 1, 10)}
	if b.Buy("sh600001", ksTomorrow) {
		t.Fatal("跨日期无快照不应买入")
	}

	// Name
	if g := b.Name(); g != "f TopN(1,升序)" {
		t.Fatalf("Name = %s", g)
	}

	// 零值：Factor nil → false，Name 为默认降序描述
	if g := (A因子TopN{}).Name(); g != "因子TopN(降序)" {
		t.Fatalf("零值 Name = %s", g)
	}
	if (A因子TopN{}).Buy("sh600001", ks) {
		t.Fatal("零值 Factor nil 不应买入")
	}
}

// 编译期确认实现 Buyer。
var _ core.Buyer = A因子TopN{}
