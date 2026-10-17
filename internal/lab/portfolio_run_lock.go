package lab

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var errPortfolioRunLocked = errors.New("实验仍由其他执行器运行")

func (s *PortfolioExperimentStore) lockRun(id string) (func() error, error) {
	if !validExperimentID(id) {
		return nil, fmt.Errorf("非法实验 ID: %q", id)
	}
	if err := os.MkdirAll(s.recordsRoot, 0755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(s.recordsRoot, ".run-"+id+".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := tryPortfolioRunLock(f); err != nil {
		f.Close()
		return nil, err
	}
	// 锁文件不删除，避免其他进程锁住不同 inode；进程退出由系统释放锁。
	return f.Close, nil
}
