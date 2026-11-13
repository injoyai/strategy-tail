package lab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/lab/builder"
	"github.com/injoyai/strategy-tail/internal/screener"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/v2/protocol"
)

type screenStrategy struct {
	ID         string                 `json:"id"`
	Name       string                 `json:"name"`
	Enabled    bool                   `json:"enabled"`
	Buy        *builder.BuilderNode   `json:"buy,omitempty"`
	SellSignal *builder.BuilderNode   `json:"sellSignal,omitempty"`
	Risks      []*builder.BuilderNode `json:"risks,omitempty"`
}

type screenConfig struct {
	Version    int              `json:"version"`
	Strategies []screenStrategy `json:"strategies"`
}

type screenState struct {
	Config    screenConfig        `json:"config"`
	Running   bool                `json:"running"`
	Preview   *screenScanProgress `json:"preview,omitempty"`
	Error     string              `json:"error,omitempty"`
	LiveError string              `json:"liveError,omitempty"`
	Result    *screener.Result    `json:"result,omitempty"`
}

type screenScanProgress struct {
	Done        int    `json:"done"`
	Total       int    `json:"total"`
	CurrentCode string `json:"currentCode"`
}

type screenService struct {
	mu      sync.Mutex
	path    string
	market  screener.Market
	now     func() time.Time
	scanner screener.Scanner
	running bool
	preview *screenScanProgress
	version int
	result  *screener.Result
	lastErr string

	historyRunning bool
	lookback       int
	legacyPath     string
	liveMinutes    screenMinutes
	liveError      string
}

func newScreenService(path string) *screenService {
	return &screenService{path: path, market: labScreenMarket{}, now: time.Now, lookback: 60}
}

func (s *screenService) load() (screenConfig, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		config := screenConfig{Version: 1}
		for _, preset := range screener.Presets(nil) {
			config.Strategies = append(config.Strategies, screenStrategy{ID: preset.Key, Name: preset.Name})
		}
		return config, nil
	}
	if err != nil {
		return screenConfig{}, err
	}
	var config screenConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return screenConfig{}, err
	}
	if config.Version < 1 || len(config.Strategies) == 0 {
		return screenConfig{}, errors.New("选股配置格式无效")
	}
	return config, nil
}

func compileScreen(config screenConfig) (map[string]bool, []screener.Rule, error) {
	presets := make(map[string]screener.Preset)
	for _, preset := range screener.Presets(nil) {
		presets[preset.Key] = preset
	}
	enabled := make(map[string]bool)
	custom := make([]screener.Rule, 0)
	seenIDs, seenNames := make(map[string]bool), make(map[string]bool)
	if len(config.Strategies) > 32 {
		return nil, nil, fmt.Errorf("最多可保存 32 套选股策略")
	}
	for _, strategy := range config.Strategies {
		if seenIDs[strategy.ID] {
			return nil, nil, fmt.Errorf("选股策略 ID 重复")
		}
		seenIDs[strategy.ID] = true
		name := strings.TrimSpace(strategy.Name)
		if seenNames[name] {
			return nil, nil, fmt.Errorf("选股策略名称重复：%s", name)
		}
		seenNames[name] = true
		if preset, ok := presets[strategy.ID]; ok {
			if strategy.Name != preset.Name || strategy.Buy != nil || strategy.SellSignal != nil || len(strategy.Risks) != 0 {
				return nil, nil, fmt.Errorf("内置策略仅可调整启用状态：%s", preset.Name)
			}
			if strategy.Enabled {
				enabled[strategy.ID] = true
			}
			continue
		}
		if !builderStrategyIDRe.MatchString(strategy.ID) {
			return nil, nil, fmt.Errorf("自定义选股策略 ID 无效")
		}
		spec := builder.BuilderSpec{Version: builder.SpecVersion, Name: name, Buy: strategy.Buy, SellSignal: strategy.SellSignal, Risks: strategy.Risks, HoldToEnd: true}
		if err := spec.Validate("run"); err != nil {
			return nil, nil, fmt.Errorf("%s：%w", name, err)
		}
		for _, node := range []*builder.BuilderNode{strategy.Buy, strategy.SellSignal} {
			if node != nil {
				if err := validateScreenLookback(node); err != nil {
					return nil, nil, fmt.Errorf("%s：%w", name, err)
				}
			}
		}
		buyer, err := spec.Buyer()
		if err != nil {
			return nil, nil, fmt.Errorf("%s：%w", name, err)
		}
		var seller core.Seller
		if spec.SellSignal != nil || len(spec.Risks) > 0 {
			seller, err = spec.Seller()
			if err != nil {
				return nil, nil, fmt.Errorf("%s：%w", name, err)
			}
		}
		if strategy.Enabled {
			custom = append(custom, screener.Rule{ID: strategy.ID, Name: name, Buyer: buyer, Seller: seller})
		}
	}
	for id := range presets {
		if !seenIDs[id] {
			return nil, nil, fmt.Errorf("内置选股策略不可移除")
		}
	}
	return enabled, custom, nil
}

