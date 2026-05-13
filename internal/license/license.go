// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package license

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	xexec "go.thesmos.sh/ergon/internal/exec"
)

// Apply rewrites every Go source file under root in place, adding
// or normalising the SPDX header per cfg.ConfigFile. Wraps the
// `go-license` binary; the user's `bootstrap` is expected to have
// installed it.
func Apply(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	root string, cfg Config,
) error {
	return run(ctx, runner, stdout, stderr, root, cfg, false)
}

// Verify checks every Go source file's header matches the
// cfg.ConfigFile template and returns an error when any file's
// header is missing or stale. Does not modify files. Wraps
// `go-license --verify`.
func Verify(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	root string, cfg Config,
) error {
	return run(ctx, runner, stdout, stderr, root, cfg, true)
}

// run is the shared body of [Apply] and [Verify]: discover the
// Go file list, invoke go-license with the appropriate flag set,
// stream the tool's output to the supplied writers.
func run(
	ctx context.Context, runner xexec.Runner, stdout, stderr io.Writer,
	root string, cfg Config, verify bool,
) error {
	cfg = withDefaults(cfg)
	files, err := goFiles(root, cfg.ExcludeDirs, cfg.ExcludeFiles)
	if err != nil {
		return fmt.Errorf("scan go files: %w", err)
	}
	if len(files) == 0 {
		return nil
	}
	args := make([]string, 0, 2+len(files))
	args = append(args, "--config="+cfg.ConfigFile)
	if verify {
		args = append(args, "--verify")
	}
	args = append(args, files...)
	return runner.Run(ctx,
		xexec.Options{Dir: root, Stdout: stdout, Stderr: stderr},
		"go-license", args...)
}

// withDefaults fills any zero-value field on cfg from [Defaults],
// so callers can pass partial overrides without losing the
// directory or file blocklists.
func withDefaults(cfg Config) Config {
	d := Defaults()
	if cfg.ConfigFile == "" {
		cfg.ConfigFile = d.ConfigFile
	}
	if cfg.ExcludeDirs == nil {
		cfg.ExcludeDirs = d.ExcludeDirs
	}
	if cfg.ExcludeFiles == nil {
		cfg.ExcludeFiles = d.ExcludeFiles
	}
	return cfg
}

// goFiles walks the tree under root and returns the repo-relative
// paths of every Go source file the license tooling should touch.
// Skips directories whose basename appears in excludeDirs and files
// whose basename matches any pattern in excludeFiles.
func goFiles(root string, excludeDirs, excludeFiles []string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := d.Name()
		if d.IsDir() {
			if slices.Contains(excludeDirs, name) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") {
			return nil
		}
		for _, pat := range excludeFiles {
			match, matchErr := filepath.Match(pat, name)
			if matchErr != nil {
				return fmt.Errorf("invalid exclude_files glob %q: %w", pat, matchErr)
			}
			if match {
				return nil
			}
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
