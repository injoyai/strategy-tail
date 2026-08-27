package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/logs"
)

// 变体定义
type variant struct {
	Mode      string
	Label     string
	TdxVolume bool
}

func main() {
	codes := common.GetNoPriceLimitCodes() // 沪深主板
	years := []int{2026}

	variants := []variant{
		{Mode: "confirm5", Label: "去下跌过滤+站上5日线+通达信倍量", TdxVolume: true},
		{Mode: "confirm5", Label: "去下跌过滤+站上5日线+3倍量"},
		{Mode: "deep", Label: "深度回撤(40%)"},
		{Mode: "mild", Label: "温和回撤(25%)"},
		{Mode: "none", Label: "不做下跌过滤"},
	}

	var rep report
	for _, v := range variants {
		buyer := buy.A下跌企稳倍量{Mode: v.Mode, TdxVolume: v.TdxVolume}
		a := core.ForwardReturnAnalysis{
			Buyer:        buyer,
			Codes:        codes,
			Years:        years,
			GetDayKlines: common.Pull.DayKlines,
			ForwardDays:  core.DefaultForwardDays(),
			Goroutines:   common.DefaultGoroutines * 2,
			KlineBefore:  30,
			KlineAfter:   30,
			CodeNames:    common.Manage.Codes.GetName,
		}
		allReturns, summaries := a.Collect()

		// 控制台输出
		core.PrintForwardReturnSummary(buyer.Name(), summaries)

		items := make([]returnItem, 0, len(allReturns))
		for _, fr := range allReturns {
			ret := make(map[string]float64, len(fr.Returns))
			for k, v := range fr.Returns {
				ret[strconv.Itoa(k)] = v
			}
			items = append(items, returnItem{
				Code:     fr.Code,
				CodeName: fr.CodeName,
				Date:     fr.BuyTime.Format("2006-01-02"),
				Returns:  ret,
			})
		}

		rep.Variants = append(rep.Variants, variantReport{
			Key:       v.Label,
			Mode:      v.Mode,
			Label:     v.Label,
			Name:      buyer.Name(),
			Count:     len(allReturns),
			Summaries: summaries,
			Items:     items,
		})
	}

	// 导出 JSON
	dir := filepath.Join("output", "future-stabilize")
	os.MkdirAll(dir, 0755)
	bs, _ := json.MarshalIndent(rep, "", "  ")
	out := filepath.Join(dir, "stabilize_report.json")
	os.WriteFile(out, bs, 0644)
	logs.Info("JSON报告已生成: " + out)
}

type report struct {
	Variants []variantReport `json:"variants"`
}

type variantReport struct {
	Key       string                      `json:"key"`
	Mode      string                      `json:"mode"`
	Label     string                      `json:"label"`
	Name      string                      `json:"name"`
	Count     int                         `json:"count"`
	Summaries []core.ForwardReturnSummary `json:"summaries"`
	Items     []returnItem                `json:"items"`
}

type returnItem struct {
	Code     string             `json:"code"`
	CodeName string             `json:"codeName"`
	Date     string             `json:"date"`
	Returns  map[string]float64 `json:"returns"`
}
