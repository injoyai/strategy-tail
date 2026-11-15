package lab

import (
	"fmt"

	"github.com/injoyai/strategy-tail/researchdata"
	f "github.com/injoyai/strategy-tail/strategies/factor"
)

const labelPolicySkipCorporateEvents = "sh000001_calendar_positive_volume_excluding_known_held_corporate_events"

// bindAnalysisProtocol binds provenance to the server's reader, including when
// the caller supplied a complete hypothesis and diagnostics.
func (r *Runner) bindAnalysisProtocol(cfg AnalyzeConfig) (ResearchProtocol, error) {
	var p ResearchProtocol
	if cfg.Protocol != nil {
		var err error
		p, err = cfg.Protocol.Normalize()
		if err != nil {
			return p, err
		}
		if p.Diagnostics.Neutralization.Mode != neutralizationNone || p.Diagnostics.Neutralization.Winsorization != winsorizationNone {
			return p, fmt.Errorf("单因子分析尚未执行中性化或去极值，请使用原始因子诊断")
		}
	} else {
		entry, _ := f.Catalog(cfg.Kind)
		days := cfg.Days
		if days <= 0 {
			days = entry.DefaultDays
		}
		p = ResearchProtocol{
			SchemaVersion: protocolSchemaVersion,
			Hypothesis: HypothesisSpec{
				ID: cfg.Kind, Thesis: "探索因子排序与后续价格收益的关系",
				ExpectedDirection: labelDirectionTwoSided, FailureCondition: "未预注册晋级条件，结果仅供探索",
			},
			Signal: SignalSpec{Frequency: signalFrequencyDaily, FormedAt: signalFormedAtClose},
			Labels: LabelSpec{Kind: labelKindNextOpenHeldSessions, Horizons: []int{cfg.HoldingSessions}},
			Diagnostics: DiagnosticSpec{
				Grouping: cfg.Grouping, HACLagMode: hacLagFixed, HACLag: max(5, cfg.HoldingSessions),
				Neutralization: NeutralizationSpec{
					Mode: neutralizationNone, Winsorization: winsorizationNone, Standardization: standardizationRank,
				},
			},
			Trial: TrialSpec{FamilyID: cfg.Kind, VariantID: fmt.Sprintf("d%d_h%d", days, cfg.HoldingSessions),
				Rationale: "因子分析探索试验"},
		}
	}
	p.Data = r.dataProvenance()

	pool := UniverseSpec{Mode: universeCurrentStatic, ID: "mysql_mainboard_daily",
		IncludeDelisted: true, MembershipPIT: membershipPITUnverified}
	switch {
	case p.Universe.Mode == universeHistoricalMembership:
		if r.universe == nil {
			return p, fmt.Errorf("历史成员股票池不可用")
		}
		snapshot := r.universe.Snapshot()
		if snapshot.Mode != researchdata.UniverseModeHistoricalMembership || p.Universe.ID != snapshot.ID {
			return p, fmt.Errorf("请求的历史成员股票池与服务端快照不符")
		}
		pool = UniverseSpec{
			Mode: snapshot.Mode, ID: snapshot.ID, Version: snapshot.Version,
			IncludeDelisted: snapshot.IncludeDelisted, MembershipPIT: snapshot.MembershipPIT,
		}
	case cfg.SampleMode == "codes":
		pool = UniverseSpec{Mode: universeCodes, ID: "requested_codes", MembershipPIT: membershipPITUnverified}
	}
	if p.Labels.Kind == labelKindNextOpenHeldSessions {
		pool.TradabilityPolicy = labelPolicySkipCorporateEvents
	}
	p.Universe = pool
	return p, p.Validate()
}
