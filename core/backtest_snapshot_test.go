package core

import (
	"math"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/v2/protocol"
)

type snapshotBuyer struct{ day time.Time }

func (snapshotBuyer) Name() string { return "快照测试买入" }
func (b snapshotBuyer) Buy(_ string, dks extend.Klines) bool {
	return dks[len(dks)-1].Time.Equal(b.day)
}

// snapshotSeller 记录 Seller 每次收到的末根快照，用于验证盘中数据可见性。
type snapshotSeller struct {
	snapshots []extend.Kline
	times     []time.Time
}

func (snapshotSeller) Name() string { return "快照测试卖出" }
func (s *snapshotSeller) Sell(_ string, dks extend.Klines, _ Buy) bool {
	last := dks[len(dks)-1]
	clone := *last
	if last.Kline != nil {
		inner := *last.Kline
		clone.Kline = &inner
	}
	s.snapshots = append(s.snapshots, clone)
	s.times = append(s.times, last.Time)
	return false
}

func snapshotDay(t *testing.T, day time.Time, close, last float64, volume int64, floatStock int64, turnover float64) *extend.Kline {
	t.Helper()
	return &extend.Kline{
		Unix:       day.Unix(),
		Kline:      &protocol.Kline{Time: day, Last: last, Open: close, High: close, Low: close, Close: close, Volume: volume},
		Turnover:   turnover,
		FloatStock: floatStock,
	}
}

func snapshotMinute(day time.Time, hour, minute int, last, open, close float64, volume int64) *protocol.Kline {
	return &protocol.Kline{
		Time:   time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, time.Local),
		Last:   last,
		Open:   open,
		High:   close,
		Low:    open,
		Close:  close,
		Volume: volume,
	}
}

func TestDo分钟快照只含当前时刻数据(t *testing.T) {
	day1 := time.Date(2024, 1, 3, 15, 0, 0, 0, time.Local)
	day2 := time.Date(2024, 1, 4, 15, 0, 0, 0, time.Local)
	const floatStock = int64(100_000_000)

	his := extend.Klines{snapshotDay(t, time.Date(2024, 1, 2, 15, 0, 0, 0, time.Local), 9.8, 9.7, 1000, floatStock, 1)}
	dks := extend.Klines{
		snapshotDay(t, day1, 10, 9.8, 1000, floatStock, 1),
		// 全天 10% 换手率与 15:00 时间戳不能泄漏给 09:31 的策略快照。
		snapshotDay(t, day2, 10.6, 10, 100_000, floatStock, 10),
	}
	originalDay2 := *dks[1].Kline

	mks := protocol.Klines{
		snapshotMinute(day2, 9, 31, 10, 10.5, 10.6, 100),
		snapshotMinute(day2, 9, 32, 10, 10.6, 10.7, 200),
	}
	seller := &snapshotSeller{}
	bt := Backtest{
		Buyer:    snapshotBuyer{day: day1},
		Seller:   seller,
		Position: PositionConfig{MaxPerCode: 1},
	}
	bt.Do("sh600000", his, dks, mks)

	if len(seller.snapshots) != 2 {
		t.Fatalf("Seller 快照数 = %d, want 2", len(seller.snapshots))
	}
	first := seller.snapshots[0]
	if got, want := first.Time, time.Date(2024, 1, 4, 9, 31, 0, 0, time.Local); !got.Equal(want) {
		t.Fatalf("09:31 快照 Time = %v, want %v", got, want)
	}
	if first.Unix != first.Time.Unix() {
		t.Fatalf("Unix = %d 与 Time %v 不一致", first.Unix, first.Time)
	}
	if math.Abs(first.Turnover-0.01) > 1e-9 {
		t.Fatalf("09:31 换手率 = %v%%, want 0.01%%（不能沿用全天 10%%）", first.Turnover)
	}
	if first.Last != 10 {
		t.Fatalf("09:31 前收 = %v, want 10（日线前收，不能取分钟开盘价）", first.Last)
	}
	if first.Volume != 100 {
		t.Fatalf("09:31 累计成交量 = %d, want 100", first.Volume)
	}
	if seller.snapshots[1].Turnover <= first.Turnover {
		t.Fatalf("09:32 换手率 %v 应随累计成交量增加", seller.snapshots[1].Turnover)
	}
	if *dks[1].Kline != originalDay2 || dks[1].Turnover != 10 || dks[1].Unix != day2.Unix() {
		t.Fatal("回测不得修改原始日线")
	}
}

