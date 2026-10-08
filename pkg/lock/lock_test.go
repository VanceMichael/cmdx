package lock

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "test.lock")
}

func acquireForTest(t *testing.T, path string, waitTimeout time.Duration, token string) *Handle {
	t.Helper()
	owner := NewOwner("test-task", "/tmp/cmdx.yaml", "test-group")
	owner.Token = token
	h, err := Acquire(t.Context(), &Options{
		Path:        path,
		Owner:       owner,
		WaitTimeout: waitTimeout,
		LogWriter:   &bytes.Buffer{},
	})
	require.NoError(t, err)
	return h
}

func readOwnerForTest(t *testing.T, path string) Owner {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	owner := Owner{}
	require.NoError(t, json.Unmarshal(b, &owner))
	return owner
}

func TestPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cmdx.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte{}, 0o600))

	p1, err := Path(cfgPath, "deploy-staging")
	require.NoError(t, err)
	assert.Equal(t, filepath.Clean(os.TempDir()), filepath.Clean(filepath.Dir(p1)))
	assert.True(t, strings.HasPrefix(filepath.Base(p1), lockFilePrefix))
	assert.True(t, strings.HasSuffix(filepath.Base(p1), lockFileSuffix))

	// The same configuration file and group always produce the same path.
	p1Again, err := Path(cfgPath, "deploy-staging")
	require.NoError(t, err)
	assert.Equal(t, p1, p1Again)

	// Different groups produce different paths.
	p2, err := Path(cfgPath, "deploy-production")
	require.NoError(t, err)
	assert.NotEqual(t, p1, p2)

	// Different configuration files produce different paths.
	cfgPath2 := filepath.Join(dir, "nested", "cmdx.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(cfgPath2), 0o700))
	require.NoError(t, os.WriteFile(cfgPath2, []byte{}, 0o600))
	p3, err := Path(cfgPath2, "deploy-staging")
	require.NoError(t, err)
	assert.NotEqual(t, p1, p3)

	// A relative path is resolved against the current directory and matches
	// the canonical absolute path.
	wd, err := os.Getwd()
	require.NoError(t, err)
	rel, err := filepath.Rel(wd, cfgPath)
	require.NoError(t, err)
	p4, err := Path(filepath.Join(rel), "deploy-staging")
	require.NoError(t, err)
	assert.Equal(t, p1, p4)

	// A symlink to the configuration file produces the same path.
	symlink := filepath.Join(t.TempDir(), "cmdx-link.yaml")
	require.NoError(t, os.Symlink(cfgPath, symlink))
	p5, err := Path(symlink, "deploy-staging")
	require.NoError(t, err)
	assert.Equal(t, p1, p5)
}

func TestAcquire_ImmediateBusyAndMetadata(t *testing.T) {
	t.Parallel()
	path := newTestPath(t)
	handle := acquireForTest(t, path, 0, "token-a")

	// The owner metadata is written after the lock is acquired.
	owner := readOwnerForTest(t, path)
	assert.Equal(t, os.Getpid(), owner.PID)
	assert.Equal(t, "test-task", owner.Task)
	assert.Equal(t, "test-group", owner.Group)
	assert.Equal(t, "token-a", owner.Token)
	assert.False(t, owner.AcquiredAt.IsZero())

	// wait_timeout 0 means failing immediately.
	var conflict *ConflictError
	_, err := Acquire(t.Context(), &Options{
		Path:        path,
		Owner:       NewOwner("test-task", "/tmp/cmdx.yaml", "test-group"),
		WaitTimeout: 0,
	})
	require.ErrorAs(t, err, &conflict)
	assert.False(t, conflict.TimedOut)
	assert.Equal(t, path, conflict.Path)
	require.NotNil(t, conflict.CurrentOwner)
	assert.Equal(t, os.Getpid(), conflict.CurrentOwner.PID)
	assert.Equal(t, "token-a", conflict.CurrentOwner.Token)
	assert.Contains(t, err.Error(), "is locked by another cmdx process")

	// The failed acquisition must not overwrite the owner metadata.
	assert.Equal(t, "token-a", readOwnerForTest(t, path).Token)

	require.NoError(t, handle.Release())

	// After release the lock can be acquired immediately.
	handle2 := acquireForTest(t, path, 0, "token-b")
	assert.Equal(t, "token-b", readOwnerForTest(t, path).Token)
	require.NoError(t, handle2.Release())
}

func TestAcquire_WaitTimeout(t *testing.T) {
	t.Parallel()
	path := newTestPath(t)
	handle := acquireForTest(t, path, 0, "token-a")

	start := time.Now()
	_, err := Acquire(t.Context(), &Options{
		Path:        path,
		Owner:       NewOwner("test-task", "/tmp/cmdx.yaml", "test-group"),
		WaitTimeout: 150 * time.Millisecond,
	})
	elapsed := time.Since(start)

	var conflict *ConflictError
	require.ErrorAs(t, err, &conflict)
	assert.True(t, conflict.TimedOut)
	assert.GreaterOrEqual(t, elapsed, 140*time.Millisecond)
	assert.Less(t, elapsed, 2*time.Second)
	assert.Equal(t, "token-a", readOwnerForTest(t, path).Token)

	require.NoError(t, handle.Release())
}

