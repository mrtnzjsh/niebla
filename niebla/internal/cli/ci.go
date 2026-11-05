package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"niebla/pkg/ci"
	"niebla/pkg/types"

	"github.com/spf13/cobra"
)

// NewCICmd constructs the `niebla ci` parent command.
func NewCICmd(flags *RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ci",
		Short: "Manage and execute CI runner jobs and report check run status",
		Long:  "niebla ci provides tools for CI runners to execute commands and report build/test statuses back to Cotejo and niebla-server.",
	}

	cmd.AddCommand(NewCIExecCmd(flags))
	cmd.AddCommand(NewCIReportCmd(flags))

	return cmd
}

// NewCIReportCmd implements `niebla ci report`.
func NewCIReportCmd(flags *RootFlags) *cobra.Command {
	var clID int64
	var patchset int
	var runnerName string
	var provider string
	var status string
	var externalURL string
	var logHash string
	var logFile string
	var serverURL string

	cmd := &cobra.Command{
		Use:   "report",
		Short: "Report a check run status directly to Cotejo / Niebla Server",
		Long:  "Report status (queued, running, passed, failed, canceled, skipped) and optional log blob to the Check Runs API.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if clID <= 0 {
				return fmt.Errorf("--cl is required and must be positive")
			}
			if runnerName == "" {
				return fmt.Errorf("--runner is required")
			}
			if status == "" {
				return fmt.Errorf("--status is required")
			}
			if serverURL == "" {
				serverURL = os.Getenv("NIEBLA_SERVER_URL")
				if serverURL == "" {
					serverURL = "http://localhost:8080"
				}
			}

			var logHashHex string = strings.TrimSpace(logHash)
			if logFile != "" && logHashHex == "" {
				data, err := os.ReadFile(logFile)
				if err != nil {
					return fmt.Errorf("failed to read log file %s: %w", logFile, err)
				}
				// Try uploading to CAS via gRPC
				serverAddr := ResolveServerAddr(flags.Server)
				ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
				defer cancel()
				conn, grpcClient, err := DialGRPC(ctx, serverAddr)
				if err == nil {
					defer conn.Close()
					uploadedHash, _, upErr := uploadBytesToCAS(ctx, grpcClient, data)
					if upErr == nil {
						logHashHex = hex.EncodeToString(uploadedHash)
					}
				}
				if logHashHex == "" {
					h := sha256.Sum256(data)
					logHashHex = hex.EncodeToString(h[:])
				}
			}

			reporter := ci.NewHTTPStatusReporter(serverURL, nil)
			report := ci.CheckRunReport{
				CLID:           clID,
				PatchsetNumber: patchset,
				RunnerName:     runnerName,
				Provider:       provider,
				Status:         status,
				ExternalURL:    externalURL,
				LogBlobSHA256:  logHashHex,
			}

			if err := reporter.ReportStatus(cmd.Context(), report); err != nil {
				return fmt.Errorf("failed to report check run status: %w", err)
			}

			if flags.JSON {
				return writeJSON(cmd.OutOrStdout(), map[string]any{
					"command":   "ci report",
					"status":    "ok",
					"check_run": report,
				})
			}

			cmd.Printf("Reported check run for CL %d (PS %d) runner %q: %s\n", clID, patchset, runnerName, status)
			return nil
		},
	}

	cmd.Flags().Int64Var(&clID, "cl", 0, "Changelist ID (required)")
	cmd.Flags().IntVar(&patchset, "patchset", 1, "Patchset number (default: 1)")
	cmd.Flags().StringVar(&runnerName, "runner", "", "Runner identifier name (required)")
	cmd.Flags().StringVar(&provider, "provider", "remote", "Provider driver name (e.g. local, forgejo, github, webhook)")
	cmd.Flags().StringVar(&status, "status", "", "Check status: queued, running, passed, failed, canceled, skipped (required)")
	cmd.Flags().StringVar(&externalURL, "url", "", "External build or workflow URL")
	cmd.Flags().StringVar(&logHash, "log-hash", "", "SHA-256 hash of build logs")
	cmd.Flags().StringVar(&logFile, "log-file", "", "Path to build log file to upload/hash")
	cmd.Flags().StringVar(&serverURL, "server-url", "", "Niebla / Cotejo HTTP URL (defaults to NIEBLA_SERVER_URL or http://localhost:8080)")

	return cmd
}

