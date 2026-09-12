//go:build !windows

package vaultsync

import (
	"errors"
	"os"
	"syscall"
)

// tryLock takes an exclusive advisory lock without waiting. flock locks belong
// to the open file description, so a second attempt from the same process
// conflicts too - which is what stops an in-process push racing a startup pull.
func tryLock(path string) (func(), bool, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, true, nil
}
