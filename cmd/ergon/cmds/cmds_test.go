// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"go.thesmos.sh/ergon/internal/checks/coverage"
	xexec "go.thesmos.sh/ergon/internal/exec"
	"go.thesmos.sh/ergon/internal/release"
	"go.thesmos.sh/ergon/internal/scaffold"
)

// TestCommandTree pins the command surface the README documents.
// The tree is assembled from ~40 independent init() functions, so
// a subcommand registered against the wrong parent (or dropped in
// a refactor) is otherwise invisible until a user types it.
func TestCommandTree(t *testing.T) {
	// Not parallel: cobra's Commands() lazily sorts and caches
	// (c.commandsAreSorted = true), so walking the shared global
	// rootCmd is a WRITE, not a read. Any two of these running
	// concurrently race — as does InitDefaultHelpCmd, which grafts
	// a command onto the tree.

	// Mirrors the "Command tree" table in README.md.
	want := map[string][]string{
		"": {
			"bootstrap", "init", "clean", "fmt", "license", "generate",
			"build", "release", "doctor", "lint", "mod", "test", "bench",
			"check", "completion",
		},
		"lint":    {"vet", "go", "md", "license", "skip-expiry", "error-prefix", "vuln"},
		"mod":     {"list", "install", "tidy", "verify"},
		"test":    {"race", "bench", "fuzz", "coverage"},
		"bench":   {"baseline", "regression", "profile"},
		"check":   {"coverage", "mutation", "branch", "commit-msg"},
		"release": {"init"},
		// `uncovered` hangs off `check coverage`, not `check` — it
		// reads the profiles that stage writes.
		"check coverage": {"uncovered"},
	}

	for parent, children := range want {
		cmd := rootCmd
		for seg := range strings.FieldsSeq(parent) {
			cmd = findSubcommand(t, cmd, seg)
		}
		for _, child := range children {
			if findOptional(cmd, child) == nil {
				t.Errorf("command %q has no subcommand %q", cmdPath(cmd), child)
			}
		}
	}
}

// TestEveryCommandHasRunnableSurface asserts each leaf command is
// either runnable or a pure group with children. A command with
// neither RunE nor subcommands prints usage and exits 0, which
// reads as success to a CI script.
func TestEveryCommandHasRunnableSurface(t *testing.T) {
	// Not parallel: cobra's Commands() lazily sorts and caches
	// (c.commandsAreSorted = true), so walking the shared global
	// rootCmd is a WRITE, not a read. Any two of these running
	// concurrently race — as does InitDefaultHelpCmd, which grafts
	// a command onto the tree.
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c.RunE == nil && c.Run == nil && !c.HasSubCommands() {
			t.Errorf("command %q has neither a run function nor subcommands", cmdPath(c))
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(rootCmd)
}

// TestCommandsDocumented asserts every command carries Short help.
// `ergon help` renders Short per line; an empty one leaves a blank
// entry in the listing.
func TestCommandsDocumented(t *testing.T) {
	// Not parallel: cobra's Commands() lazily sorts and caches
	// (c.commandsAreSorted = true), so walking the shared global
	// rootCmd is a WRITE, not a read. Any two of these running
	// concurrently race — as does InitDefaultHelpCmd, which grafts
	// a command onto the tree.
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Short == "" && c.Name() != "help" {
			t.Errorf("command %q has no Short description", cmdPath(c))
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(rootCmd)
}

// TestStageOpts covers the root-flag → stage.Options translation.
// The globals are process-wide, so this test cannot run in
// parallel with anything else that reads them.
func TestStageOpts(t *testing.T) {
	origFast, origVerbose := fastMode, verboseMode
	t.Cleanup(func() { fastMode, verboseMode = origFast, origVerbose })

	for _, tc := range []struct{ fast, verbose bool }{
		{false, false}, {true, false}, {false, true}, {true, true},
	} {
		fastMode, verboseMode = tc.fast, tc.verbose
		got := stageOpts()
		if got.Fast != tc.fast || got.Verbose != tc.verbose {
			t.Errorf("stageOpts() with (fast=%v, verbose=%v) = %+v", tc.fast, tc.verbose, got)
		}
	}
}

// TestAnyRequiresBranch covers the opt-in signal that joins the
// slow gobco pass to the `ergon check` umbrella.
func TestAnyRequiresBranch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		layers []coverage.Layer
		want   bool
	}{
		{"nil layers", nil, false},
		{"empty layers", []coverage.Layer{}, false},
		{"none required", []coverage.Layer{{Path: "a"}, {Path: "b"}}, false},
		{"first required", []coverage.Layer{{Path: "a", RequireBranch: true}, {Path: "b"}}, true},
		{"last required", []coverage.Layer{{Path: "a"}, {Path: "b", RequireBranch: true}}, true},
		{"all required", []coverage.Layer{{Path: "a", RequireBranch: true}}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := anyRequiresBranch(tc.layers); got != tc.want {
				t.Errorf("anyRequiresBranch(%+v) = %v, want %v", tc.layers, got, tc.want)
			}
		})
	}
}

