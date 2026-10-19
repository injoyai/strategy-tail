package lab

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/lab/builder"
)

// One service owns these files. Immutable stock snapshots are shared by every
// question/training in a bank. A training, its command receipts, and all attempt
// boundaries commit in ONE atomic rename, so a crash cannot advance half a turn.
type TrainingStore struct {
	root string
	mu   sync.Mutex
}

func NewTrainingStore(root string) *TrainingStore { return &TrainingStore{root: root} }

var trainingIDPattern = regexp.MustCompile(`^(bank|training)_\d{8}T\d{9}Z_[0-9a-f]{8}$`)

type practiceBankBlock struct {
	End int `json:"end"`
}
type practiceBank struct {
	Version     int                   `json:"version"`
	ID          string                `json:"id"`
	Phase       string                `json:"phase"`
	Request     practiceStartRequest  `json:"request"`
	Spec        builder.BuilderSpec   `json:"spec"`
	Cost        core.Cost             `json:"cost"`
	CreatedAt   string                `json:"createdAt"`
	ReadyAt     string                `json:"readyAt,omitempty"`
	FailReason  string                `json:"failReason,omitempty"`
	Total       int                   `json:"total"`
	Blocks      []practiceBankBlock   `json:"blocks,omitempty"`
	Coverage    *practiceScanCoverage `json:"coverage,omitempty"`
	DataEndDate string                `json:"dataEndDate,omitempty"`
}
type practiceBankStock struct {
	Market  *practiceMarket `json:"market"`
	Signals []int           `json:"signals"`
}
type trainingAttempt struct {
	Block   int                     `json:"block"`
	Signal  int                     `json:"signal"`
	Session practiceSession         `json:"session"`
	Result  *trainingQuestionResult `json:"result,omitempty"`
}
type trainingReceipt struct {
	Action     string `json:"action"`
	QuestionID string `json:"questionId"`
}
type practiceTraining struct {
	Version   int               `json:"version"`
	ID        string            `json:"id"`
	BankID    string            `json:"bankId"`
	RequestID string            `json:"requestId"`
	Phase     string            `json:"phase"`
	Revision  int64             `json:"revision"`
	StartedAt string            `json:"startedAt"`
	EndedAt   string            `json:"endedAt,omitempty"`
	Attempts  []trainingAttempt `json:"attempts"`
	// Lazy Fisher-Yates: space is proportional to questions actually drawn, not
	// the potentially millions of opportunities in the bank.
	Shuffle  map[int]int                `json:"shuffle"`
	Receipts map[string]trainingReceipt `json:"receipts"`
}
type trainingCommand struct {
	Action           string `json:"action"`
	QuestionID       string `json:"questionId"`
	CommandID        string `json:"commandId"`
	ExpectedRevision int64  `json:"expectedRevision"`
}

