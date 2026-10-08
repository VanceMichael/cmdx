// Package lock provides an inter-process exclusive lock for cmdx tasks.
//
// The lock is backed by an OS-level file lock so that the lock is released
// automatically by the operating system even when the holder process is
// killed. The lock file also stores the owner metadata as JSON for diagnosis.
// The metadata is only overwritten after the OS lock is acquired, so a stale
// metadata file of a dead process never makes the lock permanently busy and
// the metadata of a new owner is never removed or overwritten by competitors.
package lock

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// pollInterval is the interval of non-blocking lock attempts while waiting.
	// Non-blocking polling lets Acquire return promptly when ctx is cancelled.
	pollInterval = 100 * time.Millisecond
	// lockFilePrefix is the prefix of lock files in the OS temporary directory.
	lockFilePrefix = "cmdx-"
	lockFileSuffix = ".lock"
	// lockFileMode is restrictive because the owner metadata may contain
	// rendered arguments.
	lockFileMode fs.FileMode = 0o600
	// tokenLength is the byte length of the random owner token (hex encoded).
	tokenLength = 8
)

// ErrUnsupported means the OS-level lock isn't supported on the platform.
var ErrUnsupported = errors.New("the concurrency lock isn't supported on this platform")

// Owner is the diagnosable metadata of a lock holder.
type Owner struct {
	PID        int       `json:"pid"`
	Hostname   string    `json:"hostname,omitempty"`
	Task       string    `json:"task,omitempty"`
	ConfigPath string    `json:"config_path,omitempty"`
	Group      string    `json:"group,omitempty"`
	AcquiredAt time.Time `json:"acquired_at"`
	// Token identifies a specific acquisition and tells the stale metadata
	// from a new owner's metadata.
	Token string `json:"token,omitempty"`
}

// NewOwner creates Owner of the current process.
func NewOwner(task, configPath, group string) Owner {
	owner := Owner{
		PID:        os.Getpid(),
		Task:       task,
		ConfigPath: configPath,
		Group:      group,
		AcquiredAt: time.Now().UTC(),
		Token:      randomToken(),
	}
	if hostname, err := os.Hostname(); err == nil {
		owner.Hostname = hostname
	}
	return owner
}

func randomToken() string {
	b := make([]byte, tokenLength)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read should never fail, but fall back to the current
		// time instead of making locking unavailable.
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b)
}

// ConflictError is returned when the lock is held by another cmdx process.
type ConflictError struct {
	// Path is the lock file path.
	Path string
	// Group is the rendered concurrency group.
	Group string
	// WaitTimeout is the configured wait_timeout.
	WaitTimeout time.Duration
	// TimedOut is true when Acquire failed after waiting for WaitTimeout.
	// It is false when Acquire failed immediately (WaitTimeout == 0).
	TimedOut bool
	// CurrentOwner is the best-effort owner metadata read from the lock file.
	// It may be nil when the metadata can't be read.
	CurrentOwner *Owner
}

func (e *ConflictError) Error() string {
	var b strings.Builder
	if e.TimedOut {
		fmt.Fprintf(&b, "timed out waiting for the concurrency group %q lock for %s", e.Group, e.WaitTimeout)
	} else {
		fmt.Fprintf(&b, "the concurrency group %q is locked by another cmdx process", e.Group)
	}
	if e.CurrentOwner != nil {
		fmt.Fprintf(&b, "; owner: pid %d", e.CurrentOwner.PID)
		if e.CurrentOwner.Hostname != "" {
			fmt.Fprintf(&b, " on %s", e.CurrentOwner.Hostname)
		}
		if e.CurrentOwner.Task != "" {
			fmt.Fprintf(&b, ", task %q", e.CurrentOwner.Task)
		}
		if !e.CurrentOwner.AcquiredAt.IsZero() {
			fmt.Fprintf(&b, ", acquired at %s", e.CurrentOwner.AcquiredAt.Format(time.RFC3339))
		}
	}
	fmt.Fprintf(&b, "; lock file: %s", e.Path)
	return b.String()
}

// Options is parameters of Acquire.
type Options struct {
	// Path is the lock file path, derived by the package-level Path function.
	Path string
	// Owner is the metadata written to the lock file after the lock is acquired.
	Owner Owner
	// WaitTimeout is the maximum time to wait for the lock.
	// Zero means failing immediately when the lock is held.
	WaitTimeout time.Duration
	// LogWriter is the optional writer to output waiting diagnostics.
	LogWriter io.Writer
}

// Handle is an acquired lock. Call Release to release it.
type Handle struct {
	file *os.File
	path string
}

// Path returns the lock file path.
func (h *Handle) Path() string {
	return h.path
}