// TestSplitOwnerRepo covers the `--homebrew owner/repo` parser.
func TestSplitOwnerRepo(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in          string
		owner, repo string
		ok          bool
	}{
		{"thesm-os/homebrew-tap", "thesm-os", "homebrew-tap", true},
		{"a/b", "a", "b", true},
		{"", "", "", false},
		{"noslash", "", "", false},
		{"/leading", "", "", false},
		{"trailing/", "", "", false},
		{"/", "", "", false},
		{"a/b/c", "", "", false},
		{"a//b", "", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			owner, repo, ok := splitOwnerRepo(tc.in)
			if owner != tc.owner || repo != tc.repo || ok != tc.ok {
				t.Errorf("splitOwnerRepo(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tc.in, owner, repo, ok, tc.owner, tc.repo, tc.ok)
			}
		})
	}
}

// TestTestOverride covers the flag + positional-pattern bundle the
// test-family commands share.
func TestTestOverride(t *testing.T) {
	orig := testFlags
	t.Cleanup(func() { testFlags = orig })

	testFlags.count, testFlags.cpu, testFlags.timeout = 3, 4, 90*time.Second

	t.Run("no positional leaves the pattern empty", func(t *testing.T) {
		got := testOverride(nil)
		if got.Pattern != "" {
			t.Errorf("Pattern = %q, want empty", got.Pattern)
		}
		if got.Count != 3 || got.CPU != 4 || got.Timeout != 90*time.Second {
			t.Errorf("override = %+v, want the flag values threaded through", got)
		}
	})

	t.Run("one positional becomes the run pattern", func(t *testing.T) {
		if got := testOverride([]string{"TestFoo"}); got.Pattern != "TestFoo" {
			t.Errorf("Pattern = %q, want TestFoo", got.Pattern)
		}
	})

	t.Run("extra positionals are ignored", func(t *testing.T) {
		// cobra's Args validator rejects these before RunE, so the
		// defensive branch must not panic or mis-assign.
		if got := testOverride([]string{"A", "B"}); got.Pattern != "" {
			t.Errorf("Pattern = %q, want empty for an out-of-contract arg count", got.Pattern)
		}
	})
}

// TestGitFilesFor covers the memoising resolver both lint stages
// share: the point of the helper is that `git ls-files` runs once
// per invocation no matter how many stages ask for the list.
func TestGitFilesFor(t *testing.T) {
	t.Parallel()

	t.Run("resolves once and caches the result", func(t *testing.T) {
		t.Parallel()
		// `git ls-files -z` emits NUL-separated names, and GitFiles
		// filters on the `.go` suffix — the fixture has to match both
		// or the resolver silently yields nothing.
		runner := &fakeCmdRunner{stdout: "a.go\x00b.go\x00README.md\x00"}
		resolve := gitFilesFor(t.Context(), runner, t.TempDir())

		first, err := resolve()
		if err != nil {
			t.Fatalf("first call: %v", err)
		}
		second, err := resolve()
		if err != nil {
			t.Fatalf("second call: %v", err)
		}
		if len(first) != 2 || len(second) != 2 {
			t.Fatalf("files = %v / %v, want two entries each", first, second)
		}
		if runner.calls != 1 {
			t.Fatalf("git invoked %d times, want exactly one (result must be cached)", runner.calls)
		}
	})

	t.Run("caches the error too", func(t *testing.T) {
		t.Parallel()
		runner := &fakeCmdRunner{err: errors.New("not a git repository")}
		resolve := gitFilesFor(t.Context(), runner, t.TempDir())

		if _, err := resolve(); err == nil {
			t.Fatal("first call returned nil, want the git error")
		}
		if _, err := resolve(); err == nil {
			t.Fatal("second call returned nil, want the cached git error")
		}
		if runner.calls != 1 {
			t.Fatalf("git invoked %d times, want one — a failure must not be retried", runner.calls)
		}
	})
}

