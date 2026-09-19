package portfolioresearch

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
)

// types_test.go v2 Task 1 领域层测试：modelHash 规范化稳定性/敏感性、
// 证据降级规则、枚举白名单、资源限制与非有限浮点拒绝。

// factorA / factorB 两个语义不同的因子引用（不同 validation/候选/参数）。
func factorA() ValidatedFactorRef {
	return ValidatedFactorRef{
		CandidateID:           "fc_20260917T150100000Z_99aabbcc",
		CandidateRevision:     1,
		ValidationID:          "fv_20260917T160000000Z_12345678",
		FactorKind:            "momentum",
		FactorDays:            20,
		ImplementationVersion: 1,
		Direction:             DirectionHigherIsBetter,
		EvidenceClass:         EvidenceRetrospective,
		PrimaryHorizon:        5,
		DataSnapshot: DataSnapshot{
			UniverseMode: "historical_membership",
			PriceSource:  "tdx",
			PriceVersion: "v1",
			PITState:     "verified",
		},
	}
}

func factorB() ValidatedFactorRef {
	return ValidatedFactorRef{
		CandidateID:           "fc_20260917T150200000Z_88aaccee",
		CandidateRevision:     1,
		ValidationID:          "fv_20260917T160100000Z_abcdef12",
		FactorKind:            "volatility",
		FactorDays:            10,
		ImplementationVersion: 1,
		Direction:             DirectionLowerIsBetter,
		EvidenceClass:         EvidenceProspective,
		PrimaryHorizon:        3,
		DataSnapshot: DataSnapshot{
			UniverseMode: "historical_membership",
			PriceSource:  "tdx",
			PriceVersion: "v1",
			PITState:     "verified",
		},
	}
}

// validModel 构造语义合法、证据等级与最弱输入一致的模型。
func validModel(factors ...ValidatedFactorRef) FactorModel {
	if len(factors) == 0 {
		factors = []ValidatedFactorRef{factorA()}
	}
	classes := make([]string, len(factors))
	for i, f := range factors {
		classes[i] = f.EvidenceClass
	}
	ec, err := CombineEvidenceClass(classes)
	if err != nil {
		panic(err)
	}
	return FactorModel{
		ModelID:          "fm_x",
		Revision:         1,
		CreatedAt:        "2026-09-18T00:00:00Z",
		CreatedBy:        "tester",
		ResearchQuestion: "动量与波动是否互补？",
		Hypothesis:       "等权秩合成样本外更稳定",
		ValidatedFactors: factors,
		TransformPipeline: TransformPipeline{
			Missing:     TransformMissingExclude,
			Winsorize:   WinsorizeSpec{Mode: TransformWinsorizeQuantile, Quantile: 0.01},
			Neutralize:  NeutralizeSpec{Mode: TransformNeutralizeNone},
			Standardize: TransformStandardizeRank,
		},
		Combination: CombinationSpec{Method: CombinationEqualWeightRank},
		PortfolioPolicy: PortfolioPolicy{
			Selection: PortfolioSelectionTopN, TopN: 20, CashBuffer: 0.05,
		},
		Execution: ExecutionSpec{
			Rebalance: RebalanceDaily, FillAt: FillNextOpen,
			SellFirst: true, T1Restriction: true, LotSize: 100,
			Cost: CostSpec{CommissionRate: 0.0003, StampDutyRate: 0.001, Slippage: 0.01, MinCommission: 5},
		},
		Benchmark:     BenchmarkSpec{ID: "hs300"},
		EvidenceClass: ec,
		CodeVersion:   "test",
		DataSnapshot:  factors[0].DataSnapshot,
	}
}

