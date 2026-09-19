package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/injoyai/goutil/oss"
	"github.com/injoyai/goutil/oss/csv"
)

// ExportYearTradesCSV 将单年度交易明细显式导出到 output/backtest/<year>.csv。
// Analyze 只负责计算；需要产物的命令由调用方显式调用本函数。
func ExportYearTradesCSV(year int, trades []Trade) (string, error) {
	sorted := append([]Trade(nil), trades...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].BuyTime.Before(sorted[j].BuyTime)
	})

	data := [][]any{
		{"代码", "买入时间", "买入价格", "卖出时间", "卖出价格", "净盈亏", "净收益率", "持有天数"},
	}
	for _, trade := range sorted {
		data = append(data, []any{
			trade.Code,
			trade.BuyTime.Format(time.DateTime), trade.BuyPrice.Float64(),
			trade.SellTime.Format(time.DateTime), trade.SellPrice.Float64(),
			tradeProfitAmount(trade),
			tradeReturnRate(trade),
			trade.HoldingDays(),
		})
	}

	buf, err := csv.Export(data)
	if err != nil {
		return "", fmt.Errorf("导出 %d 年交易 CSV: %w", year, err)
	}
	dir := filepath.Join("output", "backtest")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("创建回测输出目录: %w", err)
	}
	output := filepath.Join(dir, fmt.Sprintf("%d.csv", year))
	if err := oss.New(output, buf); err != nil {
		return "", fmt.Errorf("写入 %d 年交易 CSV: %w", year, err)
	}
	return output, nil
}
