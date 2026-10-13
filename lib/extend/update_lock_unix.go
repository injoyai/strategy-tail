//go:build linux || darwin

package extend

import (
	"errors"
	"os"
	"syscall"
)

func tryFileLock(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return ErrUpdateLocked
	}
	return err
}
