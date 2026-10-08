package execute

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/suzuki-shunsuke/cmdx/pkg/lock"
	"github.com/suzuki-shunsuke/go-error-with-exit-code/ecerror"
)

type Executor struct{}

func New() *Executor {
	return &Executor{}
}

type Params struct {
	Shell       []string
	Script      string
	WorkingDir  string
	Envs        []string
	Quiet       bool
	DryRun      bool
	Timeout     *Timeout
	Concurrency *Concurrency
}

type Timeout struct {
	Duration  time.Duration
	KillAfter time.Duration
}

// Concurrency is the resolved concurrency policy of a task.
type Concurrency struct {
	// ConfigPath is the root configuration file path used to determine the
	// lock file path.
	ConfigPath string
	// Group is the rendered concurrency group.
	Group string
	// TaskName is the task name used for the lock owner metadata.
	TaskName string
	// WaitTimeout is the maximum time to wait for the lock. Zero means
	// failing immediately when the lock is held.
	WaitTimeout time.Duration
}

func setCancel(cmd *exec.Cmd, waitDelay time.Duration) *exec.Cmd {
	cmd.Cancel = func() error {
		return cmd.Process.Signal(os.Interrupt) //nolint:wrapcheck
	}
	if waitDelay == 0 {
		waitDelay = 1000 * time.Hour //nolint:mnd
	}
	cmd.WaitDelay = waitDelay
	return cmd
}

func (exc *Executor) Run(ctx context.Context, params *Params) error {
	shell := params.Shell
	if len(shell) == 0 {
		// Try to use bash with safer options, fall back to sh if bash isn't available
		if _, err := exec.LookPath("bash"); err != nil {
			shell = []string{"sh", "-c"}
		} else {
			// Use bash with safer options by default
			shell = []string{"bash", "-euo", "pipefail", "-c"}
		}
	}

	// dry-run never acquires, waits for, or writes the lock.
	if params.DryRun {
		if !params.Quiet {
			fmt.Fprintln(os.Stderr, "+ "+params.Script)
		}
		return nil
	}

	// Acquire the inter-process lock right before the subprocess starts and
	// release it after the subprocess exits. The deferred release runs on
	// every path including a non-zero exit, the task timeout, and SIGINT.
	// If this process is killed, the operating system releases the lock
	// automatically.
	var lockHandle *lock.Handle
	if params.Concurrency != nil {
		lockPath, err := lock.Path(params.Concurrency.ConfigPath, params.Concurrency.Group)
		if err != nil {
			return err
		}
		lockHandle, err = lock.Acquire(ctx, &lock.Options{
			Path:        lockPath,
			WaitTimeout: params.Concurrency.WaitTimeout,
			Owner: lock.NewOwner(
				params.Concurrency.TaskName,
				params.Concurrency.ConfigPath,
				params.Concurrency.Group,
			),
			LogWriter: os.Stderr,
		})
		if err != nil {
			return err
		}
		defer func() {
			if err := lockHandle.Release(); err != nil {
				fmt.Fprintf(os.Stderr, "cmdx: failed to release the lock: %s\n", err)
			}
		}()
	}

	// Apply the task timeout to the context before creating the command so
	// that CommandContext watches the timeout context and the command is
	// actually canceled when the timeout elapses.
	if params.Timeout.Duration > 0 {
		c, cancel := context.WithTimeout(ctx, params.Timeout.Duration)
		defer cancel()
		ctx = c
	}

	cmd := exec.CommandContext(ctx, shell[0], append(shell[1:], params.Script)...) //nolint:gosec
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Dir = params.WorkingDir

	cmd.Env = append(os.Environ(), params.Envs...)
	if !params.Quiet {
		fmt.Fprintln(os.Stderr, "+ "+params.Script)
	}

	setCancel(cmd, params.Timeout.KillAfter)

	go func() {
		<-ctx.Done()
		err := ctx.Err()
		if errors.Is(err, context.DeadlineExceeded) {
			fmt.Fprintf(os.Stderr, "command is terminated by timeout: %d seconds\n",
				int(params.Timeout.Duration/time.Second))
		}
	}()
	if err := cmd.Run(); err != nil {
		return ecerror.Wrap(err, cmd.ProcessState.ExitCode())
	}
	return nil
}
