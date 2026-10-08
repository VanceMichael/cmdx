package domain

import (
	"github.com/suzuki-shunsuke/cmdx/pkg/prompt"
)

type GlobalFlags struct {
	DryRun     bool
	Quiet      *bool
	WorkingDir string
	// ConfigPath is the canonical root configuration file path, used to
	// determine the concurrency lock file path.
	ConfigPath string
}

type Flag struct {
	Name       string        `json:"name"`
	Short      string        `json:"short,omitempty"`
	Usage      string        `json:"usage,omitempty"`
	Default    string        `json:"default,omitempty"`
	InputEnvs  []string      `json:"input_envs,omitempty" yaml:"input_envs"`
	ScriptEnvs []string      `json:"script_envs,omitempty" yaml:"script_envs"`
	Type       string        `json:"type,omitempty"`
	Required   bool          `json:"required,omitempty"`
	Prompt     prompt.Prompt `json:"prompt,omitzero"`
	Validate   []Validate    `json:"validate,omitempty"`
}

type Task struct {
	Name        string            `json:"name"`
	Short       string            `json:"short,omitempty"`
	Description string            `json:"description,omitempty"`
	Usage       string            `json:"usage,omitempty"`
	Flags       []Flag            `json:"flags,omitempty"`
	Args        []Arg             `json:"args,omitempty"`
	InputEnvs   []string          `json:"input_envs,omitempty" yaml:"input_envs"`
	ScriptEnvs  []string          `json:"script_envs,omitempty" yaml:"script_envs"`
	Environment map[string]string `json:"environment,omitempty"`
	Script      string            `json:"script,omitempty"`
	Timeout     Timeout           `json:"timeout,omitzero"`
	Concurrency Concurrency       `json:"concurrency,omitzero"`
	Require     Require           `json:"require,omitzero"`
	Quiet       *bool             `json:"quiet,omitempty"`
	Shell       []string          `json:"shell,omitempty"`
	Tasks       []Task            `json:"tasks,omitempty"`
}

type Arg struct {
	Name       string        `json:"name"`
	Usage      string        `json:"usage,omitempty"`
	Default    string        `json:"default,omitempty"`
	InputEnvs  []string      `json:"input_envs,omitempty" yaml:"input_envs"`
	ScriptEnvs []string      `json:"script_envs,omitempty" yaml:"script_envs"`
	Required   bool          `json:"required,omitempty"`
	Prompt     prompt.Prompt `json:"prompt,omitzero"`
	Validate   []Validate    `json:"validate,omitempty"`
}

type Require struct {
	Exec        []StrList `json:"exec,omitempty"`
	Environment []StrList `json:"environment,omitempty"`
}

type Timeout struct {
	Duration  int `json:"duration,omitempty"`
	KillAfter int `json:"kill_after,omitempty" yaml:"kill_after"`
}

// Concurrency is the configuration to prevent tasks from running concurrently.
// Group is rendered with the same template data as task.script after flags,
// arguments, prompts and input environment variables are resolved.
// WaitTimeout is the maximum time in seconds to wait for the lock. It is a
// pointer so that an unset value inherits the parent policy while an explicit
// zero means failing immediately when another process holds the lock.
type Concurrency struct {
	Group       string `json:"group,omitempty"`
	WaitTimeout *int   `json:"wait_timeout,omitempty" yaml:"wait_timeout"`
}

type Config struct {
	Tasks       []Task            `json:"tasks"`
	InputEnvs   []string          `json:"input_envs,omitempty" yaml:"input_envs"`
	ScriptEnvs  []string          `json:"script_envs,omitempty" yaml:"script_envs"`
	Environment map[string]string `json:"environment,omitempty"`
	Timeout     Timeout           `json:"timeout,omitzero"`
	Concurrency Concurrency       `json:"concurrency,omitzero"`
	Quiet       *bool             `json:"quiet,omitempty"`
}

type Validate struct {
	Type      string   `json:"type,omitempty"`
	RegExp    string   `json:"regexp,omitempty" yaml:"regexp"`
	MinLength int      `json:"min_length,omitempty" yaml:"min_length"`
	MaxLength int      `json:"max_length,omitempty" yaml:"max_length"`
	Prefix    string   `json:"prefix,omitempty"`
	Suffix    string   `json:"suffix,omitempty"`
	Contain   string   `json:"contain,omitempty"`
	Enum      []string `json:"enum,omitempty"`

	Min int `json:"min,omitempty"`
	Max int `json:"max,omitempty"`
}

type HasIsSet interface {
	IsSet(k string) bool
}
