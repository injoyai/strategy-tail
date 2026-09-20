package factor

import (
	"math"
	"testing"
	"time"

	"github.com/injoyai/tdx/protocol"
)

func Test跳过近月动量(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)
	// Days=22、固定跳过最近 21 个交易日：比较索引 1 与索引 0，15/10-1=0.5。
	cs := make([]float64, 23)
	cs[0], cs[1] = 10, 15
	for i := 2; i < len(cs); i++ {
		cs[i] = 100 // 最近 21 日的价格不参与信号，防止误用当日收盘。
	}
	f := 跳过近月动量{Days: 22}
	wantVal(t, f.Name(), f.Value("sh600000", closes(base, cs...)), 0.5, 1e-12)

	wantNaN(t, "窗口不大于跳过期", (跳过近月动量{Days: 21}).Value("sh600000", closes(base, cs...)))
	wantNaN(t, "数据不足", f.Value("sh600000", closes(base, cs[:22]...)))
	wantNaN(t, "基准收盘为零", f.Value("sh600000", closes(base, append([]float64{0}, cs[1:]...)...)))
	if got := (跳过近月动量{}).Name(); got != "跳过近月动量(252)" {
		t.Fatalf("默认参数名异常: %s", got)
	}
}

func TestN日短期反转(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)
	f := N日短期反转{Days: 2}
	wantVal(t, f.Name(), f.Value("sh600000", closes(base, 100, 110, 121)), -0.21, 1e-12)
	wantNaN(t, "数据不足", f.Value("sh600000", closes(base, 100, 110)))
	wantNaN(t, "基准收盘为零", f.Value("sh600000", closes(base, 0, 1, 2)))
	if got := (N日短期反转{}).Name(); got != "N日短期反转(20)" {
		t.Fatalf("默认参数名异常: %s", got)
	}
}

func TestAmihud非流动性(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)
	ks := closes(base, 100, 110, 99)
	ks[1].Amount = protocol.Yuan(100_000_000)
	ks[2].Amount = protocol.Yuan(100_000_000)
	// 两日绝对收益率均为 10%，成交额均为 1 亿元，缩放后均为 0.1。
	f := Amihud非流动性{Days: 2}
	wantVal(t, f.Name(), f.Value("sh600000", ks), 0.1, 1e-12)

	ks[2].Amount = 0
	wantNaN(t, "窗口内零成交额", f.Value("sh600000", ks))
	wantNaN(t, "数据不足", f.Value("sh600000", ks[:2]))
	zeroBase := closes(base, 0, 1)
	zeroBase[1].Amount = protocol.Yuan(100_000_000)
	wantNaN(t, "前收盘为零", (Amihud非流动性{Days: 1}).Value("sh600000", zeroBase))
	if got := (Amihud非流动性{}).Name(); got != "Amihud非流动性(20)" {
		t.Fatalf("默认参数名异常: %s", got)
	}
}

func TestN日收盘高点距离(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)
	f := N日收盘高点距离{Days: 3}
	wantVal(t, f.Name(), f.Value("sh600000", closes(base, 10, 20, 15)), -0.25, 1e-12)
	wantVal(t, "创新高", f.Value("sh600000", closes(base, 10, 15, 20)), 0, 1e-12)
	wantNaN(t, "数据不足", f.Value("sh600000", closes(base, 10, 20)))
	wantNaN(t, "非正最高收盘", f.Value("sh600000", closes(base, 0, 0, 0)))
	if got := (N日收盘高点距离{}).Name(); got != "N日收盘高点距离(252)" {
		t.Fatalf("默认参数名异常: %s", got)
	}
}

func Test对数流通市值(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)
	ks := closes(base, 10)
	ks[0].FloatStock = 4_000_000_000 // 10 元 × 40 亿股 = 400 亿元。
	f := 对数流通市值{}
	wantVal(t, f.Name(), f.Value("sh600000", ks), math.Log(400), 1e-12)

	ks[0].FloatStock = 0
	wantNaN(t, "流通股本缺失", f.Value("sh600000", ks))
	wantNaN(t, "数据不足", f.Value("sh600000", nil))
	if got := f.Name(); got != "对数流通市值" {
		t.Fatalf("名称异常: %s", got)
	}
}