// TestResolveReleaseInitVars covers the `ergon release init` flag
// normalisation, including the defaults applied when --name and
// --main are omitted.
func TestResolveReleaseInitVars(t *testing.T) {
	t.Parallel()

	newRepo := func(t *testing.T) string {
		t.Helper()
		dest := t.TempDir()
		writeFile(t, filepath.Join(dest, "go.mod"), "module example.test/proj\n\ngo 1.26\n")
		return dest
	}

	t.Run("defaults name to the destination basename", func(t *testing.T) {
		t.Parallel()
		dest := newRepo(t)
		vars, err := resolveReleaseInitVars(dest, "", nil, false, "", "")
		if err != nil {
			t.Fatalf("resolveReleaseInitVars: %v", err)
		}
		if vars.Name != filepath.Base(dest) {
			t.Errorf("Name = %q, want the dest basename %q", vars.Name, filepath.Base(dest))
		}
		if len(vars.Builds) != 1 {
			t.Fatalf("Builds = %+v, want one default build", vars.Builds)
		}
		if want := "./cmd/" + filepath.Base(dest); vars.Builds[0].MainPath != want {
			t.Errorf("MainPath = %q, want %q", vars.Builds[0].MainPath, want)
		}
	})

	t.Run("explicit name overrides the basename", func(t *testing.T) {
		t.Parallel()
		vars, err := resolveReleaseInitVars(newRepo(t), "ergon", []string{"./cmd/ergon"}, false, "", "")
		if err != nil {
			t.Fatalf("resolveReleaseInitVars: %v", err)
		}
		if vars.Name != "ergon" {
			t.Errorf("Name = %q, want ergon", vars.Name)
		}
	})

	t.Run("multiple --main produce one build each", func(t *testing.T) {
		t.Parallel()
		vars, err := resolveReleaseInitVars(
			newRepo(t), "proj", []string{"./cmd/foo", "./cmd/bar"}, false, "", "")
		if err != nil {
			t.Fatalf("resolveReleaseInitVars: %v", err)
		}
		if len(vars.Builds) != 2 {
			t.Fatalf("Builds = %+v, want two", vars.Builds)
		}
		if vars.Builds[0].BinaryName != "foo" || vars.Builds[1].BinaryName != "bar" {
			t.Errorf("binaries = (%q, %q), want (foo, bar)",
				vars.Builds[0].BinaryName, vars.Builds[1].BinaryName)
		}
	})

	t.Run("upx flag passes through", func(t *testing.T) {
		t.Parallel()
		vars, err := resolveReleaseInitVars(newRepo(t), "proj", nil, true, "", "")
		if err != nil {
			t.Fatalf("resolveReleaseInitVars: %v", err)
		}
		if !vars.UPX {
			t.Error("UPX = false, want true")
		}
	})

	t.Run("homebrew owner/repo splits into tap fields", func(t *testing.T) {
		t.Parallel()
		vars, err := resolveReleaseInitVars(
			newRepo(t), "proj", nil, false, "thesm-os/homebrew-tap", "")
		if err != nil {
			t.Fatalf("resolveReleaseInitVars: %v", err)
		}
		if !vars.Homebrew {
			t.Error("Homebrew = false, want true")
		}
		if vars.HomebrewTapOwner != "thesm-os" || vars.HomebrewTapName != "homebrew-tap" {
			t.Errorf("tap = (%q, %q), want (thesm-os, homebrew-tap)",
				vars.HomebrewTapOwner, vars.HomebrewTapName)
		}
	})

	t.Run("malformed homebrew value is a usage error", func(t *testing.T) {
		t.Parallel()
		_, err := resolveReleaseInitVars(newRepo(t), "proj", nil, false, "no-slash", "")
		if err == nil {
			t.Fatal("resolveReleaseInitVars returned nil, want a usage error")
		}
		if !strings.Contains(err.Error(), "owner/repo") {
			t.Errorf("err = %v, want it to name the expected shape", err)
		}
	})

	t.Run("docker prefix derives the registry", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct{ prefix, registry string }{
			{"ghcr.io/thesm-os", "ghcr.io"},
			{"docker.io/thesmos", "docker.io"},
			{"thesmos", "docker.io"},
			{"localhost:5000/thesmos", "localhost:5000"},
		} {
			vars, err := resolveReleaseInitVars(newRepo(t), "proj", nil, false, "", tc.prefix)
			if err != nil {
				t.Fatalf("resolveReleaseInitVars(%q): %v", tc.prefix, err)
			}
			if !vars.Docker {
				t.Errorf("%q: Docker = false, want true", tc.prefix)
			}
			if vars.DockerPrefix != tc.prefix {
				t.Errorf("%q: DockerPrefix = %q, want it passed through verbatim",
					tc.prefix, vars.DockerPrefix)
			}
			if vars.DockerRegistry != tc.registry {
				t.Errorf("%q: DockerRegistry = %q, want %q",
					tc.prefix, vars.DockerRegistry, tc.registry)
			}
		}
	})

	t.Run("a --main with no enclosing go.mod errors", func(t *testing.T) {
		t.Parallel()
		// No go.mod written anywhere, so the upward walk finds nothing.
		dest := t.TempDir()
		if _, err := resolveReleaseInitVars(dest, "proj", []string{"./cmd/proj"}, false, "", ""); err == nil {
			t.Fatal("resolveReleaseInitVars returned nil, want ErrNoEnclosingModule")
		}
	})
}

