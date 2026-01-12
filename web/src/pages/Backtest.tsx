import React, { useState } from 'react';
import { Card, Form, DatePicker, Button, Statistic, Table, Row, Col, message } from 'antd';
import axios from 'axios';
import { BacktestResult } from '../types';
import { createChart, ColorType } from 'lightweight-charts';

export const Backtest: React.FC = () => {
    const [loading, setLoading] = useState(false);
    const [result, setResult] = useState<BacktestResult | null>(null);

    const onFinish = async (values: any) => {
        setLoading(true);
        try {
            const res = await axios.post('/api/backtest', {
                start_date: values.dateRange ? values.dateRange[0].format('YYYY-MM-DD') : '',
                end_date: values.dateRange ? values.dateRange[1].format('YYYY-MM-DD') : '',
                initial_cash: 100000,
                strategy_type: 'tail_strategy'
            });
            setResult(res.data);
            message.success('回测完成');
        } catch (err) {
            message.error('回测失败');
        } finally {
            setLoading(false);
        }
    };

    const EquityChart = ({ data }: { data: { time: string, value: number }[] }) => {
        const chartContainerRef = React.useRef<HTMLDivElement>(null);
    
        React.useEffect(() => {
            if (!chartContainerRef.current) return;
            const chart = createChart(chartContainerRef.current, {
                layout: { background: { type: ColorType.Solid, color: 'white' } },
                width: chartContainerRef.current.clientWidth,
                height: 300,
            });
            const lineSeries = chart.addLineSeries({ color: '#2962FF' });
            lineSeries.setData(data);
            chart.timeScale().fitContent();
            return () => chart.remove();
        }, [data]);
    
        return <div ref={chartContainerRef} />;
    };

    return (
        <div className="p-4">
            <Card title="策略回测" className="mb-4">
                <Form layout="inline" onFinish={onFinish}>
                    <Form.Item name="dateRange" label="回测区间">
                        <DatePicker.RangePicker />
                    </Form.Item>
                    <Form.Item>
                        <Button type="primary" htmlType="submit" loading={loading}>开始回测</Button>
                    </Form.Item>
                </Form>
            </Card>

            {result && (
                <>
                    <Row gutter={16} className="mb-4">
                        <Col span={12}>
                            <Card>
                                <Statistic title="总收益率" value={result.total_return} precision={2} suffix="%" valueStyle={{ color: result.total_return >= 0 ? '#cf1322' : '#3f8600' }} />
                            </Card>
                        </Col>
                        <Col span={12}>
                            <Card>
                                <Statistic title="胜率" value={result.win_rate * 100} precision={2} suffix="%" />
                            </Card>
                        </Col>
                    </Row>
                    
                    <Card title="资金曲线" className="mb-4">
                        <EquityChart data={result.equity_curve.map(p => ({ time: p.date, value: p.value }))} />
                    </Card>

                    <Card title="交易明细">
                        <Table 
                            dataSource={result.trades} 
                            rowKey={(record) => record.stock_code + record.buy_date}
                            columns={[
                                { title: '代码', dataIndex: 'stock_code' },
                                { title: '买入日期', dataIndex: 'buy_date' },
                                { title: '卖出日期', dataIndex: 'sell_date' },
                                { title: '买入价格', dataIndex: 'buy_price', render: (val) => val.toFixed(2) },
                                { title: '卖出价格', dataIndex: 'sell_price', render: (val) => val.toFixed(2) },
                                { title: '盈亏', dataIndex: 'profit', render: (val) => <span style={{ color: val >= 0 ? 'red' : 'green' }}>{val.toFixed(2)}</span> },
                            ]}
                        />
                    </Card>
                </>
            )}
        </div>
    );
};
