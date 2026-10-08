package execute

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suzuki-shunsuke/cmdx/pkg/lock"
)

const concurrencyTestGroup = "execute-test-group"

func concurrencyParams(t *testing.T, group, script string, waitTimeout time.Duration) *Params {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "cmdx.yaml")
	return &Params{
		Script: script,
		Timeout: &Timeout{
			Duration:  10 * time.Second,
			KillAfter: time.Second,
		},
		Concurrency: &Concurrency{
			ConfigPath:  configPath,
			Group:       group,
			TaskName:    "test-task",
			WaitTimeout: waitTimeout,
		},
	}
}

func concurrencyLockPath(t *testing.T, params *Params) string {
	t.Helper()
	path, err := lock.Path(params.Concurrency.ConfigPath, params.Concurrency.Group)
	require.NoError(t, err)
	return path
}

func TestExecutor_Run_DryRunDoesNotUseLock(t *testing.T) {
	params := concurrencyParams(t, concurrencyTestGroup, "echo hello", 0)
	params.DryRun = true

	require.NoError(t, New().Run(t.Context(), params))

	// dry-run must not create the lock file.
	_, err := os.Stat(concurrencyLockPath(t, params))
	assert.True(t, os.IsNotExist(err))
}

func TestExecutor_Run_SameGroupSerialized(t *testing.T) {
	params1 := concurrencyParams(t, concurrencyTestGroup, "sleep 0.5", 10*time.Second)
	params2 := concurrencyParams(t, concurrencyTestGroup, "sleep 0.5", 10*time.Second)
	// Use the same configuration file path so both runs share the namespace.
	params2.Concurrency.ConfigPath = params1.Concurrency.ConfigPath

	exc := New()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	start := time.Now()
	for _, params := range []*Params{params1, params2} {
		wg.Go(func() {
			errs <- exc.Run(t.Context(), params)
		})
	}
	wg.Wait()
	elapsed := time.Since(start)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	// Two 0.5 second scripts of the same group must not overlap, and the lock
	// polling adds at most the poll interval.
	assert.GreaterOrEqual(t, elapsed, 900*time.Millisecond)
}

func TestExecutor_Run_DifferentGroupsParallel(t *testing.T) {
	params1 := concurrencyParams(t, concurrencyTestGroup+"-a", "sleep 0.5", 10*time.Second)
	params2 := concurrencyParams(t, concurrencyTestGroup+"-b", "sleep 0.5", 10*time.Second)
	params2.Concurrency.ConfigPath = params1.Concurrency.ConfigPath

	exc := New()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	start := time.Now()
	for _, params := range []*Params{params1, params2} {
		wg.Go(func() {
			errs <- exc.Run(t.Context(), params)
		})
	}
	wg.Wait()
	elapsed := time.Since(start)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	// Different groups run in parallel.
	assert.Less(t, elapsed, 900*time.Millisecond)
}

func TestExecutor_Run_BusyFailsWithoutRunningScript(t *testing.T) {
	params := concurrencyParams(t, concurrencyTestGroup, "printf started > \"$MARKER\"", 0)
	marker := filepath.Join(t.TempDir(), "marker")
	t.Setenv("MARKER", marker)

	holder, err := lock.Acquire(t.Context(), &lock.Options{
		Path:        concurrencyLockPath(t, params),
		Owner:       lock.NewOwner("holder", params.Concurrency.ConfigPath, params.Concurrency.Group),
		WaitTimeout: 0,
	})
	require.NoError(t, err)
	defer func() { _ = holder.Release() }()

	err = New().Run(t.Context(), params)
	var conflict *lock.ConflictError
	require.ErrorAs(t, err, &conflict)
	assert.Contains(t, err.Error(), "is locked by another cmdx process")

	// The script must not have started.
	_, err = os.Stat(marker)
	assert.True(t, os.IsNotExist(err))
}

func TestExecutor_Run_LockReleasedOnNonZeroExit(t *testing.T) {
	params := concurrencyParams(t, concurrencyTestGroup, "false", 0)
	err := New().Run(t.Context(), params)
	require.Error(t, err)

	// The lock is released despite the non-zero exit.
	handle, err := lock.Acquire(t.Context(), &lock.Options{
		Path:        concurrencyLockPath(t, params),
		Owner:       lock.NewOwner("after", "", ""),
		WaitTimeout: 0,
	})
	require.NoError(t, err)
	require.NoError(t, handle.Release())
}

func TestExecutor_Run_LockReleasedOnTimeout(t *testing.T) {
	params := concurrencyParams(t, concurrencyTestGroup, "sleep 30", 0)
	params.Timeout.Duration = 300 * time.Millisecond
	params.Timeout.KillAfter = time.Second

	start := time.Now()
	err := New().Run(t.Context(), params)
	elapsed := time.Since(start)
	require.Error(t, err)
	assert.Less(t, elapsed, 5*time.Second)

	handle, err := lock.Acquire(t.Context(), &lock.Options{
		Path:        concurrencyLockPath(t, params),
		Owner:       lock.NewOwner("after", "", ""),
		WaitTimeout: 0,
	})
	require.NoError(t, err)
	require.NoError(t, handle.Release())
}

func TestExecutor_Run_LockReleasedOnContextCancel(t *testing.T) {
	params := concurrencyParams(t, concurrencyTestGroup,
		`trap 'kill $! 2>/dev/null; exit 0' INT; sleep 30 & wait $!`, 0)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := New().Run(ctx, params)
	elapsed := time.Since(start)
	// exec.Cmd returns the context error when the context is cancelled, even
	// if the script exits with 0 after catching SIGINT.
	require.ErrorIs(t, err, context.Canceled)
	assert.Less(t, elapsed, 5*time.Second)

	// The lock must be released after SIGINT.
	handle, err := lock.Acquire(t.Context(), &lock.Options{
		Path:        concurrencyLockPath(t, params),
		Owner:       lock.NewOwner("after", "", ""),
		WaitTimeout: 0,
	})
	require.NoError(t, err)
	require.NoError(t, handle.Release())
}

func TestExecutor_Run_ContextCancelledWhileWaitingNeverStartsScript(t *testing.T) {
	params := concurrencyParams(t, concurrencyTestGroup, "printf started > \"$MARKER\"", 30*time.Second)
	marker := filepath.Join(t.TempDir(), "marker")
	t.Setenv("MARKER", marker)

	holder, err := lock.Acquire(t.Context(), &lock.Options{
		Path:        concurrencyLockPath(t, params),
		Owner:       lock.NewOwner("holder", params.Concurrency.ConfigPath, params.Concurrency.Group),
		WaitTimeout: 0,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err = New().Run(ctx, params)
	elapsed := time.Since(start)
	require.ErrorIs(t, err, context.Canceled)
	assert.Less(t, elapsed, 3*time.Second)

	_, err = os.Stat(marker)
	assert.True(t, os.IsNotExist(err))
	require.NoError(t, holder.Release())
}
