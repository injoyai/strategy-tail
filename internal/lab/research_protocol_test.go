package lab

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// validResearchProtocol 构造一份全部字段合法的基准协议（historical_membership
// + next_open_to_close + 全部已验证），deriveEvidenceClass 应给 retrospective。
func validResearchProtocol() ResearchProtocol {
	return ResearchProtocol{
		SchemaVersion: protocolSchemaVersion,
		Hypothesis: HypothesisSpec{
			ID:                "hyp-momentum-20",
			Thesis:            "近期涨幅存在惯性，未来短期收益延续",
			ExpectedDirection: labelDirectionPositive,
			FailureCondition:  "全区间 IC 均值不显著为正或方向反转",
		},
		Universe: UniverseSpec{
			Mode:              universeHistoricalMembership,
			ID:                "all-a-share",
			Version:           "2026-01",
			IncludeDelisted:   true,
			MembershipPIT:     membershipPITVerified,
			TradabilityPolicy: "skip_suspended_and_limit_up",
		},
		Data: DataProvenance{
			PriceSource:      "local-klines",
			PriceVersion:     "2026-01",
			Adjustment:       adjustmentForward,
			ResearchDataView: "researchdata-default",
			PITState:         pitVerified,
			SnapshotAt:       "2026-01-02T00:00:00Z",
		},
		Signal: SignalSpec{Frequency: signalFrequencyDaily, FormedAt: signalFormedAtClose},
		Labels: LabelSpec{Kind: labelKindNextOpenToClose, Horizons: []int{1, 5, 10, 20}},
		Diagnostics: DiagnosticSpec{
			HACLagMode:      hacLagHorizonMinusOne,
			TurnoverPeriods: []int{1, 5},
			Neutralization: NeutralizationSpec{
				Mode:            neutralizationNone,
				Winsorization:   winsorizationNone,
				Standardization: standardizationRank,
			},
		},
		Trial: TrialSpec{FamilyID: "momentum", VariantID: "mom-20-v1", Rationale: "验证动量在可交易标签下的稳健性"},
	}
}

func TestResearchProtocolValid(t *testing.T) {
	p := validResearchProtocol()
	if err := p.Validate(); err != nil {
		t.Fatalf("基准协议应合法, got %v", err)
	}
	got, err := p.Normalize()
	if err != nil {
		t.Fatalf("Normalize 不应报错: %v", err)
	}
	// Normalize 不改变内容，且 slice 不共享底层数组。
	if len(got.Labels.Horizons) != len(p.Labels.Horizons) {
		t.Fatalf("Normalize 不得改变 Horizons 长度")
	}
	got.Labels.Horizons[0] = 99
	if p.Labels.Horizons[0] != 1 {
		t.Fatalf("Normalize 返回值修改不得影响原协议")
	}
}

func TestResearchProtocolSchemaVersionOnlyKnown(t *testing.T) {
	for _, v := range []int{0, 2, 99} {
		p := validResearchProtocol()
		p.SchemaVersion = v
		if err := p.Validate(); err == nil {
			t.Fatalf("SchemaVersion=%d 应被拒绝", v)
		}
	}
}

func TestResearchProtocolRestrictedIDs(t *testing.T) {
	illegal := []string{
		"",
		"../evil",
		"a/b",
		`a\b`,
		"..",
		"UPPER",
		"has space",
		"中文",
		"dot.name",
		strings.Repeat("a", 65),
		"-start-dash",
	}
	for _, field := range []struct {
		name string
		set  func(*ResearchProtocol, string)
	}{
		{"hypothesis.id", func(p *ResearchProtocol, v string) { p.Hypothesis.ID = v }},
		{"trial.familyId", func(p *ResearchProtocol, v string) { p.Trial.FamilyID = v }},
		{"trial.variantId", func(p *ResearchProtocol, v string) { p.Trial.VariantID = v }},
	} {
		for _, bad := range illegal {
			p := validResearchProtocol()
			field.set(&p, bad)
			if err := p.Validate(); err == nil {
				t.Fatalf("%s=%q 应被拒绝（受限 ID，不接受路径字符）", field.name, bad)
			}
		}
	}
}

