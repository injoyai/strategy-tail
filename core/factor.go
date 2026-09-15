package core

import (
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
)

// Factor 策略因子：每股每天一个数值度量（与 Buyer 的布尔决策相对）。
// 因子是无状态纯函数，只读 dks（截至当日的 K 线前缀，引擎保证无前视）。
//
// 约定：
//   - 数据不足/除零/零方差返回 math.NaN()（与数值 0 区分，下游统一按"无效"处理）
//   - 全部为比值计算，与价格标度无关
type Factor interface {
	Name() string
	// Value 返回该股票当日的因子值；dks 为截至当日（含）的 K 线序列。
	Value(code string, dks extend.Klines) float64
}

// DayOf 把任意时间截断到当日墙钟零点（交易日归一）。
// 不能用 Truncate(24h)：它按 UTC 绝对时间截断，跨时区会切错日期。
func DayOf(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}
