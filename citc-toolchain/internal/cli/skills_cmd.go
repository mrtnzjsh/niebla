package cli

import (
	"fmt"
	"io"
	"text/tabwriter"

	"citc-toolchain/internal/cli/skills"

	"github.com/spf13/cobra"
)

// NewSkillsCmd constructs the 'skills' command group.
func NewSkillsCmd(rootFlags *RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Manage AI agent skills for developer harnesses",
		Long:  "Commands to install, synchronize, and inspect CitC AI agent skills across harnesses (Antigravity, Claude Code, Universal .agents, Cursor).",
	}

	cmd.AddCommand(newSkillsSyncCmd(rootFlags))
	cmd.AddCommand(newSkillsStatusCmd(rootFlags))
	cmd.AddCommand(newSkillsExportCmd(rootFlags))

	return cmd
}

func newSkillsSyncCmd(rootFlags *RootFlags) *cobra.Command {
	var proactive bool
	var customHub string

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Synchronize Hub and harness spoke symlinks",
		Long:  "Ensures the canonical Hub skill directory is populated and creates/repairs symlinks to all installed agent harnesses.",
		RunE: func(cmd *cobra.Command, args []string) error {
			hubDir, err := skills.EnsureHub(customHub)
			if err != nil {
				return fmt.Errorf("failed to ensure hub: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Hub populated at: %s\n\n", hubDir)

			statuses, err := skills.SyncSpokes(hubDir, proactive, "")
			if err != nil {
				return fmt.Errorf("failed to sync spokes: %w", err)
			}

			renderSpokeTable(cmd.OutOrStdout(), statuses)
			return nil
		},
	}

	cmd.Flags().BoolVarP(&proactive, "proactive", "p", true, "Pre-create configuration directories for all supported harnesses")
	cmd.Flags().StringVar(&customHub, "hub", "", "Custom Hub directory (defaults to ~/.local/share/citc/skills/citc)")

	return cmd
}

func newSkillsStatusCmd(rootFlags *RootFlags) *cobra.Command {
	var customHub string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Check Hub and spoke status across agent harnesses",
		RunE: func(cmd *cobra.Command, args []string) error {
			hubDir := customHub
			if hubDir == "" {
				var err error
				hubDir, err = skills.DefaultHubDir()
				if err != nil {
					return err
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Target Hub: %s\n\n", hubDir)

			statuses, err := skills.CheckSpokes(hubDir, "")
			if err != nil {
				return fmt.Errorf("failed to check spokes: %w", err)
			}

			renderSpokeTable(cmd.OutOrStdout(), statuses)
			return nil
		},
	}

	cmd.Flags().StringVar(&customHub, "hub", "", "Custom Hub directory")

	return cmd
}

func newSkillsExportCmd(rootFlags *RootFlags) *cobra.Command {
	var exportDir string

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export embedded skill assets to a target directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			if exportDir == "" {
				exportDir = "./citc-skills"
			}
			if err := skills.ExtractEmbeddedSkills(exportDir); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Exported skills to: %s\n", exportDir)
			return nil
		},
	}

	cmd.Flags().StringVarP(&exportDir, "dir", "d", "", "Directory to export skills into (defaults to ./citc-skills)")

	return cmd
}

func renderSpokeTable(out io.Writer, statuses []skills.HarnessStatus) {
	tw := tabwriter.NewWriter(out, 0, 8, 4, ' ', 0)
	fmt.Fprintln(tw, "HARNESS\tSTATUS\tSPOKE PATH\tDETAILS")

	for _, s := range statuses {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.Name, s.Status, s.SpokePath, s.Message)
	}

	_ = tw.Flush()
}