// TestModelHashCanonicalJSONDifferentKeyOrder 相同语义、不同 JSON 键序与
// 不同浮点表示得到同一 hash（map 键排序归一 + 浮点统一格式化）。
func TestModelHashCanonicalJSONDifferentKeyOrder(t *testing.T) {
	// 版本 A：键序一，浮点用十进制小数。
	rawA := `{
		"validatedFactors": [{
			"candidateId": "fc_20260917T150100000Z_99aabbcc",
			"candidateRevision": 1,
			"validationId": "fv_20260917T160000000Z_12345678",
			"factorKind": "momentum",
			"factorDays": 20,
			"implementationVersion": 1,
			"direction": "higher_is_better",
			"evidenceClass": "retrospective",
			"primaryHorizon": 5,
			"dataSnapshot": {"universeMode": "historical_membership", "priceSource": "tdx", "priceVersion": "v1", "pitState": "verified"}
		}],
		"transformPipeline": {
			"missing": "exclude",
			"winsorize": {"mode": "quantile", "quantile": 0.01},
			"neutralize": {"mode": "none"},
			"standardize": "rank"
		},
		"combination": {"method": "equal_weight_rank"},
		"portfolioPolicy": {"selection": "top_n", "topN": 20, "cashBuffer": 0.05},
		"execution": {"rebalance": "daily", "fillAt": "next_open", "sellFirst": true, "t1Restriction": true, "lotSize": 100, "cost": {"commissionRate": 0.0003, "stampDutyRate": 0.001, "slippage": 0.01, "minCommission": 5}},
		"benchmark": {"id": "hs300"},
		"dataSnapshot": {"universeMode": "historical_membership", "priceSource": "tdx", "priceVersion": "v1", "pitState": "verified"}
	}`
	// 版本 B：键序打乱，浮点改用科学计数/整数写法（反序列化后同一 float64）。
	rawB := `{
		"dataSnapshot": {"pitState": "verified", "priceVersion": "v1", "priceSource": "tdx", "universeMode": "historical_membership"},
		"benchmark": {"id": "hs300"},
		"execution": {"lotSize": 100, "t1Restriction": true, "sellFirst": true, "fillAt": "next_open", "rebalance": "daily", "cost": {"minCommission": 5.0, "slippage": 1e-2, "stampDutyRate": 1e-3, "commissionRate": 3e-4}},
		"portfolioPolicy": {"cashBuffer": 5e-2, "topN": 20, "selection": "top_n"},
		"combination": {"method": "equal_weight_rank"},
		"transformPipeline": {"standardize": "rank", "neutralize": {"mode": "none"}, "winsorize": {"quantile": 1e-2, "mode": "quantile"}, "missing": "exclude"},
		"validatedFactors": [{
			"dataSnapshot": {"pitState": "verified", "priceVersion": "v1", "priceSource": "tdx", "universeMode": "historical_membership"},
			"primaryHorizon": 5,
			"evidenceClass": "retrospective",
			"direction": "higher_is_better",
			"implementationVersion": 1,
			"factorDays": 20,
			"factorKind": "momentum",
			"validationId": "fv_20260917T160000000Z_12345678",
			"candidateRevision": 1,
			"candidateId": "fc_20260917T150100000Z_99aabbcc"
		}]
	}`
	var mA, mB FactorModel
	if err := json.Unmarshal([]byte(rawA), &mA); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(rawB), &mB); err != nil {
		t.Fatal(err)
	}
	hA, err := ModelHash(mA)
	if err != nil {
		t.Fatal(err)
	}
	hB, err := ModelHash(mB)
	if err != nil {
		t.Fatal(err)
	}
	if hA != hB {
		t.Fatalf("相同语义不同键序/浮点表示 hash 应一致:\n%s\n%s", hA, hB)
	}
}

// TestModelHashMetaNotIncluded 元数据（modelId/revision/createdAt/codeVersion
// 与派生 evidenceClass）不改变语义 hash。
func TestModelHashMetaNotIncluded(t *testing.T) {
	m1 := validModel()
	m2 := m1
	m2.ModelID = "fm_another"
	m2.Revision = 2
	m2.CreatedAt = "2030-01-01T00:00:00Z"
	m2.CodeVersion = "v999"
	h1, err := ModelHash(m1)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := ModelHash(m2)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("元数据不应进入语义 hash: %s vs %s", h1, h2)
	}
}

// TestModelHashFactorOrderIrrelevant 因子集合相同但顺序不同 → 同一 hash。
func TestModelHashFactorOrderIrrelevant(t *testing.T) {
	m1 := validModel(factorA(), factorB())
	m2 := validModel(factorB(), factorA())
	h1, err := ModelHash(m1)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := ModelHash(m2)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("因子顺序不应改变模型身份: %s vs %s", h1, h2)
	}
}

