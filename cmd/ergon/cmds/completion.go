// Copyright Thesmos B.V. 2026
// SPDX-License-Identifier: MIT

package cmds

import (
	"fmt"

	"github.com/spf13/cobra"
)

// completionCmd is `ergon completion <shell>`. Emits the shell
// completion script cobra generates for the user's shell on
// stdout; the user is expected to source it from the appropriate
// rc file. Cobra owns the actual script generation; this file is
// the cobra wiring plus the per-shell installation hint.
//
// The four supported shells track cobra's built-in surface:
// bash, zsh, fish, and PowerShell.
var completionCmd = &cobra.Command{
	Use:   "completion [bash|zsh|fish|powershell]",
	Short: "Print shell-completion script for the given shell",
	Long: "Prints the shell-completion script for the named shell on " +
		"stdout. Source the output from your shell's rc file:\n\n" +
		"  bash       ergon completion bash       > /etc/bash_completion.d/ergon\n" +
		"  zsh        ergon completion zsh        > \"${fpath[1]}/_ergon\"\n" +
		"  fish       ergon completion fish       > ~/.config/fish/completions/ergon.fish\n" +
		"  powershell ergon completion powershell > ergon.ps1\n\n" +
		"After installing, restart the shell to pick up the script.",
	DisableFlagsInUseLine: true,
	ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
	Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		switch args[0] {
		case "bash":
			return rootCmd.GenBashCompletionV2(out, true)
		case "zsh":
			return rootCmd.GenZshCompletion(out)
		case "fish":
			return rootCmd.GenFishCompletion(out, true)
		case "powershell":
			return rootCmd.GenPowerShellCompletionWithDesc(out)
		default:
			return fmt.Errorf("completion: unsupported shell %q", args[0])
		}
	},
}

func init() {
	rootCmd.AddCommand(completionCmd)
}