func (s *TrainingStore) dir(kind, id string) (string, error) {
	if !trainingIDPattern.MatchString(id) || (kind == "banks" && id[:5] != "bank_") || (kind == "trainings" && id[:9] != "training_") {
		return "", errPracticeNotFound
	}
	return filepath.Join(s.root, kind, id), nil
}
func trainingRead(path string, v any) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return errPracticeNotFound
	}
	if err != nil {
		return err
	}
	if err = json.Unmarshal(b, v); err != nil {
		return errPracticeCorrupt
	}
	return nil
}
func trainingWrite(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return atomicWrite(path, b)
}
func (s *TrainingStore) bank(id string) (*practiceBank, error) {
	d, err := s.dir("banks", id)
	if err != nil {
		return nil, err
	}
	var b practiceBank
	if err = trainingRead(filepath.Join(d, "bank.json"), &b); err != nil {
		return nil, err
	}
	if b.Version != 1 || b.ID != id || b.Total < 0 {
		return nil, errPracticeCorrupt
	}
	end := 0
	for _, block := range b.Blocks {
		if block.End <= end {
			return nil, errPracticeCorrupt
		}
		end = block.End
	}
	if end != b.Total || (b.Phase == "ready" && b.Total == 0) {
		return nil, errPracticeCorrupt
	}
	return &b, nil
}
func (s *TrainingStore) writeBank(b *practiceBank) error {
	d, e := s.dir("banks", b.ID)
	if e != nil {
		return e
	}
	return trainingWrite(filepath.Join(d, "bank.json"), b)
}
func (s *TrainingStore) bankIDs() ([]os.DirEntry, error) {
	es, e := os.ReadDir(filepath.Join(s.root, "banks"))
	if errors.Is(e, os.ErrNotExist) {
		return nil, nil
	}
	return es, e
}
func (s *TrainingStore) findBank(req practiceStartRequest) (*practiceBank, error) {
	es, e := s.bankIDs()
	if e != nil {
		return nil, e
	}
	for _, entry := range es {
		if !entry.IsDir() {
			continue
		}
		b, e := s.bank(entry.Name())
		if errors.Is(e, errPracticeNotFound) {
			continue
		}
		if e != nil {
			return nil, e
		}
		if b.Request.RequestID == req.RequestID {
			if b.Request != req {
				return nil, errPracticeRequestConflict
			}
			return b, nil
		}
	}
	return nil, nil
}
func (s *TrainingStore) createBank(req practiceStartRequest, spec builder.BuilderSpec, cost core.Cost) (*practiceBank, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, e := s.findBank(req); b != nil || e != nil {
		return b, e
	}
	id, e := newPrefixedID("bank", time.Now(), nil)
	if e != nil {
		return nil, e
	}
	d, _ := s.dir("banks", id)
	if e = os.MkdirAll(d, 0755); e != nil {
		return nil, e
	}
	b := &practiceBank{Version: 1, ID: id, Phase: practicePhasePreparing, Request: req, Spec: spec, Cost: practiceCostSnapshot(cost), CreatedAt: practiceNowUTC()}
	return b, s.writeBank(b)
}
func (s *TrainingStore) stock(b *practiceBank, block int) (*practiceBankStock, error) {
	if block < 0 || block >= len(b.Blocks) {
		return nil, errPracticeCorrupt
	}
	d, e := s.dir("banks", b.ID)
	if e != nil {
		return nil, e
	}
	var v practiceBankStock
	if e = trainingRead(filepath.Join(d, fmt.Sprintf("stock-%d.json", block)), &v); e != nil {
		return nil, e
	}
	start := 0
	if block > 0 {
		start = b.Blocks[block-1].End
	}
	if v.Market == nil || v.Market.Version != practiceFormatV1 || len(v.Signals) != b.Blocks[block].End-start {
		return nil, errPracticeCorrupt
	}
	prev := -1
	for _, index := range v.Signals {
		if index <= prev || index >= len(v.Market.Days) {
			return nil, errPracticeCorrupt
		}
		prev = index
	}
	return &v, nil
}

