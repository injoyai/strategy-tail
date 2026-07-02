package core

import (
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
)

// Annotation 是K线图上的一个标注点。
// 策略通过实现 Visualizer 接口返回关键点，供可视化工具在图上标注。
type Annotation struct {
	Index int       // 在 dks 中的索引
	Time  time.Time // 时间，用于图表X轴对齐
	Price float64   // 标注价格（Y轴位置）
	Label string    // 显示文字，如 "H1"、"买入"
	Color string    // 点颜色，如 "#ef4444"（红）、"#22c55e"（绿）
	Note  string    // 补充说明，悬浮显示，如 "高点 12.50 @ 2026-06-10"
}

// Visualizer 策略可选实现，返回要在K线图上标注的关键点。
// 未实现此接口的策略只显示裸K线 + 诊断树。
type Visualizer interface {
	Annotate(code string, dks extend.Klines) []Annotation
}

// ExplainStep 是策略判定过程中的一条可读规则结果。
type ExplainStep struct {
	Name    string `json:"name"`
	Matched bool   `json:"matched"`
	Detail  string `json:"detail"`
}

// Explainer 策略可选实现，返回策略内部判定过程。
type Explainer interface {
	Explain(code string, dks extend.Klines) []ExplainStep
}
