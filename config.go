package common

import (
	"path/filepath"

	"github.com/injoyai/conv/cfg"
	"github.com/injoyai/strategy-tail/core"
)

func init() {
	// cfg 默认按启动目录读文件；统一到数据使用的根目录，保留文件优先于环境变量的顺序。
	cfg.Init(cfg.WithFile(ResolveRuntimePath(filepath.Join("config", "config.yaml"))), cfg.WithEnv())
}

// LoadBacktestConfig 读取回测公共配置；未配置项使用以下默认值。
// 卖出和风控由各命令或 Lab 请求构造为 Seller。
func LoadBacktestConfig() (cost core.Cost, pos core.PositionConfig, years []int, benchmark string, mcIterations int) {
	cost = core.Cost{
		CommissionRate:  cfg.GetFloat64("backtest.cost.commission_rate", 0.0001),
		StampDutyRate:   cfg.GetFloat64("backtest.cost.stamp_duty_rate", 0.0005),
		TransferFeeRate: cfg.GetFloat64("backtest.cost.transfer_fee_rate", 0.00001),
		Slippage:        cfg.GetFloat64("backtest.cost.slippage", 0.01),
		MinCommission:   cfg.GetFloat64("backtest.cost.min_commission", 0),
	}
	pos = core.PositionConfig{
		MaxPositions: cfg.GetInt("backtest.position.max_positions", 0),
		MaxPerCode:   cfg.GetInt("backtest.position.max_per_code", 1),
		SharesPerLot: cfg.GetInt("backtest.position.shares_per_lot", 100),
	}
	years = cfg.GetInts("backtest.years", []int{2020, 2021, 2022, 2023, 2024, 2025, 2026})
	benchmark = cfg.GetString("backtest.benchmark", "sh000300")
	mcIterations = cfg.GetInt("backtest.monte_carlo_iterations", 1000)
	return
}
