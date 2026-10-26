package cli

import (
	"github.com/spf13/cobra"
)

// RootFlags holds global flags for the niebla CLI.
type RootFlags struct {
	Server    string
	Workspace string
	ConfigDir string
}

// NewRootCmd constructs the root niebla command.
func NewRootCmd() *cobra.Command {
	flags := &RootFlags{}

	rootCmd := &cobra.Command{
		Use:           "niebla",
		Short:         "Niebla Developer Tool - Unified monorepo client",
		Long:          "niebla is the developer command-line interface for Niebla server-authoritative workspaces.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	rootCmd.PersistentFlags().StringVar(&flags.Server, "server", "", "Niebla server gRPC endpoint (precedence: --server, NIEBLA_SERVER, 127.0.0.1:50051)")
	rootCmd.PersistentFlags().StringVar(&flags.Workspace, "workspace", "", "Explicit workspace name (overrides PWD detection)")
	rootCmd.PersistentFlags().StringVar(&flags.ConfigDir, "config-dir", "", "Custom configuration directory for workspace metadata")

	// Register subcommands
	rootCmd.AddCommand(NewWorkspaceCmd(flags))
	rootCmd.AddCommand(NewStatusCmd(flags))
	rootCmd.AddCommand(NewDiffCmd(flags))
	rootCmd.AddCommand(NewShelveCmd(flags))
	rootCmd.AddCommand(NewSubmitCmd(flags))
	rootCmd.AddCommand(NewSyncCmd(flags))
	rootCmd.AddCommand(NewSkillsCmd(flags))
	rootCmd.AddCommand(NewConfigCmd(flags))
	rootCmd.AddCommand(NewBackupCmd(flags))

	return rootCmd
}