func TestResearchProtocolHorizonsContract(t *testing.T) {
	mut := func(fn func(*LabelSpec)) ResearchProtocol {
		p := validResearchProtocol()
		fn(&p.Labels)
		return p
	}
	cases := []struct {
		name string
		p    ResearchProtocol
	}{
		{"空列表", mut(func(l *LabelSpec) { l.Horizons = nil })},
		{"含 0", mut(func(l *LabelSpec) { l.Horizons = []int{0, 1} })},
		{"含 61", mut(func(l *LabelSpec) { l.Horizons = []int{61} })},
		{"未升序", mut(func(l *LabelSpec) { l.Horizons = []int{5, 1} })},
		{"重复", mut(func(l *LabelSpec) { l.Horizons = []int{5, 5} })},
		{"超过 8 个", mut(func(l *LabelSpec) { l.Horizons = []int{1, 2, 3, 4, 5, 6, 7, 8, 9} })},
	}
	for _, c := range cases {
		if err := c.p.Validate(); err == nil {
			t.Fatalf("Horizons %s 应被拒绝", c.name)
		}
	}
	if err := mut(func(l *LabelSpec) { l.Horizons = []int{1, 60} }).Validate(); err != nil {
		t.Fatalf("边界 [1,60] 应合法: %v", err)
	}
}

func TestResearchProtocolLabelKindWhitelist(t *testing.T) {
	p := validResearchProtocol()
	p.Labels.Kind = "close_to_close"
	if err := p.Validate(); err == nil {
		t.Fatalf("未知标签类型应被拒绝")
	}
	for _, kind := range []string{labelKindNextOpenToClose, labelKindSameCloseToCloseLegacy} {
		p := validResearchProtocol()
		p.Labels.Kind = kind
		if err := p.Validate(); err != nil {
			t.Fatalf("标签 %s 应合法: %v", kind, err)
		}
	}
}

func TestResearchProtocolHACLag(t *testing.T) {
	cases := []struct {
		mode  string
		lag   int
		legal bool
	}{
		{hacLagHorizonMinusOne, 0, true},
		{hacLagHorizonMinusOne, 3, false}, // 默认模式不接受多余固定值
		{hacLagFixed, 0, true},
		{hacLagFixed, 60, true},
		{hacLagFixed, 61, false},
		{hacLagFixed, -1, false},
		{"auto", 0, false}, // 未知模式
	}
	for _, c := range cases {
		p := validResearchProtocol()
		p.Diagnostics.HACLagMode = c.mode
		p.Diagnostics.HACLag = c.lag
		err := p.Validate()
		if c.legal && err != nil {
			t.Fatalf("HAC(mode=%s,lag=%d) 应合法: %v", c.mode, c.lag, err)
		}
		if !c.legal && err == nil {
			t.Fatalf("HAC(mode=%s,lag=%d) 应被拒绝", c.mode, c.lag)
		}
	}
}

func TestResearchProtocolTurnoverPeriods(t *testing.T) {
	ok := validResearchProtocol()
	ok.Diagnostics.TurnoverPeriods = nil // 允许为空：不计算换手
	if err := ok.Validate(); err != nil {
		t.Fatalf("空换手间隔应合法: %v", err)
	}
	bad := validResearchProtocol()
	bad.Diagnostics.TurnoverPeriods = []int{2, 1}
	if err := bad.Validate(); err == nil {
		t.Fatalf("未升序换手间隔应被拒绝")
	}
	bad = validResearchProtocol()
	bad.Diagnostics.TurnoverPeriods = []int{61}
	if err := bad.Validate(); err == nil {
		t.Fatalf("换手间隔 61 应被拒绝")
	}
}

