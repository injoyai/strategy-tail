package core

import (
	"fmt"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
)

// DiagnoseResult 单层诊断结果。
type DiagnoseResult struct {
	Name     string           // 条件名称
	Matched  bool             // 是否满足
	Children []DiagnoseResult // 子条件诊断（组合策略才有）
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
func (d *Diagnoser) Check(code string) (matched bool, result DiagnoseResult) {
	now := time.Now()
	dks, err := d.GetDayKlines(code, now.AddDate(-1, 0, 0), now)
	if err != nil {
		return false, DiagnoseResult{Name: fmt.Sprintf("数据错误: %v", err)}
	}
	matched, result = diagnose(d.Buyer, code, dks)
	return
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
