package core

import (
	"fmt"
	"time"

	"github.com/injoyai/conv"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// DiagnoseResult 单层诊断结果。
type DiagnoseResult struct {
	Name     string           `json:"name"`     // 条件名称
	Matched  bool             `json:"matched"`  // 是否满足
	Children []DiagnoseResult `json:"children"` // 子条件诊断（组合策略才有）
}

// Diagnoser 策略诊断器：配置好 Buyer + 数据源，传入代码即可逐层诊断。
// 用法：
//
//	d := &core.Diagnoser{
//	    Buyer:        buy.And{...},
//	    GetDayKlines: common.Pull.DayKlines,
//	}
//	matched, result := d.Check("sh600000")
//	fmt.Println(result)
type Diagnoser struct {
	Buyer        Buyer
	GetDayKlines func(code string, start, end time.Time) (extend.Klines, error)
}

// Check 传入股票代码，自动拉取近 1 年日线，返回是否匹配 + 诊断树。
func (d *Diagnoser) Check(code string, at ...time.Time) (matched bool, result DiagnoseResult) {
	now := conv.Default(time.Now(), at...)
	dks, err := d.GetDayKlines(code, now.AddDate(-1, 0, 0), now)
	if err != nil {
		return false, DiagnoseResult{Name: fmt.Sprintf("数据错误: %v", err)}
	}
	matched, result = Diagnose(d.Buyer, code, dks)
	return
}

// Diagnose 对给定策略和数据执行递归诊断，返回是否匹配 + 诊断树。
// 与 Diagnoser.Check 不同，此函数直接使用调用方提供的 K 线数据，无需配置 GetDayKlines。
func Diagnose(b Buyer, code string, dks extend.Klines) (matched bool, result DiagnoseResult) {
	return diagnose(b, code, dks)
}

// diagnose 递归诊断。对实现 CompositeBuyer 接口的组合策略自动展开子节点。
func diagnose(b Buyer, code string, dks extend.Klines) (matched bool, result DiagnoseResult) {
	result.Name = b.Name()
	result.Matched = b.Buy(code, dks)

	if cb, ok := b.(CompositeBuyer); ok {
		for _, child := range cb.Children() {
			_, cr := diagnose(child, code, dks)
			result.Children = append(result.Children, cr)
		}
	}

	return result.Matched, result
}

// String 将诊断树渲染为可读的多行缩进文本。
func (r DiagnoseResult) String() string {
	return renderNode(r, "")
}

func renderNode(r DiagnoseResult, indent string) string {
	mark := "\u2717" // ✗
	if r.Matched {
		mark = "\u2713" // ✓
	}
	out := indent + mark + " " + r.Name + "\n"
	for _, child := range r.Children {
		out += renderNode(child, indent+"  ")
	}
	return out
}
