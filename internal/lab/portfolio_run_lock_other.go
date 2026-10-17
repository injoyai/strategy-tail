//go:build !linux && !darwin && !windows

package lab

import (
	"fmt"
	"os"
)

func tryPortfolioRunLock(*os.File) error {
	return fmt.Errorf("当前系统不支持安全实验运行锁")
}