func TestResearchProtocolEnumWhitelist(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*ResearchProtocol)
	}{
		{"expectedDirection", func(p *ResearchProtocol) { p.Hypothesis.ExpectedDirection = "both" }},
		{"universe.mode", func(p *ResearchProtocol) { p.Universe.Mode = "future_list" }},
		{"membershipPit", func(p *ResearchProtocol) { p.Universe.MembershipPIT = "maybe" }},
		{"adjustment", func(p *ResearchProtocol) { p.Data.Adjustment = "auto" }},
		{"pitState", func(p *ResearchProtocol) { p.Data.PITState = "high" }},
		{"frequency", func(p *ResearchProtocol) { p.Signal.Frequency = "minute" }},
		{"formedAt", func(p *ResearchProtocol) { p.Signal.FormedAt = "open" }},
		{"neutralization.mode", func(p *ResearchProtocol) { p.Diagnostics.Neutralization.Mode = "beta" }},
		{"winsorization", func(p *ResearchProtocol) { p.Diagnostics.Neutralization.Winsorization = "clip" }},
		{"standardization", func(p *ResearchProtocol) { p.Diagnostics.Neutralization.Standardization = "minmax" }},
		{"hacLagMode", func(p *ResearchProtocol) { p.Diagnostics.HACLagMode = "auto" }},
	}
	for _, c := range cases {
		p := validResearchProtocol()
		c.mut(&p)
		if err := p.Validate(); err == nil {
			t.Fatalf("枚举 %s 非法值应被拒绝", c.name)
		}
	}
}

func TestResearchProtocolNeutralizationDatasets(t *testing.T) {
	p := validResearchProtocol()
	p.Diagnostics.Neutralization = NeutralizationSpec{
		Mode:            neutralizationIndustrySize,
		IndustryDataset: "sw-industry",
		SizeDataset:     "float-mcap",
		Winsorization:   winsorizationMAD,
		Standardization: standardizationZScore,
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("industry_size 且数据集齐全应合法: %v", err)
	}
	for _, drop := range []func(*NeutralizationSpec){
		func(n *NeutralizationSpec) { n.IndustryDataset = "" },
		func(n *NeutralizationSpec) { n.SizeDataset = "" },
	} {
		q := validResearchProtocol()
		q.Diagnostics.Neutralization.Mode = neutralizationIndustrySize
		drop(&q.Diagnostics.Neutralization)
		if err := q.Validate(); err == nil {
			t.Fatalf("industry_size 缺少数据集应被拒绝")
		}
	}
}

func TestResearchProtocolRequiredText(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*ResearchProtocol)
	}{
		{"thesis", func(p *ResearchProtocol) { p.Hypothesis.Thesis = "" }},
		{"failureCondition", func(p *ResearchProtocol) { p.Hypothesis.FailureCondition = "" }},
		{"trial.rationale", func(p *ResearchProtocol) { p.Trial.Rationale = "" }},
		{"historical_membership 缺来源 ID", func(p *ResearchProtocol) { p.Universe.ID = "" }},
		{"priceSource", func(p *ResearchProtocol) { p.Data.PriceSource = "" }},
		{"priceVersion", func(p *ResearchProtocol) { p.Data.PriceVersion = "" }},
		{"researchDataView", func(p *ResearchProtocol) { p.Data.ResearchDataView = "" }},
	}
	for _, c := range cases {
		p := validResearchProtocol()
		c.mut(&p)
		if err := p.Validate(); err == nil {
			t.Fatalf("缺失 %s 应被拒绝", c.name)
		}
	}
	// current_static 不要求来源 ID。
	p := validResearchProtocol()
	p.Universe.Mode = universeCurrentStatic
	p.Universe.ID = ""
	if err := p.Validate(); err != nil {
		t.Fatalf("current_static 允许空来源 ID: %v", err)
	}
}

