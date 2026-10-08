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

	testTaskName        = "deploy"
	testGroupTpl        = "deploy-{{.env}}"
	testEnvVar          = "env"
	testEnvProduction   = "production"
	testGroupProduction = "deploy-production"
)

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

func Test_buildConcurrency(t *testing.T) {
	const configPath = "/path/to/cmdx.yaml"
	data := []struct {
		title string
		task  domain.Task
		vars  map[string]any
		isErr bool
		exp   *execute.Concurrency
	}{
		{
			title: "concurrency isn't configured",
			task:  domain.Task{},
			exp:   nil,
		},
		{
			title: "the static group is empty (lock-free behavior)",
			task: domain.Task{
				Concurrency: domain.Concurrency{
					Group: "   ",
				},
			},
			exp: nil,
		},
		{
			title: "the group template is rendered",
			task: domain.Task{
				Name: testTaskName,
				Concurrency: domain.Concurrency{
					Group:       testGroupTpl,
					WaitTimeout: new(30),
				},
			},
			vars: map[string]any{
				testEnvVar: testEnvProduction,
			},
			exp: &execute.Concurrency{
				ConfigPath:  configPath,
				Group:       testGroupProduction,
				TaskName:    testTaskName,
				WaitTimeout: 30 * time.Second,
			},
		},
		{
			title: "an unset wait_timeout defaults to immediate failure",
			task: domain.Task{
				Name: testTaskName,
				Concurrency: domain.Concurrency{
					Group: testGroupTpl,
				},
			},
			vars: map[string]any{
				testEnvVar: testEnvProduction,
			},
			exp: &execute.Concurrency{
				ConfigPath: configPath,
				Group:      testGroupProduction,
				TaskName:   testTaskName,
			},
		},
		{
			title: "an explicit wait_timeout zero is kept",
			task: domain.Task{
				Name: testTaskName,
				Concurrency: domain.Concurrency{
					Group:       testGroupTpl,
					WaitTimeout: new(0),
				},
			},
			vars: map[string]any{
				testEnvVar: testEnvProduction,
			},
			exp: &execute.Concurrency{
				ConfigPath: configPath,
				Group:      testGroupProduction,
				TaskName:   testTaskName,
			},
		},
		{
			title: "the group renders to an empty string",
			task: domain.Task{
				Name: testTaskName,
				Concurrency: domain.Concurrency{
					Group: "{{." + testEnvVar + "}}",
				},
			},
			vars: map[string]any{
				testEnvVar: "",
			},
			isErr: true,
		},
		{
			title: "the group renders to only whitespace",
			task: domain.Task{
				Name: testTaskName,
				Concurrency: domain.Concurrency{
					Group: "  {{." + testEnvVar + "}}  ",
				},
			},
			vars: map[string]any{
			testEnvVar: " ",
		},
		isErr: true,
	},
	{
		title: "the group template is invalid",
		task: domain.Task{
			Name: testTaskName,
			Concurrency: domain.Concurrency{
				Group: testGroupTpl[:len(testGroupTpl)-1],
			},
		},
		vars:  map[string]any{},
		isErr: true,
	},
}
	for _, d := range data {
		t.Run(d.title, func(t *testing.T) {
			conc, err := buildConcurrency(&d.task, d.vars, configPath)
			if err != nil {
				assert.True(t, d.isErr, "unexpected error: %s", err)
				return
			}
			assert.False(t, d.isErr)
			assert.Equal(t, d.exp, conc)
		})
	}
}
