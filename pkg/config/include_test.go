package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suzuki-shunsuke/cmdx/pkg/domain"
)

func writeFile(t *testing.T, p string, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
}

func taskNames(tasks []domain.Task) []string {
	names := make([]string, 0, len(tasks))
	for _, task := range tasks {
		names = append(names, task.Name)
	}
	return names
}

func findTask(t *testing.T, tasks []domain.Task, name string) domain.Task {
	t.Helper()
	for _, task := range tasks {
		if task.Name == name {
			return task
		}
	}
	t.Fatalf("task %q is not found: %v", name, taskNames(tasks))
	return domain.Task{}
}

func TestLoad_noIncludes(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "cmdx.yaml")
	writeFile(t, root, `---
tasks:
- name: hello
  script: echo hello
`)

	cfg, err := New().Load(root)
	require.NoError(t, err)
	require.Len(t, cfg.Tasks, 1)
	task := cfg.Tasks[0]
	assert.Equal(t, "hello", task.Name)
	assert.Equal(t, defaultTimeout, task.Timeout.Duration)
	assert.Equal(t, dir, task.WorkingDir)
}

func TestLoad_depthFirstOrder(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "cmdx.yaml")
	writeFile(t, root, `---
includes:
- a.yaml
- b.yaml
tasks:
- name: root1
  script: echo root
`)
	writeFile(t, filepath.Join(dir, "a.yaml"), `---
includes:
- c.yaml
tasks:
- name: a1
  script: echo a
`)
	writeFile(t, filepath.Join(dir, "b.yaml"), `---
tasks:
- name: b1
  script: echo b
`)
	writeFile(t, filepath.Join(dir, "c.yaml"), `---
tasks:
- name: c1
  script: echo c
`)

	cfg, err := New().Load(root)
	require.NoError(t, err)
	// Each included file's tasks come first in DFS order, then the current
	// file's tasks.
	assert.Equal(t, []string{"c1", "a1", "b1", "root1"}, taskNames(cfg.Tasks))
}

func TestLoad_nestedIncludeRelativePathAndWorkingDir(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "cmdx.yaml")
	writeFile(t, root, `---
includes:
- sub/a.yaml
tasks:
- name: root1
  script: echo root
`)
	writeFile(t, filepath.Join(dir, "sub", "a.yaml"), `---
includes:
- deep/c.yaml
tasks:
- name: a1
  script: echo a
- name: group
  tasks:
  - name: nested
    script: echo nested
`)
	writeFile(t, filepath.Join(dir, "sub", "deep", "c.yaml"), `---
tasks:
- name: c1
  script: echo c
`)

	cfg, err := New().Load(root)
	require.NoError(t, err)
	assert.Equal(t, []string{"c1", "a1", "group", "root1"}, taskNames(cfg.Tasks))

	// Include paths are resolved relative to the directory of the file that
	// declares them, and tasks run in the declaring file's directory by
	// default. Nested tasks inherit the directory too.
	assert.Equal(t, filepath.Join(dir, "sub", "deep"), findTask(t, cfg.Tasks, "c1").WorkingDir)
	assert.Equal(t, filepath.Join(dir, "sub"), findTask(t, cfg.Tasks, "a1").WorkingDir)
	group := findTask(t, cfg.Tasks, "group")
	require.Len(t, group.Tasks, 1)
	assert.Equal(t, filepath.Join(dir, "sub"), group.Tasks[0].WorkingDir)
	assert.Equal(t, dir, findTask(t, cfg.Tasks, "root1").WorkingDir)
}

func TestLoad_diamondIncludeIsDeduplicated(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "cmdx.yaml")
	writeFile(t, root, `---
includes:
- a.yaml
- b.yaml
tasks:
- name: root1
  script: echo root
`)
	writeFile(t, filepath.Join(dir, "a.yaml"), `---
includes:
- d.yaml
tasks:
- name: a1
  script: echo a
`)
	writeFile(t, filepath.Join(dir, "b.yaml"), `---
includes:
- d.yaml
tasks:
- name: b1
  script: echo b
`)
	writeFile(t, filepath.Join(dir, "d.yaml"), `---
tasks:
- name: d1
  script: echo d
`)

	cfg, err := New().Load(root)
	require.NoError(t, err)
	// d.yaml is identified by its normalized real path and contributes only
	// once, at the position where it is first encountered.
	assert.Equal(t, []string{"d1", "a1", "b1", "root1"}, taskNames(cfg.Tasks))
}

