package extend

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var ErrUpdateLocked = errors.New("已有数据更新正在运行，请等待完成")

// lockUpdates keeps the lock file: unlinking it can let another process lock a
// different inode. The operating system releases the lock when a process exits.
func lockUpdates(dir string) (func() error, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("创建 K 线数据目录: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, ".kline-update.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := tryFileLock(f); err != nil {
		f.Close()
		return nil, err
	}
	return f.Close, nil
}
