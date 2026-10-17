# SQLite 买点持久化重构设计

## 背景

当前 `/cmd/screen` 使用 JSON 文件（`buy_history.json` + `sold_history.json`）管理历史买卖点，存在以下问题：

1. JSON 文件管理不够简单，容易出现数据不一致
2. 已卖出的买点每次刷新都重新判定卖出逻辑
3. WS 推送的历史买卖点现价只更新一次就不更新了

## 核心概念

### 今日买点 vs 历史买点

| | 今日买点 | 历史买点 |
|---|---|---|
| **来源** | 盘中实时计算，每次刷新重算 | 收盘后确认，从 DB 加载 |
| **持久化** | 仅内存，不写 DB | 写入 SQLite |
| **卖出判定** | 不参与 | 未卖出的参与 |
| **现价更新** | 实时行情直接展示 | 每次刷新时更新 |
| **WS 推送** | `{type:"buy"}` | `{type:"history"}` |

### 关键逻辑

- 今日买点是"实时候选"，盘中可能变化（上午是买点下午可能不是），收盘后才确认
- 收盘后用收盘数据重算今日买点，确认后写入 SQLite 成为历史买点
- 卖出判定仅针对历史买点（已收盘确认的），今日实时买点不参与
- 卖出一旦确认（sold=1），不再重复判定
- 历史买卖点的现价每次选股刷新时都更新

## SQLite 表结构

```sql
CREATE TABLE IF NOT EXISTS buy_points (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    code TEXT NOT NULL,
    buy_time DATETIME NOT NULL,
    buy_price REAL NOT NULL,
    buy_date TEXT NOT NULL,
    sold INTEGER DEFAULT 0,
    sell_price REAL DEFAULT 0,
    sell_time DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_buy_date ON buy_points(buy_date);
CREATE INDEX IF NOT EXISTS idx_sold ON buy_points(sold);
```

DB 文件路径：`./data/screen.db`

## 流程设计

### 1. 启动时

- 打开/创建 SQLite DB
- 从 DB 加载最近 N 天（sellLookbackDays）的历史买点到内存
- 不加载今日数据（今日盘中实时计算）
- 异步回填缺失日期的历史买点（已有逻辑，改为写入 DB）

### 2. 盘中每次刷新（交易时间 9:30-15:01）

1. 实时计算今日买点 → 推送 `{type:"buy"}`（仅内存）
2. 对未卖出（sold=0）的历史买点判定卖出：
   - 调用 Seller.Sell() 判定
   - 如果是卖出点 → 标记 sold=1，记录 sell_price/sell_time → 更新 DB
   - 已 sold=1 的跳过，不再判定
3. 更新所有历史买卖点的现价（用实时行情 Close）→ 推送 `{type:"history"}`
4. 推送今日卖点 `{type:"sell"}`

### 3. 收盘后（15:01 左右）

- 用收盘数据重算今日买点，确认后写入 SQLite
- 更新内存中的历史买点
- 对新写入的历史买点也执行一次卖出判定

### 4. 跨天 00:00

- 用收盘数据刷新上一交易日买点（已有逻辑，改为更新 DB）

## 内存数据结构变更

### 删除

- `buyHistory map[string][]core.Buy` → 不再按日期分组
- `soldBuys map[string]*SellItem` → sold 状态直接存在 BuyPoint 中
- `persistedBuy` 结构体
- `loadHistoryFile`、`saveHistoryFile`、`loadSoldHistory`、`persistSoldHistory` 函数
- `historyFilePath`、`soldFilePath` 常量

### 新增

```go
// BuyPoint - 历史买点（内存+DB 对应）
type BuyPoint struct {
    ID        int64
    Code      string
    BuyTime   time.Time
    BuyPrice  float64
    BuyDate   string
    Sold      bool
    SellPrice float64
    SellTime  time.Time
}
```

### ScreenService 变更

```go
type ScreenService struct {
    mu               sync.RWMutex
    sellLookbackDays int
    db               *sql.DB              // SQLite 连接
    historyBuys      []BuyPoint           // 历史买点（从 DB 加载）
    lastBuys         *BuyResponse         // 今日买点快照
    lastSells        *SellResponse        // 卖点快照
    lastHistory      *HistoryResponse     // 历史买点快照
    subscribers      map[*fbr.Websocket]bool
}
```

## WS 推送数据变更

`history` 消息中的 BuyItem 现价每次刷新都更新：
- 未卖出：用实时行情的 Close 更新 `curr_price` 和 `income_rate`
- 已卖出：`curr_price` 仍用实时行情更新（方便看当前价），`income_rate` 用卖出价计算

## 删除的文件/逻辑

- `data/buy_history.json` 不再使用
- `data/sold_history.json` 不再使用
- 所有 JSON 文件读写相关代码

## 依赖

项目已有 `modernc.org/sqlite` 和 `xorm.io/xorm` 依赖，使用 `database/sql` + `github.com/glebarez/go-sqlite` 驱动即可（已在 go.sum 中）。
