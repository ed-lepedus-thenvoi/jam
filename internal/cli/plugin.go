package cli

import (
	"fmt"
	"io"
	"os/exec"

	"github.com/spf13/cobra"
)

const (
	defaultMarketplaceRepo = "ed-lepedus-thenvoi/jam-marketplace"
	defaultPluginName      = "band-peer"
	defaultMarketplaceName = "jam-marketplace"
)

func newPluginCmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Install, update, and uninstall jam's Claude Code plugins",
	}
	cmd.AddCommand(newPluginInstallCmd(stdout, stderr))
	cmd.AddCommand(newPluginUpdateCmd(stdout, stderr))
	cmd.AddCommand(newPluginUninstallCmd(stdout, stderr))
	return cmd
}

// runClaude shells out to the `claude` CLI. Reused by install/update/uninstall
// so they all surface the same "claude not installed" error and stream
// stdout/stderr the same way.
func runClaude(stdout, stderr io.Writer, args ...string) error {
	if _, err := exec.LookPath("claude"); err != nil {
		return fmt.Errorf("`claude` CLI not on $PATH; install Claude Code first (https://claude.com/claude-code)")
	}
	cmd := exec.Command("claude", args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	fmt.Fprintf(stdout, "$ claude %s\n", joinArgs(args))
	return cmd.Run()
}

func joinArgs(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}

func newPluginInstallCmd(stdout, stderr io.Writer) *cobra.Command {
	var marketplace, plugin string
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the band-peer Claude Code plugin (and add its marketplace)",
		Long: "Convenience wrapper that runs `claude plugin marketplace add <repo>` followed by " +
			"`claude plugin install <plugin>@<marketplace-name>`. Restart Claude Code after to " +
			"pick up the new plugin.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := runClaude(stdout, stderr, "plugin", "marketplace", "add", marketplace); err != nil {
				return fmt.Errorf("claude plugin marketplace add: %w", err)
			}
			spec := fmt.Sprintf("%s@%s", plugin, defaultMarketplaceName)
			if err := runClaude(stdout, stderr, "plugin", "install", spec); err != nil {
				return fmt.Errorf("claude plugin install: %w", err)
			}
			fmt.Fprintf(stdout, "\nDone. Restart Claude Code, then use /band-peer (or 'let's jam with @<handle>') in any session.\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&marketplace, "marketplace", defaultMarketplaceRepo,
		"Marketplace source (GitHub repo, URL, or local path)")
	cmd.Flags().StringVar(&plugin, "plugin", defaultPluginName,
		"Plugin name within the marketplace to install")
	return cmd
}

func newPluginUpdateCmd(stdout, stderr io.Writer) *cobra.Command {
	var plugin string
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update the installed band-peer plugin to the latest version",
		Long: "Runs `claude plugin update <plugin>`. The marketplace is refreshed automatically " +
			"by Claude Code as part of the update. Restart Claude Code after to apply.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := runClaude(stdout, stderr, "plugin", "update", plugin); err != nil {
				return fmt.Errorf("claude plugin update: %w", err)
			}
			fmt.Fprintf(stdout, "\nDone. Restart Claude Code to apply.\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&plugin, "plugin", defaultPluginName, "Plugin name to update")
	return cmd
}

func newPluginUninstallCmd(stdout, stderr io.Writer) *cobra.Command {
	var plugin string
	var pruneMarketplace bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Uninstall the band-peer plugin",
		Long: "Runs `claude plugin uninstall <plugin>`. Pass --prune-marketplace to also remove " +
			"the jam-marketplace registration (only meaningful if you don't plan to reinstall).",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := runClaude(stdout, stderr, "plugin", "uninstall", plugin); err != nil {
				return fmt.Errorf("claude plugin uninstall: %w", err)
			}
			if pruneMarketplace {
				if err := runClaude(stdout, stderr, "plugin", "marketplace", "remove", defaultMarketplaceName); err != nil {
					fmt.Fprintf(stderr, "warning: removing marketplace failed: %v\n", err)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&plugin, "plugin", defaultPluginName, "Plugin name to uninstall")
	cmd.Flags().BoolVar(&pruneMarketplace, "prune-marketplace", false,
		"Also remove the jam-marketplace registration after uninstall")
	return cmd
}