// NewCIExecCmd implements `niebla ci exec`.
func NewCIExecCmd(flags *RootFlags) *cobra.Command {
	var clID int64
	var patchset int
	var runnerName string
	var provider string
	var externalURL string
	var serverURL string

	cmd := &cobra.Command{
		Use:   "exec --cl <id> --runner <name> -- <command> [args...]",
		Short: "Wrap a build command, automatically reporting running -> passed/failed and uploading logs",
		Long: `niebla ci exec runs any command, automatically reporting 'running' at start
and 'passed'/'failed' upon exit to Cotejo. Output logs are captured and uploaded to CAS.`,
		DisableFlagParsing: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			if clID <= 0 {
				return fmt.Errorf("--cl is required and must be positive")
			}
			if runnerName == "" {
				return fmt.Errorf("--runner is required")
			}
			if len(args) == 0 {
				return fmt.Errorf("no command provided to execute: pass command after -- (e.g. niebla ci exec ... -- go test ./...)")
			}
			if serverURL == "" {
				serverURL = os.Getenv("NIEBLA_SERVER_URL")
				if serverURL == "" {
					serverURL = "http://localhost:8080"
				}
			}

			reporter := ci.NewHTTPStatusReporter(serverURL, nil)
			ctx := cmd.Context()
			startTime := time.Now().UTC()

			_ = reporter.ReportStatus(ctx, ci.CheckRunReport{
				CLID:           clID,
				PatchsetNumber: patchset,
				RunnerName:     runnerName,
				Provider:       provider,
				Status:         string(types.CheckRunStatusRunning),
				ExternalURL:    externalURL,
				StartedAt:      &startTime,
			})

			execCmd := exec.CommandContext(ctx, args[0], args[1:]...)
			var logBuf bytes.Buffer

			if flags.JSON {
				execCmd.Stdout = &logBuf
				execCmd.Stderr = &logBuf
			} else {
				execCmd.Stdout = io.MultiWriter(cmd.OutOrStdout(), &logBuf)
				execCmd.Stderr = io.MultiWriter(cmd.ErrOrStderr(), &logBuf)
			}

			execErr := execCmd.Run()
			endTime := time.Now().UTC()

			exitCode := 0
			runStatus := types.CheckRunStatusPassed
			if execErr != nil {
				runStatus = types.CheckRunStatusFailed
				if exitErr, ok := execErr.(*exec.ExitError); ok {
					exitCode = exitErr.ExitCode()
				} else {
					exitCode = 1
				}
			}

			var logHashHex string
			logData := logBuf.Bytes()
			if len(logData) > 0 {
				serverAddr := ResolveServerAddr(flags.Server)
				uploadCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				conn, grpcClient, err := DialGRPC(uploadCtx, serverAddr)
				if err == nil {
					defer conn.Close()
					uploadedHash, _, upErr := uploadBytesToCAS(uploadCtx, grpcClient, logData)
					if upErr == nil {
						logHashHex = hex.EncodeToString(uploadedHash)
					}
				}
				if logHashHex == "" {
					h := sha256.Sum256(logData)
					logHashHex = hex.EncodeToString(h[:])
				}
			}

			_ = reporter.ReportStatus(ctx, ci.CheckRunReport{
				CLID:           clID,
				PatchsetNumber: patchset,
				RunnerName:     runnerName,
				Provider:       provider,
				Status:         string(runStatus),
				ExternalURL:    externalURL,
				LogBlobSHA256:  logHashHex,
				StartedAt:      &startTime,
				CompletedAt:    &endTime,
			})

			if flags.JSON {
				_ = writeJSON(cmd.OutOrStdout(), map[string]any{
					"command":         "ci exec",
					"cl_id":           clID,
					"patchset_number": patchset,
					"runner_name":     runnerName,
					"status":          runStatus,
					"exit_code":       exitCode,
					"log_blob_sha256": logHashHex,
				})
			}

			if exitCode != 0 {
				return fmt.Errorf("command %v exited with code %d", args, exitCode)
			}
			return nil
		},
	}

	cmd.Flags().Int64Var(&clID, "cl", 0, "Changelist ID (required)")
	cmd.Flags().IntVar(&patchset, "patchset", 1, "Patchset number (default: 1)")
	cmd.Flags().StringVar(&runnerName, "runner", "", "Runner identifier name (required)")
	cmd.Flags().StringVar(&provider, "provider", "local", "Provider driver name (default: local)")
	cmd.Flags().StringVar(&externalURL, "url", "", "External build or workflow URL")
	cmd.Flags().StringVar(&serverURL, "server-url", "", "Niebla / Cotejo HTTP URL (defaults to NIEBLA_SERVER_URL or http://localhost:8080)")

	return cmd
}