// TestModelHashSensitiveToSemanticChanges 任一因子/方向/变换/约束/成本变化
// 导致 hash 变化。
func TestModelHashSensitiveToSemanticChanges(t *testing.T) {
	base := validModel()
	baseHash, err := ModelHash(base)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*FactorModel){
		"因子实例参数变化": func(m *FactorModel) { m.ValidatedFactors[0].FactorDays = 21 },
		"因子引用变化":   func(m *FactorModel) { m.ValidatedFactors[0].ValidationID = "fv_20990101T000000000Z_ffffffff" },
		"方向变化":     func(m *FactorModel) { m.ValidatedFactors[0].Direction = DirectionLowerIsBetter },
		"变换变化":     func(m *FactorModel) { m.TransformPipeline.Standardize = TransformStandardizeZScore },
		"约束变化":     func(m *FactorModel) { m.PortfolioPolicy.TopN = 30 },
		"成本变化":     func(m *FactorModel) { m.Execution.Cost.CommissionRate = 0.0005 },
		"合成方法变化": func(m *FactorModel) {
			m.Combination = CombinationSpec{
				Method:    CombinationRollingICWeight,
				RollingIC: &RollingICSpec{WindowYears: 3, Shrinkage: 0.5, MaxAbsWeight: 0.3, Fallback: CombinationFallbackEqualWeight},
			}
		},
	}
	for name, mut := range cases {
		// 深拷贝因子切片：各 case 的 mutation 不得污染 base/其他 case
		// （ValidatedFactors 是引用类型，值拷贝仍共享底层数组）。
		changed := base
		changed.ValidatedFactors = make([]ValidatedFactorRef, len(base.ValidatedFactors))
		copy(changed.ValidatedFactors, base.ValidatedFactors)
		mut(&changed)
		changedHash, err := ModelHash(changed)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if changedHash == baseHash {
			t.Fatalf("%s 应改变 modelHash", name)
		}
	}
}

// TestModelHashRejectsNonFinite NaN/Inf 浮点拒绝进入 hash。
func TestModelHashRejectsNonFinite(t *testing.T) {
	cases := map[string]func(*FactorModel){
		"commission NaN": func(m *FactorModel) { m.Execution.Cost.CommissionRate = math.NaN() },
		"cashBuffer Inf": func(m *FactorModel) { m.PortfolioPolicy.CashBuffer = math.Inf(1) },
		"shrinkage NaN": func(m *FactorModel) {
			m.Combination = CombinationSpec{Method: CombinationRollingICWeight,
				RollingIC: &RollingICSpec{WindowYears: 3, Shrinkage: math.NaN(), MaxAbsWeight: 0.3, Fallback: CombinationFallbackCash}}
		},
	}
	for name, mut := range cases {
		m := validModel()
		mut(&m)
		if _, err := ModelHash(m); err == nil {
			t.Fatalf("%s 应拒绝非有限浮点", name)
		}
	}
}

// TestModelHashNormalizesNegativeZero -0.0 与 0.0 语义相同但序列化字节不同
// （"-0" vs "0"）：归一化后必须产生同一 hash。
func TestModelHashNormalizesNegativeZero(t *testing.T) {
	mNeg := validModel()
	mNeg.Execution.Cost.CommissionRate = math.Copysign(0, -1) // -0.0
	mZero := validModel()
	mZero.Execution.Cost.CommissionRate = 0.0
	// 先证实二者序列化字节确实不同，确保测试不是恒真。
	negBuf, err := json.Marshal(mNeg.Execution.Cost.CommissionRate)
	if err != nil {
		t.Fatal(err)
	}
	zeroBuf, err := json.Marshal(mZero.Execution.Cost.CommissionRate)
	if err != nil {
		t.Fatal(err)
	}
	if string(negBuf) == string(zeroBuf) {
		t.Fatalf("测试前提不成立：-0.0 与 0.0 序列化字节应不同（%s vs %s）", negBuf, zeroBuf)
	}
	hNeg, err := ModelHash(mNeg)
	if err != nil {
		t.Fatal(err)
	}
	hZero, err := ModelHash(mZero)
	if err != nil {
		t.Fatal(err)
	}
	if hNeg != hZero {
		t.Fatalf("-0.0 与 0.0 语义相同应产生同一 hash:\n%s\n%s", hNeg, hZero)
	}
	// 入参不被污染：-0.0 输入在归一化后仍是 -0.0。
	if got := mNeg.Execution.Cost.CommissionRate; !math.Signbit(got) {
		t.Fatalf("normalizeSemantic 不得修改入参: %v", got)
	}
}

