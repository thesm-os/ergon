// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package format implements `ergon fmt`: applies SPDX license
// headers, runs gofumpt + gci per module to format Go sources,
// then runs markdownlint-cli2 across the workspace's Markdown
// files. The orchestration mirrors what the Makefile templates
// converged on.
package format

// Config carries the per-repository overrides for [Run]. The
// defaults provided by [Defaults] follow the patterns the Makefile
// templates used; a repo with project-specific exclusions appends
// to [Config.MarkdownGlobs].
type Config struct {
	// MarkdownGlobs is the argument list handed to
	// markdownlint-cli2. Glob patterns select files; entries
	// prefixed with `#` exclude paths. The defaults cover the
	// common exclude set (vendor, dist, node_modules).
	MarkdownGlobs []string `mapstructure:"markdown_globs"`
}

// Defaults returns the Config ergon uses when a repository's
// `.ergon.yaml` does not override the format section.
func Defaults() Config {
	return Config{
		MarkdownGlobs: []string{
			"**/*.md",
			"#vendor",
			"#dist",
			"#node_modules",
		},
	}
}
