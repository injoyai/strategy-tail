package common

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/injoyai/conv/cfg"
	"github.com/injoyai/strategy-tail/core"
)

func init() {
	initConfig()
}

func initConfig() {
	// 私有连接文件不进入 Git 或镜像；其他配置保留公共文件优先的原有约定。
	private := ResolveRuntimePath(filepath.Join("config", "mysql.yaml"))
	if _, err := os.Stat(private); err == nil {
		cfg.Init(cfg.WithFile(private), cfg.WithFile(ResolveRuntimePath(filepath.Join("config", "config.yaml"))), cfg.WithEnv())
		return
	}
	cfg.Init(cfg.WithFile(ResolveRuntimePath(filepath.Join("config", "config.yaml"))), cfg.WithEnv())
}

// MySQLDSN 是所有运行入口共用的只读数据源配置。显式环境值优先，包括空值。
func MySQLDSN() (string, error) {
	value, explicit := os.LookupEnv("pull.mysql")
	if !explicit {
		value = cfg.GetString("pull.mysql", "")
	}
	if value = strings.TrimSpace(value); value != "" {
		return value, nil
	}
	return "", fmt.Errorf("未配置行情 MySQL：请设置 pull.mysql 或 config/mysql.yaml 中的 pull.mysql")
}

// LoadBacktestConfig 读取回测公共配置；未配置项使用以下默认值。
// 卖出和风控由各命令或 Lab 请求构造为 Seller。
func LoadBacktestConfig() (cost core.Cost, pos core.PositionConfig) {
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
	return
}
