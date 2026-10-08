package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/suzuki-shunsuke/cmdx/pkg/domain"
	"github.com/suzuki-shunsuke/cmdx/pkg/validate"
	"go.yaml.in/yaml/v3"
)

// Load reads the root configuration file and all configuration files that are
// referenced via "includes" recursively, and merges their tasks into one
// configuration.
//
// Include paths are resolved relative to the directory of the configuration
// file that declares them. Files are identified by their normalized real paths,
// so the same file is loaded only once and cyclic includes are detected.
// Tasks are merged in the depth-first order of includes: the tasks of each
// included file come first, followed by the tasks declared in the current file.
//
// The whole include graph is read, parsed, initialized with file level
// default values, and validated before the merged configuration is returned.
// If any error occurs, no partially built configuration is returned.
func (client *Client) Load(rootPath string) (*domain.Config, error) {
	l := &loader{
		loaded: map[string]*loadedConfig{},
	}
	root, _, err := l.load(rootPath, "", nil, nil)
	if err != nil {
		return nil, err
	}

	cfg := root.raw
	cfg.Tasks = root.tasks
	if err := validate.Config(cfg); err != nil {
		return nil, fmt.Errorf("please fix the configuration file: %w", err)
	}
	return cfg, nil
}

type loadedConfig struct {
	raw   *domain.Config
	tasks []domain.Task
}

type loader struct {
	// loaded caches configuration files that have been already processed,
	// keyed by their normalized real paths.
	loaded map[string]*loadedConfig
}

// load reads one configuration file and returns it together with the merged
// task list of its include sub graph.
//
// included is false when the file was already loaded through another include
// path, in which case the caller must not append its tasks again.
//
// declaredPath is the include path written in the parent configuration file.
// baseDir is the directory of the parent configuration file and is empty only
// for the root configuration file. chain and realChain represent the include
// chain from the root file to the parent file, by apparent paths and
// normalized real paths respectively.
func (l *loader) load(
	declaredPath, baseDir string, chain, realChain []string,
) (lc *loadedConfig, included bool, err error) {
	apparentPath := declaredPath
	if baseDir != "" && !filepath.IsAbs(apparentPath) {
		// Relative include paths are resolved from the directory of the
		// file that declares them; absolute paths are used as-is.
		apparentPath = filepath.Join(baseDir, declaredPath)
	}
	abs, err := filepath.Abs(filepath.Clean(apparentPath))
	if err != nil {
		return nil, false, fmt.Errorf("failed to resolve the configuration file path %q: %w", apparentPath, err)
	}
	fileChain := append(append([]string{}, chain...), abs)

	b, err := os.ReadFile(abs)
	if err != nil {
		return nil, false, fmt.Errorf(
			"failed to read the configuration file %s: %w\ninclude chain: %s",
			abs, err, strings.Join(fileChain, " -> "))
	}

	var raw domain.Config
	if err := yaml.Unmarshal(b, &raw); err != nil {
		return nil, false, fmt.Errorf(
			"failed to parse the configuration file. the configuration file is invalid: %s: %w\ninclude chain: %s",
			abs, err, strings.Join(fileChain, " -> "))
	}

	// Identify files by the normalized real path so that different include
	// paths pointing at the same file (e.g. via symlinks) are recognized as
	// one file for de-duplication and cycle detection.
	realPath, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, false, fmt.Errorf(
			"failed to resolve the real path of the configuration file %s: %w\ninclude chain: %s",
			abs, err, strings.Join(fileChain, " -> "))
	}

	for _, p := range realChain {
		if p == realPath {
			return nil, false, fmt.Errorf(
				"a cyclic configuration include is detected:\ninclude chain: %s",
				formatIncludeChain(
					fileChain,
					append(append([]string{}, realChain...), realPath)))
		}
	}

	if cached, ok := l.loaded[realPath]; ok {
		// The same file was already included and processed at an earlier
		// position in the depth-first traversal.
		return cached, false, nil
	}

	// Apply the file level default values only to the tasks that this file
	// declares. They must never leak to tasks declared in sibling files.
	if err := setupConfig(&raw); err != nil {
		return nil, false, fmt.Errorf(
			"please fix the configuration file: %w\ninclude chain: %s",
			err, strings.Join(fileChain, " -> "))
	}
	dir := filepath.Dir(abs)
	setWorkingDir(raw.Tasks, dir)

	childRealChain := append(append([]string{}, realChain...), realPath)
	tasks := []domain.Task{}
	for _, inc := range raw.Includes {
		child, childIncluded, err := l.load(inc, dir, fileChain, childRealChain)
		if err != nil {
			return nil, false, err
		}
		if childIncluded {
			tasks = append(tasks, child.tasks...)
		}
	}
	tasks = append(tasks, raw.Tasks...)

	lc = &loadedConfig{
		raw:   &raw,
		tasks: tasks,
	}
	l.loaded[realPath] = lc
	return lc, true, nil
}

// formatIncludeChain renders the include chain from the root file to the
// failing file. paths and realPaths are aligned apparent paths and normalized
// real paths. When they differ (for example because a path contains a
// symlink), the real path is annotated so that cycle detection is auditable.
func formatIncludeChain(paths, realPaths []string) string {
	arr := make([]string, len(paths))
	for i, p := range paths {
		arr[i] = p
		if i < len(realPaths) && realPaths[i] != p {
			arr[i] += " (real path: " + realPaths[i] + ")"
		}
	}
	return strings.Join(arr, " -> ")
}
