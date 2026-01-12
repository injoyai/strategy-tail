import React, { useEffect, useState, useRef } from 'react';
import { Card, InputNumber, Form, Button, Row, Col, Statistic, Tag, Spin, Space } from 'antd';
import axios from 'axios';
import { Stock } from '../types';
import { KLineChart } from '../components/KLineChart';

export const StockSelection: React.FC = () => {
    const [stocks, setStocks] = useState<Stock[]>([]);
    const [loading, setLoading] = useState(true);
    const [filters, setFilters] = useState({ minMarketCap: 0, maxMarketCap: 0 });
    const wsRef = useRef<WebSocket | null>(null);

    const fetchStocks = async () => {
        setLoading(true);
        try {
            const res = await axios.get('/api/stocks', {
                params: {
                    min_market_cap: filters.minMarketCap,
                    max_market_cap: filters.maxMarketCap,
                }
            });
            setStocks(res.data);
        } catch (err) {
            console.error(err);
        } finally {
            setLoading(false);
        }
    };

    useEffect(() => {
        fetchStocks();

        // Setup WebSocket
        const connectWs = () => {
            const ws = new WebSocket(`ws://${window.location.host}/ws`);
            ws.onmessage = (event) => {
                const updatedStocks = JSON.parse(event.data) as Stock[];
                // Merge updates efficiently
                setStocks(prev => {
                    const stockMap = new Map(prev.map(s => [s.code, s]));
                    updatedStocks.forEach(s => {
                        // We only want to update price and change if the stock exists in current filtered list
                        // But wait, the backend broadcasts ALL stocks.
                        // We need to match efficiently.
                        if (stockMap.has(s.code)) {
                            const existing = stockMap.get(s.code)!;
                            stockMap.set(s.code, { ...existing, price: s.price, change: s.change, k_lines: s.k_lines });
                        }
                    });
                    return Array.from(stockMap.values());
                });
            };
            ws.onclose = () => {
                setTimeout(connectWs, 3000);
            };
            wsRef.current = ws;
        };

        connectWs();

        return () => {
            if (wsRef.current) wsRef.current.close();
        };
    }, []); // Run once on mount

    // Re-fetch when filters change (manual trigger)
    const onFinish = (values: any) => {
        setFilters({
            minMarketCap: values.minMarketCap || 0,
            maxMarketCap: values.maxMarketCap || 0,
        });
        // We need to trigger fetchStocks after state update, or just call it directly with values
        setLoading(true);
        axios.get('/api/stocks', {
            params: {
                min_market_cap: values.minMarketCap,
                max_market_cap: values.maxMarketCap,
            }
        }).then(res => {
            setStocks(res.data);
            setLoading(false);
        });
    };

    return (
        <div className="p-2">
            <div className="mb-4 flex items-center justify-between">
                <div>
                    <div className="text-base font-semibold">选股列表</div>
                    <div className="text-xs text-gray-500">实时刷新 · 每个卡片含 K 线与均线</div>
                </div>
                <Tag color="blue">{stocks.length} 只</Tag>
            </div>

            <Card className="mb-4 shadow-sm" bodyStyle={{ padding: 16 }}>
                <Form layout="inline" onFinish={onFinish} initialValues={{ minMarketCap: 0, maxMarketCap: 0 }}>
                    <Space wrap size={12}>
                        <Form.Item name="minMarketCap" label="最小市值(亿)">
                            <InputNumber min={0} placeholder="例如 50" />
                        </Form.Item>
                        <Form.Item name="maxMarketCap" label="最大市值(亿)">
                            <InputNumber min={0} placeholder="例如 300" />
                        </Form.Item>
                        <Form.Item>
                            <Button type="primary" htmlType="submit">应用筛选</Button>
                        </Form.Item>
                    </Space>
                </Form>
            </Card>

            {loading ? (
                <div className="text-center p-10"><Spin size="large" /></div>
            ) : (
                <Row gutter={[16, 16]}>
                    {stocks.map(stock => (
                        <Col xs={24} sm={12} lg={8} key={stock.code}>
                            <Card 
                                size="small" 
                                title={
                                    <div className="flex items-center justify-between">
                                        <div className="min-w-0">
                                            <div className="truncate font-medium">{stock.name}</div>
                                            <div className="text-xs text-gray-500">{stock.code}</div>
                                        </div>
                                    </div>
                                }
                                extra={
                                    <Tag color={stock.change >= 0 ? 'red' : 'green'}>
                                        {stock.change >= 0 ? '+' : ''}{stock.change.toFixed(2)}%
                                    </Tag>
                                }
                                className="shadow-sm hover:shadow-md transition-shadow"
                                bodyStyle={{ padding: 12 }}
                            >
                                <div className="flex items-center justify-between mb-3">
                                    <div>
                                        <div className="text-xs text-gray-500 mb-1">现价</div>
                                        <div className={`text-xl font-semibold ${stock.change >= 0 ? 'text-red-600' : 'text-green-600'}`}>
                                            {stock.price.toFixed(2)}
                                        </div>
                                    </div>
                                    <Statistic 
                                        title="市值(亿)" 
                                        value={stock.market_cap} 
                                        precision={2} 
                                    />
                                </div>
                                <div className="h-[200px] w-full rounded bg-white">
                                    <KLineChart data={stock.k_lines} height={200} />
                                </div>
                            </Card>
                        </Col>
                    ))}
                </Row>
            )}
        </div>
    );
};