// Called only by the bank's single preparation task. No complete market pool or
// complete question list is retained in RAM; each successful stock is flushed.
func (s *TrainingStore) appendStock(b *practiceBank, m *practiceMarket, signals []int) error {
	if len(signals) == 0 {
		return nil
	}
	d, e := s.dir("banks", b.ID)
	if e != nil {
		return e
	}
	if e = trainingWrite(filepath.Join(d, fmt.Sprintf("stock-%d.json", len(b.Blocks))), practiceBankStock{m, signals}); e != nil {
		return e
	}
	b.Total += len(signals)
	b.Blocks = append(b.Blocks, practiceBankBlock{b.Total})
	if end := m.Days[len(m.Days)-1].Date; end > b.DataEndDate {
		b.DataEndDate = end
	}
	return nil
}
func (s *TrainingStore) Banks() ([]*practiceBank, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	es, e := s.bankIDs()
	if e != nil {
		return nil, e
	}
	list := []*practiceBank{}
	for _, entry := range es {
		if !entry.IsDir() {
			continue
		}
		b, e := s.bank(entry.Name())
		if errors.Is(e, errPracticeNotFound) {
			continue
		}
		if e != nil {
			return nil, e
		}
		list = append(list, b)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt > list[j].CreatedAt })
	return list, nil
}
func (s *TrainingStore) recoverBanks() error {
	banks, e := s.Banks()
	if e != nil {
		return e
	}
	for _, b := range banks {
		if b.Phase == practicePhasePreparing {
			b.Phase = practicePhaseFailed
			b.FailReason = practiceFailInterrupt
			if e = s.writeBank(b); e != nil {
				return e
			}
		}
	}
	return nil
}
func (s *TrainingStore) training(id string) (*practiceTraining, error) {
	d, e := s.dir("trainings", id)
	if e != nil {
		return nil, e
	}
	var t practiceTraining
	if e = trainingRead(filepath.Join(d, "training.json"), &t); e != nil {
		return nil, e
	}
	if t.Version != 1 || t.ID != id || len(t.Attempts) == 0 || t.Revision < 1 || (t.Phase != "active" && t.Phase != "finished") || t.Shuffle == nil || t.Receipts == nil {
		return nil, errPracticeCorrupt
	}
	return &t, nil
}
func (s *TrainingStore) writeTraining(t *practiceTraining) error {
	d, e := s.dir("trainings", t.ID)
	if e != nil {
		return e
	}
	return trainingWrite(filepath.Join(d, "training.json"), t)
}
func (s *TrainingStore) trainings() ([]*practiceTraining, error) {
	es, e := os.ReadDir(filepath.Join(s.root, "trainings"))
	if errors.Is(e, os.ErrNotExist) {
		return []*practiceTraining{}, nil
	}
	if e != nil {
		return nil, e
	}
	list := []*practiceTraining{}
	for _, entry := range es {
		if !entry.IsDir() {
			continue
		}
		t, e := s.training(entry.Name())
		if errors.Is(e, errPracticeNotFound) {
			continue
		}
		if e != nil {
			return nil, e
		}
		list = append(list, t)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].StartedAt > list[j].StartedAt })
	return list, nil
}
func (s *TrainingStore) Trainings() ([]*practiceTraining, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.trainings()
}
func (s *TrainingStore) draw(t *practiceTraining, b *practiceBank) error {
	remaining := b.Total - len(t.Attempts)
	if remaining <= 0 {
		return errPracticeInvalidCommand
	}
	slot := rand.IntN(remaining)
	lookup := func(i int) int {
		if v, ok := t.Shuffle[i]; ok {
			return v
		}
		return i
	}
	q := lookup(slot)
	if q < 0 || q >= b.Total {
		return errPracticeCorrupt
	}
	tail := lookup(remaining - 1)
	delete(t.Shuffle, remaining-1)
	if slot != remaining-1 {
		t.Shuffle[slot] = tail
	}
	block := sort.Search(len(b.Blocks), func(i int) bool { return b.Blocks[i].End > q })
	stock, e := s.stock(b, block)
	if e != nil {
		return e
	}
	start := 0
	if block > 0 {
		start = b.Blocks[block-1].End
	}
	signal := stock.Signals[q-start]
	id, e := newPrefixedID("practice", time.Now(), nil)
	if e != nil {
		return e
	}
	now := practiceNowUTC()
	sess := practiceSession{Version: practiceFormatV1, ID: id, Revision: 1, Phase: practicePhaseActive, Stage: practiceStageWaiting, StrategyID: b.Request.StrategyID, StrategyVersion: b.Request.StrategyVersion, StrategySpec: b.Spec, Cost: b.Cost, BudgetYuan: practiceBudgetYuan, FromYear: b.Request.FromYear, ToYear: b.Request.ToYear, CreatedAt: now, UpdatedAt: now, SignalIndex: signal, CurrentIndex: signal, BuyIndex: -1, SellIndex: -1}
	t.Attempts = append(t.Attempts, trainingAttempt{Block: block, Signal: signal, Session: sess})
	return nil
}
func (s *TrainingStore) Start(bankID, requestID string) (*practiceTraining, error) {
	if requestID == "" {
		return nil, errPracticeRequestIDRequired
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ts, e := s.trainings()
	if e != nil {
		return nil, e
	}
	for _, t := range ts {
		if t.RequestID == requestID {
			if t.BankID != bankID {
				return nil, errPracticeRequestConflict
			}
			return t, nil
		}
	}
	b, e := s.bank(bankID)
	if e != nil {
		return nil, e
	}
	if b.Phase != "ready" {
		return nil, errPracticeNotFinished
	}
	id, e := newPrefixedID("training", time.Now(), nil)
	if e != nil {
		return nil, e
	}
	d, _ := s.dir("trainings", id)
	if e = os.MkdirAll(d, 0755); e != nil {
		return nil, e
	}
	t := &practiceTraining{Version: 1, ID: id, BankID: bankID, RequestID: requestID, Phase: "active", Revision: 1, StartedAt: practiceNowUTC(), Shuffle: map[int]int{}, Receipts: map[string]trainingReceipt{}}
	if e = s.draw(t, b); e != nil {
		return nil, e
	}
	return t, s.writeTraining(t)
}
func (s *TrainingStore) attemptMarket(b *practiceBank, a *trainingAttempt) (*practiceMarket, error) {
	stock, e := s.stock(b, a.Block)
	if e != nil {
		return nil, e
	}
	if a.Signal != a.Session.SignalIndex {
		return nil, errPracticeCorrupt
	}
	i := sort.SearchInts(stock.Signals, a.Signal)
	if i == len(stock.Signals) || stock.Signals[i] != a.Signal {
		return nil, errPracticeCorrupt
	}
	if e = validatePracticeState(&a.Session, stock.Market); e != nil {
		return nil, e
	}
	stock.Market.Coverage = b.Coverage
	return stock.Market, nil
}
func (s *TrainingStore) Apply(id string, c trainingCommand) (*practiceTraining, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, e := s.training(id)
	if e != nil {
		return nil, e
	}
	if c.CommandID == "" {
		return nil, errPracticeInvalidCommand
	}
	if receipt, ok := t.Receipts[c.CommandID]; ok {
		if receipt.Action != c.Action || receipt.QuestionID != c.QuestionID {
			return nil, errPracticeCommandMismatch
		}
		return t, nil
	}
	if t.Revision != c.ExpectedRevision {
		return nil, errPracticeVersionConflict
	}
	if t.Phase != "active" {
		return nil, errPracticeInvalidCommand
	}
	a := &t.Attempts[len(t.Attempts)-1]
	if a.Session.ID != c.QuestionID {
		return nil, errPracticeVersionConflict
	}
	b, e := s.bank(t.BankID)
	if e != nil {
		return nil, e
	}
	if c.Action == "next_question" {
		if a.Session.Phase != practicePhaseFinished {
			return nil, errPracticeNotFinished
		}
		if e = s.draw(t, b); e != nil {
			return nil, e
		}
	} else {
		m, e := s.attemptMarket(b, a)
		if e != nil {
			return nil, e
		}
		action := c.Action
		if action == "end_training" {
			action = practiceActionFinish
		}
		if a.Session.Phase != practicePhaseFinished {
			if _, e = a.Session.applyCommand(m, practiceCommand{Action: action, CommandID: c.CommandID, ExpectedRevision: a.Session.Revision}); e != nil {
				return nil, e
			}
		} else if c.Action != "end_training" {
			return nil, errPracticeInvalidCommand
		}
		a.Session.UpdatedAt = practiceNowUTC()
		if a.Session.Phase == practicePhaseFinished && a.Result == nil {
			r := trainingResultOf(&a.Session, m)
			a.Result = &r
		}
		if c.Action == "end_training" || (a.Session.Phase == practicePhaseFinished && len(t.Attempts) == b.Total) {
			t.Phase = "finished"
			t.EndedAt = practiceNowUTC()
		}
	}
	t.Revision++
	t.Receipts[c.CommandID] = trainingReceipt{c.Action, c.QuestionID}
	return t, s.writeTraining(t)
}

// The public state is assembled while holding the same lock as writes. No bank
// identities, dates, index lists, or future bars are exposed by this projection.
func (s *TrainingStore) View(id string) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, e := s.training(id)
	if e != nil {
		return nil, e
	}
	b, e := s.bank(t.BankID)
	if e != nil {
		return nil, e
	}
	a := &t.Attempts[len(t.Attempts)-1]
	m, e := s.attemptMarket(b, a)
	if e != nil {
		return nil, e
	}
	return map[string]any{"id": t.ID, "bankId": t.BankID, "strategyName": b.Spec.Name, "phase": t.Phase, "revision": t.Revision, "total": b.Total, "practiced": len(t.Attempts), "startedAt": t.StartedAt, "endedAt": t.EndedAt, "session": practiceSessionViewOf(&a.Session, m)}, nil
}
func (s *TrainingStore) Review(id, questionID string) (*practiceReviewView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, e := s.training(id)
	if e != nil {
		return nil, e
	}
	b, e := s.bank(t.BankID)
	if e != nil {
		return nil, e
	}
	for i := range t.Attempts {
		a := &t.Attempts[i]
		if a.Session.ID == questionID {
			if a.Session.Phase != practicePhaseFinished {
				return nil, errPracticeNotFinished
			}
			m, e := s.attemptMarket(b, a)
			if e != nil {
				return nil, e
			}
			v := practiceReviewViewOf(&a.Session, m)
			return &v, nil
		}
	}
	return nil, errPracticeNotFound
}

