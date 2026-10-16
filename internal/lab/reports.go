package lab

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/injoyai/strategy-tail/core"
)

type reportItem struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Script string `json:"script"`
	Time   string `json:"time"`
	Start  string `json:"startYear"`
	End    string `json:"endYear"`
}

func validReportID(id string) bool {
	return id != "" && !strings.ContainsAny(id, `/\`) && !strings.HasPrefix(id, ".")
}

func readReport(id string) (*Report, error) {
	if !validReportID(id) {
		return nil, fmt.Errorf("非法报告 ID")
	}
	root, err := os.OpenRoot(filepath.Join("output", "trades"))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err := root.Lstat(id)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("报告目录无效")
	}
	data, err := root.ReadFile(filepath.Join(id, "report.json"))
	if err != nil {
		return nil, err
	}
	var rep Report
	if err := json.Unmarshal(data, &rep); err != nil {
		return nil, err
	}
	return &rep, nil
}

func listReports() ([]reportItem, error) {
	items := []reportItem{}
	entries, err := os.ReadDir(filepath.Join("output", "trades"))
	if errors.Is(err, os.ErrNotExist) {
		return items, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() || !validReportID(e.Name()) {
			continue
		}
		rep, err := readReport(e.Name())
		if err != nil {
			continue
		}
		name := rep.Config.ScriptName
		if rep.Config.Builder != nil {
			name = rep.Config.Builder.Name
		} else if rep.StrategySpec != nil && rep.StrategySpec.Name != "" {
			name = rep.StrategySpec.Name
		}
		items = append(items, reportItem{ID: e.Name(), Name: name, Script: rep.Config.ScriptName,
			Time: rep.FinishedAt, Start: fmt.Sprint(rep.Config.StartYear), End: fmt.Sprint(rep.Config.EndYear)})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Time > items[j].Time })
	return items, nil
}

func (s *Server) handleDeleteReport(w http.ResponseWriter, r *http.Request) {
	if !requireSameOrigin(w, r) {
		return
	}
	id := r.PathValue("id")
	if !validReportID(id) || id == "latest" {
		writeErr(w, http.StatusBadRequest, "非法报告 ID")
		return
	}
	// 与保存报告共用任务锁，避免删除仍在发布的文件。
	if !s.runner.mu.TryLock() {
		writeErr(w, http.StatusConflict, "任务运行中，请完成后再删除回测结果")
		return
	}
	defer s.runner.mu.Unlock()
	rep, err := readReport(id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeErr(w, http.StatusNotFound, "报告不存在")
		} else {
			writeErr(w, http.StatusInternalServerError, "读取报告失败，未删除")
		}
		return
	}
	if len(rep.FinishedAt) < 19 || rep.RunID() != id {
		writeErr(w, http.StatusBadRequest, "报告目录与记录不一致，未删除")
		return
	}
	root, err := os.OpenRoot(filepath.Join("output", "trades"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "打开报告目录失败")
		return
	}
	defer root.Close()
	// 旧版 CSV/HTML 与其他运行共享策略目录，只删除本次运行的确切文件名。
	names := []string{core.TradesExportName(id+"_summary") + ".html"}
	for _, v := range rep.Variants {
		names = append(names, core.TradesExportName(id+"_"+v.Name)+".csv")
	}
	for _, name := range names {
		if err := root.Remove(filepath.Join(core.TradesExportName(rep.Config.ScriptName), name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			writeErr(w, http.StatusInternalServerError, "删除导出文件失败，请重试")
			return
		}
	}
	if err := root.RemoveAll(id); err != nil {
		writeErr(w, http.StatusInternalServerError, "删除报告失败，请重试")
		return
	}
	if latest := s.runner.LatestReport(); latest != nil && latest.RunID() == id {
		s.runner.lastReport.CompareAndSwap(latest, nil)
		s.runner.runID.Store(nil)
		if task := s.runner.task.Load(); task == nil || *task == "backtest" {
			idle := "idle"
			s.runner.state.Store(&idle)
		}
	}
	writeJSON(w, map[string]bool{"ok": true})
}