// TestCombineEvidenceClass 证据降级：模型证据等级 = 最弱输入。
func TestCombineEvidenceClass(t *testing.T) {
	if got, err := CombineEvidenceClass([]string{EvidenceRetrospective}); err != nil || got != EvidenceRetrospective {
		t.Fatalf("单一 retrospective = %q/%v", got, err)
	}
	if got, err := CombineEvidenceClass([]string{EvidenceRetrospective, EvidenceProspective}); err != nil || got != EvidenceRetrospective {
		t.Fatalf("retrospective+prospective 应降级为 retrospective: %q/%v", got, err)
	}
	if got, err := CombineEvidenceClass([]string{EvidenceExploratory, EvidenceProspective, EvidenceRetrospective}); err != nil || got != EvidenceExploratory {
		t.Fatalf("含 exploratory 应降级为 exploratory: %q/%v", got, err)
	}
	if _, err := CombineEvidenceClass(nil); err == nil {
		t.Fatal("空列表应报错")
	}
	if _, err := CombineEvidenceClass([]string{"bogus"}); err == nil {
		t.Fatal("非法证据等级应报错")
	}
}

// TestFactorModelValidateRejectsSelfReportedEvidence 模型证据等级必须等于
// 后端派生的最弱输入；客户端自报更高等级被拒绝。
func TestFactorModelValidateRejectsSelfReportedEvidence(t *testing.T) {
	m := validModel()
	if err := m.Validate(DefaultModelLimits()); err != nil {
		t.Fatalf("合法模型应通过: %v", err)
	}
	// 自报 prospective（输入是 retrospective）→ 拒绝，错误指向证据等级。
	m.EvidenceClass = EvidenceProspective
	err := m.Validate(DefaultModelLimits())
	if err == nil {
		t.Fatal("客户端自报高于输入的证据等级应被拒绝")
	}
	if !strings.Contains(err.Error(), "evidenceClass") {
		t.Fatalf("错误应指向 evidenceClass: %v", err)
	}
}

// TestFactorModelValidateLimits 资源限制：超限错误包含具体字段与允许范围。
func TestFactorModelValidateLimits(t *testing.T) {
	limits := DefaultModelLimits()

	m := validModel()
	m.ValidatedFactors = make([]ValidatedFactorRef, limits.MaxFactors+1)
	for i := range m.ValidatedFactors {
		m.ValidatedFactors[i] = factorA()
	}
	err := m.Validate(limits)
	if err == nil {
		t.Fatal("因子超限应拒绝")
	}
	want := "factors: 最多 20 个，实际 21"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("错误应包含 %q: %v", want, err)
	}

	m = validModel()
	m.ValidatedFactors[0].PrimaryHorizon = limits.MaxHorizonDays + 1
	if err := m.Validate(limits); err == nil || !strings.Contains(err.Error(), "primaryHorizon") {
		t.Fatalf("primaryHorizon 超限应拒绝并指向字段: %v", err)
	}

	m = validModel()
	m.PortfolioPolicy.TopN = limits.MaxTopN + 1
	if err := m.Validate(limits); err == nil || !strings.Contains(err.Error(), "topN") {
		t.Fatalf("topN 超限应拒绝并指向字段: %v", err)
	}
}