// Screening loads up to 1,200 daily bars. Leave room for factors that need
// an additional previous observation or a warm-up window.
func validateScreenLookback(node *builder.BuilderNode) error {
	if node.Factor != nil && node.Factor.Days > 1000 {
		return errors.New("选股因子的回看天数不能超过 1000")
	}
	for _, child := range node.Children {
		if err := validateScreenLookback(child); err != nil {
			return err
		}
	}
	return nil
}

func (s *screenService) state(config screenConfig) screenState {
	state := screenState{Config: config, Running: s.running, Preview: s.preview, LiveError: s.liveError}
	if s.version == config.Version {
		state.Result, state.Error = s.result, s.lastErr
	}
	return state
}

// start is called under mu. A configuration change during a scan queues the
// newest version automatically and never exposes the superseded result.
func (s *screenService) start(config screenConfig) {
	if s.running {
		return
	}
	if !hasEnabledScreenStrategy(config) {
		return
	}
	s.running = true
	s.version = config.Version
	s.lastErr = ""
	s.preview = &screenScanProgress{}
	go s.run(context.Background(), config, func(done, total int, code string) {
		s.mu.Lock()
		s.preview = &screenScanProgress{Done: done, Total: total, CurrentCode: code}
		s.mu.Unlock()
	})
}

func hasEnabledScreenStrategy(config screenConfig) bool {
	for _, st := range config.Strategies {
		if st.Enabled {
			return true
		}
	}
	return false
}

func (s *screenService) run(ctx context.Context, config screenConfig, progress screener.ScanProgress) error {
	presets, custom, err := compileScreen(config)
	var result screener.Result
	if err == nil {
		result, err = s.scanner.ScanWithProgress(ctx, s.market, s.now(), presets, custom, common.DefaultGoroutines, progress)
	}
	if err != nil && ctx.Err() == nil {
		log.Printf("[screen] 选股失败: %v", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = false
	s.preview = nil
	current, loadErr := s.load()
	if loadErr != nil {
		log.Printf("[screen] 读取策略配置失败: %v", loadErr)
		return errors.New("无法读取选股配置")
	}
	if current.Version != config.Version {
		s.result = nil
		s.start(current)
		return errors.New("选股策略配置已变化")
	}
	if ctx.Err() != nil {
		return errStopped
	}
	if err != nil {
		s.lastErr = "选股失败，请检查行情或策略配置后重试。"
		return errors.New(s.lastErr)
	}
	s.result = &result
	return nil
}

// RunScreen scans enabled strategies at startup, during trading, and once
// the current session's official day bars have been published.
func (s *Server) RunScreen(ctx context.Context, interval time.Duration, lookback int) {
	if interval <= 0 {
		interval = time.Minute
	}
	screen := s.screen
	screen.mu.Lock()
	screen.lookback = min(max(1, lookback), 240)
	screen.mu.Unlock()
	go screen.runLive(ctx, interval)
	tick := time.NewTicker(interval)
	defer tick.Stop()
	screen.startSaved(false)
	for {
		select {
		case <-ctx.Done():
			return
		case at := <-tick.C:
			at = at.In(protocol.Location())
			if at.Weekday() == time.Saturday || at.Weekday() == time.Sunday {
				continue
			}
			if common.IsTradingTime() {
				screen.startSaved(false)
			} else if at.Hour() >= 15 {
				screen.startSaved(true)
			}
		}
	}
}

func (s *screenService) startSaved(afterClose bool) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	config, err := s.load()
	if err != nil {
		s.mu.Unlock()
		log.Printf("[screen] 读取策略配置失败: %v", err)
		return
	}
	if !afterClose {
		s.start(config)
		s.mu.Unlock()
		return
	}

	date := s.now().In(protocol.Location()).Format(time.DateOnly)
	alreadyScanned := s.version == config.Version && s.result != nil &&
		s.result.DailyAvailable && strings.HasPrefix(s.result.AsOf, date)
	s.mu.Unlock()
	if alreadyScanned || !hasEnabledScreenStrategy(config) {
		return
	}
	latest, err := s.market.LatestDayDate(screener.BenchmarkCode)
	if err != nil || latest != date {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.load()
	if err == nil && current.Version == config.Version {
		s.start(current)
	}
}

func (s *Server) handleScreenState(w http.ResponseWriter, r *http.Request) {
	screen := s.screen
	screen.mu.Lock()
	config, err := screen.load()
	state := screen.state(config)
	screen.mu.Unlock()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "无法读取选股配置")
		return
	}
	writeJSON(w, state)
}

