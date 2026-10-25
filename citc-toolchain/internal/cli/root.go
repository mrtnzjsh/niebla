package cli

import (
	"github.com/spf13/cobra"
)

// RootFlags holds global flags for the citc CLI.
type RootFlags struct {
	Server    string
	Workspace string
	ConfigDir string
}

// NewRootCmd constructs the root citc command.
func NewRootCmd() *cobra.Command {
	flags := &RootFlags{}

	rootCmd := &cobra.Command{
		Use:   "citc",
		Short: "CitC Developer Tool - Unified monorepo client",
		Long:  "citc is the developer command-line interface for CitC server-authoritative workspaces.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	rootCmd.PersistentFlags().StringVar(&flags.Server, "server", "", "CitC server gRPC endpoint (precedence: --server, CITC_SERVER, 127.0.0.1:50051)")
	rootCmd.PersistentFlags().StringVar(&flags.Workspace, "workspace", "", "Explicit workspace name (overrides PWD detection)")
	rootCmd.PersistentFlags().StringVar(&flags.ConfigDir, "config-dir", "", "Custom configuration directory for workspace metadata")

	// Register subcommands
	rootCmd.AddCommand(NewWorkspaceCmd(flags))
	rootCmd.AddCommand(NewStatusCmd(flags))
	rootCmd.AddCommand(NewDiffCmd(flags))
	rootCmd.AddCommand(NewShelveCmd(flags))
	rootCmd.AddCommand(NewSubmitCmd(flags))
	rootCmd.AddCommand(NewSkillsCmd(flags))
	rootCmd.AddCommand(NewConfigCmd(flags))
	rootCmd.AddCommand(NewBackupCmd(flags))

	return rootCmd
}