// TestFactorModelValidateTextLimits 文本字段 rune 上限并入 Validate：超长
// 文本报错并包含字段名与允许范围（fail closed 读取路径同样拦截）。
func TestFactorModelValidateTextLimits(t *testing.T) {
	limits := DefaultModelLimits()
	cases := map[string]struct {
		field string
		limit int
		mut   func(*FactorModel)
	}{
		"createdBy 超长": {
			field: "createdBy",
			limit: limits.MaxCreatedByRunes,
			mut:   func(m *FactorModel) { m.CreatedBy = strings.Repeat("长", limits.MaxCreatedByRunes+1) },
		},
		"researchQuestion 超长": {
			field: "researchQuestion",
			limit: limits.MaxQuestionRunes,
			mut:   func(m *FactorModel) { m.ResearchQuestion = strings.Repeat("问", limits.MaxQuestionRunes+1) },
		},
		"hypothesis 超长": {
			field: "hypothesis",
			limit: limits.MaxHypothesisRunes,
			mut:   func(m *FactorModel) { m.Hypothesis = strings.Repeat("假", limits.MaxHypothesisRunes+1) },
		},
	}
	for name, tc := range cases {
		m := validModel()
		tc.mut(&m)
		err := m.Validate(limits)
		if err == nil {
			t.Fatalf("%s 应拒绝", name)
		}
		if !strings.Contains(err.Error(), tc.field) {
			t.Fatalf("%s 错误应包含字段名 %q: %v", name, tc.field, err)
		}
		if !strings.Contains(err.Error(), fmt.Sprintf("1～%d", tc.limit)) {
			t.Fatalf("%s 错误应包含允许范围 1～%d: %v", name, tc.limit, err)
		}
	}
}

// TestTransformPipelineValidate 变换流水线枚举白名单与参数一致性。
func TestTransformPipelineValidate(t *testing.T) {
	valid := TransformPipeline{
		Missing:     TransformMissingExclude,
		Winsorize:   WinsorizeSpec{Mode: TransformWinsorizeQuantile, Quantile: 0.01},
		Neutralize:  NeutralizeSpec{Mode: TransformNeutralizeNone},
		Standardize: TransformStandardizeRank,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("合法流水线应通过: %v", err)
	}
	cases := map[string]func(*TransformPipeline){
		"非法缺失策略":      func(p *TransformPipeline) { p.Missing = "bogus" },
		"quantile 越界": func(p *TransformPipeline) { p.Winsorize.Quantile = 0.6 },
		"none 携带阈值": func(p *TransformPipeline) {
			p.Winsorize = WinsorizeSpec{Mode: TransformWinsorizeNone, Quantile: 0.01}
		},
		"industry_size 缺数据集": func(p *TransformPipeline) {
			p.Neutralize = NeutralizeSpec{Mode: TransformNeutralizeIndustrySize}
		},
		"非法标准化": func(p *TransformPipeline) { p.Standardize = "minmax" },
	}
	for name, mut := range cases {
		p := valid
		mut(&p)
		if err := p.Validate(); err == nil {
			t.Fatalf("%s 应拒绝", name)
		}
	}
}

// TestCombinationSpecValidate 合成方法白名单与滚动参数范围。
func TestCombinationSpecValidate(t *testing.T) {
	if err := (CombinationSpec{Method: CombinationEqualWeightRank}).Validate(); err != nil {
		t.Fatalf("等权秩应通过: %v", err)
	}
	if err := (CombinationSpec{Method: CombinationEqualWeightRank,
		RollingIC: &RollingICSpec{}}).Validate(); err == nil {
		t.Fatal("等权秩携带 rollingIc 应拒绝")
	}
	rolling := CombinationSpec{Method: CombinationRollingICWeight,
		RollingIC: &RollingICSpec{WindowYears: 3, Shrinkage: 0.5, MaxAbsWeight: 0.3, Fallback: CombinationFallbackEqualWeight}}
	if err := rolling.Validate(); err != nil {
		t.Fatalf("合法滚动权重应通过: %v", err)
	}
	if err := (CombinationSpec{Method: CombinationRollingICWeight}).Validate(); err == nil {
		t.Fatal("滚动权重缺 rollingIc 应拒绝")
	}
	bad := rolling
	bad.RollingIC.MaxAbsWeight = 1.5
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "maxAbsWeight") {
		t.Fatalf("maxAbsWeight 越界应指向字段: %v", err)
	}
}

