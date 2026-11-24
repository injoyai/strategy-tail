package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestEventsURL(t *testing.T) {
	cases := []struct {
		name   string
		server string
		after  int64
		want   string
	}{
		{"基础地址", "http://192.168.1.10", 7, "ws://192.168.1.10/api/screen/events?after=7"},
		{"带端口和斜杠", "http://192.168.1.10:8765/", 0, "ws://192.168.1.10:8765/api/screen/events?after=0"},
		{"省略协议", "192.168.1.10:8765", 3, "ws://192.168.1.10:8765/api/screen/events?after=3"},
		{"安全端口", "https://lab.example.com", 12, "wss://lab.example.com/api/screen/events?after=12"},
		{"已是ws地址", "ws://10.0.0.2:80", 1, "ws://10.0.0.2:80/api/screen/events?after=1"},
		{"完整路径不重复", "http://10.0.0.2/api/screen/events", 9, "ws://10.0.0.2/api/screen/events?after=9"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := eventsURL(c.server, c.after)
			if err != nil {
				t.Fatalf("eventsURL(%q) 返回错误: %v", c.server, err)
			}
			if got != c.want {
				t.Fatalf("eventsURL(%q) = %q，期望 %q", c.server, got, c.want)
			}
		})
	}
}

func TestEventsURLRejectsInvalid(t *testing.T) {
	for _, server := range []string{"", "   ", "ftp://192.168.1.10", "http://"} {
		if _, err := eventsURL(server, 0); err == nil {
			t.Fatalf("eventsURL(%q) 应返回错误", server)
		}
	}
}

func TestSaleEventText(t *testing.T) {
	var event saleEvent
	event.Type = "sell"
	event.ID = 42
	event.StrategyName = "MACD精选"
	event.Signal.Code = "sh600000"
	event.Signal.Name = "浦发银行"
	event.Signal.SellTime = "2026-11-23 10:01:00"
	event.Signal.SellPrice = 11.5

	if got, want := event.title(), "卖点提醒 · sh600000 浦发银行"; got != want {
		t.Fatalf("title = %q，期望 %q", got, want)
	}
	if got, want := event.body(), "MACD精选 · 2026-11-23 10:01:00 · 11.50 元"; got != want {
		t.Fatalf("body = %q，期望 %q", got, want)
	}
}

func TestSaleEventTextWithoutName(t *testing.T) {
	var event saleEvent
	event.Signal.Code = "sz000001"
	event.Signal.SellPrice = 9
	if got, want := event.title(), "卖点提醒 · sz000001"; got != want {
		t.Fatalf("title = %q，期望 %q", got, want)
	}
	if got, want := event.body(), "9.00 元"; got != want {
		t.Fatalf("body = %q，期望 %q", got, want)
	}
}

func TestCursorRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "cursor")
	if got, _ := readServerCursor(path, "http://a"); got != 0 {
		t.Fatalf("文件不存在时 readCursor = %d，期望 0", got)
	}
	if err := writeServerCursor(path, "http://a", 123); err != nil {
		t.Fatalf("writeCursor 失败: %v", err)
	}
	if got, _ := readServerCursor(path, "http://a"); got != 123 {
		t.Fatalf("readCursor = %d，期望 123", got)
	}
	if err := writeServerCursor(path, "http://a", 456); err != nil {
		t.Fatalf("覆盖写入失败: %v", err)
	}
	if got, _ := readServerCursor(path, "http://a"); got != 456 {
		t.Fatalf("覆盖后 readCursor = %d，期望 456", got)
	}
}

func TestTrayIcon(t *testing.T) {
	icon := trayIcon()
	if len(icon) < 22 {
		t.Fatalf("图标长度 %d 过短", len(icon))
	}
	if icon[0] != 0 || icon[1] != 0 || icon[2] != 1 || icon[3] != 0 || icon[4] != 1 || icon[5] != 0 {
		t.Fatalf("ICONDIR 头不正确: %v", icon[:6])
	}
	if icon[6] != 32 || icon[7] != 32 {
		t.Fatalf("图标尺寸应为 32x32，实际 %dx%d", icon[6], icon[7])
	}
	resourceLen := binary.LittleEndian.Uint32(icon[14:18])
	offset := binary.LittleEndian.Uint32(icon[18:22])
	if int(offset)+int(resourceLen) != len(icon) {
		t.Fatalf("资源范围 %d+%d 与文件长度 %d 不一致", offset, resourceLen, len(icon))
	}
	if want := 40 + 32*32*4 + 32*4; int(resourceLen) != want {
		t.Fatalf("资源长度 %d，期望 %d", resourceLen, want)
	}
}

func TestReadCursorInvalidContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cursor")
	if err := os.WriteFile(path, []byte("not-a-number"), 0644); err != nil {
		t.Fatal(err)
	}
	if got, _ := readServerCursor(path, "http://a"); got != 0 {
		t.Fatalf("非法内容时 readCursor = %d，期望 0", got)
	}
}

func TestServerStoreChangeSignal(t *testing.T) {
	store := newServerStore("http://a")
	old := store.Changed()
	store.Set("http://a")
	select {
	case <-old:
		t.Fatal("相同地址不应触发变更")
	default:
	}

	store.Set("http://b")
	select {
	case <-old:
	default:
		t.Fatal("地址变更后旧信号应关闭")
	}
	if got := store.Get(); got != "http://b" {
		t.Fatalf("Get = %q，期望 http://b", got)
	}
	select {
	case <-store.Changed():
		t.Fatal("新信号不应立即关闭")
	default:
	}
}

func TestSaveLoadServer(t *testing.T) {
	state := filepath.Join(t.TempDir(), "nested", "cursor")
	if got := loadServer(state); got != "" {
		t.Fatalf("未保存时 loadServer = %q，期望空", got)
	}
	if err := saveServer(state, "http://192.168.1.10"); err != nil {
		t.Fatalf("saveServer 失败: %v", err)
	}
	if got := loadServer(state); got != "http://192.168.1.10" {
		t.Fatalf("loadServer = %q", got)
	}
	if err := os.WriteFile(configFilePath(state), []byte("{"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := loadServer(state); got != "" {
		t.Fatalf("配置损坏时 loadServer = %q，期望空", got)
	}
}
