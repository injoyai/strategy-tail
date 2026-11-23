// Command notify 是运行在 Windows 上的卖点提醒客户端。
//
// 它连接 Lab 的 /api/screen/events WebSocket，收到卖点事件后弹出 Windows
// 桌面通知，并把事件位置保存到本地文件，重连或重启后从上次位置继续。
// Windows 下默认以托盘程序运行。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fasthttp/websocket"
)

var errServerChanged = errors.New("服务器地址已变更")

const (
	eventsPath      = "/api/screen/events"
	shanghaiOffset  = 8 * 3600
	firstRunLogHint = "首次运行只提醒启动后的新卖点，-replay 可补发已有提醒"
)

type saleEvent struct {
	Type         string `json:"type"`
	ID           int64  `json:"id"`
	StrategyName string `json:"strategyName"`
	Signal       struct {
		Code      string  `json:"code"`
		Name      string  `json:"name"`
		SellTime  string  `json:"sellTime"`
		SellPrice float64 `json:"sellPrice"`
	} `json:"signal"`
}

type config struct {
	server         *serverStore
	statePath      string
	retry          time.Duration
	notify         bool
	suppressBefore string
	onStatus       func(string)
}

// serverStore 允许托盘修改服务器地址时安全地打断当前连接。
type serverStore struct {
	mu      sync.Mutex
	value   string
	changed chan struct{}
}

func newServerStore(value string) *serverStore {
	return &serverStore{value: value, changed: make(chan struct{})}
}

func (s *serverStore) Get() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value
}

func (s *serverStore) Set(value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.value == value {
		return
	}
	s.value = value
	close(s.changed)
	s.changed = make(chan struct{})
}

func (s *serverStore) Changed() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.changed
}

func (c config) status(text string) {
	if c.onStatus != nil {
		c.onStatus(text)
	}
}

func main() {
	server := flag.String("server", "", "家里 Lab 地址；未指定时读取已保存配置，首次运行会弹出设置窗口")
	state := flag.String("state", defaultStatePath(), "提醒进度文件，默认保存在当前用户的配置目录")
	after := flag.Int64("after", -1, "覆盖已保存的事件位置；-1 表示读取进度文件")
	retry := flag.Duration("retry", 3*time.Second, "断线重连间隔")
	notify := flag.Bool("notify", true, "显示 Windows 桌面通知")
	replay := flag.Bool("replay", false, "首次运行时补发已有卖点提醒")
	tray := flag.Bool("tray", true, "Windows 下显示托盘图标；false 时只在后台运行")
	flag.Parse()
	setupFileLog(*state)

	serverAddr := strings.TrimSpace(*server)
	if serverAddr == "" {
		serverAddr = loadServer(*state)
	}
	if _, err := eventsURL(serverAddr, 0); err != nil {
		if runtime.GOOS != "windows" {
			flag.Usage()
			startupFailed("缺少有效的 -server 参数")
		}
		entered, dialogErr := promptServer(serverAddr)
		if dialogErr != nil || strings.TrimSpace(entered) == "" {
			startupFailed("未配置服务器地址")
		}
		serverAddr = strings.TrimSpace(entered)
		if _, err := eventsURL(serverAddr, 0); err != nil {
			startupFailed("服务器地址无效: " + serverAddr)
		}
		if err := saveServer(*state, serverAddr); err != nil {
			log.Printf("保存配置失败: %v", err)
		}
	}

	cursor := readCursor(*state)
	suppressBefore := ""
	if *after >= 0 {
		cursor = *after
	} else if !*replay {
		if _, err := os.Stat(*state); errors.Is(err, os.ErrNotExist) {
			suppressBefore = time.Now().In(shanghaiZone()).Add(-time.Minute).Format(time.DateTime)
			log.Print(firstRunLogHint)
		}
	}

	cfg := config{server: newServerStore(serverAddr), statePath: *state, retry: *retry, notify: *notify, suppressBefore: suppressBefore}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	log.Printf("监听 %s，当前事件位置 %d", cfg.server.Get(), cursor)
	if *tray && runtime.GOOS == "windows" {
		runDesktop(ctx, stop, cfg, cursor)
		return
	}
	run(ctx, cfg, cursor)
}