func TestAcquire_WaitsAndAcquires(t *testing.T) {
	t.Parallel()
	path := newTestPath(t)
	handle := acquireForTest(t, path, 0, "token-a")

	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = handle.Release()
	}()

	start := time.Now()
	handle2 := acquireForTest(t, path, 5*time.Second, "token-b")
	elapsed := time.Since(start)

	assert.GreaterOrEqual(t, elapsed, 190*time.Millisecond)
	assert.Less(t, elapsed, 2*time.Second)
	assert.Equal(t, "token-b", readOwnerForTest(t, path).Token)
	require.NoError(t, handle2.Release())
}

func TestAcquire_ContextCancelledWhileWaiting(t *testing.T) {
	t.Parallel()
	path := newTestPath(t)
	handle := acquireForTest(t, path, 0, "token-a")

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := Acquire(ctx, &Options{
		Path:        path,
		Owner:       NewOwner("test-task", "/tmp/cmdx.yaml", "test-group"),
		WaitTimeout: 10 * time.Second,
	})
	elapsed := time.Since(start)

	require.ErrorIs(t, err, context.Canceled)
	assert.Less(t, elapsed, 2*time.Second)
	// The cancelled waiter must not have written its metadata and the holder
	// must still own the lock.
	assert.Equal(t, "token-a", readOwnerForTest(t, path).Token)
	_, err = Acquire(t.Context(), &Options{
		Path:        path,
		Owner:       NewOwner("test-task", "/tmp/cmdx.yaml", "test-group"),
		WaitTimeout: 0,
	})
	var conflict *ConflictError
	require.ErrorAs(t, err, &conflict)

	require.NoError(t, handle.Release())
}

func TestAcquire_ReplacesStaleMetadata(t *testing.T) {
	t.Parallel()
	path := newTestPath(t)
	// A previous process released the lock but left its metadata, which is
	// also what the file looks like after a force kill (minus the kernel lock).
	handle := acquireForTest(t, path, 0, "stale-token")
	require.NoError(t, handle.Release())
	assert.Equal(t, "stale-token", readOwnerForTest(t, path).Token)

	handle2 := acquireForTest(t, path, 0, "new-token")
	owner := readOwnerForTest(t, path)
	assert.Equal(t, "new-token", owner.Token)
	assert.NotEqual(t, "stale-token", owner.Token)
	require.NoError(t, handle2.Release())
}

func TestAcquire_TimedOutWaiterDoesNotOverwriteNewOwner(t *testing.T) {
	t.Parallel()
	path := newTestPath(t)
	first := acquireForTest(t, path, 0, "token-a")

	var wg sync.WaitGroup
	wg.Add(1)
	var waiterErr error
	var waiterHandle *Handle
	go func() {
		defer wg.Done()
		waiterHandle, waiterErr = Acquire(t.Context(), &Options{
			Path:        path,
			Owner:       NewOwner("test-task", "/tmp/cmdx.yaml", "test-group"),
			WaitTimeout: 2 * time.Second,
		})
	}()

	// The first holder releases while the waiter is waiting; the waiter takes
	// over the lock.
	time.Sleep(200 * time.Millisecond)
	require.NoError(t, first.Release())
	wg.Wait()
	require.NoError(t, waiterErr)
	defer func() { _ = waiterHandle.Release() }()
	assert.Equal(t, os.Getpid(), readOwnerForTest(t, path).PID)

	// Another competitor that times out must not overwrite the new owner.
	_, err := Acquire(t.Context(), &Options{
		Path:        path,
		Owner:       NewOwner("other", "/tmp/cmdx.yaml", "test-group"),
		WaitTimeout: 100 * time.Millisecond,
	})
	var conflict *ConflictError
	require.ErrorAs(t, err, &conflict)
	assert.Equal(t, "test-task", readOwnerForTest(t, path).Task)
}

func TestAcquire_WaitingLog(t *testing.T) {
	t.Parallel()
	path := newTestPath(t)
	handle := acquireForTest(t, path, 0, "token-a")
	defer func() { _ = handle.Release() }()

	logs := &bytes.Buffer{}
	_, err := Acquire(t.Context(), &Options{
		Path:        path,
		Owner:       NewOwner("waiting-task", "/tmp/cmdx.yaml", "waiting-group"),
		WaitTimeout: 50 * time.Millisecond,
		LogWriter:   logs,
	})
	require.Error(t, err)
	output := logs.String()
	assert.Contains(t, output, "waiting for the lock")
	assert.Contains(t, output, "waiting-group")
	assert.Contains(t, output, strconv.Itoa(os.Getpid()))
}

func TestAcquire_EmptyPath(t *testing.T) {
	t.Parallel()
	_, err := Acquire(t.Context(), &Options{
		Owner:       NewOwner("t", "c", "g"),
		WaitTimeout: 0,
	})
	require.Error(t, err)
}
