package execute

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suzuki-shunsuke/go-error-with-exit-code/ecerror"
)

// countScript increments the counter file pointed by CMDX_TEST_FILE
// and succeeds when the count reaches CMDX_TEST_SUCCEED_AT.
const countScript = `n=0
if [ -f "$CMDX_TEST_FILE" ]; then n=$(cat "$CMDX_TEST_FILE"); fi
n=$((n+1))
printf '%s' "$n" > "$CMDX_TEST_FILE"
[ "$n" -ge "$CMDX_TEST_SUCCEED_AT" ]
`

func readCount(t *testing.T, filePath string) string {
	t.Helper()
	b, err := os.ReadFile(filePath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func counterParams(filePath string, attempts, succeedAt int) *Params {
	return &Params{
		Script: countScript,
		Envs: []string{
			"CMDX_TEST_FILE=" + filePath,
			"CMDX_TEST_SUCCEED_AT=" + strconv.Itoa(succeedAt),
		},
		Timeout: &Timeout{},
		Retry: &Retry{
			Attempts: attempts,
		},
	}
}

func TestExecutor_Run_RetrySucceeds(t *testing.T) {
	t.Parallel()
	filePath := filepath.Join(t.TempDir(), "count")
	params := counterParams(filePath, 3, 2)
	exc := New()
	require.NoError(t, exc.Run(t.Context(), params))
	assert.Equal(t, "2", readCount(t, filePath))
}

func TestExecutor_Run_RetryExhausted(t *testing.T) {
	t.Parallel()
	params := &Params{
		Script:  "exit 7",
		Timeout: &Timeout{},
		Retry: &Retry{
			Attempts: 3,
		},
	}
	exc := New()
	err := exc.Run(t.Context(), params)
	require.Error(t, err)
	assert.Equal(t, 7, ecerror.GetExitCode(err))
}

func TestExecutor_Run_ZeroAttemptsRunsOnce(t *testing.T) {
	t.Parallel()
	filePath := filepath.Join(t.TempDir(), "count")
	params := counterParams(filePath, 0, 2)
	exc := New()
	err := exc.Run(t.Context(), params)
	require.Error(t, err)
	assert.Equal(t, "1", readCount(t, filePath))
}

func TestExecutor_Run_ExitCodesFilter(t *testing.T) {
	t.Parallel()
	params := &Params{
		Script:  "exit 7",
		Timeout: &Timeout{},
		Retry: &Retry{
			Attempts:  3,
			ExitCodes: []int{2},
		},
	}
	exc := New()
	err := exc.Run(t.Context(), params)
	require.Error(t, err)
	// The exit code 7 isn't allowed by the policy, so it isn't retried.
	assert.Equal(t, 7, ecerror.GetExitCode(err))
}

func TestExecutor_Run_DryRunOnce(t *testing.T) {
	t.Parallel()
	filePath := filepath.Join(t.TempDir(), "count")
	params := counterParams(filePath, 3, 2)
	params.DryRun = true
	exc := New()
	require.NoError(t, exc.Run(t.Context(), params))
	// The command is never started.
	assert.Equal(t, "", readCount(t, filePath))
}

func TestExecutor_Run_CancelDuringWait(t *testing.T) {
	t.Parallel()
	filePath := filepath.Join(t.TempDir(), "count")
	params := counterParams(filePath, 3, 2)
	params.Retry.Delay = 10 * time.Second

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	exc := New()
	start := time.Now()
	err := exc.Run(ctx, params)
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Less(t, elapsed, 3*time.Second)
	// Only the first attempt is started; no new attempt starts after cancellation.
	assert.Equal(t, "1", readCount(t, filePath))
	assert.Equal(t, 1, ecerror.GetExitCode(err))
}

func TestExecutor_Run_CancelDuringCommand(t *testing.T) {
	t.Parallel()
	params := &Params{
		Script:  "sleep 30",
		Timeout: &Timeout{},
		Retry: &Retry{
			Attempts: 3,
		},
	}

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	exc := New()
	start := time.Now()
	err := exc.Run(ctx, params)
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Less(t, elapsed, 5*time.Second)
	assert.NotEqual(t, 0, ecerror.GetExitCode(err))
}

func TestExecutor_Run_CancelBeforeStart(t *testing.T) {
	t.Parallel()
	filePath := filepath.Join(t.TempDir(), "count")
	params := counterParams(filePath, 3, 1)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	exc := New()
	err := exc.Run(ctx, params)
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, "", readCount(t, filePath))
}

func TestExecutor_Run_ProcessLaunchErrorIsNotRetried(t *testing.T) {
	t.Parallel()
	params := &Params{
		Shell:   []string{filepath.Join(t.TempDir(), "no-such-shell"), "-c"},
		Script:  "true",
		Timeout: &Timeout{},
		Retry: &Retry{
			Attempts: 3,
		},
	}
	exc := New()
	err := exc.Run(t.Context(), params)
	require.Error(t, err)
	// A process launch error isn't wrapped as a subprocess exit code.
	assert.Equal(t, 1, ecerror.GetExitCode(err))
}

func Test_isRetryableExitCode(t *testing.T) {
	t.Parallel()
	data := []struct {
		title string
		code  int
		retry *Retry
		exp   bool
	}{
		{title: "no policy", code: 1},
		{title: "success", code: 0, retry: &Retry{Attempts: 3}, exp: false},
		{title: "any non-zero", code: 130, retry: &Retry{Attempts: 3}, exp: true},
		{title: "listed", code: 2, retry: &Retry{Attempts: 3, ExitCodes: []int{2, 3}}, exp: true},
		{title: "not listed", code: 7, retry: &Retry{Attempts: 3, ExitCodes: []int{2, 3}}, exp: false},
	}
	for _, d := range data {
		t.Run(d.title, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, d.exp, isRetryableExitCode(d.code, d.retry))
		})
	}
}

func Test_nextDelay(t *testing.T) {
	t.Parallel()
	data := []struct {
		title string
		delay time.Duration
		retry *Retry
		exp   time.Duration
	}{
		{title: "no backoff", delay: time.Second, retry: &Retry{Backoff: 1}, exp: time.Second},
		{title: "backoff", delay: time.Second, retry: &Retry{Backoff: 2}, exp: 2 * time.Second},
		{title: "capped", delay: time.Second, retry: &Retry{Backoff: 2, MaxDelay: 1500 * time.Millisecond}, exp: 1500 * time.Millisecond},
		{title: "zero delay stays zero", delay: 0, retry: &Retry{Backoff: 2}, exp: 0},
	}
	for _, d := range data {
		t.Run(d.title, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, d.exp, nextDelay(d.delay, d.retry))
		})
	}
}
