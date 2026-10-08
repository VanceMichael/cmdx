//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd || windows)

package lock

import (
	"errors"
	"os"
)

// errWouldBlock is never returned by tryLock on unsupported platforms. It
// exists so that the portable code compiles; Acquire treats the
// ErrUnsupported error as a fatal error instead of waiting.
var errWouldBlock = errors.New("the lock would block") //nolint:gochecknoglobals

func tryLock(_ *os.File) error {
	return ErrUnsupported
}

func release(_ *os.File) error {
	return ErrUnsupported
}