// Acquire acquires the exclusive lock.
//
// If waitTimeout is zero and the lock is held, it returns *ConflictError
// immediately. Otherwise it keeps retrying until the lock is acquired,
// waitTimeout elapses, or ctx is cancelled. When ctx is cancelled while
// waiting, it returns the context error without starting the protected work.
func Acquire(ctx context.Context, opts *Options) (*Handle, error) {
	if opts.Path == "" {
		return nil, errors.New("the lock path is empty")
	}
	f, err := openLockFile(opts.Path)
	if err != nil {
		return nil, err
	}

	var deadline time.Time
	if opts.WaitTimeout > 0 {
		deadline = time.Now().Add(opts.WaitTimeout)
	}

	contended := false
	for {
		err := tryLock(f)
		if err == nil {
			if contended && opts.LogWriter != nil {
				fmt.Fprintf(opts.LogWriter, "cmdx: acquired the lock of the concurrency group %q\n", opts.Owner.Group)
			}
			if err := writeOwner(f, opts.Owner); err != nil {
				_ = release(f)
				_ = f.Close()
				return nil, err
			}
			return &Handle{file: f, path: opts.Path}, nil
		}
		if !errors.Is(err, errWouldBlock) {
			_ = f.Close()
			return nil, fmt.Errorf("failed to acquire the lock file %s: %w", opts.Path, err)
		}

		if ctx.Err() != nil {
			_ = f.Close()
			return nil, ctx.Err()
		}

		if !contended {
			contended = true
			if opts.LogWriter != nil {
				writeWaitingLog(opts.LogWriter, opts.Path, opts.Owner.Group, opts.WaitTimeout)
			}
		}

		if opts.WaitTimeout <= 0 {
			_ = f.Close()
			return nil, newConflictError(opts, false)
		}
		now := time.Now()
		if !now.Before(deadline) {
			_ = f.Close()
			return nil, newConflictError(opts, true)
		}
		timer := time.NewTimer(min(pollInterval, deadline.Sub(now)))
		select {
		case <-ctx.Done():
			_ = timer.Stop()
			_ = f.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func openLockFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, lockFileMode)
	if err != nil {
		return nil, fmt.Errorf("failed to open the lock file %s: %w", path, err)
	}
	return f, nil
}

func writeWaitingLog(w io.Writer, path, group string, waitTimeout time.Duration) {
	owner := readOwner(path)
	if owner != nil {
		fmt.Fprintf(w,
			"cmdx: waiting for the lock of the concurrency group %q held by pid %d (wait timeout: %s)\n",
			group, owner.PID, waitTimeout)
		return
	}
	fmt.Fprintf(w,
		"cmdx: waiting for the lock of the concurrency group %q (wait timeout: %s)\n",
		group, waitTimeout)
}

func newConflictError(opts *Options, timedOut bool) error {
	return &ConflictError{
		Path:         opts.Path,
		Group:        opts.Owner.Group,
		WaitTimeout:  opts.WaitTimeout,
		TimedOut:     timedOut,
		CurrentOwner: readOwner(opts.Path),
	}
}

// writeOwner replaces the owner metadata in the lock file.
//
// The caller must hold the exclusive OS lock, so this never overwrites a new
// owner's metadata. The file itself isn't replaced (no rename) because the OS
// lock is associated with the file, not the path.
func writeOwner(f *os.File, owner Owner) error {
	b, err := json.MarshalIndent(owner, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal the lock owner metadata: %w", err)
	}
	if err := f.Truncate(0); err != nil {
		return fmt.Errorf("failed to update the lock file: %w", err)
	}
	if _, err := f.WriteAt(append(b, '\n'), 0); err != nil {
		return fmt.Errorf("failed to write the lock owner metadata: %w", err)
	}
	return nil
}

// readOwner reads the best-effort owner metadata from the lock file.
// It is used only for diagnostics and never affects locking decisions.
func readOwner(path string) *Owner {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	owner := &Owner{}
	if err := json.Unmarshal(b, owner); err != nil {
		return nil
	}
	if owner.PID == 0 {
		return nil
	}
	return owner
}

// Release releases the OS lock and closes the lock file.
// The lock file itself is intentionally kept; the operating system releases
// the lock automatically even if Release isn't called (e.g. SIGKILL).
func (h *Handle) Release() error {
	if h == nil || h.file == nil {
		return nil
	}
	err := release(h.file)
	if closeErr := h.file.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	h.file = nil
	return err
}

// Path derives the lock file path in the OS temporary directory from the
// canonical root configuration file path and the rendered group.
//
// The same configuration file and group always produce the same path, while
// different groups or different configuration files produce different paths.
// The values are hashed so arbitrary group strings are safe as file names.
func Path(configPath, group string) (string, error) {
	canonical, err := canonicalConfigPath(configPath)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(canonical + "\x00" + group))
	return filepath.Join(os.TempDir(), lockFilePrefix+hex.EncodeToString(sum[:])+lockFileSuffix), nil
}

func canonicalConfigPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("failed to resolve the configuration file path %s: %w", p, err)
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// EvalSymlinks fails when a component doesn't exist. The configuration
		// file should exist, but fall back to the absolute path.
		return abs, nil //nolint:nilerr
	}
	return canonical, nil
}
