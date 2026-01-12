import React, { useState } from 'react';
import { Layout, Tabs, theme } from 'antd';
import { StockOutlined, LineChartOutlined } from '@ant-design/icons';
import { StockSelection } from './pages/StockSelection';
import { Backtest } from './pages/Backtest';

const { Header, Content } = Layout;

const Logo: React.FC = () => {
  return (
    <div className="w-8 h-8 rounded-xl bg-gradient-to-br from-blue-600 via-indigo-600 to-purple-600 shadow-sm flex items-center justify-center">
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" aria-hidden="true">
        <path
          d="M4 16.5L9 11.5L12.5 15L20 7.5"
          stroke="white"
          strokeWidth="2.25"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
        <path
          d="M4 20H20"
          stroke="white"
          strokeOpacity="0.65"
          strokeWidth="2"
          strokeLinecap="round"
        />
      </svg>
    </div>
  );
};

const App: React.FC = () => {
  const [currentKey, setCurrentKey] = useState('stocks');
  const {
    token: { colorBgContainer, borderRadiusLG, colorBorderSecondary },
  } = theme.useToken();

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Header
        style={{
          padding: 0,
          background: colorBgContainer,
          borderBottom: `1px solid ${colorBorderSecondary}`,
        }}
      >
        <div className="h-full flex items-center px-6 gap-8">
          <div className="flex items-center gap-3">
            <Logo />
            <div className="text-[18px] font-semibold tracking-wide">Strategy-Tail</div>
          </div>
          <Tabs
            activeKey={currentKey}
            onChange={setCurrentKey}
            size="large"
            className="translate-y-[13px]"
            items={[
              {
                key: 'stocks',
                label: (
                  <span className="inline-flex items-center gap-2">
                    <StockOutlined />
                    选股
                  </span>
                ),
              },
              {
                key: 'backtest',
                label: (
                  <span className="inline-flex items-center gap-2">
                    <LineChartOutlined />
                    回测
                  </span>
                ),
              },
            ]}
          />
        </div>
      </Header>

      <Content style={{ margin: '16px 16px' }}>
        <div
          style={{
            padding: 24,
            minHeight: 360,
            background: colorBgContainer,
            borderRadius: borderRadiusLG,
          }}
        >
          {currentKey === 'stocks' && <StockSelection />}
          {currentKey === 'backtest' && <Backtest />}
        </div>
      </Content>
    </Layout>
  );
};

export default App;
