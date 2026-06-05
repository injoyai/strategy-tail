// main.go - 实时选股屏幕程序
//
// 该程序实现以下功能：
// 1. 从通达信获取实时行情数据
// 2. 基于预设的MACD反转策略筛选股票
// 3. 在终端实时打印选股结果（交易期间每分钟刷新）
// 4. 通过WebSocket推送选股结果到客户端
//
// 交易时间：
// - 上午：9:30 - 11:30
// - 下午：13:00 - 15:00
//
// 非交易时间不执行选股刷新

package main

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/injoyai/frame"
	"github.com/injoyai/frame/fbr" // Web框架
	"github.com/injoyai/logs"      // 日志库
	"github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core" // 选股核心模块
	"github.com/injoyai/tdx"                // 通达信SDK
	"github.com/injoyai/tdx/extend"         // 通达信扩展功能
	"github.com/injoyai/tdx/protocol"       // 通达信协议定义
)

// getRealtimeQuotes - 批量获取实时行情
// 参数：
//
//	codes: 股票代码列表
//
// 返回：
//
//	map[string]*protocol.Quote: 股票代码到行情的映射
//	error: 错误信息
//
// 注意：通达信API每次最多支持80个代码，需要分批获取
func getRealtimeQuotes(codes []string) (map[string]*protocol.Quote, error) {
	if len(codes) == 0 {
		return nil, nil
	}

	quoteMap := make(map[string]*protocol.Quote, len(codes))
	batchSize := 80 // 每批最多80个代码

	// 分批获取行情
	for i := 0; i < len(codes); i += batchSize {
		end := i + batchSize
		if end > len(codes) {
			end = len(codes)
		}

		// 使用通达信客户端获取行情
		if err := common.Manage.Do(func(c *tdx.Client) error {
			quotes, err := c.GetQuote(codes[i:end]...)
			if err != nil {
				return err
			}
			// 将行情存入map，key为带前缀的股票代码
			for _, q := range quotes {
				quoteMap[protocol.AddPrefix(q.Code)] = q
			}
			return nil
		}); err != nil {
			logs.Errf("[行情] 批量获取失败(%d-%d): %v", i, end, err)
		}
	}

	return quoteMap, nil
}

// quoteToKline - 将实时行情转换为K线数据
// 参数：
//
//	quote: 实时行情数据
//	prevKline: 上一根K线（用于继承流通股和总股本）
//
// 返回：
//
//	*extend.Kline: 转换后的K线数据
func quoteToKline(quote *protocol.Quote, prevKline *extend.Kline) *extend.Kline {
	now := time.Now()
	return &extend.Kline{
		Unix: now.Unix(),
		Kline: &protocol.Kline{
			Last:   quote.K.Last,                 // 昨收价
			Open:   quote.K.Open,                 // 今开盘价
			High:   quote.K.High,                 // 最高价
			Low:    quote.K.Low,                  // 最低价
			Close:  quote.K.Close,                // 当前价（作为收盘价）
			Volume: int64(quote.TotalHand) * 100, // 成交量（手×100）
			Amount: protocol.Yuan(quote.Amount),  // 成交额
			Time:   now,                          // 当前时间
		},
		FloatStock: prevKline.FloatStock, // 流通股数（继承上一日数据）
		TotalStock: prevKline.TotalStock, // 总股本数（继承上一日数据）
	}
}

// makeIntradayGetDayKlines - 构造盘中版K线获取函数
// 参数：
//
//	quoteMap: 实时行情映射
//
// 返回：
//
//	func(code string, start, end time.Time) (extend.Klines, error): K线获取闭包
//
// 核心逻辑：
// 1. 从本地数据库读取历史日线
// 2. 如果当天是交易日，用实时行情拼接今日K线
// 3. 时间范围过滤
func makeIntradayGetDayKlines(quoteMap map[string]*protocol.Quote) func(code string, start, end time.Time) (extend.Klines, error) {
	return func(code string, start, end time.Time) (extend.Klines, error) {
		// 1. 从数据库读取历史日线
		ks, err := common.Pull.DayKlines(code)
		if err != nil {
			return nil, err
		}

		// 2. 按时间范围过滤
		var ls extend.Klines
		for _, k := range ks {
			if !k.Time.Before(start) && !k.Time.After(end) {
				ls = append(ls, k)
			}
		}

		// 没有数据直接返回
		if len(ls) == 0 {
			return ls, nil
		}

		// 3. 如果有实时行情，拼接今日K线
		if quote, ok := quoteMap[code]; ok {
			last := ls[len(ls)-1]
			todayKline := quoteToKline(quote, last)

			nowDate := time.Now().Format(time.DateOnly)
			if last.Time.Format(time.DateOnly) == nowDate {
				// 今天数据已存在，替换为实时数据
				ls[len(ls)-1] = todayKline
			} else {
				// 追加今日K线
				ls = append(ls, todayKline)
			}
		}

		return ls, nil
	}
}