// startupFailed 在 GUI 模式没有控制台时，用桌面通知告知启动失败原因。
func startupFailed(msg string) {
	log.Print(msg)
	fmt.Fprintln(os.Stderr, msg)
	if runtime.GOOS == "windows" {
		notifyDesktop("策略卖点提醒 · 启动失败", msg)
	}
	os.Exit(1)
}

// setupFileLog 让 GUI 模式（无控制台）也能把运行日志写入用户配置目录。
func setupFileLog(statePath string) {
	if runtime.GOOS != "windows" {
		return
	}
	path := logFilePath(statePath)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	log.SetOutput(file)
}

func logFilePath(statePath string) string {
	return filepath.Join(filepath.Dir(statePath), "notify.log")
}

func run(ctx context.Context, cfg config, cursor int64) {
	for {
		err := listen(ctx, cfg, &cursor)
		if errors.Is(err, errServerChanged) {
			cfg.status("正在切换服务器")
			continue
		}
		if err != nil && ctx.Err() == nil {
			cfg.status("重连中")
			log.Printf("连接中断: %v；%s 后重试", err, cfg.retry)
		}
		select {
		case <-ctx.Done():
			cfg.status("已退出")
			log.Print("已退出")
			return
		case <-time.After(cfg.retry):
		}
	}
}

func listen(ctx context.Context, cfg config, cursor *int64) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	target, err := eventsURL(cfg.server.Get(), *cursor)
	if err != nil {
		return err
	}
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, resp, err := dialer.Dial(target, nil)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("连接失败（HTTP %s）: %w", resp.Status, err)
		}
		return fmt.Errorf("连接失败: %w", err)
	}
	defer conn.Close()
	cfg.status("已连接")
	log.Printf("已连接 %s", target)

	changed := cfg.server.Changed()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-changed:
			conn.Close()
		case <-done:
		}
	}()

	for {
		var event saleEvent
		if err := conn.ReadJSON(&event); err != nil {
			select {
			case <-changed:
				return errServerChanged
			default:
				return err
			}
		}
		if event.Type != "sell" || event.ID <= *cursor {
			continue
		}
		*cursor = event.ID
		if err := writeCursor(cfg.statePath, event.ID); err != nil {
			log.Printf("保存进度失败: %v", err)
		}
		if cfg.suppressBefore != "" && event.Signal.SellTime <= cfg.suppressBefore {
			log.Printf("跳过启动前的提醒 #%d %s %s", event.ID, event.Signal.Code, event.Signal.SellTime)
			continue
		}
		title, body := event.title(), event.body()
		log.Printf("%s | %s", title, body)
		if cfg.notify {
			notifyDesktop(title, body)
		}
	}
}

func eventsURL(server string, after int64) (string, error) {
	raw := strings.TrimSpace(server)
	if raw == "" {
		return "", errors.New("地址为空")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("无效地址 %q", server)
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("不支持的协议 %q", u.Scheme)
	}
	path := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(path, eventsPath) {
		path += eventsPath
	}
	u.Path = path
	query := u.Query()
	query.Set("after", strconv.FormatInt(after, 10))
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func (e saleEvent) title() string {
	label := strings.TrimSpace(e.Signal.Code)
	if name := strings.TrimSpace(e.Signal.Name); name != "" {
		label += " " + name
	}
	if label == "" {
		return "卖点提醒"
	}
	return "卖点提醒 · " + label
}

func (e saleEvent) body() string {
	parts := make([]string, 0, 3)
	if name := strings.TrimSpace(e.StrategyName); name != "" {
		parts = append(parts, name)
	}
	if at := strings.TrimSpace(e.Signal.SellTime); at != "" {
		parts = append(parts, at)
	}
	parts = append(parts, fmt.Sprintf("%.2f 元", e.Signal.SellPrice))
	return strings.Join(parts, " · ")
}

func defaultStatePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "notify-cursor"
	}
	return filepath.Join(dir, "strategy-notify", "cursor")
}

func readCursor(path string) int64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	value, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func writeCursor(path string, id int64) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.FormatInt(id, 10)), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func shanghaiZone() *time.Location {
	return time.FixedZone("CST", shanghaiOffset)
}