// Retakes go to the existing standalone store. The deterministic request key is
// durable before returning; failures can be retried after either process restarts.
func (s *TrainingStore) Retake(ps *PracticeStore, id, questionID, requestID string) (*practiceSession, error) {
	if requestID == "" {
		return nil, errPracticeRequestIDRequired
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t, e := s.training(id)
	if e != nil {
		return nil, e
	}
	b, e := s.bank(t.BankID)
	if e != nil {
		return nil, e
	}
	for i := range t.Attempts {
		a := &t.Attempts[i]
		if a.Session.ID != questionID {
			continue
		}
		if a.Session.Phase != practicePhaseFinished {
			return nil, errPracticeNotFinished
		}
		m, e := s.attemptMarket(b, a)
		if e != nil {
			return nil, e
		}
		ps.mu.Lock()
		defer ps.mu.Unlock()
		key := "training-retake:" + id + ":" + questionID + ":" + requestID
		old, e := ps.findByRequestIDLocked(key)
		if e != nil {
			return nil, e
		}
		if old != nil {
			return old, nil
		}
		newID, e := ps.generateID()
		if e != nil {
			return nil, e
		}
		d, e := ps.practiceDir(newID)
		if e != nil {
			return nil, e
		}
		if e = os.MkdirAll(d, 0755); e != nil {
			return nil, e
		}
		sess := a.Session
		sess.ID = newID
		sess.Revision = 1
		sess.Phase = practicePhaseActive
		sess.Stage = practiceStageWaiting
		sess.CurrentIndex = sess.SignalIndex
		sess.Quantity = 0
		sess.BuyIndex = -1
		sess.SellIndex = -1
		sess.BuyCostYuan = 0
		sess.SellIncomeYuan = 0
		sess.EndReason = ""
		sess.SourceSessionID = questionID
		sess.RequestID = key
		sess.LastCommandID = ""
		sess.LastCommandAction = ""
		sess.CreatedAt = practiceNowUTC()
		sess.UpdatedAt = sess.CreatedAt
		if e = trainingWrite(filepath.Join(d, practiceMarketFile), m); e != nil {
			return nil, e
		}
		if e = ps.writeSessionLocked(&sess); e != nil {
			return nil, e
		}
		return &sess, nil
	}
	return nil, errPracticeNotFound
}

func (s *TrainingStore) Question(id, questionID string) (*practiceSessionView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, e := s.training(id)
	if e != nil {
		return nil, e
	}
	b, e := s.bank(t.BankID)
	if e != nil {
		return nil, e
	}
	for i := range t.Attempts {
		a := &t.Attempts[i]
		if a.Session.ID == questionID {
			if a.Session.Phase != practicePhaseFinished {
				return nil, errPracticeNotFinished
			}
			m, e := s.attemptMarket(b, a)
			if e != nil {
				return nil, e
			}
			v := practiceSessionViewOf(&a.Session, m)
			return &v, nil
		}
	}
	return nil, errPracticeNotFound
}
