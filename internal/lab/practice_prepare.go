package lab

import (
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/lab/builder"
	"github.com/injoyai/strategy-tail/internal/researchrun"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// practice_prepare.go 盘感练习出题：扫描本地行情、等概率抽题并发布快照。
//
// 实施文档 §2：作为后台任务 practice_prepare 执行，复用 Runner 的任务互斥、
// 进度与取消；最多并行扫描四只股票，独立构建 Buyer 并按历史前缀求值；
// 内存只容纳一批股票和一个候选（保留候选行情副本，保存阶段不重读行情）。年份范围内的所有
// 命中机会（股票＋信号日期）等概率成为本题，不按后续收益筛选。
//
// 信息隔离：扫描覆盖（含失败明细）只落盘快照供复盘披露；进度只含阶段与
// 数量，不暴露股票身份；错误详情写服务日志，对外返回脱敏消息。

// practiceTaskPrepare 后台任务类型名（/api/status 进度契约）。
const practiceTaskPrepare = "practice_prepare"

// 出题任务阶段（进度 Phase）。
const (
	practicePhaseKeyPrepare = "prepare"
	practicePhaseKeyScan    = "scan"
	practicePhaseKeySave    = "save"
)

// practiceMarketSource 行情快照来源标识（当前只支持本地日线库）。
const practiceMarketSource = "local_daily"

// practiceMinYear 出题起始年下限（同回测口径：起始年份不能早于 1990）。
const practiceMinYear = 1990

// practiceStartRequest 出题请求体。
type practiceStartRequest struct {
	StrategyID      string `json:"strategyId"`
	StrategyVersion int    `json:"strategyVersion"`
	FromYear        int    `json:"fromYear"`
	ToYear          int    `json:"toYear"`
	RequestID       string `json:"requestId"`
}

// practiceStartResult 出题受理结果。Started=false 表示同请求的会话已存在
// （幂等），客户端应继续轮询该会话而不是重开任务。
type practiceStartResult struct {
	SessionID string `json:"sessionId"`
	Started   bool   `json:"started"`
}

// 出题入口的请求级错误（可原文返回客户端，不含路径与股票身份）。
var (
	errPracticeStrategyNotFound  = errors.New("策略不存在")
	errPracticeStrategyVersion   = errors.New("策略版本已更新，请刷新后重试")
	errPracticeBusy              = errors.New("已有后台任务在运行，请稍后再试")
	errPracticeStrategyRead      = errors.New("读取策略失败")
	errPracticeInternal          = errors.New("练习操作失败，请稍后重试")
	errPracticeNoLocalKlinePULL  = errors.New("本地行情读取器未初始化")
	errPracticeBuyEmptyProjected = errors.New("买入条件为空：请至少添加一个买入条件（或显式选择「全部样本买入」）")
)

// practiceValidateYears 校验出题年份范围：正数、升序、不早于 1990、
// 不超过当前年（同回测与策略搭建器口径）。
func practiceValidateYears(from, to int) error {
	if from <= 0 || to <= 0 || from > to {
		return fmt.Errorf("年份范围无效: %d-%d", from, to)
	}
	if from < practiceMinYear {
		return fmt.Errorf("起始年份不能早于 %d", practiceMinYear)
	}
	if to > time.Now().Year() {
		return fmt.Errorf("结束年份 %d 超过当前年份", to)
	}
	return nil
}

// practiceValidateBuySide 校验并投影策略：只保留买入侧（需求 P01/P12，
// 练习不使用卖出规则）。投影按 save 模式校验（允许空树整体通过，再由
// Buy==nil 拦截），并实际构建一次 Buyer 确保可执行。
func practiceValidateBuySide(spec builder.BuilderSpec) (builder.BuilderSpec, error) {
	proj := builder.BuilderSpec{Version: spec.Version, Name: spec.Name, Buy: spec.Buy}
	if err := proj.Validate("save"); err != nil {
		return proj, err
	}
	if proj.Buy == nil {
		return proj, errPracticeBuyEmptyProjected
	}
	if _, err := proj.Buyer(); err != nil {
		return proj, err
	}
	return proj, nil
}

// practiceKlineSource 出题行情来源抽象（测试注入固定行情；生产读本地库）。
type practiceKlineSource interface {
	Codes() ([]string, error)
	DayKlinesAll(code string) (extend.Klines, error)
}

// practiceLocalKlineSource 本地行情来源：沪深主板（sh60/sz00，实施文档 §2）。
// 科创板一手 200 股与 100 股取整的预算口径冲突，不纳入出题范围。
type practiceLocalKlineSource struct{}

func (practiceLocalKlineSource) Codes() ([]string, error) {
	return common.Market沪深主板.LocalCodes()
}

func (practiceLocalKlineSource) DayKlinesAll(code string) (extend.Klines, error) {
	if common.Pull == nil {
		return nil, errPracticeNoLocalKlinePULL
	}
	return common.Pull.DayKlinesAll(code)
}

// practiceDayCompleted 判断该交易日收盘后（15:00 起）数据是否可用作当日决策。
// 未收盘的最后一根行情从出题范围剔除，避免用未完成数据出题。
func practiceDayCompleted(unix int64, now time.Time) bool {
	d := time.Unix(unix, 0).In(time.FixedZone("Asia/Shanghai", 8*60*60))
	closeAt := time.Date(d.Year(), d.Month(), d.Day(), 15, 0, 0, 0, d.Location())
	return !now.Before(closeAt)
}

// practiceTrimUnfinished 截掉未收盘的尾部行情，返回底层子切片。
func practiceTrimUnfinished(ks extend.Klines, now time.Time) extend.Klines {
	n := len(ks)
	for n > 0 && !practiceDayCompleted(ks[n-1].Unix, now) {
		n--
	}
	return ks[:n:n]
}

// practiceScanFailure 单股票扫描失败明细（仅复盘披露，含代码）。
type practiceScanFailure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// practiceScanCoverage 扫描覆盖统计：出题可靠性的诚实披露（需求 §3）。
type practiceScanCoverage struct {
	Codes     int                   `json:"codes"`              // 参与扫描的股票总数
	WithData  int                   `json:"withData"`           // 有可用日线的股票数
	Succeeded int                   `json:"succeeded"`          // 完整求值且没有异常的股票数
	Scanned   int                   `json:"scanned"`            // 实际执行策略求值的股票数
	Hits      int                   `json:"hits"`               // 年份范围内命中机会总数
	Failed    int                   `json:"failed"`             // 失败股票数（恒精确）
	Failures  []practiceScanFailure `json:"failures,omitempty"` // 明细上限 100 条
}

// practiceScanFailuresMax 失败明细条数上限；超出仅累计 Failed 计数。
const practiceScanFailuresMax = 100

func (c *practiceScanCoverage) addFailure(code, msg string) {
	c.Failed++
	if len(c.Failures) < practiceScanFailuresMax {
		c.Failures = append(c.Failures, practiceScanFailure{Code: code, Message: msg})
	}
}

// practiceScanFailMsg 截断失败消息（外部数据错误可能很长，落盘限长）。
func practiceScanFailMsg(err error) string {
	msg := []rune(err.Error())
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return string(msg)
}

// practiceCandidate 当前候选题：股票、信号日索引与行情副本（截尾后）。
// 保留副本使保存阶段无需重读行情，消除二次读取失败路径。
type practiceCandidate struct {
	Code  string
	Index int
	Ks    extend.Klines
}

// practiceReservoirChoose 蓄水池抽样：第 n（全局序号，从 1 起）个命中以
// 1/n 概率替换当前候选，保证所有命中机会等概率。
func practiceReservoirChoose(n int) bool { return rand.IntN(n) == 0 }

// practiceScanParams 扫描参数。Buyer 每股票调用一次（独立实例，防引擎内
// 状态串题）；Choose 为 nil 时接受全部命中（最终取最后一个命中，测试用），
// 生产传 practiceReservoirChoose 保证等概率。
type practiceScanParams struct {
	Workers    int // 0 默认串行，生产按 CPU 数启用最多四路并行
	Codes      []string
	Source     practiceKlineSource
	FromYear   int
	ToYear     int
	Now        time.Time
	Buyer      func() (core.Buyer, error)
	Choose     func(n int) bool
	OnProgress func(done, total int)
	OnStock    func(code string, ks extend.Klines, hits []int) error
	Stop       <-chan struct{}
}

// practiceScan 逐股票扫描并抽题。返回候选（可能为 nil=无命中）与覆盖统计。
// 单股票求值异常记入覆盖统计并继续，不中断整体出题。
func practiceScan(p practiceScanParams) (*practiceCandidate, *practiceScanCoverage, error) {
	cov := &practiceScanCoverage{Codes: len(p.Codes)}
	var cand *practiceCandidate
	workers := max(1, min(p.Workers, 4))
	// 按小批次合并，保证内存有界；抽样仍按原股票和交易日顺序累计全局命中数。
	for start := 0; start < len(p.Codes); start += workers {
		batch := p.Codes[start:min(start+workers, len(p.Codes))]
		results := make([]practiceStockScan, len(batch))
		var wg sync.WaitGroup
		for i, code := range batch {
			wg.Add(1)
			go func(i int, code string) {
				defer wg.Done()
				results[i] = practiceScanOne(code, p)
			}(i, code)
		}
		wg.Wait()
		for i, result := range results {
			select {
			case <-p.Stop:
				return cand, cov, errStopped
			default:
			}
			code := batch[i]
			if result.withData {
				cov.WithData++
			}
			if result.scanned {
				cov.Scanned++
			}
			if result.err != nil {
				if errors.Is(result.err, errStopped) {
					return cand, cov, errStopped
				}
				cov.addFailure(code, practiceScanFailMsg(result.err))
			} else {
				if result.scanned {
					cov.Succeeded++
				}
				if p.OnStock != nil && len(result.hits) > 0 {
					if err := p.OnStock(code, result.ks, result.hits); err != nil {
						return cand, cov, err
					}
				}
				for _, index := range result.hits {
					cov.Hits++
					if p.OnStock == nil && (p.Choose == nil || p.Choose(cov.Hits)) {
						cand = &practiceCandidate{Code: code, Index: index, Ks: result.ks}
					}
				}
			}
			if p.OnProgress != nil {
				p.OnProgress(start+i+1, cov.Codes)
			}
		}
	}
	select {
	case <-p.Stop:
		return cand, cov, errStopped
	default:
	}
	return cand, cov, nil
}

type practiceStockScan struct {
	ks                extend.Klines
	hits              []int
	withData, scanned bool
	err               error
}

// 每只股票独占 Buyer；异常股票整批命中作废，不能污染蓄水池或其他股票。
func practiceScanOne(code string, p practiceScanParams) (result practiceStockScan) {
	defer func() {
		if v := recover(); v != nil {
			result.hits = nil
			result.err = fmt.Errorf("求值异常: %v", v)
		}
	}()
	select {
	case <-p.Stop:
		result.err = errStopped
		return
	default:
	}
	ks, err := p.Source.DayKlinesAll(code)
	if err != nil {
		result.err = err
		return
	}
	result.ks = practiceTrimUnfinished(ks, p.Now)
	if len(result.ks) == 0 {
		return
	}
	result.withData = true
	buyer, err := p.Buyer()
	if err != nil {
		result.err = fmt.Errorf("构建买入条件失败: %w", err)
		return
	}
	result.scanned = true
	for i, k := range result.ks {
		select {
		case <-p.Stop:
			result.err = errStopped
			return
		default:
		}
		y := k.Time.Year()
		if y < p.FromYear {
			continue
		}
		if y > p.ToYear {
			break
		}
		if buyer.Buy(code, result.ks[:i+1:i+1]) {
			result.hits = append(result.hits, i)
		}
	}
	return
}

// SetPracticeStores 注入盘感练习依赖（Server 启动时调用；无并发，无锁）。
func (r *Runner) SetPracticeStores(ps *PracticeStore, bs *BuilderStore) {
	r.practiceStore = ps
	r.builderStore = bs
}

// practiceSourceOf 返回出题行情来源；测试注入优先，否则本地库。
func (r *Runner) practiceSourceOf() practiceKlineSource {
	if r.practiceSource != nil {
		return r.practiceSource
	}
	return practiceLocalKlineSource{}
}

// practiceStartCost 读取当前费用配置作为开题快照（滑点在快照内清零）。
func practiceStartCost() core.Cost {
	cost, _ := common.LoadBacktestConfig()
	return cost
}

// StartPractice 受理出题请求：校验策略与买入侧、按 requestID 幂等创建
// preparing 会话，再启动后台任务发布快照。返回的会话立即落盘，重启可恢复。
func (r *Runner) StartPractice(req practiceStartRequest) (*practiceStartResult, error) {
	if req.StrategyID == "" {
		return nil, fmt.Errorf("缺少策略 ID")
	}
	if req.RequestID == "" {
		return nil, fmt.Errorf("缺少 requestId")
	}
	if err := practiceValidateYears(req.FromYear, req.ToYear); err != nil {
		return nil, err
	}
	if r.practiceStore == nil || r.builderStore == nil {
		return nil, fmt.Errorf("练习依赖未初始化")
	}
	if existing, err := r.practiceStore.findStartRequest(req); err != nil {
		if errors.Is(err, errPracticeRequestConflict) {
			return nil, err
		}
		return nil, errPracticeInternal
	} else if existing != nil {
		return &practiceStartResult{SessionID: existing.ID, Started: false}, nil
	}

	st, err := r.builderStore.Get(req.StrategyID)
	if err != nil {
		if errors.Is(err, errBuilderNotFound) {
			return nil, errPracticeStrategyNotFound
		}
		log.Printf("[lab] 读取策略 %s 失败: %v", req.StrategyID, err)
		return nil, errPracticeStrategyRead
	}
	if st.Version != req.StrategyVersion {
		return nil, errPracticeStrategyVersion
	}
	proj, err := practiceValidateBuySide(st.Spec)
	if err != nil {
		return nil, err
	}

	if !r.mu.TryLock() {
		return nil, errPracticeBusy
	}

	sess, err := r.practiceStore.CreatePreparing(req.StrategyID, req.StrategyVersion, proj,
		practiceCostSnapshot(practiceStartCost()), req.FromYear, req.ToYear, req.RequestID)
	if err != nil {
		r.mu.Unlock()
		if errors.Is(err, errPracticeRequestIDRequired) || errors.Is(err, errPracticeRequestConflict) {
			return nil, err
		}
		log.Printf("[lab] 创建练习会话失败: %v", err)
		return nil, errPracticeInternal
	}
	if sess.Phase != practicePhasePreparing {
		// 同请求会话已完成准备（幂等重放），不再开任务。
		r.mu.Unlock()
		return &practiceStartResult{SessionID: sess.ID, Started: false}, nil
	}

	ctl := r.beginTask(taskStart{kind: practiceTaskPrepare, phase: practicePhaseKeyPrepare, runID: sess.ID})
	choose := practiceReservoirChoose
	if r.practiceChoose != nil {
		choose = r.practiceChoose
	}
	r.launchTask(ctl, "idle", func(stop chan struct{}) error {
		return r.runPracticePrepare(sess, proj, choose, stop)
	})
	return &practiceStartResult{SessionID: sess.ID, Started: true}, nil
}

// markPracticeFailed 标记会话失败；状态写失败仅记日志（无更好出路）。
func (r *Runner) markPracticeFailed(id, reason string) {
	if err := r.practiceStore.MarkFailed(id, reason); err != nil {
		log.Printf("[lab] 练习 %s 标记失败状态出错: %v", id, err)
	}
}

// markPracticeCancelled 标记会话取消。
func (r *Runner) markPracticeCancelled(id string) {
	if err := r.practiceStore.MarkCancelled(id); err != nil {
		log.Printf("[lab] 练习 %s 标记取消状态出错: %v", id, err)
	}
}

// runPracticePrepare 后台出题：读取股票列表 → 扫描抽题 → 构建并发布快照。
// choose 为抽样决策函数（测试可注入固定决策）。进度只含阶段与数量；取消走
// MarkCancelled，失败走 MarkFailed 并返回脱敏错误（经 /api/status 展示，
// 不得携带股票身份与路径）。
func (r *Runner) runPracticePrepare(sess *practiceSession, proj builder.BuilderSpec, choose func(n int) bool, stop chan struct{}) (err error) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("[lab] 练习 %s 出题异常: %v\n%s", sess.ID, p, debug.Stack())
			r.markPracticeFailed(sess.ID, practiceFailInternal)
			err = fmt.Errorf("练习出题内部异常，请重新出题")
		}
	}()

	r.setTaskProgress(practiceTaskPrepare, "stocks", researchrun.Progress{Phase: practicePhaseKeyPrepare})

	src := r.practiceSourceOf()
	codes, err := src.Codes()
	if err != nil {
		r.markPracticeFailed(sess.ID, practiceFailData)
		log.Printf("[lab] 读取本地股票列表失败: %v", err)
		return fmt.Errorf("读取本地股票列表失败")
	}

	r.setTaskProgress(practiceTaskPrepare, "stocks", researchrun.Progress{Phase: practicePhaseKeyScan, Total: len(codes)})
	cand, cov, err := practiceScan(practiceScanParams{
		Codes:    codes,
		Workers:  runtime.GOMAXPROCS(0),
		Source:   src,
		FromYear: sess.FromYear,
		ToYear:   sess.ToYear,
		Now:      time.Now(),
		Buyer:    proj.Buyer,
		Choose:   choose,
		OnProgress: func(done, total int) {
			r.setTaskProgress(practiceTaskPrepare, "stocks", researchrun.Progress{
				Phase: practicePhaseKeyScan, Done: done, Total: total,
			})
		},
		Stop: stop,
	})
	if err == errStopped {
		r.markPracticeCancelled(sess.ID)
		return errStopped
	}
	if err != nil {
		r.markPracticeFailed(sess.ID, practiceFailData)
		log.Printf("[lab] 练习 %s 扫描行情失败: %v", sess.ID, err)
		return fmt.Errorf("扫描本地行情失败")
	}
	if cand == nil {
		r.markPracticeFailed(sess.ID, practiceFailNoMatch)
		return fmt.Errorf("指定年份范围内没有命中机会")
	}

	r.setTaskProgress(practiceTaskPrepare, "stocks", researchrun.Progress{
		Phase: practicePhaseKeySave,
	})
	m, err := buildPracticeMarket(cand.Code, cand.Ks, practiceMarketSource, practiceNowUTC())
	if err != nil {
		r.markPracticeFailed(sess.ID, practiceFailData)
		log.Printf("[lab] 练习 %s 构建行情快照失败: %v", sess.ID, err)
		return fmt.Errorf("构建行情快照失败")
	}
	m.Coverage = cov
	select {
	case <-stop:
		r.markPracticeCancelled(sess.ID)
		return errStopped
	default:
	}
	if err := r.practiceStore.Publish(sess.ID, m, cand.Index); err != nil {
		r.markPracticeFailed(sess.ID, practiceFailInternal)
		log.Printf("[lab] 练习 %s 保存快照失败: %v", sess.ID, err)
		return fmt.Errorf("保存练习失败")
	}
	return nil
}
