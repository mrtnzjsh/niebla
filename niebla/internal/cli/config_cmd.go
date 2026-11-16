package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"niebla/internal/config"
	"niebla/pkg/nieblafs/preflight"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
)

// NewConfigCmd constructs the 'config' command group.
func NewConfigCmd(rootFlags *RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect, initialize, and validate unified toolchain configuration",
		Long:  "Commands to manage the ~/.config/niebla/config.toml file, validate settings, and view active options.",
	}

	cmd.AddCommand(newConfigPathCmd())
	cmd.AddCommand(newConfigShowCmd())
	cmd.AddCommand(newConfigValidateCmd())
	cmd.AddCommand(newConfigInitCmd())

	return cmd
}

func newConfigPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the path to the active configuration file",
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := config.DefaultConfigPath()
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), path)
			if dir, err := config.DefaultConfigDir(); err == nil {
				if files, err := config.DiscoverConfigFiles(dir); err == nil && len(files) > 1 {
					for _, file := range files {
						if file != path {
							fmt.Fprintln(cmd.OutOrStdout(), file)
						}
					}
				}
			}
			return nil
		},
	}
}

func newConfigValidateCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "validate [file]",
		Short: "Validate configuration syntax and semantics (fail fast)",
		Long:  "Parses and validates the active configuration (including all *.toml files in ~/.config/niebla/) or specified file against schema rules, duplicate header checks, port collision checks, and theme presets. Fails fast with descriptive error messages.",
		RunE: func(cmd *cobra.Command, args []string) error {
			targetPath := configPath
			if len(args) > 0 {
				targetPath = args[0]
			}
			if targetPath == "" {
				var err error
				targetPath, err = config.DefaultConfigPath()
				if err != nil {
					return err
				}
			}

			cfg, err := config.LoadFromPath(targetPath)
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "❌ Configuration validation failed:")
				fmt.Fprintln(cmd.ErrOrStderr(), err)
				return err
			}

			theme := config.ResolveTheme(cfg.Critique.Theme)

			fmt.Fprintf(cmd.OutOrStdout(), "✓ Configuration is valid: %s\n", targetPath)
			dir := targetPath
			if info, err := os.Stat(targetPath); err == nil && !info.IsDir() {
				dir = filepath.Dir(targetPath)
			}
			if files, err := config.DiscoverConfigFiles(dir); err == nil && len(files) > 1 {
				var names []string
				for _, file := range files {
					names = append(names, filepath.Base(file))
				}
				fmt.Fprintf(cmd.OutOrStdout(), "  • Combined files   : %v\n", names)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  • Server gRPC      : %s\n", cfg.Core.ServerAddr)
			fmt.Fprintf(cmd.OutOrStdout(), "  • Workspace root   : %s\n", cfg.FS.MountBase)
			fmt.Fprintf(cmd.OutOrStdout(), "  • Critique web UI  : http://%s:%d\n", cfg.Critique.Host, cfg.Critique.Port)
			fmt.Fprintf(cmd.OutOrStdout(), "  • Critique theme   : %s (syntax: %s)\n", theme.Name, theme.SyntaxStyle)
			fmt.Fprintf(cmd.OutOrStdout(), "  • Default reviewer : %s\n", cfg.Critique.DefaultReviewer)
			fuseReport := preflight.VerifyFUSEReadiness()
			fuseStatus := "ready (/dev/fuse, fusermount3 setuid root, user_allow_other)"
			if !fuseReport.Ready {
				fuseStatus = "unavailable (missing /dev/fuse or fusermount3)"
			} else if len(fuseReport.Warnings) > 0 {
				fuseStatus = "warnings detected (check permissions or /etc/fuse.conf)"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  • FUSE readiness   : %s\n", fuseStatus)
			return nil
		},
	}

	cmd.Flags().StringVarP(&configPath, "file", "f", "", "Custom configuration file path to validate")
	return cmd
}

func newConfigShowCmd() *cobra.Command {
	var resolved bool

	cmd := &cobra.Command{
		Use:   "show",
		Short: "Display the active configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := config.DefaultConfigPath()
			if err != nil {
				return err
			}

			if !resolved {
				dir := filepath.Dir(path)
				if files, err := config.DiscoverConfigFiles(dir); err == nil && len(files) > 1 {
					for i, file := range files {
						data, rErr := os.ReadFile(file)
						if rErr != nil {
							continue
						}
						if i > 0 {
							fmt.Fprintln(cmd.OutOrStdout())
						}
						fmt.Fprintf(cmd.OutOrStdout(), "# === %s ===\n", filepath.Base(file))
						fmt.Fprint(cmd.OutOrStdout(), string(data))
					}
					return nil
				}
				// Display raw single file content if available
				data, err := os.ReadFile(path)
				if err == nil {
					fmt.Fprint(cmd.OutOrStdout(), string(data))
					return nil
				}
			}

			// Fallback or explicit resolved view
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			out, err := toml.Marshal(cfg)
			if err != nil {
				return fmt.Errorf("failed to format config: %w", err)
			}
			fmt.Fprint(cmd.OutOrStdout(), string(out))
			return nil
		},
	}

	cmd.Flags().BoolVarP(&resolved, "resolved", "r", false, "Display fully resolved and merged configuration with environment variable expansions")
	return cmd
}

func newConfigInitCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize configuration with baseline defaults",
		Long:  "Creates ~/.config/niebla/config.toml from the embedded canonical base configuration if not present.",
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := config.DefaultConfigPath()
			if err != nil {
				return err
			}

			if _, err := os.Stat(path); err == nil && !force {
				fmt.Fprintf(cmd.OutOrStdout(), "Configuration file already exists at: %s\n(use --force to overwrite with default baseline)\n", path)
				return nil
			}

			dir := filepath.Dir(path)
			if err := os.MkdirAll(dir, 0755); err != nil {
				return fmt.Errorf("failed to create config directory %s: %w", dir, err)
			}

			if err := os.WriteFile(path, config.BaseConfigBytes, 0644); err != nil {
				return fmt.Errorf("failed to write baseline config to %s: %w", path, err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "✓ Initialized configuration file at: %s\n", path)
			return nil
		},
	}

	cmd.Flags().BoolVarP(&force, "force", "f", false, "Overwrite existing configuration with canonical defaults")
	return cmd
}
