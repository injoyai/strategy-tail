package extend

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/injoyai/ios"
	"github.com/injoyai/tdx"
	"github.com/injoyai/tdx/lib/xorms"
	"github.com/injoyai/tdx/protocol"
)

func emptyKlineManager(t *testing.T) *tdx.Manage {
	t.Helper()
	conn, server := net.Pipe()
	ready := make(chan *tdx.Client, 1)
	t.Cleanup(func() { conn.Close(); server.Close() })
	go func() {
		var c *tdx.Client
		for {
			header := make([]byte, 12)
			if _, err := io.ReadFull(server, header); err != nil {
				return
			}
			if _, err := io.CopyN(io.Discard, server, int64(binary.LittleEndian.Uint16(header[6:8]))-2); err != nil {
				return
			}
			if binary.LittleEndian.Uint16(header[10:12]) != protocol.TypeKline {
				continue
			}
			if c == nil {
				c = <-ready
			}
			key := strconv.FormatUint(uint64(binary.LittleEndian.Uint32(header[1:5])), 10)
			deadline := time.Now().Add(time.Second)
			for !c.Wait.IsWait(key) {
				if time.Now().After(deadline) {
					return
				}
				time.Sleep(time.Millisecond)
			}
			// A valid K-line response with a zero record count.
			response := []byte{0xb1, 0xcb, 0x74, 0, 0x1c, 0, 0, 0, 0, 0, 0, 0, 2, 0, 2, 0, 0, 0}
			copy(response[5:9], header[1:5])
			copy(response[10:12], header[10:12])
			if _, err := server.Write(response); err != nil {
				return
			}
		}
	}()
	c, err := tdx.DialWith(func(context.Context) (ios.ReadWriteCloser, string, error) {
		return conn, "empty-kline-test", nil
	}, tdx.WithLevel(tdx.LevelNone))
	if err != nil {
		t.Fatal(err)
	}
	ready <- c
	t.Cleanup(func() {
		c.Close()
		select {
		case <-c.Done():
		case <-time.After(time.Second):
			t.Error("TDX test client did not stop")
		}
	})
	pool, err := tdx.NewPool(func() (*tdx.Client, error) { return c, nil }, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close() })
	return &tdx.Manage{IPool: pool}
}

func TestEmptyKlineUpdatesCompleteAndPreserveData(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "without existing data"
		if existing {
			name = "with existing data"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			const code = "sz001381"
			at := time.Date(time.Now().Year(), 1, 2, 15, 0, 0, 0, time.Local)
			bar := &protocol.Kline{Time: at, Close: 12345}
			if existing {
				dayDB, err := xorms.NewSqlite(filepath.Join(dir, DirDay, code+".db"))
				if err != nil {
					t.Fatal(err)
				}
				defer dayDB.Close()
				if err := dayDB.Sync2(new(Kline)); err != nil {
					t.Fatal(err)
				}
				if _, err := dayDB.Insert(&Kline{Unix: at.Unix(), Kline: bar}); err != nil {
					t.Fatal(err)
				}
				minuteDB, err := xorms.NewSqlite(filepath.Join(dir, DirMinute, code, code+"-"+at.Format("2006")+".db"))
				if err != nil {
					t.Fatal(err)
				}
				defer minuteDB.Close()
				if err := minuteDB.Sync2(new(protocol.Kline)); err != nil {
					t.Fatal(err)
				}
				if _, err := minuteDB.Insert(bar); err != nil {
					t.Fatal(err)
				}
			}
			db, err := xorms.NewSqlite(filepath.Join(dir, "update.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			updated, err := tdx.NewUpdated(db, updateCloseHour, updateCloseMinute)
			if err != nil {
				t.Fatal(err)
			}
			pull := &PullKline{Config: PullKlineConfig{Dir: dir, Codes: []string{code}, Goroutines: 1}, Types: []string{Day, Minute}, Updated: updated}
			var progress UpdateProgress
			if err := pull.UpdateWithProgress(emptyKlineManager(t), true, func(p UpdateProgress) { progress = p }); err != nil {
				t.Fatal(err)
			}
			if progress.Done != 2 || progress.Total != 2 || progress.Failed != 0 || progress.Skipped {
				t.Fatalf("empty data reported as failure: %+v", progress)
			}
			var marker tdx.UpdateModel
			if found, err := db.Where("`Key`=?", "pull").Get(&marker); err != nil || !found || marker.Time == 0 {
				t.Fatalf("completed batch marker: %+v, found=%v err=%v", marker, found, err)
			}
			days, err := pull.DayKlinesAll(code)
			if err != nil {
				t.Fatal(err)
			}
			minutes, err := pull.MinKlines(code, at, at)
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if existing {
				want = 1
			}
			if len(days) != want || len(minutes) != want {
				t.Fatalf("stored data changed: day=%d minute=%d want=%d", len(days), len(minutes), want)
			}
			if existing && (days[0].Unix != at.Unix() || days[0].Close != bar.Close || !minutes[0].Time.Equal(at) || minutes[0].Close != bar.Close) {
				t.Fatal("existing bars changed after empty response")
			}
		})
	}
}

