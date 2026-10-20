package lab

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/xuri/excelize/v2"
)

var errReportExcelTooLarge = errors.New("数据超过单个工作表上限（1048575 行），请缩小回测范围")

func (s *Server) handleReportExcel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validReportID(id) || id == "latest" {
		writeErr(w, http.StatusBadRequest, "请选择具体回测报告后导出")
		return
	}
	rep, err := readReport(id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeErr(w, http.StatusNotFound, "报告不存在，请刷新历史列表")
		} else {
			writeErr(w, http.StatusInternalServerError, "读取报告失败")
		}
		return
	}
	f, err := reportWorkbook(rep)
	if err != nil {
		if errors.Is(err, errReportExcelTooLarge) {
			writeErr(w, http.StatusBadRequest, err.Error())
		} else {
			writeErr(w, http.StatusInternalServerError, "生成 Excel 失败，请重试")
		}
		return
	}
	defer f.Close()
	// 完成编码后再发送响应头，生成失败不会下载一个损坏的工作簿。
	buf, err := f.WriteToBuffer()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "编码 Excel 失败，请重试")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="report.xlsx"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Write(buf.Bytes())
}

// 只投影报告中已冻结的数据；不在导出层重算绩效、成交或策略规则。
func reportWorkbook(rep *Report) (_ *excelize.File, err error) {
	f := excelize.NewFile()
	defer func() {
		if err != nil {
			f.Close()
		}
	}()
	header, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill: excelize.Fill{Type: "pattern", Color: []string{"15243A"}, Pattern: 1}})
	if err != nil {
		return nil, err
	}
	number, err := f.NewStyle(&excelize.Style{NumFmt: 2})
	if err != nil {
		return nil, err
	}
	params, err := reportExcelSheet(f, "策略参数", []any{"项目", "值（报告快照）"}, header)
	if err != nil {
		return nil, err
	}
	settings := []struct {
		label string
		value any
	}{
		{"运行配置", rep.Config}, {"策略配置", rep.StrategySpec},
		{"数据覆盖", rep.Coverage}, {"对比摘要", rep.Comparison},
	}
	paramRows := [][]any{{"开始时间", rep.StartedAt}, {"完成时间", rep.FinishedAt}, {"来源", rep.Source},
		{"回测起始年", rep.Config.StartYear}, {"回测结束年", rep.Config.EndYear}}
	for _, setting := range settings {
		data, marshalErr := json.MarshalIndent(setting.value, "", "  ")
		if marshalErr != nil {
			return nil, marshalErr
		}
		// Excel 单元格最多 32767 字符；分段保留完整快照，避免库静默截断。
		runes := []rune(string(data))
		for part := 1; len(runes) > 0; part++ {
			n := min(len(runes), 16000)
			paramRows = append(paramRows, []any{fmt.Sprintf("%s（%d）", setting.label, part), string(runes[:n])})
			runes = runes[n:]
		}
	}
	for i, row := range paramRows {
		if err = params.SetRow(fmt.Sprintf("A%d", i+2), row); err != nil {
			return nil, err
		}
	}
	if err = params.Flush(); err != nil {
		return nil, err
	}
	stats, err := reportExcelSheet(f, "绩效汇总", []any{"变体", "交易数", "盈利笔数", "亏损笔数", "胜率(%)", "盈亏比", "平均收益率(%)", "最大盈利(%)", "最大亏损(%)"}, header)
	if err != nil {
		return nil, err
	}
	for i, v := range rep.Variants {
		s := v.Stats
		var pf any = "—"
		if s.ProfitFactor != nil {
			pf = *s.ProfitFactor
		} else if s.WinSum > 0 && s.LossSum == 0 {
			pf = "∞"
		}
		if err = stats.SetRow(fmt.Sprintf("A%d", i+2), []any{v.Name, s.Total, s.Win, s.Loss, s.WinRate, pf, s.AvgProfit, s.MaxProfit, s.MaxLoss}); err != nil {
			return nil, err
		}
	}
	if err = stats.Flush(); err != nil {
		return nil, err
	}
	trades, err := reportExcelSheet(f, "交易明细", []any{"变体", "代码", "买入时间", "买入价(元)", "卖出时间", "卖出价(元)", "数量(股)", "盈亏(元)", "收益率(%)", "持仓天数", "成交类型"}, header)
	if err != nil {
		return nil, err
	}
	num := func(value float64) excelize.Cell { return excelize.Cell{StyleID: number, Value: value} }
	row := 2
	for _, v := range rep.Variants {
		for _, t := range v.Trades {
			if row > 1048576 {
				return nil, errReportExcelTooLarge
			}
			kind := "实际成交"
			if t.Virtual {
				kind = "期末估值（Virtual）"
			}
			if err = trades.SetRow(fmt.Sprintf("A%d", row), []any{v.Name, t.Code, t.BuyTime, num(t.BuyPrice), t.SellTime, num(t.SellPrice), t.Quantity, num(t.Profit), num(t.Rate), t.HoldingDays, kind}); err != nil {
				return nil, err
			}
			row++
		}
	}
	if err = trades.Flush(); err != nil {
		return nil, err
	}
	if err = reportExcelAccounts(f, rep, header); err != nil {
		return nil, err
	}
	if err = f.DeleteSheet("Sheet1"); err != nil {
		return nil, err
	}
	f.SetActiveSheet(0)
	return f, nil
}

func reportExcelAccounts(f *excelize.File, rep *Report, header int) error {
	var accounts *excelize.StreamWriter
	row := 2
	for _, v := range rep.Variants {
		if v.Account == nil {
			continue
		}
		if accounts == nil {
			var err error
			accounts, err = reportExcelSheet(f, "账户净值", []any{"变体", "日期", "资产(元)", "现金(元)", "持仓只数", "累计收益率(%)", "分钟缺失天数", "沿用旧价估值天数"}, header)
			if err != nil {
				return err
			}
		}
		for _, p := range v.Account.Points {
			if row > 1048576 {
				return errReportExcelTooLarge
			}
			if err := accounts.SetRow(fmt.Sprintf("A%d", row), []any{v.Name, p.Date, p.Equity, p.Cash, p.Holdings, p.Return, v.Account.MissingMinuteDays, v.Account.StaleValuationDays}); err != nil {
				return err
			}
			row++
		}
	}
	if accounts != nil {
		if err := accounts.Flush(); err != nil {
			return err
		}
	}
	if len(rep.Benchmark) > 0 {
		benchmark, err := reportExcelSheet(f, "上证基准", []any{"日期", "累计收益率(%)"}, header)
		if err != nil {
			return err
		}
		for i, p := range rep.Benchmark {
			if err := benchmark.SetRow(fmt.Sprintf("A%d", i+2), []any{p.Date, p.Return}); err != nil {
				return err
			}
		}
		return benchmark.Flush()
	}
	return nil
}

func reportExcelSheet(f *excelize.File, name string, headings []any, header int) (*excelize.StreamWriter, error) {
	if _, err := f.NewSheet(name); err != nil {
		return nil, err
	}
	if err := f.SetPanes(name, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"}); err != nil {
		return nil, err
	}
	sw, err := f.NewStreamWriter(name)
	if err != nil {
		return nil, err
	}
	if err := sw.SetColWidth(1, len(headings), 23); err != nil {
		return nil, err
	}
	if name == "策略参数" {
		if err := sw.SetColWidth(2, 2, 90); err != nil {
			return nil, err
		}
	}
	if err := sw.SetRow("A1", headings, excelize.RowOpts{StyleID: header}); err != nil {
		return nil, err
	}
	return sw, nil
}
