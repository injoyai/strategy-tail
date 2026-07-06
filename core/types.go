package core

import (
	"fmt"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

const (
	TypeBuy  = "buy"
	TypeSell = "sell"

	// SharesPerLot A 股一手 = 100 股
	SharesPerLot = 100
)

type (
	GetDayKlines func(code string, start, end time.Time) (extend.Klines, error)
	GetMinKlines func(code string, start, end time.Time) (protocol.Klines, error)
)

// ============================================================================
// 交易记录
// ============================================================================

// Trade 描述一笔完整的买卖交易（含成本明细）。
// BuyPrice/SellPrice 为原始收盘价；
// BuyExecPrice/SellExecPrice 为含滑点的实际成交价；
// BuyCost/SellCost 为含佣金的实际投入/收回金额（元）。
type Trade struct {
	Code string
	// 时间
	BuyTime  time.Time
	SellTime time.Time
	// 价格（原始收盘价，用于策略复盘）
	BuyPrice  protocol.Price
	SellPrice protocol.Price
	// 实际成交价（含滑点）
	BuyExecPrice  protocol.Price
	SellExecPrice protocol.Price
	// 金额（元，含佣金/印花税等）
	BuyCost    float64 // 买入总支出 = 成交价×数量 + 佣金
	SellIncome float64 // 卖出总收入 = 成交价×数量 - 佣金 - 印花税
	// 数量（股）
	Quantity int
	// 是否为期末未平仓的虚拟成交
	Virtual bool
}

// Profit 收益率（%，按实际成本口径）。
// = (SellIncome - BuyCost) / BuyCost × 100
func (t Trade) Profit() float64 {
	if t.BuyCost <= 0 {
		return 0
	}
	return (t.SellIncome - t.BuyCost) / t.BuyCost * 100
}

// ProfitAmount 绝对盈亏（元）= SellIncome - BuyCost
func (t Trade) ProfitAmount() float64 {
	return t.SellIncome - t.BuyCost
}

// HoldingDays 持仓交易日数（按自然日近似，用于分布分析）
func (t Trade) HoldingDays() int {
	return int(t.SellTime.Sub(t.BuyTime).Hours() / 24)
}

// ============================================================================
// 策略接口
// ============================================================================

type (
	Price  = protocol.Price
	Klines = protocol.Klines
)

type Buyer interface {
	Name() string
	Buy(code string, dks extend.Klines) bool
}

// CompositeBuyer 可展开的组合买入策略接口，供诊断器递归展开。
type CompositeBuyer interface {
	Buyer
	Children() []Buyer
}

type Buy struct {
	Code  string
	Time  time.Time
	Price protocol.Price
}

func (b *Buy) String() string {
	return fmt.Sprintf("代码: %s  买入价: %.2f", b.Code, b.Price.Float64())
}

type Seller interface {
	Name() string
	Sell(code string, dks extend.Klines, buy Buy) bool
}

type Sell struct {
	Code  string
	Time  time.Time
	Price protocol.Price
}

func (s *Sell) String() string {
	return fmt.Sprintf("代码: %s  卖出价: %.2f", s.Code, s.Price.Float64())
}

// ============================================================================
// 成本模型
// ============================================================================

// Cost 描述交易成本模型。
// 各费率均为小数（如 0.0003 = 万三）。
type Cost struct {
	CommissionRate  float64        // 佣金费率（买/卖双边）
	StampDutyRate   float64        // 印花税率（仅卖出）
	TransferFeeRate float64        // 过户费率（沪市双边，深市为0）
	Slippage        protocol.Price // 滑点（每股绝对值，单边加减）
	MinCommission   float64        // 最低佣金（元），不足按此收取
}

// DefaultCost 返回 A 股常见成本参数。
// 佣金万三双边、印花税千一卖出、滑点 0.01 元、最低佣金 5 元。
func DefaultCost() Cost {
	return Cost{
		CommissionRate:  0.0001,              //佣金费率,万一
		StampDutyRate:   0.0005,              //印花税,0.05%
		TransferFeeRate: 0.00001,             //过户费,万0.1
		Slippage:        protocol.Yuan(0.01), //滑点,0.01元
		MinCommission:   0,                   //最低佣金,例5元
	}
}

// ============================================================================
// 仓位与资金管理
// ============================================================================

// PositionConfig 仓位管理配置。
// 按"笔数"模型：每笔买入固定股数（默认一手100股），
// 通过 MaxPerCode 限制单票最多同时持仓笔数，
// 通过 MaxPositions 限制全局同时持仓股票数。
type PositionConfig struct {
	MaxPositions int // 全局最大同时持仓股票数（0=不限）
	MaxPerCode   int // 单票最大同时持仓笔数（0=不限，1=单仓位T+1模型）
	SharesPerLot int // 每笔买入股数（默认100，即一手）
}

// DefaultPositionConfig 默认仓位配置：单票1笔、全局不限、每笔100股。
func DefaultPositionConfig() PositionConfig {
	return PositionConfig{
		MaxPositions: 0,
		MaxPerCode:   1,
		SharesPerLot: SharesPerLot,
	}
}
