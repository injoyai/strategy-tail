package rule

// 持仓标量操作数（T4）：附录 C 持仓批次组件所需的当前时刻持仓指标。
//
// 命名即合同（同 operand_kline.go）：Price 后缀为整数厘（protocol.Price，
// int64 精确比较），Float 后缀为浮点元，Percent 后缀为浮点比率。
// 持仓不可用（买入侧求值或调用方未提供）一律返回无效值，
// 不伪造买入价或买入时间（Input.Position 契约）。
//
// 买入日定位算法不统一（计划第 4 节）：本文件不提供通用的买入日定位；
// PositionHoldingDays 的"顺序首个不早于买入时刻"是旧组件
// A持仓N天/A持仓N天尾盘 的既定口径，随构造器文档固化；
// 其余组件各自的日期串/逆序定位算法保留在各自门面与逃逸口中。

// PositionBuyPrice 买入价（整数厘）。保留厘级整数比较的组件使用
// （如买入次日跌破买入价的精确表达）。持仓不可用返回无效值。
// 不做 buy<=0 守卫：旧组件无此预检查，买入价 0 厘为有效值以保持精确语义。
func PositionBuyPrice() Operand {
	return factorOperand{name: "买入价(厘)", eval: func(in Input) Value {
		pos, ok := in.Position()
		if !ok {
			return InvalidValue()
		}
		return PriceValue(pos.Price)
	}}
}

// PositionBuyPriceFloat 买入价（浮点元）。与浮点因子/收益率同域比较的组件使用。
// 持仓不可用或买入价非正（<=0）返回无效值——非正买入价无法参与收益率
// 计算，统一在此归一为无效，调用方不必重复预检查。
func PositionBuyPriceFloat() Operand {
	return factorOperand{name: "买入价(元)", eval: func(in Input) Value {
		pos, ok := in.Position()
		if !ok || pos.Price.Float64() <= 0 {
			return InvalidValue()
		}
		return FloatValue(pos.Price.Float64())
	}}
}

// PositionProfitPercent 当前持仓收益率（浮点比率）：(今收-买入价)/买入价。
// 持仓不可用、买入价非正或空前缀返回无效值。浮点运算次序与旧组件
// 一致：今收与买入价先各自转元再相减相除，无中间取整。
func PositionProfitPercent() Operand {
	return positionProfitOperand{}
}

type positionProfitOperand struct{}

func (positionProfitOperand) Name() string { return "持仓收益率" }

func (positionProfitOperand) Value(in Input) Value {
	pos, ok := in.Position()
	if !ok || pos.Price.Float64() <= 0 || len(in.Klines) == 0 {
		return InvalidValue()
	}
	buy := pos.Price.Float64()
	last := in.Klines[len(in.Klines)-1].Close.Float64()
	return FloatValue((last - buy) / buy)
}

// PositionHoldingDays 已持仓交易日数：末根索引减买入日索引。
// 定位口径为旧组件 A持仓N天/A持仓N天尾盘 的既定算法：顺序扫描可见前缀，
// 首个不早于买入时刻（!k.Time.Before(买入时刻)）的 K 线即买入日；
// 找不到、空前缀或持仓不可用返回无效值。
func PositionHoldingDays() Operand {
	return factorOperand{name: "已持仓交易日数", eval: func(in Input) Value {
		pos, ok := in.Position()
		if !ok || len(in.Klines) == 0 {
			return InvalidValue()
		}
		for i, k := range in.Klines {
			if !k.Time.Before(pos.Time) {
				return FloatValue(float64(len(in.Klines) - i - 1))
			}
		}
		return InvalidValue()
	}}
}