// findSubcommand locates a direct subcommand by name, failing the
// test when it is absent.
func findSubcommand(t *testing.T, parent *cobra.Command, name string) *cobra.Command {
	t.Helper()
	if c := findOptional(parent, name); c != nil {
		return c
	}
	t.Fatalf("command %q has no subcommand %q", cmdPath(parent), name)
	return nil
}

// findOptional locates a direct subcommand by name, returning nil
// when it is absent.
func findOptional(parent *cobra.Command, name string) *cobra.Command {
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}

// cmdPath renders a command's full path for error messages.
func cmdPath(c *cobra.Command) string {
	if c.Name() == "" {
		return "<root>"
	}
	return c.CommandPath()
}

// writeFile writes body to path, creating parent directories.
func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// fakeCmdRunner satisfies [xexec.Runner] for the command-layer
// tests: it counts invocations, writes stdout, and returns err.
type fakeCmdRunner struct {
	calls  int
	stdout string
	err    error
}

func (f *fakeCmdRunner) Run(
	_ context.Context, opts xexec.Options, _ string, _ ...string,
) error {
	f.calls++
	if opts.Stdout != nil {
		_, _ = opts.Stdout.Write([]byte(f.stdout))
	}
	return f.err
}

func (*fakeCmdRunner) LookPath(name string) (string, error) {
	return "/usr/local/bin/" + name, nil
}

// commandLine matches a line that actually invokes the binary: a
// YAML `run:` / `entry:` step, or a Makefile recipe line (leading
// tab, possibly via the $(ERGON) indirection). Restricting to these
// keeps prose in comments ("ergon recognises ...") out of the scan.
var commandLine = regexp.MustCompile(`^\s*(?:-\s*)?(?:run|entry):\s*(.+)$|^\t\s*(.+)$`)

// ergonCall matches `ergon` (or the $(ERGON) / $(ERGON_BIN) macro)
// followed by its subcommand path ON THE SAME LINE. `[^\S\n]`
// rather than `\s` is deliberate: `\s` spans newlines, which makes
// a trailing `ergon` on one line swallow the first word of the
// next and invents invocations nobody wrote.
var ergonCall = regexp.MustCompile(
	`(?:\bergon|\$\(ERGON[A-Z_]*\))((?:[^\S\n]+[a-z][a-z0-9-]*)*)`)

