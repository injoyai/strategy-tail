//go:build !linux && !darwin && !windows

package extend

import (
	"fmt"
	"os"
)

func tryFileLock(*os.File) error {
	return fmt.Errorf("当前系统不支持安全数据更新锁")
}
