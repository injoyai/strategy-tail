package extend

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/injoyai/bar"
	"github.com/injoyai/conv"
	"github.com/injoyai/logs"
	"github.com/injoyai/strategy-tail/internal/marketstore"
	"github.com/injoyai/strategy-tail/internal/update"
	"github.com/injoyai/tdx/v2"
	"github.com/injoyai/tdx/v2/extend/metadata"
	"github.com/injoyai/tdx/v2/extend/pull"
	"github.com/injoyai/tdx/v2/extend/storage"
	"github.com/robfig/cron/v3"
)

const (
	DefaultDatabaseDir = "./data/database"
	Day                = "day"
	Minute             = "minute"

	DirMinute         = "min-kline"
	DirDay            = "day-kline"
	updateCloseHour   = 15
	updateCloseMinute = 30
)

// ErrNotEnoughKline K线数据不足,无法按指定序号取值
var ErrNotEnoughKline = errors.New("k线数据不足")

var ErrReadOnlyMarket = errors.New("行情由外部项目更新，本项目仅提供读取")

var ErrMarketStoreRequired = errors.New("行情存储未配置，请配置 MySQL")

// 主板前缀 默认仅拉取沪深主板股票（与 common.go 中 Market沪深主板.Prefixes 保持一致）。
// 北交所/科创板/创业板/ETF 数据已清理，不再拉取，避免定时任务重新下载。
var 主板前缀 = []string{"sh60", "sz00"}

// 指数白名单 仅保留上证指数与沪深300（回测基准依赖），日线与分钟线均拉取；其余指数数据已清理。
var 指数白名单 = []string{"sh000001", "sh000300"}

// hasAnyPrefix 判断 code 是否以任一前缀开头。
func hasAnyPrefix(code string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(code, p) {
			return true
		}
	}
	return false
}

type PullKlineConfig struct {
	Store             storage.BarStore      // Optional source-time storage; owned by the caller.
	SourceDSN         string                // Required when Store is not injected.
	Codes             []string              //操作代码
	Types             []string              //更新类型
	Dir               string                // Directory for the application update lock.
	Goroutines        int                   //协程数量
	StartAt           time.Time             //数据开始时间
	Equity            func() *metadata.Gbbq // Optional point-in-time equity lookup for strategy reads.
	ValuationDatabase string                // 非空时，每个收盘批次额外采集沪深主板估值快照。
}

func NewPullKline(cfg PullKlineConfig) (*PullKline, error) {
	if cfg.Goroutines <= 0 {
		cfg.Goroutines = 1
	}
	if len(cfg.Dir) == 0 {
		cfg.Dir = filepath.Join(DefaultDatabaseDir)
	}
	var owned *marketstore.MySQLStore
	if cfg.Store == nil {
		dsn := strings.TrimSpace(cfg.SourceDSN)
		if dsn == "" {
			return nil, ErrMarketStoreRequired
		}
		store, err := marketstore.NewMySQLStore(dsn)
		if err != nil {
			return nil, fmt.Errorf("连接源行情 MySQL 失败: %w", err)
		}
		cfg.Store, owned = store, store
	}
	updated, err := update.NewWithStore(cfg.Store, updateCloseHour, updateCloseMinute)
	if err != nil {
		if owned != nil {
			_ = owned.Close()
		}
		return nil, err
	}
	cfg.SourceDSN = "" // The open store owns the connection settings.
	return &PullKline{
		Config:  cfg,
		Updated: updated,
		Types:   cfg.Types,
		owned:   owned,
	}, nil
}

type PullKline struct {
	Config  PullKlineConfig
	Updated *update.Tracker
	Types   []string
	owned   *marketstore.MySQLStore
	reader  *marketstore.MySQLReader
}

// OpenReadOnlyPullKline opens the configured database without a writable store.
func OpenReadOnlyPullKline(dsn string) (*PullKline, error) {
	reader, err := marketstore.OpenMySQLReader(dsn)
	if err != nil {
		return nil, err
	}
	return &PullKline{reader: reader}, nil
}

func (this *PullKline) Close() error {
	if this.reader != nil {
		return this.reader.Close()
	}
	if this.owned == nil {
		return nil
	}
	return this.owned.Close()
}

func (this *PullKline) Run(m *MarketData, spec string) error {
	if this.reader != nil {
		return ErrReadOnlyMarket
	}
	this.Update(m, true)
	cr := cron.New(cron.WithSeconds())
	_, err := cr.AddFunc(spec, func() { this.Update(m) })
	cr.Start()
	return err
}

// UpdateProgress counts completed code/type jobs after their final retry.
// Callbacks are serialized; Failed jobs never mark the batch as up to date.
type UpdateProgress struct {
	Stage      string `json:"stage"`
	Done       int    `json:"done"`
	Total      int    `json:"total"`
	Code       string `json:"code"`
	Failed     int    `json:"failed"`
	Skipped    bool   `json:"skipped"`
	Message    string `json:"message,omitempty"`
	StageDone  int    `json:"stageDone"`
	StageTotal int    `json:"stageTotal"`
}

func (this *PullKline) Update(m *MarketData, must ...bool) error {
	return this.UpdateWithProgress(m, len(must) > 0 && must[0], nil)
}

