package local

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"niebla/pkg/cas"
	"niebla/pkg/ci"
	"niebla/pkg/materialize"
	"niebla/pkg/tree"
	"niebla/pkg/types"
)

// Config defines execution parameters for the local sandbox runner.
type Config struct {
	Name           string
	WorkDir        string   // Directory where checkouts are materialized, e.g. /tmp/niebla-ci
	Command        []string // Build command to run, e.g. ["go", "test", "./..."]
	TimeoutSeconds int      // Timeout per run in seconds (default 300)
	Concurrency    int      // Concurrency for tree materialization
	CASStorage     *cas.Storage
	TreeFetcher    tree.FetchTreeFunc
	BlobFetcher    materialize.BlobFetcher
	Reporter       ci.StatusReporter
}

// Provider implements ci.Provider for local execution in a lightweight sandbox directory.
type Provider struct {
	cfg Config
}

// NewProvider creates a new local sandbox runner.
func NewProvider(cfg Config) *Provider {
	if cfg.Name == "" {
		cfg.Name = "local"
	}
	if cfg.WorkDir == "" {
		cfg.WorkDir = filepath.Join(os.TempDir(), "niebla-ci")
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = 300
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 4
	}
	return &Provider{cfg: cfg}
}

func (p *Provider) Name() string {
	return p.cfg.Name
}

// Trigger materializes the patchset, executes the configured command, and reports status.
func (p *Provider) Trigger(ctx context.Context, req ci.DispatchRequest) (string, error) {
	if len(p.cfg.Command) == 0 {
		return "", fmt.Errorf("no command configured for local runner %q", p.cfg.Name)
	}

	destDir := filepath.Join(p.cfg.WorkDir, fmt.Sprintf("cl-%d-ps-%d", req.CLID, req.Patchset))
	_ = os.RemoveAll(destDir)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create sandbox directory %s: %w", destDir, err)
	}

	startTime := time.Now().UTC()

	if p.cfg.Reporter != nil {
		_ = p.cfg.Reporter.ReportStatus(ctx, ci.CheckRunReport{
			CLID:           req.CLID,
			PatchsetNumber: req.Patchset,
			RunnerName:     p.cfg.Name,
			Provider:       "local",
			Status:         string(types.CheckRunStatusRunning),
			StartedAt:      &startTime,
		})
	}

	if err := p.materializeIfRequested(ctx, req, destDir, startTime); err != nil {
		return destDir, err
	}

	timeout := time.Duration(p.cfg.TimeoutSeconds) * time.Second
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, p.cfg.Command[0], p.cfg.Command[1:]...)
	cmd.Dir = destDir

	cmd.Env = append(os.Environ(),
		fmt.Sprintf("NIEBLA_CL_ID=%s", strconv.FormatInt(req.CLID, 10)),
		fmt.Sprintf("NIEBLA_PATCHSET=%s", strconv.Itoa(req.Patchset)),
		fmt.Sprintf("NIEBLA_TREE_HASH=%s", req.TreeHash),
		fmt.Sprintf("NIEBLA_AUTHOR=%s", req.Author),
		fmt.Sprintf("NIEBLA_SERVER_URL=%s", req.ServerURL),
		fmt.Sprintf("NIEBLA_TOKEN=%s", req.RunnerToken),
	)

	var outputBuf bytes.Buffer
	cmd.Stdout = &outputBuf
	cmd.Stderr = &outputBuf

	execErr := cmd.Run()
	endTime := time.Now().UTC()

	status := types.CheckRunStatusPassed
	if execErr != nil {
		if runCtx.Err() == context.DeadlineExceeded {
			status = types.CheckRunStatusCanceled
		} else {
			status = types.CheckRunStatusFailed
		}
	}

	var logHashHex string
	if p.cfg.CASStorage != nil && outputBuf.Len() > 0 {
		hashBytes, _, _, _, err := p.cfg.CASStorage.Put(bytes.NewReader(outputBuf.Bytes()))
		if err == nil {
			logHashHex = hex.EncodeToString(hashBytes)
		}
	}

	if p.cfg.Reporter != nil {
		_ = p.cfg.Reporter.ReportStatus(ctx, ci.CheckRunReport{
			CLID:           req.CLID,
			PatchsetNumber: req.Patchset,
			RunnerName:     p.cfg.Name,
			Provider:       "local",
			Status:         string(status),
			LogBlobSHA256:  logHashHex,
			StartedAt:      &startTime,
			CompletedAt:    &endTime,
		})
	}

	if execErr != nil {
		return destDir, fmt.Errorf("local command failed with status %s: %w", status, execErr)
	}

	return destDir, nil
}

func (p *Provider) materializeIfRequested(ctx context.Context, req ci.DispatchRequest, destDir string, startTime time.Time) error {
	if req.TreeHash == "" || p.cfg.TreeFetcher == nil {
		return nil
	}
	treeHashBytes, err := hex.DecodeString(strings.TrimSpace(req.TreeHash))
	if err != nil || len(treeHashBytes) == 0 {
		return nil
	}
	opts := materialize.Options{
		Concurrency: p.cfg.Concurrency,
		HardlinkCAS: p.cfg.CASStorage,
	}
	_, matErr := materialize.MaterializeTree(ctx, treeHashBytes, destDir, p.cfg.TreeFetcher, p.cfg.BlobFetcher, opts)
	if matErr != nil {
		endTime := time.Now().UTC()
		if p.cfg.Reporter != nil {
			_ = p.cfg.Reporter.ReportStatus(ctx, ci.CheckRunReport{
				CLID:           req.CLID,
				PatchsetNumber: req.Patchset,
				RunnerName:     p.cfg.Name,
				Provider:       "local",
				Status:         string(types.CheckRunStatusFailed),
				StartedAt:      &startTime,
				CompletedAt:    &endTime,
			})
		}
		return fmt.Errorf("failed to materialize tree: %w", matErr)
	}
	return nil
}
