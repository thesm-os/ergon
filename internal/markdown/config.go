// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

// Package markdown wraps markdownlint-cli2. The package owns the
// glob list every caller passes to the tool and exposes two
// entry points: [Format] (auto-fix mode, used by `ergon fmt`) and
// [Lint] (reporting-only, used by `ergon lint`).
//
// Both entry points behave the same way when markdownlint-cli2 is
// not on PATH: the call returns nil after emitting a warning on
// stderr pointing at `ergon bootstrap`. Markdown is consistently
// best-effort across the ergon surface; the user installs it once
// during bootstrap and forgets about it.
package markdown

// Config carries the per-repository glob list passed to
// markdownlint-cli2. Pattern syntax matches the tool's own: bare
// patterns select files; entries prefixed with `#` exclude paths.
type Config struct {
	// Globs is the argument list handed to markdownlint-cli2. The
	// defaults from [Defaults] cover the common exclude set; repos
	// with extra carve-outs override or extend the list.
	Globs []string `mapstructure:"globs"`
}

// Defaults returns the Config ergon uses when a repository's
// `.ergon.yaml` does not override the markdown section.
func Defaults() Config {
	return Config{
		Globs: []string{
			"**/*.md",
			"#vendor",
			"#dist",
			"#node_modules",
		},
	}
}
