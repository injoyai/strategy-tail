package rule

// K 线字段操作数：附录 A 组件所需的当日/上一交易日原始字段。
//
// 命名即合同：Price 后缀为整数厘（protocol.Price，int64 精确比较），
// Float 后缀为浮点元；Prev 前缀取上一交易日（索引 n-2）。
// 空前缀返回无效值；Prev 系列在不足 2 根时返回无效值，不补 0。
// 此处只读取 K 线原始字段，不做任何窗口统计；指标公式统一见
// operand_factor.go（委托 strategies/factor 现有实现）。

// ClosePrice 今收（整数厘）。保留厘级整数比较的组件使用
// （如唐奇安下沿、收盘大于昨收/昨开的精确表达）。
func ClosePrice() Operand {
	return factorOperand{name: "今收(厘)", eval: func(in Input) Value {
		if len(in.Klines) == 0 {
			return InvalidValue()
		}
		return PriceValue(in.Klines[len(in.Klines)-1].Close)
	}}
}

// CloseFloat 今收（浮点元）。与浮点因子同域比较的组件使用
// （如收盘高于均线、回到均线）。
func CloseFloat() Operand {
	return factorOperand{name: "今收(元)", eval: func(in Input) Value {
		if len(in.Klines) == 0 {
			return InvalidValue()
		}
		return FloatValue(in.Klines[len(in.Klines)-1].Close.Float64())
	}}
}

// OpenPrice 今开（整数厘）。
func OpenPrice() Operand {
	return factorOperand{name: "今开(厘)", eval: func(in Input) Value {
		if len(in.Klines) == 0 {
			return InvalidValue()
		}
		return PriceValue(in.Klines[len(in.Klines)-1].Open)
	}}
}

// PrevClosePrice 昨收（整数厘，n-2 根）。不足 2 根返回无效值。
func PrevClosePrice() Operand {
	return factorOperand{name: "昨收(厘)", eval: func(in Input) Value {
		if len(in.Klines) < 2 {
			return InvalidValue()
		}
		return PriceValue(in.Klines[len(in.Klines)-2].Close)
	}}
}

// PrevOpenPrice 昨开（整数厘，n-2 根）。不足 2 根返回无效值。
func PrevOpenPrice() Operand {
	return factorOperand{name: "昨开(厘)", eval: func(in Input) Value {
		if len(in.Klines) < 2 {
			return InvalidValue()
		}
		return PriceValue(in.Klines[len(in.Klines)-2].Open)
	}}
}

// Volume 今量（手，浮点）。与 factor.A成交量 读取同一字段，口径一致。
func Volume() Operand {
	return factorOperand{name: "今量(手)", eval: func(in Input) Value {
		if len(in.Klines) == 0 {
			return InvalidValue()
		}
		return FloatValue(float64(in.Klines[len(in.Klines)-1].Volume))
	}}
}

// Turnover 当日成交额（浮点元）。与 factor.A成交额 读取同一字段，口径一致；
// 注意与 extend.Kline.Turnover（换手率）无关。
func Turnover() Operand {
	return factorOperand{name: "成交额(元)", eval: func(in Input) Value {
		if len(in.Klines) == 0 {
			return InvalidValue()
		}
		return FloatValue(in.Klines[len(in.Klines)-1].Amount.Float64())
	}}
}
