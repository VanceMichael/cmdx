package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suzuki-shunsuke/cmdx/pkg/domain"
)

const (
	envFOO      = "FOO"
	envBAR      = "BAR"
	valBar      = "bar"
	valFoo      = "foo"
	valHello    = "hello"
	valEnv      = "env"
	titleNormal = "normal"
)

func Test_setupEnvs(t *testing.T) {
	envs, err := setupEnvs([]string{"{{.name}}-zoo"}, valFoo)
	require.NoError(t, err)
	assert.Equal(t, []string{"FOO_ZOO"}, envs)
}

func Test_setupTask(t *testing.T) {
	data := []struct {
		title string
		task  *domain.Task
		base  *domain.Task
		isErr bool
		exp   *domain.Task
	}{
		{
			title: "flags and args are empty",
			task:  &domain.Task{},
			base:  &domain.Task{},
			exp: &domain.Task{
				Timeout: domain.Timeout{
					Duration: defaultTimeout,
				},
				Environment: map[string]string{},
			},
		},
		{
			title: "set environment variable",
			task:  &domain.Task{},
			base: &domain.Task{
				Environment: map[string]string{
					envFOO: valFoo,
				},
			},
			exp: &domain.Task{
				Timeout: domain.Timeout{
					Duration: defaultTimeout,
				},
				Environment: map[string]string{
					envFOO: valFoo,
				},
			},
		},
		{
			title: titleNormal,
			task: &domain.Task{
				Flags: []domain.Flag{
					{
						Name: valFoo,
					},
				},
				Args: []domain.Arg{
					{
						Name: valBar,
					},
				},
				Environment: map[string]string{
					"ZOO":  "zoo",
					envBAR: valBar,
				},
			},
			base: &domain.Task{
				InputEnvs: []string{"{{.name}}"},
				Environment: map[string]string{
					envFOO: valFoo,
					envBAR: valHello,
				},
			},
			exp: &domain.Task{
				Timeout: domain.Timeout{
					Duration: defaultTimeout,
				},
				Flags: []domain.Flag{
					{
						Name:       valFoo,
						InputEnvs:  []string{envFOO},
						ScriptEnvs: []string{},
					},
				},
				Args: []domain.Arg{
					{
						Name:       valBar,
						InputEnvs:  []string{envBAR},
						ScriptEnvs: []string{},
					},
				},
				Environment: map[string]string{
					envFOO: valFoo,
					envBAR: valBar,
					"ZOO":  "zoo",
				},
			},
		},
	}
	for _, d := range data {
		t.Run(d.title, func(t *testing.T) {
			err := setupTask(d.task, d.base)
			if err != nil {
				if d.isErr {
					return
				}
				assert.Error(t, err)
				return
			}
			assert.False(t, d.isErr)
			assert.Equal(t, d.exp, d.task)
		})
	}
}

func Test_setupConfig(t *testing.T) {
	data := []struct {
		title string
		cfg   *domain.Config
		exp   *domain.Config
		isErr bool
	}{
		{
			title: titleNormal,
			cfg: &domain.Config{
				Tasks: []domain.Task{
					{
						Name:   valFoo,
						Script: valEnv,
					},
				},
			},
			exp: &domain.Config{
				Tasks: []domain.Task{
					{
						Name:   valFoo,
						Script: valEnv,
						Timeout: domain.Timeout{
							Duration: defaultTimeout,
						},
						Environment: map[string]string{},
					},
				},
			},
		},
	}
	for _, d := range data {
		t.Run(d.title, func(t *testing.T) {
			err := setupConfig(d.cfg)
			if err != nil {
				if d.isErr {
					return
				}
				assert.Error(t, err)
				return
			}
			assert.False(t, d.isErr)
			assert.Equal(t, d.exp, d.cfg)
		})
	}
}
