// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package commitmsg

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRun pins the script-style entry point: reads the file at
// path, runs [Validate], and surfaces success / failure on the
// matching writer.
func TestRun(t *testing.T) {
	t.Parallel()

	t.Run("valid message writes OK to stdout", func(t *testing.T) {
		t.Parallel()
		path := writeTempMsg(t, "feat: add flag\n")
		var stdout, stderr bytes.Buffer
		if err := Run(&stdout, &stderr, path, Defaults()); err != nil {
			t.Fatalf("Run err: %v", err)
		}
		if !strings.Contains(stdout.String(), "OK") {
			t.Fatalf("stdout = %q, want it to contain OK", stdout.String())
		}
	})

	t.Run("invalid message writes the diagnostic to stderr", func(t *testing.T) {
		t.Parallel()
		path := writeTempMsg(t, "no colon here\n")
		var stdout, stderr bytes.Buffer
		err := Run(&stdout, &stderr, path, Defaults())
		if !errors.Is(err, ErrInvalidFormat) {
			t.Fatalf("Run err = %v, want ErrInvalidFormat", err)
		}
		if !strings.Contains(stderr.String(), "commit-msg") {
			t.Fatalf("stderr = %q, want it to mention commit-msg", stderr.String())
		}
	})

	t.Run("missing file surfaces a wrapped read error", func(t *testing.T) {
		t.Parallel()
		var stdout, stderr bytes.Buffer
		err := Run(&stdout, &stderr, filepath.Join(t.TempDir(), "nope"), Defaults())
		if err == nil {
			t.Fatal("Run err = nil for missing file, want non-nil")
		}
	})
}

// writeTempMsg writes body to a temp COMMIT_EDITMSG file and
// returns the path.
func writeTempMsg(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "COMMIT_EDITMSG")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