// manual bypasses the trading-day check, but keeps the existing freshness check.
func (this *PullKline) UpdateWithProgress(m *MarketData, manual bool, report func(UpdateProgress)) error {
	if this.reader != nil {
		return ErrReadOnlyMarket
	}
	release, err := lockUpdates(this.Config.Dir)
	if err != nil {
		return err
	}
	defer release()
	if m == nil || this.Updated == nil || this.Config.Store == nil {
		return fmt.Errorf("行情更新器尚未初始化")
	}
	progress := UpdateProgress{Stage: "checking"}
	emit := func() {
		if report != nil {
			report(progress)
		}
	}
	emit()
	if !manual {
		trading, known, err := m.CheckTradingDay(time.Now())
		if err != nil {
			return fmt.Errorf("刷新交易日信息: %w", err)
		}
		if !trading {
			progress.Skipped, progress.Message = true, "今天不是交易日，本次自动更新已跳过"
			if !known {
				progress.Message = "通达信日历尚未确认今天为交易日，本次自动更新已跳过，可手动重试"
			}
			emit()
			return nil
		}
	}
	updated, err := this.Updated.IsUpdated(this.sourceUpdateKey())
	if err != nil {
		return err
	}
	if updated {
		progress.Skipped, progress.Message = true, "最近收盘批次已完整更新，无需重复下载"
		progress, err = this.updateValuation(m, progress, report)
		emit()
		return err
	}
	types := append([]string(nil), this.Types...)
	if len(types) == 0 {
		return fmt.Errorf("未配置日线或分钟线更新类型")
	}
	seen := map[string]bool{}
	for _, kind := range types {
		if (kind != Day && kind != Minute) || seen[kind] {
			return fmt.Errorf("无效或重复的更新类型: %s", kind)
		}
		seen[kind] = true
	}
	codes := append([]string(nil), this.Config.Codes...)
	if len(codes) == 0 {
		if m.Codes == nil {
			return fmt.Errorf("行情代码列表尚未就绪")
		}
		for _, code := range m.Codes.GetStockCodes() {
			if hasAnyPrefix(code, 主板前缀) {
				codes = append(codes, code)
			}
		}
		for _, code := range m.Codes.GetIndexCodes() {
			if hasAnyPrefix(code, 指数白名单) {
				codes = append(codes, code)
			}
		}
	}
	if len(codes) == 0 {
		return fmt.Errorf("没有可更新的股票或指数")
	}
	progress.Total = len(codes) * len(types)
	if this.Config.ValuationDatabase != "" {
		progress.Total++
	}
	start := ""
	if !this.Config.StartAt.IsZero() {
		start = this.Config.StartAt.Format("20060102")
	}
	service, storeErr := pull.New(&pull.Config{Store: this.Config.Store, Codes: codes, Pool: m.ClientPool, StartAt: start, Day: seen[Day], Minute: seen[Minute]})
	if service != nil {
		defer service.Close()
	}
	var updateErrors []error
	for _, kind := range types {
		progress.Stage, progress.Code = kind, ""
		progress.StageDone, progress.StageTotal = 0, len(codes)
		emit()
		onCode := func(code string, err error) {
			progress.Done++
			progress.StageDone++
			progress.Code = code
			if err != nil {
				progress.Failed++
			}
			emit()
		}
		err = runUpdateCodes(codes, this.Config.Goroutines, func(code string) error {
			if storeErr != nil {
				return storeErr
			}
			return fetchSource(service, m, code, kind)
		}, onCode)
		if err != nil {
			updateErrors = append(updateErrors, fmt.Errorf("%s: %w", kind, err))
		}
	}
	progress, err = this.updateValuation(m, progress, report)
	if err != nil {
		updateErrors = append(updateErrors, err)
	}
	if len(updateErrors) > 0 {
		progress.Message = fmt.Sprintf("全部更新单元已处理，失败 %d/%d，可手动重试", progress.Failed, progress.Total)
		emit()
		return fmt.Errorf("%s: %w", progress.Message, errors.Join(updateErrors...))
	}
	progress.Stage, progress.Code = "finalize", ""
	progress.StageDone, progress.StageTotal = 0, 0
	emit()
	if err := this.Updated.Mark("pull"); err != nil {
		return err
	}
	return this.Updated.Mark(this.sourceUpdateKey())
}

func (this *PullKline) Name() string {
	return "拉取k线数据"
}

// DayKline 获取任意一天的数据,默认最新一天,即n=-1,同python支持负数
func (this *PullKline) DayKline(code string, n ...int) (*Kline, error) {
	ks, err := this.DayKlines(code, time.Time{}, time.Now())
	if err != nil {
		return nil, err
	}

	_n := conv.Default(-1, n...)
	if _n >= 0 {
		//数量不满足
		if len(ks) <= _n {
			return nil, ErrNotEnoughKline
		}
		return ks[_n], nil
	}

	if len(ks) < -_n {
		return nil, ErrNotEnoughKline
	}

	return ks[len(ks)+_n], nil
}

// Unlike bar.GoRetry, this retains the terminal error and reports each job once.
func runUpdateCodes(codes []string, workers int, update func(string) error, report ...func(string, error)) error {
	if workers <= 0 {
		workers = 1
	}
	b := bar.NewCoroutine(len(codes), workers, bar.WithPrefix("[xx000000]"))
	defer b.Close()
	var mu sync.Mutex
	var firstErr error
	failed := 0
	for _, code := range codes {
		b.Go(func() {
			var err error
			for attempt := 0; attempt < tdx.DefaultRetry; attempt++ {
				err = update(code)
				if err == nil {
					break
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed++
				if firstErr == nil {
					firstErr = fmt.Errorf("%s: %w", code, err)
				}
				logs.Err(err)
			}
			for _, onCode := range report {
				if onCode != nil {
					onCode(code, err)
				}
			}
		})
	}
	b.Wait()
	if firstErr != nil {
		return fmt.Errorf("更新失败 %d/%d（首个错误：%w）", failed, len(codes), firstErr)
	}
	return nil
}
