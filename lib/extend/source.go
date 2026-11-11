package extend

import (
	"fmt"
	"time"

	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/extend/pull"
	"github.com/injoyai/tdx/v2/protocol"
)

func standardSourceCode(code string) (model.Code, error) {
	c, err := pull.ParseCode(code)
	if err != nil {
		return c, err
	}
	info, _ := c.Market.Info()
	if info.Protocol != protocol.ProtocolStandard {
		return c, fmt.Errorf("回测模型仅支持标准市场: %s", code)
	}
	return c, nil
}

func sourceVolume(code model.Code, volume int64) (int64, error) {
	if volume >= 0 && (code.Market == protocol.MarketIndex || code.Market == protocol.MarketBlock) {
		return volume, nil
	}
	if volume < 0 || volume%protocol.SharesPerLot != 0 {
		return 0, fmt.Errorf("成交量 %d 股无法无损转换为整数手", volume)
	}
	return protocol.FromShares(volume), nil
}

func (r *MarketReader) DayKlines(code string, start, end time.Time) (Klines, error) {
	c, err := standardSourceCode(code)
	if err != nil {
		return nil, err
	}
	var startDate, endDate string
	if !start.IsZero() {
		startDate = start.Format(time.DateOnly)
	}
	if !end.IsZero() {
		endDate = end.Format(time.DateOnly)
	}
	// Read the preceding bars as well to retain the true previous close.
	rows, err := r.source.QueryDay(c, "", endDate)
	if err != nil {
		return nil, err
	}
	out := make(Klines, 0, len(rows))
	var last float64
	for _, row := range rows {
		previous := last
		last = row.Close
		if startDate != "" && row.Time < startDate {
			continue
		}
		at, err := time.ParseInLocation(time.DateOnly, row.Time, protocol.Location())
		if err != nil {
			return nil, err
		}
		at = at.Add(15 * time.Hour)
		volume, err := sourceVolume(c, row.Volume)
		if err != nil {
			return nil, err
		}
		bar := &Kline{Unix: at.Unix(), Kline: &protocol.Kline{Time: at, Last: previous, Open: row.Open, High: row.High, Low: row.Low, Close: row.Close, Volume: volume, Amount: amountValue(row.Amount)}}
		if c.Market == protocol.MarketAStock {
			if v := r.EquityAt(c.Code, at); v != nil {
				bar.FloatStock, bar.TotalStock = v.Float, v.Total
				if v.Float > 0 {
					bar.Turnover = v.Turnover(row.Volume)
				}
			}
		}
		out = append(out, bar)
	}
	return out, nil
}

// MinKlines retains the research runner's whole-year loading contract.
func (r *MarketReader) MinKlines(code string, start, end time.Time) (protocol.Klines, error) {
	return r.readMinutes(code, fmt.Sprintf("%04d-01-01 00:00", start.Year()), fmt.Sprintf("%04d-12-31 23:59", end.Year()))
}

// MinuteKlines queries a bounded interval for consumers that need only recent bars.
func (r *MarketReader) MinuteKlines(code string, start, end time.Time) (protocol.Klines, error) {
	return r.readMinutes(code, start.In(protocol.Location()).Format(protocol.TimeLayout), end.In(protocol.Location()).Format(protocol.TimeLayout))
}

func (r *MarketReader) readMinutes(code, start, end string) (protocol.Klines, error) {
	c, err := standardSourceCode(code)
	if err != nil {
		return nil, err
	}
	rows, err := r.source.QueryMinute(c, start, end)
	if err != nil {
		return nil, err
	}
	out := make(protocol.Klines, 0, len(rows))
	for _, row := range rows {
		at, err := time.ParseInLocation(protocol.TimeLayout, row.Time, protocol.Location())
		if err != nil {
			return nil, err
		}
		volume, err := sourceVolume(c, row.Volume)
		if err != nil {
			return nil, err
		}
		out = append(out, &protocol.Kline{Time: at, Open: row.Open, High: row.High, Low: row.Low, Close: row.Close, Volume: volume, Amount: amountValue(row.Amount)})
	}
	return out, nil
}

func amountValue(amount *float64) float64 {
	if amount == nil {
		return 0
	}
	return *amount
}