// ScreenResponse - 选股响应结构
// 用于封装选股结果，便于JSON序列化和传输
type ScreenResponse struct {
	Count   int       `json:"count"`   // 选股数量
	Time    string    `json:"time"`    // 选股时间
	Results []BuyItem `json:"results"` // 选股结果列表
}

// BuyItem - 买入信号条目
// 表示一只符合条件的股票信息
type BuyItem struct {
	Code  string  `json:"code"`  // 股票代码（带前缀）
	Time  string  `json:"time"`  // 信号产生时间
	Price float64 `json:"price"` // 当前价格
	Rise  float64 `json:"rise"`  // 涨幅百分比
}

// doScreen - 执行选股
// 返回选股结果和错误信息
//
// 执行流程：
// 1. 获取股票代码列表
// 2. 批量获取实时行情
// 3. 构造K线获取函数（历史+实时拼接）
// 4. 执行选股策略
// 5. 计算涨幅并格式化结果
func doScreen() (*ScreenResponse, error) {
	codes := common.GetNoPriceLimitCodes() // 获取股票代码
	now := time.Now()

	// 1. 拉取实时行情
	logs.Infof("[选股] 拉取实时行情，共 %d 只股票", len(codes))
	quoteMap, err := getRealtimeQuotes(codes)
	if err != nil {
		return nil, err
	}
	logs.Infof("[选股] 实时行情获取完成，有效 %d 只", len(quoteMap))

	// 2. 创建选股器
	s := core.Screen{
		Buyer:        common.DefaultBuyer,
		Codes:        codes,
		Goroutines:   10,
		GetDayKlines: makeIntradayGetDayKlines(quoteMap),
	}

	// 3. 执行选股
	buys, err := s.Run(codes, now)
	if err != nil {
		return nil, err
	}

	// 4. 格式化结果，计算涨幅
	items := make([]BuyItem, 0, len(buys))
	for _, b := range buys {
		riseRate := 0.0
		if b.Price > 0 {
			if quote := quoteMap[b.Code]; quote != nil && quote.K.Last > 0 {
				// 涨幅 = (现价 - 昨收) / 昨收 × 100%
				riseRate = (b.Price.Float64() - quote.K.Last.Float64()) / quote.K.Last.Float64() * 100
			}
		}
		items = append(items, BuyItem{
			Code:  b.Code,
			Time:  b.Time.Format(time.DateTime),
			Price: b.Price.Float64(),
			Rise:  riseRate,
		})
	}

	return &ScreenResponse{
		Count:   len(buys),
		Time:    now.Format(time.DateTime),
		Results: items,
	}, nil
}

// isTradingTime - 判断是否处于交易时间段
// 返回：true表示在交易时间内，false表示不在
//
// 交易时间：
// - 上午：09:30 - 11:30
// - 下午：13:00 - 15:00
func isTradingTime() bool {
	now := time.Now()
	hour, minute := now.Hour(), now.Minute()

	// 上午交易时段
	if hour >= 9 && hour <= 11 {
		if hour == 9 && minute >= 30 {
			return true
		}
		if hour == 10 {
			return true
		}
		if hour == 11 && minute <= 30 {
			return true
		}
	}

	// 下午交易时段（13:00 - 15:00）
	return hour >= 13 && hour < 15
}

// printResults - 在终端打印选股结果
// 参数：
//
//	results: 选股结果列表
//
// 输出格式：
// - 表格形式展示
// - 涨幅颜色：上涨绿色，下跌红色
func printResults(results []BuyItem) {
	if len(results) == 0 {
		fmt.Println("┌─────────────────────────────────────────────┐")
		fmt.Println("│              暂无符合条件的股票              │")
		fmt.Println("└─────────────────────────────────────────────┘")
		return
	}

	// 打印表头
	fmt.Println("┌────────────┬──────────┬────────┬──────────┐")
	fmt.Println("│    代码    │    价格   │  涨幅   │    时间   │")
	fmt.Println("├────────────┼──────────┼────────┼──────────┤")

	// 打印每行数据
	for _, item := range results {
		// 根据涨幅设置颜色：上涨绿色，下跌红色
		riseColor := "\033[32m" // 绿色
		if item.Rise < 0 {
			riseColor = "\033[31m" // 红色
		}
		fmt.Printf("│ %-10s │ %-8.2f │%s %+.2f%%\033[0m │ %-8s │\n",
			item.Code, item.Price, riseColor, item.Rise, item.Time[11:19])
	}

	// 打印表尾
	fmt.Println("└────────────┴──────────┴────────┴──────────┘")
}