func (s *Server) handleScreenSave(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Version    int              `json:"version"`
		Strategies []screenStrategy `json:"strategies"`
	}
	if !decodeStrict(r, w, &request, maxCandidateBody) {
		return
	}
	screen := s.screen
	screen.mu.Lock()
	defer screen.mu.Unlock()
	current, err := screen.load()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "无法读取选股配置")
		return
	}
	if current.Version != request.Version {
		writeErr(w, http.StatusConflict, "选股配置已更新，请读取最新配置后重试")
		return
	}

	for i := range request.Strategies {
		strategy := &request.Strategies[i]
		strategy.Name = strings.TrimSpace(strategy.Name)
		if strategy.ID == "" {
			strategy.ID, err = newBuilderStrategyID(screen.now(), nil)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, "无法生成选股策略 ID")
				return
			}
		}
	}
	next := screenConfig{Version: current.Version + 1, Strategies: request.Strategies}
	if _, _, err := compileScreen(next); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	data, err := json.MarshalIndent(next, "", "  ")
	if err == nil {
		err = os.MkdirAll(filepath.Dir(screen.path), 0755)
	}
	if err == nil {
		err = atomicWrite(screen.path, data)
	}
	if err != nil {
		log.Printf("[screen] 保存策略配置失败: %v", err)
		writeErr(w, http.StatusInternalServerError, "保存选股配置失败")
		return
	}

	screen.result = nil
	screen.start(next)
	writeJSON(w, screen.state(next))
}

func (s *Server) handleScreenRun(w http.ResponseWriter, r *http.Request) {
	screen := s.screen
	screen.mu.Lock()
	defer screen.mu.Unlock()
	config, err := screen.load()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "无法读取选股配置")
		return
	}
	if screen.running {
		writeErr(w, http.StatusConflict, "正在选股，请等待当前扫描完成")
		return
	}
	if !hasEnabledScreenStrategy(config) {
		writeErr(w, http.StatusBadRequest, "请先启用至少一套选股策略")
		return
	}
	if !s.runner.mu.TryLock() {
		writeErr(w, http.StatusConflict, "已有批量任务在运行")
		return
	}
	screen.running, screen.version, screen.lastErr = true, config.Version, ""
	const task = "screen_scan"
	ctl := s.runner.beginTask(taskStart{kind: task, phase: "preparing"})
	s.runner.launchTask(ctl, "idle", func(stop chan struct{}) error {
		ctx, cancel := screenTaskContext(stop)
		defer cancel()
		return screen.run(ctx, config, s.runner.screenProgress(task))
	})
	writeJSON(w, screen.state(config))
}

type labScreenMarket struct{}

func (labScreenMarket) MinuteKlines(code string, start, end time.Time) (protocol.Klines, error) {
	if common.Pull == nil {
		return nil, errors.New("行情运行时尚未初始化")
	}
	return common.Pull.MinuteKlines(code, start, end)
}

func (labScreenMarket) Codes() ([]string, error) {
	if common.Pull == nil || common.Manage == nil {
		return nil, errors.New("行情运行时尚未初始化")
	}
	if codes := common.Manage.Codes.GetStockCodes(); len(codes) > 0 {
		return codes, nil
	}
	return common.Pull.LocalStockCodes()
}

func (labScreenMarket) Name(code string) string {
	if common.Manage == nil {
		return ""
	}
	return common.Manage.Codes.GetName(code)
}

func (labScreenMarket) LatestDayDate(code string) (string, error) {
	if common.Pull == nil {
		return "", errors.New("行情运行时尚未初始化")
	}
	return common.Pull.LatestDayDate(code)
}

func (labScreenMarket) DayKlines(code string, start, end time.Time) (extend.Klines, error) {
	if common.Pull == nil {
		return nil, errors.New("行情运行时尚未初始化")
	}
	return common.Pull.DayKlines(code, start, end)
}

func (labScreenMarket) StockSessionBars(codes []string, at time.Time) (map[string]*protocol.Kline, error) {
	return common.Pull.StockSessionBars(codes, at)
}

func (labScreenMarket) EquityAt(code string, at time.Time) *protocol.Equity {
	return common.Pull.EquityAt(code, at)
}
