# Strategy-Tail

这是一个基于 Golang 和 React 的股票尾盘策略系统（Strategy-Tail），包含实时选股和策略回测功能。

## 功能特性

- **实时选股**: 
  - 实时显示股票行情和 K 线图。
  - 支持按市值筛选。
  - 网格布局 (3列)，每个卡片包含 K 线图和均线。
  - 模拟实时价格波动。
- **策略回测**:
  - 设置回测区间。
  - 展示回测结果：总收益率、胜率、资金曲线、交易明细。

## 技术栈

- **后端**: Golang, Gin, Gorilla WebSocket
- **前端**: React, Vite, TypeScript, Ant Design, Tailwind CSS, Lightweight Charts

## 运行说明

### 1. 启动后端

在项目根目录下运行：

```bash
go mod tidy
go run main.go
```

后端服务将在 `http://localhost:8080` 启动。

### 2. 启动前端

进入 `web` 目录并运行：

```bash
cd web
npm install
npm run dev
```

前端服务将在 `http://localhost:5173` 启动。

打开浏览器访问 `http://localhost:5173` 即可使用。