// TestScaffoldedCommandsResolve renders the `ergon init` file set
// and asserts every `ergon <subcommand>` line it emits resolves to
// a real command in the tree.
//
// Regression: the scaffolded CI workflow ran `ergon check vuln`,
// but `vuln` is a stage of the `lint` umbrella, not `check`. Every
// repository created by `ergon init` therefore shipped a CI job
// that failed immediately with `unknown command "vuln" for "ergon
// check"`. Nothing connected the template text to the command tree,
// so a template could name a command that never existed.
func TestScaffoldedCommandsResolve(t *testing.T) {
	// Not parallel: cobra's Commands() lazily sorts and caches
	// (c.commandsAreSorted = true), so walking the shared global
	// rootCmd is a WRITE, not a read. Any two of these running
	// concurrently race — as does InitDefaultHelpCmd, which grafts
	// a command onto the tree.

	dest := t.TempDir()
	if err := scaffold.Run(io.Discard, dest, scaffold.Vars{
		Name: "proj", Module: "example.test/proj",
	}, false); err != nil {
		t.Fatalf("scaffold.Run: %v", err)
	}
	// `ergon release init` ships a second template set into the same
	// tree. It carries no ergon run-steps today, but it writes a CI
	// workflow — so it is scanned here rather than left as the one
	// scaffold path where a bad command name could ship unnoticed.
	// The release scaffold resolves each build to its enclosing
	// module; `ergon init` does not write a go.mod, so supply one.
	writeFile(t, filepath.Join(dest, "go.mod"), "module example.test/proj\n\ngo 1.26\n")
	spec, err := release.ResolveBuildSpec(dest, "./cmd/proj")
	if err != nil {
		t.Fatalf("ResolveBuildSpec: %v", err)
	}
	if err := release.Scaffold(io.Discard, dest, release.ScaffoldVars{
		Name: "proj", Builds: []release.BuildSpec{spec},
	}, false); err != nil {
		t.Fatalf("release.Scaffold: %v", err)
	}

	checked := 0
	walkErr := filepath.WalkDir(dest, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, _ := filepath.Rel(dest, path)
		for line := range strings.SplitSeq(string(body), "\n") {
			m := commandLine.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			cmdText := m[1] + m[2]
			for _, call := range ergonCall.FindAllStringSubmatch(cmdText, -1) {
				words := strings.Fields(call[1])
				if len(words) == 0 {
					continue
				}
				checked++
				if bad, ok := resolveCommandPath(words); !ok {
					t.Errorf("%s: `ergon %s` does not resolve — no subcommand %q",
						rel, strings.Join(words, " "), bad)
				}
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk: %v", walkErr)
	}
	if checked == 0 {
		t.Fatal("no ergon invocations found in the scaffolded output; the scan is not working")
	}
	t.Logf("validated %d scaffolded ergon invocations", checked)
}

// TestScaffoldedCommandsResolveCatchesTheRegression proves the
// check above fails on the shape that actually shipped, rather than
// passing vacuously.
func TestScaffoldedCommandsResolveCatchesTheRegression(t *testing.T) {
	// Not parallel: cobra's Commands() lazily sorts and caches
	// (c.commandsAreSorted = true), so walking the shared global
	// rootCmd is a WRITE, not a read. Any two of these running
	// concurrently race — as does InitDefaultHelpCmd, which grafts
	// a command onto the tree.
	if bad, ok := resolveCommandPath([]string{"check", "vuln"}); ok {
		t.Error("`ergon check vuln` resolved, but vuln is a stage of lint")
	} else if bad != "vuln" {
		t.Errorf("unresolved token = %q, want vuln", bad)
	}
	if _, ok := resolveCommandPath([]string{"lint", "vuln"}); !ok {
		t.Error("`ergon lint vuln` does not resolve")
	}
}

// resolveCommandPath walks words down the command tree. Returns the
// first token that names neither a subcommand nor a legal
// positional argument, or ok=true when the whole path resolves.
//
// A token that is not a subcommand is accepted when the command
// reached so far takes positionals — `ergon test TestFoo` and
// `ergon check mutation internal/checks` are both valid, and the
// trailing words are arguments rather than commands.
func resolveCommandPath(words []string) (unresolved string, ok bool) {
	cur := rootCmd
	cur.InitDefaultHelpCmd() // `help` is registered lazily by cobra
	for _, w := range words {
		if next := findOptional(cur, w); next != nil {
			cur = next
			continue
		}
		if acceptsArgs(cur) {
			return "", true
		}
		return w, false
	}
	return "", true
}

// acceptsArgs reports whether c tolerates a positional argument, by
// probing its Args validator. A nil validator on a command with
// subcommands means "subcommands only".
func acceptsArgs(c *cobra.Command) bool {
	if c.Args == nil {
		return !c.HasSubCommands()
	}
	return c.Args(c, []string{"probe"}) == nil
}
