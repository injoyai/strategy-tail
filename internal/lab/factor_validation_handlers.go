package lab

import (
	"errors"
	"net/http"
	"os"

	"github.com/injoyai/strategy-tail/internal/portfolioresearch"
)

func (s *Server) handleFactorValidationInputs(w http.ResponseWriter, r *http.Request) {
	c, err := s.candidateStore.Get(r.PathValue("id"))
	if err != nil {
		factorValidationError(w, err)
		return
	}
	rep, err := s.analysisStore.Get(c.Evidence.AnalysisID)
	if err != nil {
		factorValidationError(w, err)
		return
	}
	if rep == nil || rep.Protocol == nil || rep.Protocol.Labels.Kind != labelKindNextOpenHeldSessions {
		writeErr(w, http.StatusBadRequest, "请运行使用次日开盘标签的分析，再从新报告保存候选")
		return
	}
	trials, err := s.runner.trials.ListFamily(rep.Protocol.Trial.FamilyID)
	if err != nil {
		factorValidationError(w, err)
		return
	}
	ids := make([]string, 0, len(trials))
	for _, t := range trials {
		ids = append(ids, t.ID)
	}
	if len(ids) == 0 {
		writeErr(w, http.StatusBadRequest, "发现期试验账本为空，请重新分析后保存候选")
		return
	}
	writeJSON(w, map[string]any{
		"candidateRevision": c.Revision, "analysisId": rep.AnalysisID, "trialIds": ids,
		"purgeDays": rep.Protocol.Labels.Horizons[len(rep.Protocol.Labels.Horizons)-1] + 1,
		"direction": rep.Protocol.Hypothesis.ExpectedDirection, "lastSignalDate": latestDiscoverySignal(*rep),
		"allowsRetrospective": rep.EvidenceClass == evidenceRetrospective,
	})
}

func (s *Server) handleCreateFactorValidation(w http.ResponseWriter, r *http.Request) {
	var req CreateValidationRequest
	if !decodeStrict(r, w, &req, maxCandidateBody) {
		return
	}
	if req.TestRange == nil {
		writeErr(w, http.StatusBadRequest, "请填写冻结的测试区间")
		return
	}
	v, created, err := s.factorValidationStore.Create(req, FreezeDependencies{
		Candidates: s.candidateStore, Analyses: s.analysisStore, Trials: s.runner.trials,
	})
	if err != nil {
		factorValidationError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSONCode(w, status, map[string]any{"created": created, "validation": v})
}

func (s *Server) handleListFactorValidations(w http.ResponseWriter, r *http.Request) {
	filter := ValidationFilter{CandidateID: r.URL.Query().Get("candidateId"),
		State: ValidationState(r.URL.Query().Get("state")), EvidenceClass: r.URL.Query().Get("evidenceClass")}
	items, err := s.factorValidationStore.List(filter)
	if err != nil {
		factorValidationError(w, err)
		return
	}
	refs := []portfolioresearch.ValidatedFactorRef{}
	if filter.State == ValidationStatePassed {
		for _, item := range items {
			if len(item.SupersededBy) > 0 {
				continue
			}
			view, err := s.factorValidationStore.Get(item.ID)
			if err != nil {
				factorValidationError(w, err)
				return
			}
			ref, err := buildValidatedFactorRef(view)
			if err == nil {
				refs = append(refs, ref)
			}
		}
	}
	writeJSON(w, map[string]any{"validations": items, "validatedFactors": refs})
}

func (s *Server) handleGetFactorValidation(w http.ResponseWriter, r *http.Request) {
	view, err := s.factorValidationStore.Get(r.PathValue("id"))
	if err != nil {
		factorValidationError(w, err)
		return
	}
	writeJSON(w, view)
}

func (s *Server) handleRunFactorValidation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// Duplicate starts for the active job return the same accepted resource.
	if running := s.runner.running.Load() && s.runner.Status()["runId"] == id; !running {
		if err := s.runner.startFactorValidation(id, s.factorValidationStore); err != nil {
			factorValidationError(w, err)
			return
		}
	}
	writeJSONCode(w, http.StatusAccepted, map[string]string{"id": id})
}

func factorValidationError(w http.ResponseWriter, err error) {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		writeErr(w, http.StatusInternalServerError, "读取或写入验证记录失败")
		return
	}
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, errCandidateNotFound), errors.Is(err, errValidationNotFound):
		status = http.StatusNotFound
	case errors.Is(err, errValidationCompleted), errors.Is(err, errIdempotencyConflict), errors.Is(err, errFactorValidationNotReady), errors.Is(err, errPortfolioRunLocked):
		status = http.StatusConflict
	}
	writeErr(w, status, err.Error())
}
