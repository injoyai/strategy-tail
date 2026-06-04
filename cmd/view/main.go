// view.go - 选股数据展示工具
//
// 连接 screen 服务的 WebSocket 接口，实时展示选股结果
// 用法：go run cmd/view/main.go [服务地址]
// 默认地址：ws://localhost:8080/screen

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fasthttp/websocket"
)

// 服务端推送的消息类型
type wsMessage struct {
	Type   string          `json:"type"`
	Result *ScreenResponse `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// ScreenResponse - 选股响应结构
type ScreenResponse struct {
	Count   int       `json:"count"`
	Time    string    `json:"time"`
	Results []BuyItem `json:"results"`
}

// BuyItem - 买入信号条目
type BuyItem struct {
	Code  string  `json:"code"`
	Time  string  `json:"time"`
	Price float64 `json:"price"`
	Rise  float64 `json:"rise"`
}

// getServerURL - 获取服务端 WebSocket 地址
func getServerURL() string {
	if len(os.Args) > 1 {
		return os.Args[1]
	}
	return "ws://localhost:8080/screen"
}

// printHeader - 打印界面头部
func printHeader(url string) {
	fmt.Println("\033[2J\033[H") // 清屏
	fmt.Println("┌──────────────────────────────────────────────────┐")
	fmt.Println("│              选股数据实时展示                      │")
	fmt.Println("├──────────────────────────────────────────────────┤")
	fmt.Printf("│  服务地址: %-38s │\n", url)
	fmt.Println("│  状态: 等待数据...                                │")
	fmt.Println("└──────────────────────────────────────────────────┘")
	fmt.Println()
}

// printResults - 打印选股结果表格
func printResults(resp *ScreenResponse) {
	fmt.Println("\033[2J\033[H") // 清屏

	// 状态指示
	now := time.Now().Format(time.DateTime)
	fmt.Println("┌──────────────────────────────────────────────────┐")
	fmt.Println("│              选股数据实时展示                      │")
	fmt.Println("├──────────────────────────────────────────────────┤")
	fmt.Printf("│  选股时间: %-22s 状态: \033[32m●已连接\033[0m  │\n", resp.Time)
	fmt.Printf("│  当前时间: %-38s │\n", now)
	fmt.Printf("│  选出数量: \033[33m%-4d\033[0m 只股票                     │\n", resp.Count)
	fmt.Println("└──────────────────────────────────────────────────┘")
	fmt.Println()

	if len(resp.Results) == 0 {
		fmt.Println("┌─────────────────────────────────────────────┐")
		fmt.Println("│              暂无符合条件的股票              │")
		fmt.Println("└─────────────────────────────────────────────┘")
		return
	}

	// 表格
	fmt.Println("┌────────────┬──────────┬────────┬──────────┐")
	fmt.Println("│    代码    │   价格   │  涨幅   │   时间   │")
	fmt.Println("├────────────┼──────────┼────────┼──────────┤")

	for _, item := range resp.Results {
		riseColor := "\033[32m" // 绿色-上涨
		if item.Rise < 0 {
			riseColor = "\033[31m" // 红色-下跌
		}
		timeStr := ""
		if len(item.Time) >= 19 {
			timeStr = item.Time[11:19]
		}
		fmt.Printf("│ %-10s │ %8.2f │%s%+7.2f%%\033[0m │ %8s │\n",
			item.Code, item.Price, riseColor, item.Rise, timeStr)
	}

	fmt.Println("└────────────┴──────────┴────────┴──────────┘")
	fmt.Println()
	fmt.Println("  按 Ctrl+C 退出")
}

// printError - 打印错误信息
func printError(errMsg string) {
	fmt.Printf("\033[31m[错误] %s\033[0m\n", errMsg)
}

// printDisconnected - 打印断开连接提示
func printDisconnected() {
	fmt.Println()
	fmt.Println("\033[31m[连接已断开] 正在尝试重连...\033[0m")
}

func main() {
	url := getServerURL()
	printHeader(url)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, syscall.SIGINT, syscall.SIGTERM)

	// 重连循环
	for {
		err := connectAndServe(url, interrupt)
		if err != nil {
			printDisconnected()
			// 等待5秒后重连，或用户中断
			select {
			case <-interrupt:
				fmt.Println("\n已退出")
				return
			case <-time.After(5 * time.Second):
				fmt.Printf("正在重连 %s ...\n", url)
				continue
			}
		}
		return
	}
}

// connectAndServe - 连接WebSocket并接收数据
func connectAndServe(url string, interrupt chan os.Signal) error {
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return fmt.Errorf("连接失败: %v", err)
	}
	defer conn.Close()

	fmt.Printf("\033[32m●\033[0m 已连接到 %s\n\n", url)

	// 消息接收循环
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}

			var data wsMessage
			if err := json.Unmarshal(msg, &data); err != nil {
				continue
			}

			switch data.Type {
			case "screen_result":
				if data.Result != nil {
					printResults(data.Result)
				}
			case "error":
				printError(data.Error)
			}
		}
	}()

	// 等待中断或连接断开
	select {
	case <-done:
		return fmt.Errorf("连接断开")
	case <-interrupt:
		// 优雅关闭
		err := conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		if err != nil {
			return nil
		}
		select {
		case <-done:
		case <-time.After(time.Second):
		}
		fmt.Println("\n已退出")
		os.Exit(0)
	}
	return nil
}