// main - 主函数
// 程序入口，执行以下操作：
// 1. 立即执行一次选股并打印到终端
// 2. 启动后台定时选股任务（仅交易时间自动刷新终端）
// 3. 注册WebSocket接口供客户端订阅选股结果
// 4. 启动Web服务器
func main() {

	state := &ScreenState{subscribers: make(map[*fbr.Websocket]bool)}

	// 启动后台定时选股任务
	go state.startBackgroundScreen(time.Minute)

	s := fbr.Default(
		fbr.WithPort(frame.DefaultPort),
		fbr.WithALL("/screen", func(c fbr.Ctx) {
			c.Websocket(func(ws *fbr.Websocket) {
				state.addSubscriber(ws)          // 订阅
				defer state.removeSubscriber(ws) // 连接关闭时取消订阅
				// 如果有上次选股结果，立即推送给新客户端
				if state.lastResult != nil {
					data, _ := json.Marshal(map[string]any{"type": "screen_result", "result": state.lastResult})
					ws.WriteText(string(data))
				}
				// 保持连接（阻塞等待）
				ws.DiscardRead()
			})
		}),
	)

	// 启动Web服务器
	s.Run()

}

// ScreenState - 选股状态管理器
// 管理WebSocket订阅者和最新选股结果
type ScreenState struct {
	mu          sync.RWMutex            // 读写锁，保证并发安全
	lastResult  *ScreenResponse         // 最新选股结果
	subscribers map[*fbr.Websocket]bool // WebSocket订阅者集合
}

// addSubscriber - 添加订阅者
func (st *ScreenState) addSubscriber(ws *fbr.Websocket) {
	st.mu.Lock()
	st.subscribers[ws] = true
	st.mu.Unlock()
}

// removeSubscriber - 移除订阅者
func (st *ScreenState) removeSubscriber(ws *fbr.Websocket) {
	st.mu.Lock()
	delete(st.subscribers, ws)
	st.mu.Unlock()
}

// updateResult - 更新选股结果并广播
// 参数：
//
//	resp: 新的选股结果
//
// 执行操作：
// 1. 更新lastResult
// 2. 清屏并打印新结果到终端
// 3. 广播结果到所有WebSocket订阅者
func (st *ScreenState) updateResult(resp *ScreenResponse) {
	st.mu.Lock()
	st.lastResult = resp
	st.mu.Unlock()

	// 清屏并打印新结果
	fmt.Printf("\n\033[2J\033[H") // ANSI清屏指令
	fmt.Printf("[%s] 选出 %d 只股票\n\n", resp.Time, resp.Count)
	printResults(resp.Results)

	// 广播到所有订阅者
	data, _ := json.Marshal(map[string]any{"type": "screen_result", "result": resp})
	st.mu.RLock()
	defer st.mu.RUnlock()
	for ws := range st.subscribers {
		ws.WriteText(string(data))
	}
}

// startBackgroundScreen - 启动后台定时选股任务
// 参数：
//
//	interval: 选股间隔时间
//
// 执行逻辑：
// - 每分钟检查一次
// - 仅在交易日且交易时间内执行选股和刷新
// - 非交易时间：不执行自动刷新（数据不会变化）
// - 更新结果后广播到所有WebSocket订阅者
func (st *ScreenState) startBackgroundScreen(interval time.Duration) {

	// 执行选股并刷新终端
	st.doScreenAndPrint()

	logs.Infof("[选股] 后台选股任务启动，间隔 %v\n", interval)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		// 仅在交易时间内执行自动刷新
		if !common.Manage.Workday.TodayIs() || !isTradingTime() {
			continue
		}

		// 执行选股并刷新终端
		st.doScreenAndPrint()
	}
}

func (st *ScreenState) doScreenAndPrint() {
	// 执行选股并刷新终端
	if resp, err := doScreen(); err != nil {
		logs.Errf("[选股] 执行失败: %v", err)
	} else {
		logs.Infof("[选股] 选出 %d 只股票", resp.Count)
		st.updateResult(resp)
	}
}
