package execute

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/suzuki-shunsuke/go-error-with-exit-code/ecerror"
)

const fallbackWaitDelay = 1000 * time.Hour //nolint:mnd

type Executor struct{}

func New() *Executor {
	return &Executor{}
}

type Params struct {
	Shell      []string
	Script     string
	WorkingDir string
	Envs       []string
	Quiet      bool
	DryRun     bool
	Timeout    *Timeout
	Retry      *Retry
}

type Timeout struct {
	Duration  time.Duration
	KillAfter time.Duration
}

// Retry is the retry policy of the command.
// Attempts is the total number of attempts including the first execution.
// A nil Retry or Attempts equal to 0 means the command is run only once.
// An empty ExitCodes means any non-zero exit code is retried.
type Retry struct {
	Attempts  int
	Delay     time.Duration
	Backoff   float64
	MaxDelay  time.Duration
	ExitCodes []int
}

func setCancel(cmd *exec.Cmd, waitDelay time.Duration) *exec.Cmd {
	cmd.Cancel = func() error {
		return cmd.Process.Signal(os.Interrupt) //nolint:wrapcheck
	}
	if waitDelay == 0 {
		waitDelay = fallbackWaitDelay
	}
	cmd.WaitDelay = waitDelay
	return cmd
}

func getShell(shell []string) []string {
	if len(shell) != 0 {
		return shell
	}
	// Try to use bash with safer options, fall back to sh if bash isn't available
	if _, err := exec.LookPath("bash"); err != nil {
		return []string{"sh", "-c"}
	}
	// Use bash with safer options by default
	return []string{"bash", "-euo", "pipefail", "-c"}
}

func (exc *Executor) Run(ctx context.Context, params *Params) error {
	if !params.Quiet {
		fmt.Fprintln(os.Stderr, "+ "+params.Script)
	}
	if params.DryRun {
		return nil
	}

	shell := getShell(params.Shell)
	timeout := params.Timeout
	if timeout == nil {
		timeout = &Timeout{}
	}

	attempts := 1
	if params.Retry != nil && params.Retry.Attempts > 1 {
		attempts = params.Retry.Attempts
	}
	delay := retryDelay(params.Retry)

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		// Never start a new attempt after the context is canceled.
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return wrapExitError(lastErr)
			}
			return err
		}
		if attempts > 1 {
			fmt.Fprintf(os.Stderr, "# attempt %d/%d\n", attempt, attempts)
		}

		err := runOnce(ctx, shell, params, timeout)
		if err == nil {
			return nil
		}
		lastErr = err

		// The context is canceled while the command is running. Don't start a new attempt.
		if ctx.Err() != nil {
			return wrapExitError(err)
		}

		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			// The command wasn't actually started, e.g. the shell binary wasn't found.
			// A process launch error must not be treated as a retryable exit.
			return err
		}

		if !isRetryableExitCode(exitErr.ExitCode(), params.Retry) || attempt == attempts {
			return ecerror.Wrap(err, exitErr.ExitCode())
		}

		fmt.Fprintf(os.Stderr,
			"command failed with exit code %d; retrying in %s (attempt %d/%d)\n",
			exitErr.ExitCode(), delay, attempt+1, attempts)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ecerror.Wrap(err, exitErr.ExitCode())
		case <-timer.C:
		}
		delay = nextDelay(delay, params.Retry)
	}
	return wrapExitError(lastErr)
}

// runOnce starts a new process every time.
// The timeout and kill_after are applied afresh to each process.
func runOnce(ctx context.Context, shell []string, params *Params, timeout *Timeout) error {
	cmdCtx := ctx
	if timeout.Duration > 0 {
		c, cancel := context.WithTimeout(ctx, timeout.Duration)
		defer cancel()
		cmdCtx = c
	}

	cmd := exec.CommandContext(cmdCtx, shell[0], append(shell[1:], params.Script)...) //nolint:gosec
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Dir = params.WorkingDir
	cmd.Env = append(os.Environ(), params.Envs...)

	setCancel(cmd, timeout.KillAfter)

	if timeout.Duration > 0 {
		go func() {
			<-cmdCtx.Done()
			if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
				fmt.Fprintf(os.Stderr, "command is terminated by timeout: %d seconds\n", timeout.Duration)
			}
		}()
	}

	return cmd.Run() //nolint:wrapcheck
}

func isRetryableExitCode(code int, retry *Retry) bool {
	if retry == nil || code == 0 {
		return false
	}
	if len(retry.ExitCodes) == 0 {
		return true
	}
	for _, exitCode := range retry.ExitCodes {
		if exitCode == code {
			return true
		}
	}
	return false
}

func retryDelay(retry *Retry) time.Duration {
	if retry == nil {
		return 0
	}
	return retry.Delay
}

func nextDelay(delay time.Duration, retry *Retry) time.Duration {
	if retry == nil || retry.Backoff <= 1 {
		return delay
	}
	next := time.Duration(float64(delay) * retry.Backoff)
	if retry.MaxDelay > 0 && next > retry.MaxDelay {
		return retry.MaxDelay
	}
	return next
}

func wrapExitError(err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return ecerror.Wrap(err, exitErr.ExitCode())
	}
	return err
}