func TestKlineUpdatesReturnDataDirectoryError(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("occupied"), 0644); err != nil {
		t.Fatal(err)
	}
	pull := &PullKline{Config: PullKlineConfig{Dir: blocked, Goroutines: 1}}

	for name, update := range map[string]func() error{
		"day":    func() error { return pull.updateDayKline(nil, nil) },
		"minute": func() error { return pull.updateMinKline(nil, nil) },
	} {
		t.Run(name, func(t *testing.T) {
			err := update()
			if err == nil || !strings.Contains(err.Error(), "创建 K 线数据目录") {
				t.Fatalf("update error = %v", err)
			}
		})
	}
}

// 验证分钟线增量更新以 UTC 命中落库的边界数据,刷新最后一根K线且不产生重复行、不误删更早数据。
func TestUpdateMinuteKlineYearRefreshesBoundaryBar(t *testing.T) {
	dir := t.TempDir()
	const code = "sz001381"
	year := time.Now().Year()
	last := time.Date(year, 1, 2, 10, 0, 0, 0, time.Local)

	db, err := xorms.NewSqlite(filepath.Join(dir, DirMinute, code, code+"-"+strconv.Itoa(year)+".db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Sync2(new(protocol.Kline)); err != nil {
		t.Fatal(err)
	}
	// 预置 last 前一分钟与 last 两条已有数据。
	if _, err := db.Insert(&protocol.Kline{Time: last.Add(-time.Minute), Close: 99}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Insert(&protocol.Kline{Time: last, Close: 100}); err != nil {
		t.Fatal(err)
	}

	// 模拟服务端返回:同一时间的最新K线(刷新边界)与下一分钟新K线。
	ks := protocol.Klines{
		{Time: last, Close: 110},
		{Time: last.Add(time.Minute), Close: 111},
	}
	pull := &PullKline{Config: PullKlineConfig{Dir: dir}}
	if _, err := pull.updateMinuteKlineYear(nil, code, year, ks); err != nil {
		t.Fatal(err)
	}

	rows := protocol.Klines{}
	if err := db.Find(&rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("boundary bar duplicated: got %d rows, want 3", len(rows))
	}
	for _, v := range rows {
		if v.Time.Equal(last) && v.Close != 110 {
			t.Fatalf("boundary bar not refreshed: close=%v", v.Close)
		}
	}
}

// 验证K线数据不足时 DayKline 返回哨兵错误,而非静默返回 nil。
func TestDayKlineNotEnoughError(t *testing.T) {
	pull := &PullKline{Config: PullKlineConfig{Dir: t.TempDir()}}
	if _, err := pull.DayKline("sz001381", 0); err != ErrNotEnoughKline {
		t.Fatalf("err = %v, want ErrNotEnoughKline", err)
	}
}
