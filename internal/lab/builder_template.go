package lab

// builder_template.go 搭建器内置模板（设计文档 §7）。
//
// MACD 模板与 cmd/backtest 的 buyer()/seller() 构造等价：
// 目录工厂按参数缺省零值构造组件（MACD 参数不传、Lookback 不传），
// 与 MACDParameters.buyer() 的零值字段形态一致，由测试 DeepEqual
// 与隔离数据逐笔比较保证。
//
// 注意：params 中的数值必须是 float64 字面量（JSON 解码后的形态，
// 工厂 numArg 做 float64 断言）；MACD fast/slow/signal 与 buy_ma_up
// lookback 保持缺省——目录默认值经前端填充后与组件内部零值默认
// 行为等价，但模板原样构造须与 macd_backtest.buyer() 逐字段一致。

// BuilderTemplate 内置模板条目。
type BuilderTemplate struct {
	ID   string      `json:"id"`
	Name string      `json:"name"`
	Desc string      `json:"desc,omitempty"`
	Spec BuilderSpec `json:"spec"`
}

// builderCond 构造条件节点。
func builderCond(componentID string, params map[string]any) *BuilderNode {
	return &BuilderNode{Type: "condition", ComponentID: componentID, Params: params}
}

// macdTemplateSpec MACD 反转模板（与 defaultMACDConfig 的 buyer()/seller() 等价）。
// 参数对照 macd_backtest.go：MinFloatCap 400、MaxPrice 120、NegativeMinDays 6、
// Rising 2~2、PriceMA 30、Trend 30/20 斜率 0.0005（界面百分数 0.05）、
// SellLookback 10。
func macdTemplateSpec() BuilderSpec {
	buy := &BuilderNode{Type: "group", Op: "and", Children: []*BuilderNode{
		builderCond("buy_market_cap", map[string]any{"min": 400.0, "max": 0.0}),
		builderCond("buy_price", map[string]any{"min": 0.0, "max": 120.0}),
		builderCond("buy_filter_limit_up", nil),
		builderCond("buy_macd_negative", map[string]any{"minDays": 6.0, "maxDays": 0.0}),
		builderCond("buy_macd_rising", map[string]any{"minDays": 2.0, "maxDays": 2.0, "lookback": 0.0}),
		builderCond("buy_price_above_ma", map[string]any{"period": 30.0}),
		{Type: "group", Op: "and", Children: []*BuilderNode{
			builderCond("buy_ma_up", map[string]any{"period": 30.0, "lookback": 0.0, "minSlope": 0.05}),
			builderCond("buy_ma_up", map[string]any{"period": 20.0, "lookback": 0.0, "minSlope": 0.05}),
		}},
	}}
	sellSignal := &BuilderNode{Type: "group", Op: "or", Children: []*BuilderNode{
		builderCond("sell_macd_reversal", map[string]any{"lookback": 10.0, "minDiff": 0.0}),
	}}
	return BuilderSpec{
		Version:    builderSpecV1,
		Name:       "MACD反转",
		Buy:        buy,
		SellSignal: sellSignal,
	}
}

// BuilderTemplates 内置模板列表（GET /api/builder/templates）。
func BuilderTemplates() []BuilderTemplate {
	return []BuilderTemplate{
		{
			ID:   "macd_reversal",
			Name: "MACD反转",
			Desc: "与 MACD 参数回测默认策略等价：市值/价格/涨停过滤 + MACD 负柱连涨 + 双均线向上趋势确认，MACD 反转卖出",
			Spec: macdTemplateSpec(),
		},
	}
}
