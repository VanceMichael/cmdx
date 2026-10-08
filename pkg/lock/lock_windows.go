//go:build windows

package lock

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockRegionBytes is the byte length of the locked region. Only the first
// region of the file needs to be locked.
const lockRegionBytes = 1 << 16

// errWouldBlock is returned by tryLock when another process holds the lock.
var errWouldBlock error = windows.ERROR_LOCK_VIOLATION

// tryLock acquires an exclusive, non-blocking lock on the file.
func tryLock(f *os.File) error {
	var ol windows.Overlapped
	return windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		lockRegionBytes,
		lockRegionBytes,
		&ol,
	)
}

// release releases the lock.
func release(f *os.File) error {
	var ol windows.Overlapped
	if err := windows.UnlockFileEx(windows.Handle(f.Fd()), 0, lockRegionBytes, lockRegionBytes, &ol); err != nil {
		// ERROR_NOT_LOCKED means the process no longer holds the lock, which is
		// the desired state.
		if errors.Is(err, windows.ERROR_NOT_LOCKED) {
			return nil
		}
		return err
	}
	return nil
}