func TestDo分钟快照不使用未来分钟数据(t *testing.T) {
	day1 := time.Date(2024, 1, 3, 15, 0, 0, 0, time.Local)
	day2 := time.Date(2024, 1, 4, 15, 0, 0, 0, time.Local)
	his := extend.Klines{snapshotDay(t, time.Date(2024, 1, 2, 15, 0, 0, 0, time.Local), 9.8, 9.7, 1000, 100_000_000, 1)}
	base := extend.Klines{
		snapshotDay(t, day1, 10, 9.8, 1000, 100_000_000, 1),
		snapshotDay(t, day2, 10.6, 10, 100_000, 100_000_000, 10),
	}
	firstMinute := snapshotMinute(day2, 9, 31, 10, 10.5, 10.6, 100)
	early := protocol.Klines{firstMinute, snapshotMinute(day2, 9, 32, 10, 10.6, 10.7, 200)}
	late := protocol.Klines{firstMinute, snapshotMinute(day2, 14, 59, 10, 10.7, 9.0, 90_000)}

	run := func(mks protocol.Klines) snapshotSeller {
		seller := &snapshotSeller{}
		bt := Backtest{Buyer: snapshotBuyer{day: day1}, Seller: seller, Position: PositionConfig{MaxPerCode: 1}}
		bt.Do("sh600000", his, base, mks)
		return *seller
	}
	a, b := run(early), run(late)
	if len(a.snapshots) == 0 || len(b.snapshots) == 0 {
		t.Fatal("缺少快照")
	}
	if a.snapshots[0].Time != b.snapshots[0].Time ||
		a.snapshots[0].Unix != b.snapshots[0].Unix ||
		a.snapshots[0].Turnover != b.snapshots[0].Turnover ||
		a.snapshots[0].Close != b.snapshots[0].Close {
		t.Fatalf("09:31 快照随未来分钟变化: %+v vs %+v", a.snapshots[0], b.snapshots[0])
	}
}

func TestDo无分钟数据沿用日线降级快照(t *testing.T) {
	day1 := time.Date(2024, 1, 3, 15, 0, 0, 0, time.Local)
	day2 := time.Date(2024, 1, 4, 15, 0, 0, 0, time.Local)
	his := extend.Klines{snapshotDay(t, time.Date(2024, 1, 2, 15, 0, 0, 0, time.Local), 9.8, 9.7, 1000, 100_000_000, 1)}
	dks := extend.Klines{
		snapshotDay(t, day1, 10, 9.8, 1000, 100_000_000, 1),
		snapshotDay(t, day2, 10.6, 10, 100_000, 100_000_000, 10),
	}
	seller := &snapshotSeller{}
	bt := Backtest{Buyer: snapshotBuyer{day: day1}, Seller: seller, Position: PositionConfig{MaxPerCode: 1}}
	bt.Do("sh600000", his, dks, nil)
	if len(seller.snapshots) != 1 {
		t.Fatalf("无分钟数据应降级为单根日线快照, got %d", len(seller.snapshots))
	}
	snap := seller.snapshots[0]
	if !snap.Time.Equal(day2) || snap.Unix != day2.Unix() {
		t.Fatalf("降级快照时间 = %v/%d, want %v", snap.Time, snap.Unix, day2)
	}
	if snap.Turnover != 10 || snap.Volume != 100_000 {
		t.Fatalf("降级快照应保留当日日线数据, got turnover=%v volume=%d", snap.Turnover, snap.Volume)
	}
	if *dks[1].Kline != *snapshotDay(t, day2, 10.6, 10, 100_000, 100_000_000, 10).Kline {
		t.Fatal("降级路径不得修改原始日线")
	}
}

func TestDo分钟快照缺少流通股本不沿用全天换手率(t *testing.T) {
	day1 := time.Date(2024, 1, 3, 15, 0, 0, 0, time.Local)
	day2 := time.Date(2024, 1, 4, 15, 0, 0, 0, time.Local)
	his := extend.Klines{snapshotDay(t, time.Date(2024, 1, 2, 15, 0, 0, 0, time.Local), 9.8, 9.7, 1000, 0, 1)}
	dks := extend.Klines{
		snapshotDay(t, day1, 10, 9.8, 1000, 0, 1),
		snapshotDay(t, day2, 10.6, 10, 100_000, 0, 10),
	}
	seller := &snapshotSeller{}
	bt := Backtest{Buyer: snapshotBuyer{day: day1}, Seller: seller, Position: PositionConfig{MaxPerCode: 1}}
	bt.Do("sh600000", his, dks, protocol.Klines{snapshotMinute(day2, 9, 31, 10, 10.5, 10.6, 100)})
	if len(seller.snapshots) == 0 {
		t.Fatal("缺少快照")
	}
	if seller.snapshots[0].Turnover != 0 {
		t.Fatalf("缺少流通股本时换手率 = %v, want 0（不能沿用全天值）", seller.snapshots[0].Turnover)
	}
}
