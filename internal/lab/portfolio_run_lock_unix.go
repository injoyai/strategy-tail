//go:build linux || darwin

package lab

import (
	"errors"
	"os"
	"syscall"
)

func tryPortfolioRunLock(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return errPortfolioRunLocked
	}
	return err
}
