export interface KLine {
    date: string;
    open: number;
    high: number;
    low: number;
    close: number;
    volume: number;
    ma5: number;
    ma10: number;
    ma20: number;
}

export interface Stock {
    code: string;
    name: string;
    price: number;
    change: number;
    market_cap: number;
    k_lines: KLine[];
}

export interface BacktestResult {
    total_return: number;
    win_rate: number;
    trades: Trade[];
    equity_curve: EquityPoint[];
}

export interface Trade {
    stock_code: string;
    buy_date: string;
    sell_date: string;
    buy_price: number;
    sell_price: number;
    profit: number;
}

export interface EquityPoint {
    date: string;
    value: number;
}
