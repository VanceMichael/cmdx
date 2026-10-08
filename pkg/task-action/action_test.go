package action

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/suzuki-shunsuke/cmdx/pkg/domain"
	"github.com/suzuki-shunsuke/cmdx/pkg/execute"
)

const (
	valFoo      = "foo"
	valBar      = "bar"
	valFooValue = "foo-value"
)

func Test_newRetryPolicy(t *testing.T) {
	t.Parallel()
	t.Run("nil policy", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, newRetryPolicy(nil))
	})

	t.Run("backoff defaults to 1 and values are converted in seconds", func(t *testing.T) {
		t.Parallel()
		policy := newRetryPolicy(&domain.Retry{
			Attempts:  3,
			Delay:     1,
			MaxDelay:  30,
			ExitCodes: []int{2, 3},
		})
		assert.Equal(t, &execute.Retry{
			Attempts:  3,
			Delay:     time.Second,
			Backoff:   1,
			MaxDelay:  30 * time.Second,
			ExitCodes: []int{2, 3},
		}, policy)
	})

	t.Run("an explicit backoff is used as is", func(t *testing.T) {
		t.Parallel()
		backoff := 2.5
		policy := newRetryPolicy(&domain.Retry{
			Attempts: 2,
			Backoff:  &backoff,
		})
		assert.Equal(t, 2.5, policy.Backoff)
	})
}

func Test_updateVarsByArgs(t *testing.T) {
	data := []struct {
		title   string
		args    []domain.Arg
		cArgs   []string
		vars    map[string]any
		isErr   bool
		expVars map[string]any
	}{
		{
			title: "args and cArgs is empty",
			expVars: map[string]any{
				"_builtin": map[string]any{
					builtinKeyArgs:          []string{},
					builtinKeyArgsString:    "",
					builtinKeyAllArgs:       []string{},
					builtinKeyAllArgsString: "",
				},
			},
		},
		{
			title: "normal",
			args: []domain.Arg{
				{
					Name:       valFoo,
					ScriptEnvs: []string{"FOO"},
				},
				{
					Name:       valBar,
					ScriptEnvs: []string{"BAR"},
					Default:    "bar-value",
				},
			},
			cArgs: []string{
				valFooValue,
			},
			expVars: map[string]any{
				valFoo: valFooValue,
				valBar: "bar-value",
				"_builtin": map[string]any{
					builtinKeyArgs:          []string{},
					builtinKeyArgsString:    "",
					builtinKeyAllArgs:       []string{valFooValue},
					builtinKeyAllArgsString: valFooValue,
				},
			},
		},
		{
			title: "required",
			args: []domain.Arg{
				{
					Name:     valFoo,
					Required: true,
				},
			},
			isErr: true,
		},
	}
	for _, d := range data {
		t.Run(d.title, func(t *testing.T) {
			if d.vars == nil {
				d.vars = map[string]any{}
			}
			if d.args == nil {
				d.args = []domain.Arg{}
			}
			if d.cArgs == nil {
				d.cArgs = []string{}
			}
			err := updateVarsByArgs(d.args, d.cArgs, d.vars)
			if err != nil {
				if d.isErr {
					return
				}
				assert.Error(t, err)
				return
			}
			assert.False(t, d.isErr)
			assert.Equal(t, d.expVars, d.vars)
		})
	}
}
