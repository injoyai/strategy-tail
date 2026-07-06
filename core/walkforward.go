package core

import (
	"fmt"
)

// ============================================================================
// 滚动前推分析（Walk-Forward Analysis）—— 阶段三
// ============================================================================
// 滚动前推分析是量化策略验证中的标准方法，用于检测策略过拟合风险。
// 核心思路：在一段历史数据（训练集）上验证策略表现，
// 然后在紧随其后的样本外数据（测试集）上检验策略是否依然有效。
// 窗口随时间向前滚动，重复此过程，最终评估样本外整体表现。
// 若训练期收益远高于测试期收益（过拟合得分 > 3），则策略疑似过拟合。
// ============================================================================

// WalkForwardResult 描述单个滚动窗口的前推分析结果。
type WalkForwardResult struct {
	TrainStart   int     // 训练期起始年份
	TrainEnd     int     // 训练期结束年份
	TestStart    int     // 测试期起始年份（样本外）
	TestEnd      int     // 测试期结束年份（样本外）
	TrainReturn  float64 // 训练期年均收益率（%）
	TestReturn   float64 // 测试期年化收益率（%）
	TrainSharpe  float64 // 训练期夏普比率（年均）
	TestSharpe   float64 // 测试期夏普比率
	TrainWinRate float64 // 训练期胜率（%）
	TestWinRate  float64 // 测试期胜率（%）
	TrainTrades  int     // 训练期总交易笔数
	TestTrades   int     // 测试期交易笔数
	OverfitScore float64 // 过拟合得分 = 训练收益 / 测试收益，>3 疑似过拟合
}

// WalkForward 执行滚动前推分析。
//
// 参数 trainYears 和 testYears 共同定义滚动窗口方案：
//   - 训练窗口大小 = len(trainYears) - len(testYears) + 1
//   - 测试窗口大小 = 1（每次取一个测试年作为样本外）
//   - 滚动步长 = 1 年
//   - 窗口数量 = len(testYears)
//
// 示例：trainYears=[2018,2019,2020], testYears=[2020,2021]
//
//	窗口1：训练 [2018,2019] → 测试 [2020]
//	窗口2：训练 [2019,2020] → 测试 [2021]
//
// 对每个窗口，分别调用 bt._backtest 在训练年与测试年上运行回测，
// 再通过 Analyze 计算年化收益、夏普、胜率、交易笔数等指标。
// 训练期取所有训练年指标的均值，测试期取该测试年的指标。
// 过拟合得分 = 训练期年均收益 / 测试期年化收益，超过 3 视为疑似过拟合。
func WalkForward(bt Backtest, trainYears []int, testYears []int) []WalkForwardResult {

	// 参数校验：训练年或测试年为空时无法分析
	if len(trainYears) == 0 || len(testYears) == 0 {
		return nil
	}

	// 计算训练窗口大小，确保至少为 1
	trainWindowSize := len(trainYears) - len(testYears) + 1
	if trainWindowSize < 1 {
		trainWindowSize = 1
	}

	results := make([]WalkForwardResult, 0, len(testYears))

	for i := 0; i < len(testYears); i++ {
		// 提取当前训练窗口
		trainEndIdx := i + trainWindowSize
		if trainEndIdx > len(trainYears) {
			break // 训练年不足，停止滚动
		}
		trainWindow := trainYears[i:trainEndIdx]
		testYear := testYears[i]

		// ---- 训练期回测：逐年回测后取均值 ----
		var trainReturnSum, trainSharpeSum, trainWinRateSum float64
		trainTradesTotal := 0
		for _, y := range trainWindow {
			trades, err := bt._backtest(bt.Codes, y)
			if err != nil {
				continue
			}
			ar := Analyze(y, trades, bt.GetDayKlines, nil, bt.Cost, bt.Position)
			trainReturnSum += ar.AnnualReturn
			trainSharpeSum += ar.Sharpe
			trainWinRateSum += ar.WinRate
			trainTradesTotal += ar.TotalTrades
		}

		// 训练期指标取均值
		trainN := len(trainWindow)
		trainReturn := 0.0
		trainSharpe := 0.0
		trainWinRate := 0.0
		if trainN > 0 {
			trainReturn = trainReturnSum / float64(trainN)
			trainSharpe = trainSharpeSum / float64(trainN)
			trainWinRate = trainWinRateSum / float64(trainN)
		}

		// ---- 测试期回测（样本外）----
		testReturn := 0.0
		testSharpe := 0.0
		testWinRate := 0.0
		testTradesCount := 0

		testTrades, err := bt._backtest(bt.Codes, testYear)
		if err == nil {
			ar := Analyze(testYear, testTrades, bt.GetDayKlines, nil, bt.Cost, bt.Position)
			testReturn = ar.AnnualReturn
			testSharpe = ar.Sharpe
			testWinRate = ar.WinRate
			testTradesCount = ar.TotalTrades
		}

		// ---- 过拟合得分 ----
		// = 训练期年均收益 / 测试期年化收益
		// 测试期收益为正时正常计算；测试期非正而训练期为正时标记高过拟合
		overfitScore := 0.0
		if testReturn > 0 {
			overfitScore = trainReturn / testReturn
		} else if trainReturn > 0 {
			overfitScore = 999.0
		}

		results = append(results, WalkForwardResult{
			TrainStart:   trainWindow[0],
			TrainEnd:     trainWindow[len(trainWindow)-1],
			TestStart:    testYear,
			TestEnd:      testYear,
			TrainReturn:  trainReturn,
			TestReturn:   testReturn,
			TrainSharpe:  trainSharpe,
			TestSharpe:   testSharpe,
			TrainWinRate: trainWinRate,
			TestWinRate:  testWinRate,
			TrainTrades:  trainTradesTotal,
			TestTrades:   testTradesCount,
			OverfitScore: overfitScore,
		})
	}

	return results
}

// String 格式化输出单个滚动窗口的前推分析结果。
func (r WalkForwardResult) String() string {
	overfitFlag := ""
	if r.OverfitScore > 3 {
		overfitFlag = "  [疑似过拟合]"
	}
	return fmt.Sprintf(
		"训练[%d-%d] → 测试[%d-%d] | "+
			"训练: 收益%.2f%% 夏普%.2f 胜率%.2f%% 交易%d笔 | "+
			"测试: 收益%.2f%% 夏普%.2f 胜率%.2f%% 交易%d笔 | "+
			"过拟合%.2f%s",
		r.TrainStart, r.TrainEnd, r.TestStart, r.TestEnd,
		r.TrainReturn, r.TrainSharpe, r.TrainWinRate, r.TrainTrades,
		r.TestReturn, r.TestSharpe, r.TestWinRate, r.TestTrades,
		r.OverfitScore, overfitFlag,
	)
}
