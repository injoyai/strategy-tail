package extend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
