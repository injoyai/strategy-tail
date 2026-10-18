package lab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/lib/extend"
)

type DataUpdateConfig struct {
	Enabled bool   `json:"enabled"`
	Time    string `json:"time"` // HH:MM, Asia/Shanghai
}

func (c DataUpdateConfig) validate() error {
	if len(c.Time) != 5 {
		return fmt.Errorf("自动更新时间须为 HH:MM")
	}
	if _, err := time.Parse("15:04", c.Time); err != nil {
		return fmt.Errorf("自动更新时间须为有效的 HH:MM")
	}
	return nil
}

type DataUpdateStatus struct {
	Config DataUpdateConfig `json:"config"`
	extend.UpdateProgress
	State              string `json:"state"`
	StartedAt          string `json:"startedAt,omitempty"`
	FinishedAt         string `json:"finishedAt,omitempty"`
	LastSuccess        string `json:"lastSuccess,omitempty"`
	LastPartialSuccess string `json:"lastPartialSuccess,omitempty"`
	LastSuccessError   string `json:"lastSuccessError,omitempty"`
	LastAutoDay        string `json:"lastAutoDay,omitempty"`
	Error              string `json:"error,omitempty"`
	ScheduleNote       string `json:"scheduleNote,omitempty"`
	TradingDate        string `json:"tradingDate,omitempty"`
	IsTradingDay       *bool  `json:"isTradingDay,omitempty"`
}

func (s DataUpdateStatus) partialSuccess() bool {
	return s.State == "error" && !s.Skipped && s.Total > 0 && s.Done == s.Total && s.Failed > 0 && s.Failed < s.Total
}

// DataUpdater shares Runner's task lock. The pull layer also locks the database
// directory across processes, including cmd/update and cmd/backtest's update.
type DataUpdater struct {
	mu       sync.Mutex
	runner   *Runner
	filename string
	location *time.Location
	status   DataUpdateStatus
	run      func(bool, func(extend.UpdateProgress)) error
}

func newDataUpdater(r *Runner, filename string, run func(bool, func(extend.UpdateProgress)) error) *DataUpdater {
	u := &DataUpdater{runner: r, filename: filename, run: run}
	u.status = DataUpdateStatus{Config: DataUpdateConfig{Time: "15:45"}, State: "idle"}
	var err error
	u.location, err = time.LoadLocation("Asia/Shanghai")
	if err != nil {
		u.status.Error = "无法加载上海时区，自动更新不可用"
		return u
	}
	b, err := os.ReadFile(filename)
	if errors.Is(err, os.ErrNotExist) {
		return u
	}
	var saved DataUpdateStatus
	if err != nil || json.Unmarshal(b, &saved) != nil || saved.Config.validate() != nil {
		u.status.Error = "读取更新设置失败，自动更新已关闭"
		return u
	}
	u.status = saved
	if u.status.LastPartialSuccess == "" && saved.partialSuccess() {
		u.status.LastPartialSuccess = saved.FinishedAt
	}
	if saved.State == "running" {
		u.status.State = "idle"
		u.status.LastAutoDay = ""
		u.status.Message = "上次更新被中断，可手动重试"
	}
	return u
}

func (u *DataUpdater) Status() DataUpdateStatus {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.status
}

func (u *DataUpdater) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(u.filename), 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(u.status, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(u.filename, b)
}

func (u *DataUpdater) Configure(c DataUpdateConfig) error {
	if err := c.validate(); err != nil {
		return err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if c.Enabled && u.location == nil {
		return fmt.Errorf("上海时区不可用，无法启用自动更新")
	}
	old := u.status.Config
	u.status.Config = c
	if err := u.persistLocked(); err != nil {
		u.status.Config = old
		return fmt.Errorf("保存更新设置失败: %w", err)
	}
	return nil
}

var errDataTaskBusy = errors.New("已有更新、回测或研究任务在运行，请等待完成")

func (u *DataUpdater) Start(manual bool) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.runner.mu.TryLock() {
		return errDataTaskBusy
	}
	config, lastSuccess, lastAutoDay := u.status.Config, u.status.LastSuccess, u.status.LastAutoDay
	u.status = DataUpdateStatus{Config: config, State: "running", StartedAt: time.Now().Format(time.RFC3339), LastSuccess: lastSuccess, LastPartialSuccess: u.status.LastPartialSuccess, LastAutoDay: lastAutoDay}
	if !manual {
		u.status.LastAutoDay = time.Now().In(u.location).Format(time.DateOnly)
	}
	r := u.runner
	ctl := r.beginTask(taskStart{kind: "data_update"})
	// 更新按数据库事务完成，Stop 仍不关闭该任务的通道。
	r.launchTask(ctl, "idle", func(chan struct{}) error {
		err := u.runUpdate(manual)
		u.mu.Lock()
		defer u.mu.Unlock()
		u.status.FinishedAt = time.Now().Format(time.RFC3339)
		state := "done"
		if errors.Is(err, extend.ErrUpdateLocked) && !manual {
			state = "idle"
			u.status.LastAutoDay = ""
			u.status.ScheduleNote = "自动更新等待其他进程完成更新"
		} else if err != nil {
			state = "error"
			message := err.Error()
			u.status.Error = message
		} else if !u.status.Skipped {
			u.status.LastSuccess = u.status.FinishedAt
			u.status.Message = "数据更新完成"
		}
		u.status.State = state
		if u.status.partialSuccess() {
			u.status.LastPartialSuccess = u.status.FinishedAt
		}
		if saveErr := u.persistLocked(); saveErr != nil {
			u.status.Error = "保存更新状态失败: " + saveErr.Error()
		}
		if state == "idle" {
			return errStopped
		}
		return err
	})
	return nil
}

