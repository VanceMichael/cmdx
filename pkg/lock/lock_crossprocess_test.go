//go:build !windows

package lock

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	helperEnv      = "CMDX_LOCK_TEST_HELPER"
	helperAcquired = "HELPER_ACQUIRED"
	helperBusy     = "HELPER_BUSY"
)

// TestHelperProcess isn't a real test. It is re-executed as a helper process
// by TestAcquire_CrossProcess to verify the OS level lock behavior across
// processes, including recovery from SIGKILL.
func TestHelperProcess(t *testing.T) {
	if os.Getenv(helperEnv) == "" {
		t.Skip()
	}
	i := slices.Index(os.Args, "--")
	if i < 0 || len(os.Args) < i+3 {
		fmt.Fprintln(os.Stderr, "invalid helper arguments")
		os.Exit(2)
	}
	path := os.Args[i+1]
	holdMs, err := strconv.Atoi(os.Args[i+2])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	waitMs := 0
	if len(os.Args) >= i+4 {
		waitMs, err = strconv.Atoi(os.Args[i+3])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}

	h, err := Acquire(context.Background(), &Options{
		Path:        path,
		Owner:       NewOwner("helper-task", "/tmp/cmdx.yaml", "helper-group"),
		WaitTimeout: time.Duration(waitMs) * time.Millisecond,
	})
	if err != nil {
		if _, ok := errors.AsType[*ConflictError](err); ok {
			// os.Stdout is an *os.File, so Write goes straight to the pipe and
			// no flush is needed.
			fmt.Println(helperBusy)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	defer func() { _ = h.Release() }()

	fmt.Println(helperAcquired)
	time.Sleep(time.Duration(holdMs) * time.Millisecond)
	os.Exit(0)
}

type helperProcess struct {
	cmd *exec.Cmd

	acquired chan struct{}
	busy     chan struct{}
	stderr   *bytes.Buffer
	killOnce sync.Once
}

func startHelper(t *testing.T, path string, holdMs, waitMs int) *helperProcess {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.CommandContext(context.Background(), exe,
		"-test.run", "^TestHelperProcess$",
		"--", path, strconv.Itoa(holdMs), strconv.Itoa(waitMs))
	cmd.Env = append(os.Environ(), helperEnv+"=1")
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	hp := &helperProcess{
		cmd:      cmd,
		acquired: make(chan struct{}),
		busy:     make(chan struct{}),
		stderr:   &bytes.Buffer{},
	}
	cmd.Stderr = hp.stderr
	require.NoError(t, cmd.Start())

	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			switch strings.TrimSpace(scanner.Text()) {
			case helperAcquired:
				close(hp.acquired)
			case helperBusy:
				close(hp.busy)
			}
		}
	}()
	return hp
}

func (hp *helperProcess) waitAcquired(t *testing.T, timeout time.Duration) {
	t.Helper()
	select {
	case <-hp.acquired:
	case <-time.After(timeout):
		t.Fatal("the helper process didn't acquire the lock")
	}
}

func (hp *helperProcess) waitBusy(t *testing.T, timeout time.Duration) {
	t.Helper()
	select {
	case <-hp.busy:
	case <-time.After(timeout):
		t.Fatalf("the helper process didn't fail with the busy error; stderr: %s", hp.stderr.String())
	}
}

func (hp *helperProcess) kill(t *testing.T) {
	t.Helper()
	hp.killOnce.Do(func() {
		_ = syscall.Kill(hp.cmd.Process.Pid, syscall.SIGKILL)
		_ = hp.cmd.Wait()
	})
}

// TestAcquire_CrossProcess verifies that the OS level lock works across
// processes, that a contender fails immediately with wait_timeout 0, and that
// the lock and the stale owner metadata are recovered atomically after the
// holder is force-killed.
func TestAcquire_CrossProcess(t *testing.T) {
	path := newTestPath(t)

	// The first holder acquires the lock.
	holderA := startHelper(t, path, 60_000, 0)
	holderA.waitAcquired(t, 3*time.Second)
	t.Cleanup(func() {
		_ = syscall.Kill(holderA.cmd.Process.Pid, syscall.SIGKILL)
		_ = holderA.cmd.Wait()
	})

	owner := readOwnerForTest(t, path)
	assert.Equal(t, holderA.cmd.Process.Pid, owner.PID)

	// Another process with wait_timeout 0 fails immediately.
	competitorB := startHelper(t, path, 100, 0)
	competitorB.waitBusy(t, 3*time.Second)
	waitErr := competitorB.cmd.Wait()
	var exitErr *exec.ExitError
	require.ErrorAs(t, waitErr, &exitErr)
	assert.NotZero(t, exitErr.ExitCode())

	// A waiting competitor is blocked while the holder is alive.
	competitorC := startHelper(t, path, 60_000, 5_000)
	select {
	case <-competitorC.acquired:
		t.Fatal("the waiting competitor acquired the lock while the holder was alive")
	case <-time.After(300 * time.Millisecond):
	}
	t.Cleanup(func() {
		_ = syscall.Kill(competitorC.cmd.Process.Pid, syscall.SIGKILL)
		_ = competitorC.cmd.Wait()
	})

	// When the holder is force-killed, the operating system releases the lock
	// and the waiting competitor acquires it.
	holderA.kill(t)
	competitorC.waitAcquired(t, 3*time.Second)

	// The metadata now belongs to the new owner C, not the dead owner A.
	owner = readOwnerForTest(t, path)
	assert.Equal(t, competitorC.cmd.Process.Pid, owner.PID)

	// This process can't acquire the lock while C is alive.
	_, err := Acquire(t.Context(), &Options{
		Path:        path,
		Owner:       NewOwner("parent", "/tmp/cmdx.yaml", "g"),
		WaitTimeout: 0,
	})
	var conflict *ConflictError
	require.ErrorAs(t, err, &conflict)
	require.NotNil(t, conflict.CurrentOwner)
	assert.Equal(t, competitorC.cmd.Process.Pid, conflict.CurrentOwner.PID)

	// Force-kill C. The stale metadata must not make the lock permanently
	// busy: this process acquires the lock promptly and replaces it.
	competitorC.kill(t)
	deadline := time.Now().Add(3 * time.Second)
	var handle *Handle
	for time.Now().Before(deadline) {
		handle, err = Acquire(t.Context(), &Options{
			Path:        path,
			Owner:       NewOwner("parent", "/tmp/cmdx.yaml", "g"),
			WaitTimeout: 0,
		})
		if err == nil {
			break
		}
		require.ErrorAs(t, err, &conflict)
		time.Sleep(20 * time.Millisecond)
	}
	require.NoError(t, err, "the lock wasn't released after the holder was force-killed")
	defer func() { _ = handle.Release() }()

	owner = readOwnerForTest(t, path)
	assert.Equal(t, os.Getpid(), owner.PID)
	assert.Equal(t, "parent", owner.Task)
}
