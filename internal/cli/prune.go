package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/thenvoi/jam/internal/band"
	"github.com/thenvoi/jam/internal/config"
	"github.com/thenvoi/jam/internal/session"
)

func newAgentPruneCmd(stdout, stderr io.Writer, env Env) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Delete orphan agents whose daemons are no longer running",
		Long: "Walks all session state files under ~/.config/jam/sessions/<profile>/ and " +
			"force-deletes the corresponding Band agent for any session whose PID is dead. " +
			"This is the recovery path for crashed daemons or sessions stopped without " +
			"`jam daemon stop`. Use --dry-run to preview before deleting.",
		RunE: func(cmd *cobra.Command, args []string) error {
			states, err := session.ListAll(env.HomeDir)
			if err != nil {
				return fmt.Errorf("listing sessions: %w", err)
			}
			// Cache per-profile configs so we hit Load once per profile.
			cfgCache := map[string]*config.Config{}
			loadCfg := func(profile string) (*config.Config, error) {
				if c, ok := cfgCache[profile]; ok {
					return c, nil
				}
				c, err := config.Load(env.HomeDir, profile)
				if err != nil {
					return nil, err
				}
				cfgCache[profile] = c
				return c, nil
			}

			var pruned, kept int
			for _, st := range states {
				if processAlive(st.PID) {
					kept++
					continue
				}
				if dryRun {
					fmt.Fprintf(stdout, "would prune %s (agent %s, pid %d, profile %s)\n",
						st.Handle, st.AgentID, st.PID, st.Profile)
					pruned++
					continue
				}
				cfg, err := loadCfg(st.Profile)
				if err != nil {
					fmt.Fprintf(stderr, "warning: cannot load config for profile %q (skipping %s): %v\n",
						st.Profile, st.AgentID, err)
					continue
				}
				if err := band.New(cfg.BaseURL, cfg.UserAPIKey).DeleteAgent(st.AgentID, true); err != nil {
					fmt.Fprintf(stderr, "warning: DELETE %s failed: %v\n", st.AgentID, err)
					continue
				}
				if err := session.Remove(env.HomeDir, st.Profile, st.Scope); err != nil {
					fmt.Fprintf(stderr, "warning: removing state file for %s failed: %v\n", st.AgentID, err)
					continue
				}
				fmt.Fprintf(stdout, "pruned %s (agent %s)\n", st.Handle, st.AgentID)
				pruned++
			}
			fmt.Fprintf(stdout, "Done: pruned %d, kept %d alive.\n", pruned, kept)
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Report what would be pruned without deleting")
	return cmd
}
