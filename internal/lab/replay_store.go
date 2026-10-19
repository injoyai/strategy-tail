package lab

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/injoyai/strategy-tail/lib/extend"
)

// replay_store.go 回放文件保存与读取（docs/plans/2026-10-18-backtest-replay.md §2、R12/R13）。
//
// 目录布局：output/trades/<RunID>/replay/variant-<index>.json 与
// kline/<code>.json。个股 K 线为跨变体共享快照，只保存实际买入过的股票
// （取自 runAccountBacktest 提供的已加载行情副本，含前导历史，MACD 用完整
// 区间因果计算）；行情更新后快照不随之变化，历史报告图表不漂移。
// 全部读取经过 os.OpenRoot("output/trades") 边界。

// replayVersionError 未知回放数据版本；文案直接面向用户（提示重新回测）。
type replayVersionError struct{ got int }

func (e *replayVersionError) Error() string {
	return fmt.Sprintf("回放数据版本不受支持: %d，请重新回测", e.got)
}

// saveReplayFiles 保存回放明细与个股 K 线快照到报告目录。
// 全部变体均未记录（旧路径/非账户模式）时不创建任何回放文件。
func saveReplayFiles(dir string, replays []*ReplayVariant, loaded map[string]extend.Klines) error {
	codes := replayCodes(replays)
	for _, code := range codes {
		if len(loaded[code]) == 0 {
			return fmt.Errorf("缺少 %s 的回放行情快照，报告未发布", code)
		}
	}
	// 回放明细先行落盘（R12：明细不完整则本次保存整体失败，report.json 不发布）。
	var anyReplay bool
	for _, rv := range replays {
		if rv != nil {
			anyReplay = true
			break
		}
	}
	if anyReplay {
		if err := os.MkdirAll(filepath.Join(dir, "replay"), 0755); err != nil {
			return err
		}
		for _, rv := range replays {
			if rv == nil {
				continue
			}
			buf, err := json.MarshalIndent(rv, "", "  ")
			if err != nil {
				return err
			}
			name := "variant-" + strconv.Itoa(rv.VariantIndex) + ".json"
			if err := atomicWrite(filepath.Join(dir, "replay", name), buf); err != nil {
				return err
			}
		}
	}
	// K 线快照：只保存实际买入过的股票，跨变体共享。
	if len(codes) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(dir, "kline"), 0755); err != nil {
		return err
	}
	for _, code := range codes {
		ks := loaded[code]
		buf, err := json.MarshalIndent(klineSeries(ks), "", "  ")
		if err != nil {
			return err
		}
		if err := atomicWrite(filepath.Join(dir, "kline", code+".json"), buf); err != nil {
			return err
		}
	}
	return nil
}

// replayCodes 收集实际买入过的股票代码（去重排序）；成交事件与每日持仓
// 并集采集，避免持仓记录口径变化时漏存快照。
func replayCodes(replays []*ReplayVariant) []string {
	set := map[string]bool{}
	for _, rv := range replays {
		if rv == nil {
			continue
		}
		for _, d := range rv.Days {
			for _, h := range d.Holdings {
				set[h.Code] = true
			}
			for _, tr := range d.Trades {
				set[tr.Code] = true
			}
		}
	}
	if len(set) == 0 {
		return nil
	}
	codes := make([]string, 0, len(set))
	for c := range set {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	return codes
}

// readReplay 读取指定报告的变体回放明细；版本不受支持时明确报错。
func readReplay(id string, index int) (*ReplayVariant, error) {
	if !validReportID(id) {
		return nil, fmt.Errorf("非法报告 ID")
	}
	if index < 0 {
		return nil, fmt.Errorf("非法变体序号")
	}
	root, err := os.OpenRoot(filepath.Join("output", "trades"))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	name := "variant-" + strconv.Itoa(index) + ".json"
	data, err := root.ReadFile(filepath.Join(id, "replay", name))
	if err != nil {
		return nil, err
	}
	var rv ReplayVariant
	if err := json.Unmarshal(data, &rv); err != nil {
		return nil, err
	}
	if rv.Version != replayVersion {
		return nil, &replayVersionError{got: rv.Version}
	}
	if rv.VariantIndex != index || len(rv.Days) == 0 || len(rv.Dates) != len(rv.Days) {
		return nil, fmt.Errorf("回放数据损坏：变体或日期数量不一致")
	}
	for i, date := range rv.Dates {
		if date != rv.Days[i].Date || (i > 0 && date <= rv.Dates[i-1]) {
			return nil, fmt.Errorf("回放数据损坏：日期错配或未按顺序排列")
		}
	}
	return &rv, nil
}

// validReplayCode 回放快照文件名只允许字母数字（如 sh600000），阻断路径
// 注入；os.OpenRoot 已保证不逃逸，此处为显式第一层校验。
func validReplayCode(code string) bool {
	if code == "" || len(code) > 16 {
		return false
	}
	for _, r := range code {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		default:
			return false
		}
	}
	return true
}

// readReplayKline 读取报告目录内的个股 K 线快照（保存时的旧快照）。
func readReplayKline(id, code string) ([]klineRow, error) {
	if !validReportID(id) {
		return nil, fmt.Errorf("非法报告 ID")
	}
	if !validReplayCode(code) {
		return nil, fmt.Errorf("非法股票代码")
	}
	root, err := os.OpenRoot(filepath.Join("output", "trades"))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	data, err := root.ReadFile(filepath.Join(id, "kline", code+".json"))
	if err != nil {
		return nil, err
	}
	var rows []klineRow
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}
