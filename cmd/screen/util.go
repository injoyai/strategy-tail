package main

import (
	"github.com/injoyai/logs"
	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/tdx"
	"github.com/injoyai/tdx/protocol"
)

// getRealtimeQuotes - 批量获取实时行情
// 通达信 API 每次最多支持 80 个代码，需分批拉取
func (this *ScreenService) getRealtimeKlines() (map[string]*protocol.Kline, error) {
	codes := this.Codes

	if len(codes) == 0 {
		return map[string]*protocol.Kline{}, nil
	}

	quoteKline := make(map[string]*protocol.Kline, len(this.Codes))
	batchSize := 80

	for i := 0; i < len(codes); i += batchSize {
		end := i + batchSize
		if end > len(codes) {
			end = len(codes)
		}

		if err := common.Manage.Do(func(c *tdx.Client) error {
			quotes, err := c.GetQuote(codes[i:end]...)
			if err != nil {
				return err
			}
			for _, q := range quotes {
				quoteKline[protocol.AddPrefix(q.Code)] = q.Kline
			}
			return nil
		}); err != nil {
			logs.Errf("[行情] 批量获取失败(%d-%d): %v", i, end, err)
		}
	}

	return quoteKline, nil
}
