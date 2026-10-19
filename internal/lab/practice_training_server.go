package lab

import (
	"log"
	"net/http"
)

func practiceBankView(b *practiceBank) map[string]any {
	v := map[string]any{"id": b.ID, "phase": b.Phase, "strategyName": b.Spec.Name, "strategyVersion": b.Request.StrategyVersion, "fromYear": b.Request.FromYear, "toYear": b.Request.ToYear, "createdAt": b.CreatedAt, "readyAt": b.ReadyAt, "failReason": b.FailReason}
	// No codes, signal indices, or per-stock future identities in catalogue APIs.
	if b.Phase == "ready" {
		v["total"] = b.Total
		v["dataEndDate"] = b.DataEndDate
		if c := b.Coverage; c != nil {
			v["coverage"] = map[string]int{"codes": c.Codes, "withData": c.WithData, "scanned": c.Scanned, "succeeded": c.Succeeded, "hits": c.Hits, "failed": c.Failed}
		}
	}
	return v
}
func trainingHTTPError(w http.ResponseWriter, e error) {
	if isPracticeRequestErr(e) {
		practiceWriteErr(w, e)
		return
	}
	log.Printf("[lab] 题库训练操作失败: %v", e)
	practiceWriteErr(w, errPracticeInternal)
}
func (s *Server) registerTrainingRoutes() {
	s.mux.HandleFunc("GET /api/practice-banks", s.handlePracticeBanks)
	s.mux.HandleFunc("POST /api/practice-banks", s.handlePracticeBankStart)
	s.mux.HandleFunc("GET /api/practice-banks/{id}", s.handlePracticeBankGet)
	s.mux.HandleFunc("GET /api/practice-trainings", s.handleTrainingList)
	s.mux.HandleFunc("POST /api/practice-trainings", s.handleTrainingStart)
	s.mux.HandleFunc("GET /api/practice-trainings/{id}", s.handleTrainingGet)
	s.mux.HandleFunc("POST /api/practice-trainings/{id}/actions", s.handleTrainingAction)
	s.mux.HandleFunc("GET /api/practice-trainings/{id}/report", s.handleTrainingReport)
	s.mux.HandleFunc("GET /api/practice-trainings/{id}/questions/{question}", s.handleTrainingQuestion)
	s.mux.HandleFunc("GET /api/practice-trainings/{id}/questions/{question}/review", s.handleTrainingReview)
	s.mux.HandleFunc("POST /api/practice-trainings/{id}/questions/{question}/retry", s.handleTrainingRetake)
}
func (s *Server) handlePracticeBanks(w http.ResponseWriter, r *http.Request) {
	list, e := s.trainingStore.Banks()
	if e != nil {
		trainingHTTPError(w, e)
		return
	}
	vs := []map[string]any{}
	for _, b := range list {
		vs = append(vs, practiceBankView(b))
	}
	writeJSON(w, map[string]any{"banks": vs})
}
func (s *Server) handlePracticeBankStart(w http.ResponseWriter, r *http.Request) {
	if !requireSameOrigin(w, r) {
		return
	}
	var req practiceStartRequest
	if !decodeStrict(r, w, &req, maxCandidateBody) {
		return
	}
	b, e := s.runner.StartPracticeBank(req)
	if e != nil {
		trainingHTTPError(w, e)
		return
	}
	writeJSONCode(w, http.StatusAccepted, practiceBankView(b))
}
func (s *Server) handlePracticeBankGet(w http.ResponseWriter, r *http.Request) {
	s.trainingStore.mu.Lock()
	b, e := s.trainingStore.bank(r.PathValue("id"))
	s.trainingStore.mu.Unlock()
	if e != nil {
		trainingHTTPError(w, e)
		return
	}
	writeJSON(w, practiceBankView(b))
}
func (s *Server) handleTrainingStart(w http.ResponseWriter, r *http.Request) {
	if !requireSameOrigin(w, r) {
		return
	}
	var req struct {
		BankID    string `json:"bankId"`
		RequestID string `json:"requestId"`
	}
	if !decodeStrict(r, w, &req, maxCandidateBody) {
		return
	}
	t, e := s.trainingStore.Start(req.BankID, req.RequestID)
	if e != nil {
		trainingHTTPError(w, e)
		return
	}
	s.writeTrainingView(w, t.ID)
}
func (s *Server) writeTrainingView(w http.ResponseWriter, id string) {
	v, e := s.trainingStore.View(id)
	if e != nil {
		trainingHTTPError(w, e)
		return
	}
	writeJSON(w, v)
}
func (s *Server) handleTrainingGet(w http.ResponseWriter, r *http.Request) {
	s.writeTrainingView(w, r.PathValue("id"))
}
func (s *Server) handleTrainingAction(w http.ResponseWriter, r *http.Request) {
	if !requireSameOrigin(w, r) {
		return
	}
	var c trainingCommand
	if !decodeStrict(r, w, &c, maxCandidateBody) {
		return
	}
	t, e := s.trainingStore.Apply(r.PathValue("id"), c)
	if e != nil {
		trainingHTTPError(w, e)
		return
	}
	s.writeTrainingView(w, t.ID)
}
func (s *Server) handleTrainingList(w http.ResponseWriter, r *http.Request) {
	list, e := s.trainingStore.Trainings()
	if e != nil {
		trainingHTTPError(w, e)
		return
	}
	out := []map[string]any{}
	for _, t := range list {
		out = append(out, map[string]any{"id": t.ID, "bankId": t.BankID, "phase": t.Phase, "startedAt": t.StartedAt, "endedAt": t.EndedAt, "practiced": len(t.Attempts)})
	}
	writeJSON(w, map[string]any{"trainings": out})
}
func (s *Server) handleTrainingReport(w http.ResponseWriter, r *http.Request) {
	v, e := s.trainingStore.Report(r.PathValue("id"))
	if e != nil {
		trainingHTTPError(w, e)
		return
	}
	writeJSON(w, v)
}
func (s *Server) handleTrainingReview(w http.ResponseWriter, r *http.Request) {
	v, e := s.trainingStore.Review(r.PathValue("id"), r.PathValue("question"))
	if e != nil {
		trainingHTTPError(w, e)
		return
	}
	writeJSON(w, v)
}
func (s *Server) handleTrainingRetake(w http.ResponseWriter, r *http.Request) {
	if !requireSameOrigin(w, r) {
		return
	}
	var req struct {
		RequestID string `json:"requestId"`
	}
	if !decodeStrict(r, w, &req, maxCandidateBody) {
		return
	}
	sess, e := s.trainingStore.Retake(s.practiceStore, r.PathValue("id"), r.PathValue("question"), req.RequestID)
	if e != nil {
		trainingHTTPError(w, e)
		return
	}
	_, m, ok := s.practiceGetForRequest(w, sess.ID)
	if ok {
		writeJSON(w, practiceSessionViewOf(sess, m))
	}
}

func (s *Server) handleTrainingQuestion(w http.ResponseWriter, r *http.Request) {
	v, e := s.trainingStore.Question(r.PathValue("id"), r.PathValue("question"))
	if e != nil {
		trainingHTTPError(w, e)
		return
	}
	writeJSON(w, v)
}