func TestDeriveEvidenceClass(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*ResearchProtocol)
		want string
	}{
		{"协议完整历史成员池", func(p *ResearchProtocol) {}, evidenceRetrospective},
		{"current_static 上限 exploratory", func(p *ResearchProtocol) {
			p.Universe.Mode = universeCurrentStatic
		}, evidenceExploratory},
		{"旧同收盘标签上限 exploratory", func(p *ResearchProtocol) {
			p.Labels.Kind = labelKindSameCloseToCloseLegacy
		}, evidenceExploratory},
		{"PIT 未验证", func(p *ResearchProtocol) {
			p.Data.PITState = pitUnverified
		}, evidenceExploratory},
		{"PIT 部分", func(p *ResearchProtocol) {
			p.Data.PITState = pitPartial
		}, evidenceExploratory},
		{"复权口径未知", func(p *ResearchProtocol) {
			p.Data.Adjustment = adjustmentUnknown
		}, evidenceExploratory},
	}
	for _, c := range cases {
		p := validResearchProtocol()
		c.mut(&p)
		if err := p.Validate(); err != nil {
			t.Fatalf("%s: 测试协议本身应合法: %v", c.name, err)
		}
		if got := deriveEvidenceClass(p); got != c.want {
			t.Fatalf("%s: evidence class = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestNormalizeDoesNotUpgradeUnknownEnums(t *testing.T) {
	for name, mut := range map[string]func(*ResearchProtocol){
		"未知 PITState":      func(p *ResearchProtocol) { p.Data.PITState = "partially-verified" },
		"未知 Adjustment":    func(p *ResearchProtocol) { p.Data.Adjustment = "auto-adjusted" },
		"空 PITState":       func(p *ResearchProtocol) { p.Data.PITState = "" },
		"空 Adjustment":     func(p *ResearchProtocol) { p.Data.Adjustment = "" },
		"未知 MembershipPIT": func(p *ResearchProtocol) { p.Universe.MembershipPIT = "assumed" },
	} {
		p := validResearchProtocol()
		mut(&p)
		if _, err := p.Normalize(); err == nil {
			t.Fatalf("%s: Normalize 必须报错，不得静默升级为 verified", name)
		}
	}
}

func TestProtocolHashDeterministic(t *testing.T) {
	p := validResearchProtocol()
	h1, err := protocolHash(p)
	if err != nil {
		t.Fatalf("protocolHash 不应报错: %v", err)
	}
	h2, err := protocolHash(p)
	if err != nil {
		t.Fatalf("protocolHash 不应报错: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("同一协议 hash 必须确定: %s vs %s", h1, h2)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(h1) {
		t.Fatalf("hash 应为 64 位十六进制: %s", h1)
	}
	// JSON 往返后 hash 不变（保存/读取一致性）。
	var rt ResearchProtocol
	buf, _ := json.Marshal(p)
	if err := json.Unmarshal(buf, &rt); err != nil {
		t.Fatalf("往返失败: %v", err)
	}
	h3, err := protocolHash(rt)
	if err != nil {
		t.Fatalf("protocolHash 不应报错: %v", err)
	}
	if h3 != h1 {
		t.Fatalf("JSON 往返后 hash 必须一致: %s vs %s", h1, h3)
	}
	// 关键字段变化 → hash 变化。
	for name, mut := range map[string]func(*ResearchProtocol){
		"horizons":   func(p *ResearchProtocol) { p.Labels.Horizons = []int{1, 5, 10} },
		"direction":  func(p *ResearchProtocol) { p.Hypothesis.ExpectedDirection = labelDirectionNegative },
		"membership": func(p *ResearchProtocol) { p.Universe.MembershipPIT = membershipPITUnverified },
		"lag":        func(p *ResearchProtocol) { p.Diagnostics.HACLagMode = hacLagFixed; p.Diagnostics.HACLag = 4 },
	} {
		q := validResearchProtocol()
		mut(&q)
		hq, err := protocolHash(q)
		if err != nil {
			t.Fatalf("%s: protocolHash 不应报错: %v", name, err)
		}
		if hq == h1 {
			t.Fatalf("%s 变化后 hash 不得相同", name)
		}
	}
}

func TestEvidenceClassConstants(t *testing.T) {
	// 兼容合同：v3 报告（无协议）派生 exploratory + legacy 标签维度。
	if evidenceExploratory != "exploratory" || evidenceRetrospective != "retrospective" {
		t.Fatalf("证据等级常量值不得变更")
	}
	if labelKindSameCloseToCloseLegacy != "same_close_to_close_legacy" ||
		labelKindNextOpenToClose != "next_open_to_close" {
		t.Fatalf("标签类型常量值不得变更")
	}
}