func TestLoad_fileLevelDefaultsDoNotLeak(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "cmdx.yaml")
	writeFile(t, root, `---
environment:
  ROOT_V: root
input_envs:
- ROOTENV
script_envs:
- ROOTSCRIPT
timeout:
  duration: 12
  kill_after: 3
quiet: true
includes:
- a.yaml
- b.yaml
tasks:
- name: roottask
  script: echo root
  flags:
  - name: src
`)
	writeFile(t, filepath.Join(dir, "a.yaml"), `---
environment:
  A_V: a
input_envs:
- AENV
timeout:
  duration: 34
quiet: false
tasks:
- name: atask
  script: echo a
  flags:
  - name: path
- name: group
  tasks:
  - name: nested
    script: echo nested
`)
	writeFile(t, filepath.Join(dir, "b.yaml"), `---
tasks:
- name: btask
  script: echo b
  flags:
  - name: file
`)

	cfg, err := New().Load(root)
	require.NoError(t, err)

	rootTask := findTask(t, cfg.Tasks, "roottask")
	assert.Equal(t, map[string]string{"ROOT_V": "root"}, rootTask.Environment)
	require.Len(t, rootTask.Flags, 1)
	assert.Equal(t, []string{"ROOTENV"}, rootTask.Flags[0].InputEnvs)
	assert.Equal(t, []string{"ROOTSCRIPT"}, rootTask.Flags[0].ScriptEnvs)
	assert.Equal(t, 12, rootTask.Timeout.Duration)
	assert.Equal(t, 3, rootTask.Timeout.KillAfter)
	require.NotNil(t, rootTask.Quiet)
	assert.True(t, *rootTask.Quiet)

	aTask := findTask(t, cfg.Tasks, "atask")
	// The root file level defaults and sibling file's defaults don't leak.
	assert.Equal(t, map[string]string{"A_V": "a"}, aTask.Environment)
	require.Len(t, aTask.Flags, 1)
	assert.Equal(t, []string{"AENV"}, aTask.Flags[0].InputEnvs)
	assert.Equal(t, 34, aTask.Timeout.Duration)
	assert.Equal(t, 0, aTask.Timeout.KillAfter)
	require.NotNil(t, aTask.Quiet)
	assert.False(t, *aTask.Quiet)

	// Nested tasks inherit the defaults of the file that declares them.
	group := findTask(t, cfg.Tasks, "group")
	require.Len(t, group.Tasks, 1)
	nested := group.Tasks[0]
	assert.Equal(t, map[string]string{"A_V": "a"}, nested.Environment)
	assert.Equal(t, 34, nested.Timeout.Duration)
	require.NotNil(t, nested.Quiet)
	assert.False(t, *nested.Quiet)

	// A file without file level defaults gets the builtin defaults.
	bTask := findTask(t, cfg.Tasks, "btask")
	assert.Equal(t, map[string]string{}, bTask.Environment)
	assert.Equal(t, defaultTimeout, bTask.Timeout.Duration)
	assert.Nil(t, bTask.Quiet)
	require.Len(t, bTask.Flags, 1)
	assert.Empty(t, bTask.Flags[0].InputEnvs)
}

func TestLoad_sameFileViaSymlinkIsLoadedOnce(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "realdir", "d.yaml"), `---
tasks:
- name: d1
  script: echo d
`)
	if err := os.Symlink(filepath.Join(dir, "realdir"), filepath.Join(dir, "alias")); err != nil {
		t.Skipf("creating a symlink is unsupported: %v", err)
	}
	root := filepath.Join(dir, "cmdx.yaml")
	writeFile(t, root, `---
includes:
- realdir/d.yaml
- alias/d.yaml
tasks:
- name: root1
  script: echo root
`)

	cfg, err := New().Load(root)
	require.NoError(t, err)
	assert.Equal(t, []string{"d1", "root1"}, taskNames(cfg.Tasks))
}

func TestLoad_absoluteIncludePath(t *testing.T) {
	dir := t.TempDir()
	other := t.TempDir()
	shared := filepath.Join(other, "shared.yaml")
	writeFile(t, shared, `---
tasks:
- name: shared
  script: echo shared
`)
	root := filepath.Join(dir, "cmdx.yaml")
	writeFile(t, root, "---\nincludes:\n- "+shared+"\ntasks: []\n")

	cfg, err := New().Load(root)
	require.NoError(t, err)
	assert.Equal(t, []string{"shared"}, taskNames(cfg.Tasks))
	assert.Equal(t, other, findTask(t, cfg.Tasks, "shared").WorkingDir)
}