// runUpdate 将更新器异常转为常规失败，使更新状态也能持久化收尾。
func (u *DataUpdater) runUpdate(manual bool) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("数据更新内部异常(panic): %v", p)
		}
	}()
	return u.run(manual, func(p extend.UpdateProgress) {
		u.mu.Lock()
		u.status.UpdateProgress = p
		u.mu.Unlock()
		r := u.runner
		r.totalCodes.Store(int64(p.Total))
		r.doneCodes.Store(int64(p.Done))
		code := p.Code
		r.currentCode.Store(&code)
	})
}

// When another task holds the lock, retry on the next tick rather than enqueueing
// duplicate work. Failed automatic runs wait until the next day or manual retry.
func (u *DataUpdater) tick(now time.Time) {
	s := u.Status()
	if !s.Config.Enabled || u.location == nil {
		return
	}
	now = now.In(u.location)
	if s.LastAutoDay == now.Format(time.DateOnly) || now.Format("15:04") < s.Config.Time {
		return
	}
	if err := u.Start(false); err != nil {
		u.mu.Lock()
		u.status.ScheduleNote = "自动更新等待当前任务完成"
		u.mu.Unlock()
	}
}

func (s *Server) RunDataUpdateScheduler(ctx context.Context) {
	s.dataUpdater.tick(time.Now())
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.dataUpdater.tick(now)
		}
	}
}

// Browsers on other origins cannot trigger writes to a local unauthenticated Lab.
func requireSameOrigin(w http.ResponseWriter, r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		if err != nil || u.Host != r.Host || u.Scheme != scheme {
			writeErr(w, http.StatusForbidden, "请从当前策略实验室页面操作")
			return false
		}
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeErr(w, http.StatusForbidden, "不允许跨站操作")
		return false
	}
	return true
}

func dataUpdateTradingDay(now time.Time, isTradingDay func(time.Time) bool) (string, *bool) {
	now = now.In(time.FixedZone("Asia/Shanghai", 8*60*60))
	date := now.Format(time.DateOnly)
	trading := false
	if now.Weekday() != time.Saturday && now.Weekday() != time.Sunday {
		if isTradingDay == nil {
			return date, nil
		}
		trading = isTradingDay(now)
	}
	return date, &trading
}

func (s *Server) handleDataUpdateStatus(w http.ResponseWriter, r *http.Request) {
	status := s.dataUpdater.Status()
	var isTradingDay func(time.Time) bool
	if common.Manage != nil && common.Manage.Workday != nil {
		isTradingDay = common.Manage.Workday.Is
	}
	status.TradingDate, status.IsTradingDay = dataUpdateTradingDay(time.Now(), isTradingDay)
	if common.Pull != nil {
		status.LastSuccess = ""
		last, err := common.Pull.LastSuccessfulUpdate()
		if err != nil {
			status.LastSuccessError = "无法读取行情库的完整更新时间"
		} else if !last.IsZero() {
			status.LastSuccess = last.Format(time.RFC3339)
		}
	}
	writeJSON(w, status)
}

func (s *Server) handleDataUpdateConfig(w http.ResponseWriter, r *http.Request) {
	if !requireSameOrigin(w, r) {
		return
	}
	var cfg DataUpdateConfig
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if err := d.Decode(&cfg); err != nil {
		writeErr(w, http.StatusBadRequest, "更新设置无效")
		return
	}
	if err := cfg.validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.dataUpdater.Configure(cfg); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.handleDataUpdateStatus(w, r)
}

func (s *Server) handleDataUpdateRun(w http.ResponseWriter, r *http.Request) {
	if !requireSameOrigin(w, r) {
		return
	}
	if err := s.dataUpdater.Start(true); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func updateData(manual bool, progress func(extend.UpdateProgress)) error {
	if common.Pull == nil || common.Manage == nil {
		return fmt.Errorf("行情运行时未初始化")
	}
	return common.Pull.UpdateWithProgress(common.Manage, manual, progress)
}
