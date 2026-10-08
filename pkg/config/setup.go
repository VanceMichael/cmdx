package config

import (
	"strings"

	"github.com/suzuki-shunsuke/cmdx/pkg/domain"
	"github.com/suzuki-shunsuke/cmdx/pkg/tmpl"
)

const defaultTimeout = 36000 // default 10H

// setupConfig applies the file level default values to the tasks that the
// configuration file declares.
// The default values never affect tasks declared in other configuration files.
func setupConfig(cfg *domain.Config) error {
	base := &domain.Task{
		InputEnvs:   cfg.InputEnvs,
		Quiet:       cfg.Quiet,
		ScriptEnvs:  cfg.ScriptEnvs,
		Environment: cfg.Environment,
		Timeout:     cfg.Timeout,
	}
	for i, task := range cfg.Tasks {
		if err := setupTask(&task, base); err != nil {
			return err
		}
		cfg.Tasks[i] = task
	}
	return nil
}

func setupEnvs(envs []string, name string) ([]string, error) {
	arr := make([]string, len(envs))
	for i, env := range envs {
		e, err := tmpl.RenderTemplate(env, map[string]string{
			"name": name,
		})
		if err != nil {
			return nil, err
		}
		arr[i] = strings.ToUpper(strings.ReplaceAll(e, "-", "_"))
	}
	return arr, nil
}

func setupTask(task, base *domain.Task) error {
	inputEnvs := task.InputEnvs
	if len(inputEnvs) == 0 {
		inputEnvs = base.InputEnvs
	}

	if task.Quiet == nil {
		task.Quiet = base.Quiet
	}

	scriptEnvs := task.ScriptEnvs
	if len(scriptEnvs) == 0 {
		scriptEnvs = base.ScriptEnvs
	}

	if task.Environment == nil {
		task.Environment = map[string]string{}
	}
	for k, v := range base.Environment {
		if _, ok := task.Environment[k]; !ok {
			task.Environment[k] = v
		}
	}

	if task.Timeout.Duration == 0 {
		if base.Timeout.Duration == 0 {
			task.Timeout.Duration = defaultTimeout
		} else {
			task.Timeout.Duration = base.Timeout.Duration
		}
	}

	if task.Timeout.KillAfter == 0 {
		task.Timeout.KillAfter = base.Timeout.KillAfter
	}

	for j, flag := range task.Flags {
		if len(flag.InputEnvs) == 0 {
			flag.InputEnvs = inputEnvs
		}
		envs, err := setupEnvs(flag.InputEnvs, flag.Name)
		if err != nil {
			return err
		}
		flag.InputEnvs = envs

		if len(flag.ScriptEnvs) == 0 {
			flag.ScriptEnvs = scriptEnvs
		}
		envs, err = setupEnvs(flag.ScriptEnvs, flag.Name)
		if err != nil {
			return err
		}
		flag.ScriptEnvs = envs
		if flag.Prompt.Type != "" {
			if flag.Prompt.Message == "" {
				flag.Prompt.Message = flag.Name
			}
		}

		task.Flags[j] = flag
	}

	for j, arg := range task.Args {
		if len(arg.InputEnvs) == 0 {
			arg.InputEnvs = inputEnvs
		}
		envs, err := setupEnvs(arg.InputEnvs, arg.Name)
		if err != nil {
			return err
		}
		arg.InputEnvs = envs

		if len(arg.ScriptEnvs) == 0 {
			arg.ScriptEnvs = scriptEnvs
		}
		envs, err = setupEnvs(arg.ScriptEnvs, arg.Name)
		if err != nil {
			return err
		}
		arg.ScriptEnvs = envs

		if arg.Prompt.Type != "" {
			if arg.Prompt.Message == "" {
				arg.Prompt.Message = arg.Name
			}
		}

		task.Args[j] = arg
	}

	task.Require.Exec = append(task.Require.Exec, base.Require.Exec...)
	task.Require.Environment = append(task.Require.Environment, base.Require.Environment...)

	if len(task.Tasks) != 0 {
		for i, t := range task.Tasks {
			if err := setupTask(&t, task); err != nil {
				return err
			}
			task.Tasks[i] = t
		}
		return nil
	}

	return nil
}

// setWorkingDir sets the default working directory of the task and its nested
// tasks to the directory of the configuration file that declares them.
func setWorkingDir(tasks []domain.Task, workingDir string) {
	for i := range tasks {
		tasks[i].WorkingDir = workingDir
		setWorkingDir(tasks[i].Tasks, workingDir)
	}
}