func TestLoad_missingIncludeReportsChain(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "cmdx.yaml")
	writeFile(t, root, `---
includes:
- sub/a.yaml
tasks: []
`)
	a := filepath.Join(dir, "sub", "a.yaml")
	writeFile(t, a, `---
includes:
- missing.yaml
tasks: []
`)

	cfg, err := New().Load(root)
	require.Error(t, err)
	assert.Nil(t, cfg)
	msg := err.Error()
	assert.Contains(t, msg, "failed to read the configuration file")
	assert.Contains(t, msg, "missing.yaml")
	assert.Contains(t, msg, "include chain:")
	assert.Contains(t, msg, root)
	assert.Contains(t, msg, a)
}

func TestLoad_syntaxErrorReportsChain(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "cmdx.yaml")
	writeFile(t, root, `---
includes:
- sub/a.yaml
tasks: []
`)
	a := filepath.Join(dir, "sub", "a.yaml")
	writeFile(t, a, `---
includes:
- bad.yaml
tasks: []
`)
	bad := filepath.Join(dir, "sub", "bad.yaml")
	writeFile(t, bad, "tasks: [\n")

	cfg, err := New().Load(root)
	require.Error(t, err)
	assert.Nil(t, cfg)
	msg := err.Error()
	assert.Contains(t, msg, "failed to parse the configuration file")
	assert.Contains(t, msg, bad)
	assert.Contains(t, msg, root)
	assert.Contains(t, msg, a)
}

func TestLoad_cyclicIncludeReportsCycle(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "cmdx.yaml")
	writeFile(t, root, `---
includes:
- a.yaml
tasks: []
`)
	a := filepath.Join(dir, "a.yaml")
	writeFile(t, a, `---
includes:
- b.yaml
tasks: []
`)
	b := filepath.Join(dir, "b.yaml")
	writeFile(t, b, `---
includes:
- a.yaml
tasks: []
`)

	cfg, err := New().Load(root)
	require.Error(t, err)
	assert.Nil(t, cfg)
	msg := err.Error()
	assert.Contains(t, msg, "cyclic configuration include")
	// The chain starts at the root and reaches the repeated file again.
	chain := msg[strings.Index(msg, "include chain:"):]
	assert.Contains(t, chain, root)
	assert.Contains(t, chain, a)
	assert.Contains(t, chain, b)
	assert.GreaterOrEqual(t, strings.Count(chain, "a.yaml"), 2)
}

func TestLoad_selfIncludeReportsCycle(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "cmdx.yaml")
	writeFile(t, root, `---
includes:
- cmdx.yaml
tasks: []
`)

	cfg, err := New().Load(root)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "cyclic configuration include")
}

func TestLoad_duplicateTaskNameAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "cmdx.yaml")
	writeFile(t, root, `---
includes:
- a.yaml
tasks:
- name: dup
  script: echo root
`)
	writeFile(t, filepath.Join(dir, "a.yaml"), `---
tasks:
- name: dup
  script: echo a
`)

	cfg, err := New().Load(root)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), `the task name duplicates: "dup"`)
}

func TestLoad_duplicateTaskShortAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "cmdx.yaml")
	writeFile(t, root, `---
includes:
- a.yaml
tasks:
- name: root-task
  short: x
  script: echo root
`)
	writeFile(t, filepath.Join(dir, "a.yaml"), `---
tasks:
- name: a-task
  short: x
  script: echo a
`)

	cfg, err := New().Load(root)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), `the task short name duplicates: "x"`)
}

func TestLoad_isDeterministic(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "cmdx.yaml")
	writeFile(t, root, `---
includes:
- a.yaml
- b.yaml
tasks:
- name: root1
  script: echo root
`)
	writeFile(t, filepath.Join(dir, "a.yaml"), `---
includes:
- d.yaml
tasks:
- name: a1
  script: echo a
`)
	writeFile(t, filepath.Join(dir, "b.yaml"), `---
includes:
- d.yaml
tasks:
- name: b1
  script: echo b
`)
	writeFile(t, filepath.Join(dir, "d.yaml"), `---
tasks:
- name: d1
  script: echo d
`)

	first, err := New().Load(root)
	require.NoError(t, err)
	second, err := New().Load(root)
	require.NoError(t, err)
	assert.Equal(t, taskNames(first.Tasks), taskNames(second.Tasks))
	assert.Equal(t, []string{"d1", "a1", "b1", "root1"}, taskNames(first.Tasks))
}