// TestPortfolioPolicyValidate 目标组合政策范围校验。
func TestPortfolioPolicyValidate(t *testing.T) {
	valid := PortfolioPolicy{Selection: PortfolioSelectionTopN, TopN: 20, CashBuffer: 0.05}
	if err := valid.Validate(DefaultModelLimits()); err != nil {
		t.Fatalf("合法政策应通过: %v", err)
	}
	if err := (PortfolioPolicy{Selection: PortfolioSelectionTopN, TopN: 0}).Validate(DefaultModelLimits()); err == nil {
		t.Fatal("topN=0 应拒绝")
	}
	if err := (PortfolioPolicy{Selection: PortfolioSelectionTopQuantile, TopQuantile: 0.5}).Validate(DefaultModelLimits()); err != nil {
		t.Fatalf("top_quantile 应通过: %v", err)
	}
	if err := (PortfolioPolicy{Selection: PortfolioSelectionTopQuantile, TopQuantile: 0.5, TopN: 5}).Validate(DefaultModelLimits()); err == nil {
		t.Fatal("top_quantile 携带 topN 应拒绝")
	}
	if err := (PortfolioPolicy{Selection: PortfolioSelectionTopN, TopN: 20, CashBuffer: 1.5}).Validate(DefaultModelLimits()); err == nil {
		t.Fatal("cashBuffer>1 应拒绝")
	}
}

// TestExecutionSpecValidate 执行规格范围校验。
func TestExecutionSpecValidate(t *testing.T) {
	valid := ExecutionSpec{
		Rebalance: RebalanceDaily, FillAt: FillNextOpen,
		SellFirst: true, T1Restriction: true, LotSize: 100,
		Cost: CostSpec{CommissionRate: 0.0003, StampDutyRate: 0.001, Slippage: 0.01, MinCommission: 5},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("合法执行规格应通过: %v", err)
	}
	if err := (ExecutionSpec{Rebalance: RebalanceDaily, FillAt: "same_close", LotSize: 100}).Validate(); err == nil {
		t.Fatal("fillAt 非法应拒绝")
	}
	if err := (ExecutionSpec{Rebalance: "bogus", FillAt: FillNextOpen, LotSize: 100}).Validate(); err == nil {
		t.Fatal("rebalance 非法应拒绝")
	}
	if err := (ExecutionSpec{Rebalance: RebalanceDaily, FillAt: FillNextOpen, LotSize: 0}).Validate(); err == nil {
		t.Fatal("lotSize=0 应拒绝")
	}
	badCost := valid
	badCost.Cost.CommissionRate = 0.1
	if err := badCost.Validate(); err == nil {
		t.Fatal("费率超上限应拒绝")
	}
}

// TestEnumWhitelists 运行状态/门禁状态/未成交原因枚举白名单。
func TestEnumWhitelists(t *testing.T) {
	for _, s := range []string{RunStateQueued, RunStateRunning, RunStateCompleted, RunStateFailed, RunStateCancelled, RunStateInsufficient} {
		if !ValidRunState(s) {
			t.Fatalf("运行状态 %q 应在白名单", s)
		}
	}
	if ValidRunState("bogus") {
		t.Fatal("非法运行状态应拒绝")
	}
	for _, s := range []string{GateStatusPassed, GateStatusFailed, GateStatusInsufficient, GateStatusError} {
		if !ValidGateStatus(s) {
			t.Fatalf("门禁状态 %q 应在白名单", s)
		}
	}
	if ValidGateStatus("bogus") {
		t.Fatal("非法门禁状态应拒绝")
	}
	for _, s := range []string{UnfilledLimitUp, UnfilledLimitDown, UnfilledSuspended, UnfilledNotListed,
		UnfilledT1Restricted, UnfilledInsufficientCash, UnfilledLotRounding, UnfilledTargetExpired} {
		if !ValidUnfilledReason(s) {
			t.Fatalf("未成交原因 %q 应在白名单", s)
		}
	}
	if ValidUnfilledReason("bogus") {
		t.Fatal("非法未成交原因应拒绝")
	}
}
