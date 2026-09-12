//go:build windows

package vaultsync

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// tryLock takes an exclusive lock without waiting. LockFileEx locks belong to
// the handle, so a second attempt from the same process conflicts too.
func tryLock(path string) (func(), bool, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	ol := new(windows.Overlapped)
	h := windows.Handle(f.Fd())
	err = windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
	if err != nil {
		f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return func() {
		_ = windows.UnlockFileEx(h, 0, 1, 0, ol)
		f.Close()
	}, true, nil
}
