import React, { useEffect, useRef } from 'react';
import { createChart, ColorType, IChartApi } from 'lightweight-charts';
import { KLine } from '../types';

interface Props {
    data: KLine[];
    height?: number;
}

export const KLineChart: React.FC<Props> = ({ data, height = 200 }) => {
    const chartContainerRef = useRef<HTMLDivElement>(null);
    const chartRef = useRef<IChartApi | null>(null);

    useEffect(() => {
        if (!chartContainerRef.current) return;

        const chart = createChart(chartContainerRef.current, {
            layout: {
                background: { type: ColorType.Solid, color: 'transparent' },
                textColor: 'black',
            },
            width: chartContainerRef.current.clientWidth,
            height: height,
            grid: {
                vertLines: { visible: false },
                horzLines: { visible: false },
            },
            rightPriceScale: {
                visible: true,
                borderVisible: false,
            },
            timeScale: {
                visible: false,
                borderVisible: false,
            },
        });

        const candlestickSeries = chart.addCandlestickSeries({
            upColor: '#ef4444', // Red for up (Chinese style)
            downColor: '#22c55e', // Green for down
            borderVisible: false,
            wickUpColor: '#ef4444',
            wickDownColor: '#22c55e',
        });

        const ma5Series = chart.addLineSeries({ color: '#2962FF', lineWidth: 1 });
        // const ma10Series = chart.addLineSeries({ color: '#FF6D00', lineWidth: 1 });
        // const ma20Series = chart.addLineSeries({ color: '#D500F9', lineWidth: 1 });

        const chartData = data.map(d => ({
            time: d.date,
            open: d.open,
            high: d.high,
            low: d.low,
            close: d.close,
        }));

        const ma5Data = data.map(d => ({ time: d.date, value: d.ma5 }));

        candlestickSeries.setData(chartData);
        ma5Series.setData(ma5Data);

        chart.timeScale().fitContent();

        chartRef.current = chart;

        const handleResize = () => {
            if (chartContainerRef.current && chartRef.current) {
                chartRef.current.applyOptions({ width: chartContainerRef.current.clientWidth });
            }
        };

        window.addEventListener('resize', handleResize);

        return () => {
            window.removeEventListener('resize', handleResize);
            chart.remove();
        };
    }, [data, height]);

    return <div ref={chartContainerRef} />;
};