// TestValidate pins the contract of [Validate]: accepts the
// standard Conventional Commits shape, rejects each documented
// failure with a specific sentinel.
func TestValidate(t *testing.T) {
	t.Parallel()

	t.Run("canonical subject passes", func(t *testing.T) {
		t.Parallel()
		err := Validate("feat: add new flag", Defaults())
		if err != nil {
			t.Fatalf("Validate err: %v", err)
		}
	})

	t.Run("scoped subject passes", func(t *testing.T) {
		t.Parallel()
		err := Validate("fix(parser): handle EOF", Defaults())
		if err != nil {
			t.Fatalf("Validate err: %v", err)
		}
	})

	t.Run("multi-line message validates the first line", func(t *testing.T) {
		t.Parallel()
		body := "feat: add flag\n\nLong-form description goes here."
		if err := Validate(body, Defaults()); err != nil {
			t.Fatalf("Validate err: %v", err)
		}
	})

	t.Run("missing colon is ErrInvalidFormat", func(t *testing.T) {
		t.Parallel()
		err := Validate("feat add flag", Defaults())
		if !errors.Is(err, ErrInvalidFormat) {
			t.Fatalf("err = %v, want ErrInvalidFormat", err)
		}
	})

	t.Run("unknown type is ErrUnknownType", func(t *testing.T) {
		t.Parallel()
		err := Validate("wat: weird type", Defaults())
		if !errors.Is(err, ErrUnknownType) {
			t.Fatalf("err = %v, want ErrUnknownType", err)
		}
	})

	t.Run("over-length subject is ErrSubjectTooLong", func(t *testing.T) {
		t.Parallel()
		long := "feat: " + strings.Repeat("x", 80)
		err := Validate(long, Defaults())
		if !errors.Is(err, ErrSubjectTooLong) {
			t.Fatalf("err = %v, want ErrSubjectTooLong", err)
		}
	})

	t.Run("trailing period is ErrTrailingPeriod", func(t *testing.T) {
		t.Parallel()
		err := Validate("feat: add flag.", Defaults())
		if !errors.Is(err, ErrTrailingPeriod) {
			t.Fatalf("err = %v, want ErrTrailingPeriod", err)
		}
	})

	t.Run("custom types override the defaults", func(t *testing.T) {
		t.Parallel()
		cfg := Config{Types: []string{"wip"}, MaxSubjectLength: 72}
		if err := Validate("wip: trying things", cfg); err != nil {
			t.Fatalf("Validate err: %v, want wip to be accepted", err)
		}
		if err := Validate("feat: add flag", cfg); !errors.Is(err, ErrUnknownType) {
			t.Fatalf("err = %v, want feat to be rejected for this Config", err)
		}
	})

	t.Run("breaking-change marker on bare type passes", func(t *testing.T) {
		t.Parallel()
		if err := Validate("feat!: drop the v0 API", Defaults()); err != nil {
			t.Fatalf("Validate err: %v, want feat! to be accepted", err)
		}
	})

	t.Run("breaking-change marker with scope passes", func(t *testing.T) {
		t.Parallel()
		if err := Validate("refactor(api)!: rename Endpoint to Route", Defaults()); err != nil {
			t.Fatalf("Validate err: %v, want refactor(api)! to be accepted", err)
		}
	})

	t.Run("scope-enum allows listed scopes", func(t *testing.T) {
		t.Parallel()
		cfg := Config{Scopes: []string{"api", "cli"}}
		if err := Validate("feat(api): new endpoint", cfg); err != nil {
			t.Fatalf("Validate err: %v, want api scope to be accepted", err)
		}
	})

	t.Run("scope-enum rejects unlisted scopes", func(t *testing.T) {
		t.Parallel()
		cfg := Config{Scopes: []string{"api", "cli"}}
		err := Validate("feat(nope): something", cfg)
		if !errors.Is(err, ErrUnknownScope) {
			t.Fatalf("err = %v, want ErrUnknownScope", err)
		}
	})

	t.Run("scope-enum still accepts commits without a scope", func(t *testing.T) {
		t.Parallel()
		cfg := Config{Scopes: []string{"api", "cli"}}
		if err := Validate("feat: scopeless", cfg); err != nil {
			t.Fatalf("Validate err: %v, want scopeless to be accepted", err)
		}
	})

	t.Run("empty scopes (default) accepts any scope", func(t *testing.T) {
		t.Parallel()
		if err := Validate("feat(anything-goes): foo", Defaults()); err != nil {
			t.Fatalf("Validate err: %v, want any scope under default config", err)
		}
	})

	t.Run("body without leading blank line fails", func(t *testing.T) {
		t.Parallel()
		msg := "feat: add flag\nthis is body without blank line"
		err := Validate(msg, Defaults())
		if !errors.Is(err, ErrBodyLeadingBlankMissing) {
			t.Fatalf("err = %v, want ErrBodyLeadingBlankMissing", err)
		}
	})

	t.Run("over-length body line fails", func(t *testing.T) {
		t.Parallel()
		long := strings.Repeat("x", 120)
		msg := "feat: add flag\n\n" + long
		err := Validate(msg, Defaults())
		if !errors.Is(err, ErrBodyLineTooLong) {
			t.Fatalf("err = %v, want ErrBodyLineTooLong", err)
		}
	})

	t.Run("body line at exactly the limit passes", func(t *testing.T) {
		t.Parallel()
		atLimit := strings.Repeat("x", 100)
		msg := "feat: add flag\n\n" + atLimit
		if err := Validate(msg, Defaults()); err != nil {
			t.Fatalf("Validate err: %v, want body line of 100 chars to pass", err)
		}
	})

	t.Run("comment lines are stripped before checks run", func(t *testing.T) {
		t.Parallel()
		// git includes a status block as `# ...` lines in
		// COMMIT_EDITMSG. Those lines are long but never enter
		// the recorded message and must not trip body checks.
		msg := "feat: add flag\n\nReal body line.\n# " + strings.Repeat("x", 200)
		if err := Validate(msg, Defaults()); err != nil {
			t.Fatalf("Validate err: %v, want `#` comment lines to be ignored", err)
		}
	})
}

// TestValidateEmptyMessage covers the empty-input branches: an
// empty string and a message that is nothing but comment lines both
// reduce to zero lines and must be rejected rather than indexing
// past the end of the slice.
func TestValidateEmptyMessage(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"", "\n", "# just a comment\n", "#a\n#b\n"} {
		if err := Validate(in, Config{}); err == nil {
			t.Errorf("Validate(%q) = nil, want the empty-message rejection", in)
		} else if !errors.Is(err, ErrInvalidFormat) {
			t.Errorf("Validate(%q) err = %v, want ErrInvalidFormat", in, err)
		}
	}
}

// TestValidateBodyLineLengthDefaults pins the zero-value
// behaviour: a zero BodyMaxLineLength does NOT disable the check,
// it inherits Defaults()'s 100 via withDefaults. There is no
// config path that turns the body-line limit off.
func TestValidateBodyLineLengthDefaults(t *testing.T) {
	t.Parallel()

	cfg := Config{Types: []string{"feat"}, MaxSubjectLength: 72, BodyMaxLineLength: 0}

	long := "feat: subject\n\n" + strings.Repeat("x", 500) + "\n"
	if err := Validate(long, cfg); err == nil {
		t.Fatal("Validate returned nil, want the default 100-byte limit applied")
	} else if !errors.Is(err, ErrBodyLineTooLong) {
		t.Fatalf("Validate err = %v, want ErrBodyLineTooLong", err)
	}

	short := "feat: subject\n\n" + strings.Repeat("x", 99) + "\n"
	if err := Validate(short, cfg); err != nil {
		t.Fatalf("Validate err = %v, want a 99-byte body line accepted", err)
	}
}
