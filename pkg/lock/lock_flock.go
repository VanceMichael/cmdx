//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package lock

import (
	"os"

	"golang.org/x/sys/unix"
)

// errWouldBlock is returned by tryLock when another process holds the lock.
var errWouldBlock = unix.EWOULDBLOCK

// tryLock acquires an exclusive, non-blocking advisory lock on the file.
// The kernel releases the lock automatically when the process exits even if
// it is killed, so a dead holder never blocks the next execution.
func tryLock(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}

// release releases the advisory lock.
func release(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
