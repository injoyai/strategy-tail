package buy

import (
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

// testBars 生成 n 根恒价日 K（N天前符合 只关心前缀长度，不关心价格）。
func testBars(n int) extend.Klines {
	ks := make(extend.Klines, 0, n)
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.Local)
	for i := 0; i < n; i++ {
		at := base.AddDate(0, 0, i)
		ks = append(ks, &extend.Kline{Kline: &protocol.Kline{
			Time: at, Open: protocol.Yuan(10), High: protocol.Yuan(10),
			Low: protocol.Yuan(10), Close: protocol.Yuan(10), Volume: 100,
		}})
	}
	return ks
}

// prefixBuyer 记录每次被评估的前缀长度（扫描位置），恒返回 false 以扫完全程。
type prefixBuyer struct{ lens []int }

func (p *prefixBuyer) Name() string { return "prefix" }
func (p *prefixBuyer) Buy(_ string, dks extend.Klines) bool {
	p.lens = append(p.lens, len(dks))
	return false
}

// TestN天前符合ClosedInterval 扫描窗口为闭区间 [len-To, len-From]：
// {From:1, To:3} 在 10 根 K 线上恰好评估前缀 9、8、7，To 端点（第 3 交易日
// 前）被计入，不得多扫一天。
func TestN天前符合ClosedInterval(t *testing.T) {
	p := &prefixBuyer{}
	b := N天前符合{From: 1, To: 3, Buyer: p}
	if b.Buy("000001", testBars(10)) {
		t.Fatal("prefixBuyer 恒 false，不应触发买入")
	}
	if len(p.lens) != 3 || p.lens[0] != 9 || p.lens[1] != 8 || p.lens[2] != 7 {
		t.Fatalf("扫描序列 = %v, want [9 8 7]", p.lens)
	}
}

// TestN天前符合ZeroFromDefaults From 缺省回退默认 5：end=5，无 To 上界时
// 扫描 [5,4,3,2,1]。
func TestN天前符合ZeroFromDefaults(t *testing.T) {
	p := &prefixBuyer{}
	b := N天前符合{From: 0, Buyer: p}
	if b.Buy("000001", testBars(10)) {
		t.Fatal("prefixBuyer 恒 false，不应触发买入")
	}
	if len(p.lens) != 5 || p.lens[0] != 5 || p.lens[4] != 1 {
		t.Fatalf("From=0 应回退默认 5，扫描 = %v, want [5 4 3 2 1]", p.lens)
	}
}

// TestN天前符合NegativeFromRejected 负 From 属无效配置：不触发、不扫描，
// 不静默回退默认窗口掩盖配置错误（与参数入口语义一致）。
func TestN天前符合NegativeFromRejected(t *testing.T) {
	p := &prefixBuyer{}
	b := N天前符合{From: -2, Buyer: p}
	if b.Buy("000001", testBars(10)) {
		t.Fatal("负 From 不应触发买入")
	}
	if len(p.lens) != 0 {
		t.Fatalf("负 From 不应产生扫描: %v", p.lens)
	}
}

// Test近N天符合NegativeDaysRejected 负 Days 使循环条件恒假：不扫描、不触发。
func Test近N天符合NegativeDaysRejected(t *testing.T) {
	p := &prefixBuyer{}
	b := 近N天符合{Days: -2, Buyer: p}
	if b.Buy("000001", testBars(10)) {
		t.Fatal("负 Days 不应触发买入")
	}
	if len(p.lens) != 0 {
		t.Fatalf("负 Days 不应产生扫描: %v", p.lens)
	}
}

// Test近N天符合ZeroDaysDefaults Days 缺省回退默认 10：扫描最近 10 根。
func Test近N天符合ZeroDaysDefaults(t *testing.T) {
	p := &prefixBuyer{}
	b := 近N天符合{Days: 0, Buyer: p}
	if b.Buy("000001", testBars(12)) {
		t.Fatal("prefixBuyer 恒 false，不应触发买入")
	}
	if len(p.lens) != 10 || p.lens[0] != 12 || p.lens[9] != 3 {
		t.Fatalf("Days=0 应回退默认 10，扫描 = %v, want 12..3", p.lens)
	}
}
