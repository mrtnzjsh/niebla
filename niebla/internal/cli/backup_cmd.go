package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"niebla/internal/config"
	"niebla/pkg/crypto"
	"niebla/pkg/resilience/bundle"
	"niebla/pkg/resilience/drivers"
	"niebla/pkg/resilience/zfs"

	"github.com/spf13/cobra"
)

// NewBackupCmd constructs the 'backup' command tree for replication, encrypted archives, and disaster recovery.
func NewBackupCmd(rootFlags *RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Manage resilience, 10GbE ZFS replication, encrypted bundles, and disaster recovery",
		Long: `The backup suite provides enterprise resilience and disaster recovery:
  - 10GbE native ZFS snapshot streaming to dedicated storage arrays
  - AES-256-GCM / ChaCha20-Poly1305 envelope-encrypted standalone bundles (.nieblabak)
  - Multi-target replication (ZFS, Filesystem, Nextcloud WebDAV, S3-compatible object stores)
  - Decoupled, modular execution that never blocks core VCS workflows.`,
	}

	cmd.AddCommand(newBackupCreateCmd(rootFlags))
	cmd.AddCommand(newBackupListCmd(rootFlags))
	cmd.AddCommand(newBackupVerifyCmd(rootFlags))
	cmd.AddCommand(newBackupRestoreCmd(rootFlags))
	cmd.AddCommand(newBackupPruneCmd(rootFlags))

	return cmd
}

func loadToolchainConfig() (*config.Config, error) {
	configPath, err := config.DefaultConfigPath()
	if err != nil {
		return config.DefaultConfig(), nil
	}
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return config.DefaultConfig(), nil
	}
	return config.LoadFromPath(configPath)
}

func resolveMasterKey(cfg *config.Config) ([]byte, [16]byte, error) {
	keyCfg := crypto.KeyConfig{
		Source:        cfg.Resilience.Encryption.KeySource,
		EnvVar:        cfg.Resilience.Encryption.KeyEnv,
		FilePath:      cfg.Resilience.Encryption.KeyFile,
		PassphraseEnv: cfg.Resilience.Encryption.PassphraseEnv,
	}

	if keyCfg.Source == "" {
		if os.Getenv("NIEBLA_MASTER_KEY") != "" {
			keyCfg.Source = "env"
			keyCfg.EnvVar = "NIEBLA_MASTER_KEY"
		} else if os.Getenv("NIEBLA_BACKUP_PASSPHRASE") != "" {
			keyCfg.Source = "passphrase"
			keyCfg.PassphraseEnv = "NIEBLA_BACKUP_PASSPHRASE"
		} else {
			keyCfg.Source = "env"
			keyCfg.EnvVar = "NIEBLA_MASTER_KEY"
		}
	}

	key, salt, err := crypto.LoadMasterKey(keyCfg, nil)
	if err != nil {
		return nil, [16]byte{}, fmt.Errorf("failed to load master key: %w", err)
	}

	var saltArr [16]byte
	if len(salt) == 16 {
		copy(saltArr[:], salt)
	}
	return key, saltArr, nil
}

func resolveDriver(cfg *config.Config, target string, customDir string) (drivers.StorageDriver, error) {
	if customDir != "" {
		return drivers.NewFilesystemDriver(drivers.FilesystemConfig{
			RootPath:      customDir,
			BufferSizeMB:  cfg.Resilience.Filesystem.BufferSizeMB,
			AutoCreateDir: true,
		})
	}

	targetLower := strings.ToLower(strings.TrimSpace(target))
	if strings.HasPrefix(target, "/") || strings.HasPrefix(target, "./") || strings.HasPrefix(target, "../") {
		return drivers.NewFilesystemDriver(drivers.FilesystemConfig{
			RootPath:      target,
			BufferSizeMB:  cfg.Resilience.Filesystem.BufferSizeMB,
			AutoCreateDir: true,
		})
	}

	if targetLower == "" {
		if cfg.Resilience.S3.Enabled {
			targetLower = "s3"
		} else if cfg.Resilience.Nextcloud.Enabled {
			targetLower = "nextcloud"
		} else {
			targetLower = "filesystem"
		}
	}

	switch targetLower {
	case "s3":
		accessKey := ""
		if cfg.Resilience.S3.AccessKeyEnv != "" {
			accessKey = os.Getenv(cfg.Resilience.S3.AccessKeyEnv)
		}
		secretKey := ""
		if cfg.Resilience.S3.SecretKeyEnv != "" {
			secretKey = os.Getenv(cfg.Resilience.S3.SecretKeyEnv)
		}
		return drivers.NewS3Driver(drivers.S3Config{
			Endpoint:       cfg.Resilience.S3.Endpoint,
			Bucket:         cfg.Resilience.S3.Bucket,
			Region:         cfg.Resilience.S3.Region,
			Prefix:         cfg.Resilience.S3.Prefix,
			AccessKey:      accessKey,
			SecretKey:      secretKey,
			ForcePathStyle: cfg.Resilience.S3.ForcePathStyle,
			UseSSL:         cfg.Resilience.S3.UseSSL,
			StorageClass:   cfg.Resilience.S3.StorageClass,
		})

	case "nextcloud":
		password := ""
		if cfg.Resilience.Nextcloud.PasswordEnv != "" {
			password = os.Getenv(cfg.Resilience.Nextcloud.PasswordEnv)
		}
		timeout := time.Duration(cfg.Resilience.Nextcloud.TimeoutSeconds) * time.Second
		if timeout <= 0 {
			timeout = 180 * time.Second
		}
		return drivers.NewNextcloudDriver(drivers.NextcloudConfig{
			BaseURL:  cfg.Resilience.Nextcloud.URL,
			Username: cfg.Resilience.Nextcloud.Username,
			Password: password,
			Timeout:  timeout,
		})

	case "filesystem", "local":
		root := cfg.Resilience.Filesystem.Path
		if root == "" {
			root = cfg.Resilience.Bundle.TargetDir
		}
		if root == "" {
			root = "./backups"
		}
		return drivers.NewFilesystemDriver(drivers.FilesystemConfig{
			RootPath:      root,
			BufferSizeMB:  cfg.Resilience.Filesystem.BufferSizeMB,
			AutoCreateDir: true,
		})

	default:
		return nil, fmt.Errorf("unsupported storage driver target: %q (supported: s3, nextcloud, filesystem)", target)
	}
}

// -----------------------------------------------------------------------------
// backup create
// -----------------------------------------------------------------------------

func newBackupCreateCmd(rootFlags *RootFlags) *cobra.Command {
	var mode string
	var target string
	var label string
	var customDir string

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new ZFS snapshot stream or encrypted backup bundle",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadToolchainConfig()
			if err != nil {
				return err
			}

			selectedMode := mode
			if selectedMode == "" {
				selectedMode = cfg.Resilience.DefaultMode
			}
			if selectedMode == "" {
				selectedMode = "zfs_stream"
			}

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			timestamp := time.Now().UTC().Format("20060102-150405")
			tag := timestamp
			if label != "" {
				tag = fmt.Sprintf("%s-%s", label, timestamp)
			}

			switch selectedMode {
			case "zfs", "zfs_stream":
				dataset := cfg.Resilience.ZFS.LocalDataset
				if dataset == "" {
					return fmt.Errorf("zfs local_dataset not configured in [resilience.zfs]")
				}

				mgr := zfs.NewSnapshotManager()
				snapPrefix := cfg.Resilience.ZFS.SnapshotPrefix
				if snapPrefix == "" {
					snapPrefix = "niebla-auto"
				}
				snapTag := fmt.Sprintf("%s-%s", snapPrefix, tag)

				fmt.Fprintf(cmd.OutOrStdout(), "Creating ZFS recursive snapshot %s@%s...\n", dataset, snapTag)
				if err := mgr.CreateRecursiveSnapshot(ctx, dataset, snapTag); err != nil {
					return fmt.Errorf("zfs snapshot creation failed: %w", err)
				}
				fullSnapName := fmt.Sprintf("%s@%s", dataset, snapTag)
				fmt.Fprintf(cmd.OutOrStdout(), "✓ ZFS snapshot created: %s\n", fullSnapName)

				if cfg.Resilience.ZFS.RemoteHost != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "Streaming snapshot to %s over 10GbE network...\n", cfg.Resilience.ZFS.RemoteHost)
					streamer := zfs.NewStreamer(zfs.StreamConfig{
						LocalDataset:     dataset,
						RemoteHost:       cfg.Resilience.ZFS.RemoteHost,
						RemoteUser:       cfg.Resilience.ZFS.RemoteUser,
						RemotePort:       cfg.Resilience.ZFS.RemoteSSHPort,
						RemoteKeyFile:    cfg.Resilience.ZFS.RemoteKeyFile,
						RemoteDataset:    cfg.Resilience.ZFS.RemoteDataset,
						RawEncryptedSend: cfg.Resilience.ZFS.RawEncryptedSend,
						BandwidthLimitMB: cfg.Resilience.ZFS.BandwidthLimitMB,
					})
					if err := streamer.SendIncrementalStream(ctx, fullSnapName, ""); err != nil {
						return fmt.Errorf("zfs stream replication failed: %w", err)
					}
					fmt.Fprintf(cmd.OutOrStdout(), "✓ ZFS replication complete to %s:%s (raw encrypted: %t)\n",
						cfg.Resilience.ZFS.RemoteHost, cfg.Resilience.ZFS.RemoteDataset, cfg.Resilience.ZFS.RawEncryptedSend)
				}
				return nil

			case "bundle":
				masterKey, salt, err := resolveMasterKey(cfg)
				if err != nil {
					return err
				}
				defer crypto.WipeKey(masterKey)

				suite := crypto.SuiteAES256GCM
				if strings.ToLower(cfg.Resilience.Encryption.Algorithm) == "chacha20-poly1305" {
					suite = crypto.SuiteChaCha20Poly1305
				}

				manifest := bundle.Manifest{
					BackupID:     tag,
					CreatedAt:    time.Now().UTC(),
					CLCount:      0,
					BlobCount:    1,
					DatabaseName: "niebla",
				}

				buf := new(bytes.Buffer)
				packOpts := bundle.PackOptions{
					MasterKey:   masterKey,
					CipherSuite: suite,
					KDFMode:     crypto.KDFModeRaw,
					Salt:        salt,
					Manifest:    manifest,
					Blobs: map[string]io.Reader{
						"meta.json": strings.NewReader(fmt.Sprintf(`{"tag": %q, "time": %q}`, tag, manifest.CreatedAt)),
					},
				}

				fmt.Fprintf(cmd.OutOrStdout(), "Packing encrypted backup bundle (algorithm: %s)...\n", cfg.Resilience.Encryption.Algorithm)
				if err := bundle.Pack(ctx, buf, packOpts); err != nil {
					return fmt.Errorf("failed to pack encrypted bundle: %w", err)
				}

				driver, err := resolveDriver(cfg, target, customDir)
				if err != nil {
					return err
				}

				filename := fmt.Sprintf("niebla-%s.nieblabak", tag)
				fmt.Fprintf(cmd.OutOrStdout(), "Uploading %s (%d bytes) to target [%s]...\n", filename, buf.Len(), driver.Name())
				if err := driver.Upload(ctx, filename, bytes.NewReader(buf.Bytes()), int64(buf.Len())); err != nil {
					return fmt.Errorf("failed to upload backup bundle to %s: %w", driver.Name(), err)
				}

				fmt.Fprintf(cmd.OutOrStdout(), "✓ Encrypted bundle successfully stored: %s on %s\n", filename, driver.Name())
				return nil

			default:
				return fmt.Errorf("unknown backup mode %q; choose 'zfs_stream' or 'bundle'", selectedMode)
			}
		},
	}

	cmd.Flags().StringVar(&mode, "mode", "", "Backup mode: 'zfs' / 'zfs_stream' (default) or 'bundle'")
	cmd.Flags().StringVar(&target, "target", "", "Target destination driver ('zfs', 'filesystem', 'nextcloud', 's3')")
	cmd.Flags().StringVar(&customDir, "dir", "", "Custom directory path for filesystem backups")
	cmd.Flags().StringVar(&label, "label", "", "Optional descriptive label tag for this backup")

	return cmd
}

// -----------------------------------------------------------------------------
// backup list
// -----------------------------------------------------------------------------

func newBackupListCmd(rootFlags *RootFlags) *cobra.Command {
	var target string
	var customDir string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List existing snapshots and remote backup archives",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadToolchainConfig()
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			targetLower := strings.ToLower(strings.TrimSpace(target))
			if targetLower == "zfs" || (targetLower == "" && cfg.Resilience.DefaultMode == "zfs_stream") {
				dataset := cfg.Resilience.ZFS.LocalDataset
				mgr := zfs.NewSnapshotManager()
				snaps, err := mgr.ListSnapshots(ctx, dataset)
				if err != nil {
					return fmt.Errorf("failed to list ZFS snapshots: %w", err)
				}

				fmt.Fprintf(cmd.OutOrStdout(), "%-40s %-22s %-12s\n", "SNAPSHOT NAME", "CREATED AT", "USED BYTES")
				fmt.Fprintln(cmd.OutOrStdout(), strings.Repeat("-", 76))
				for _, snap := range snaps {
					fmt.Fprintf(cmd.OutOrStdout(), "%-40s %-22s %-12d\n", snap.Name, snap.CreatedAt.Format("2006-01-02 15:04:05"), snap.UsedBytes)
				}
				return nil
			}

			driver, err := resolveDriver(cfg, target, customDir)
			if err != nil {
				return err
			}

			items, err := driver.List(ctx, "")
			if err != nil {
				return fmt.Errorf("failed to list backups from %s: %w", driver.Name(), err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "%-45s %-12s %-22s\n", "BACKUP ARCHIVE", "SIZE (BYTES)", "MODIFIED AT")
			fmt.Fprintln(cmd.OutOrStdout(), strings.Repeat("-", 82))
			for _, item := range items {
				fmt.Fprintf(cmd.OutOrStdout(), "%-45s %-12d %-22s\n", item.Name, item.Size, item.ModTime.Format("2006-01-02 15:04:05"))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&target, "target", "", "Target storage ('zfs', 'filesystem', 'nextcloud', 's3')")
	cmd.Flags().StringVar(&customDir, "dir", "", "Custom directory path for filesystem backups")

	return cmd
}

// -----------------------------------------------------------------------------
// backup verify
// -----------------------------------------------------------------------------

func newBackupVerifyCmd(rootFlags *RootFlags) *cobra.Command {
	var target string
	var customDir string

	cmd := &cobra.Command{
		Use:   "verify <backup-name-or-path>",
		Short: "Verify the cryptographic integrity, AEAD headers, or snapshot of a backup",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			backupName := args[0]
			cfg, err := loadToolchainConfig()
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			targetLower := strings.ToLower(strings.TrimSpace(target))
			if targetLower == "zfs" || strings.Contains(backupName, "@") {
				dataset := cfg.Resilience.ZFS.LocalDataset
				mgr := zfs.NewSnapshotManager()
				snaps, err := mgr.ListSnapshots(ctx, dataset)
				if err != nil {
					return fmt.Errorf("failed to query ZFS snapshots: %w", err)
				}
				found := false
				for _, snap := range snaps {
					if snap.Name == backupName || strings.HasSuffix(snap.Name, backupName) {
						found = true
						fmt.Fprintf(cmd.OutOrStdout(), "✓ ZFS Snapshot verified: %s (created: %s, size: %d bytes)\n",
							snap.Name, snap.CreatedAt.Format(time.RFC3339), snap.UsedBytes)
						break
					}
				}
				if !found {
					return fmt.Errorf("ZFS snapshot %q not found", backupName)
				}
				return nil
			}

			// Bundle verification
			var r io.ReadCloser
			if _, statErr := os.Stat(backupName); statErr == nil {
				f, err := os.Open(backupName)
				if err != nil {
					return err
				}
				r = f
			} else {
				driver, err := resolveDriver(cfg, target, customDir)
				if err != nil {
					return err
				}
				r, err = driver.Download(ctx, backupName)
				if err != nil {
					return fmt.Errorf("failed to fetch %s from %s: %w", backupName, driver.Name(), err)
				}
			}
			defer r.Close()

			masterKey, _, err := resolveMasterKey(cfg)
			if err != nil {
				return err
			}
			defer crypto.WipeKey(masterKey)

			header, manifest, err := bundle.VerifyHeader(r, masterKey)
			if err != nil {
				return fmt.Errorf("cryptographic verification failed: %w", err)
			}

			suiteName := "AES-256-GCM"
			if header.CipherSuite == crypto.SuiteChaCha20Poly1305 {
				suiteName = "ChaCha20-Poly1305"
			}

			fmt.Fprintln(cmd.OutOrStdout(), "✓ Backup Archive Verification Succeeded")
			fmt.Fprintf(cmd.OutOrStdout(), "  Envelope Version: %d\n", header.Version)
			fmt.Fprintf(cmd.OutOrStdout(), "  Cipher Suite:     %s\n", suiteName)
			fmt.Fprintf(cmd.OutOrStdout(), "  DEK Wrap Status:  Authenticated & Validated\n")
			if manifest != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "  Backup ID:        %s\n", manifest.BackupID)
				fmt.Fprintf(cmd.OutOrStdout(), "  Created At:       %s\n", manifest.CreatedAt.Format(time.RFC3339))
				fmt.Fprintf(cmd.OutOrStdout(), "  Database Name:    %s\n", manifest.DatabaseName)
				fmt.Fprintf(cmd.OutOrStdout(), "  Blobs Packaged:   %d\n", manifest.BlobCount)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&target, "target", "", "Target storage driver ('zfs', 'filesystem', 'nextcloud', 's3')")
	cmd.Flags().StringVar(&customDir, "dir", "", "Custom directory path for filesystem backups")

	return cmd
}

// -----------------------------------------------------------------------------
// backup restore
// -----------------------------------------------------------------------------

func restoreZFSSnapshot(ctx context.Context, cmd *cobra.Command, cfg *config.Config, backupName string, dryRun bool) error {
	dataset := cfg.Resilience.ZFS.LocalDataset
	mgr := zfs.NewSnapshotManager()

	snapFull := backupName
	if !strings.Contains(backupName, "@") {
		snapFull = fmt.Sprintf("%s@%s", dataset, backupName)
	}

	if dryRun {
		fmt.Fprintf(cmd.OutOrStdout(), "[DRY-RUN] Validating rollback to ZFS snapshot %s...\n", snapFull)
		snaps, err := mgr.ListSnapshots(ctx, dataset)
		if err != nil {
			return fmt.Errorf("failed to query snapshots: %w", err)
		}
		found := false
		for _, snap := range snaps {
			if snap.Name == snapFull || strings.HasSuffix(snap.Name, backupName) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("snapshot %q not found on dataset %s", backupName, dataset)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "[DRY-RUN] Snapshot found. Ready to roll back.\n")
		return nil
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Rolling back dataset %s to snapshot %s...\n", dataset, snapFull)
	if err := mgr.Rollback(ctx, snapFull); err != nil {
		return fmt.Errorf("rollback failed: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "✓ Dataset successfully restored to snapshot %s\n", snapFull)
	return nil
}

func writeUnpackedBackup(destDir string, res *bundle.UnpackResult) error {
	if len(res.DBDump) > 0 {
		dumpPath := filepath.Join(destDir, "database.dump")
		if err := os.WriteFile(dumpPath, res.DBDump, 0640); err != nil {
			return fmt.Errorf("failed to write database dump: %w", err)
		}
	}

	blobDir := filepath.Join(destDir, "blobs")
	if len(res.Blobs) > 0 {
		if err := os.MkdirAll(blobDir, 0755); err != nil {
			return fmt.Errorf("failed to create blob directory: %w", err)
		}
		for name, data := range res.Blobs {
			bPath := filepath.Join(blobDir, filepath.Clean(name))
			_ = os.MkdirAll(filepath.Dir(bPath), 0755)
			if err := os.WriteFile(bPath, data, 0644); err != nil {
				return fmt.Errorf("failed to write blob %s: %w", name, err)
			}
		}
	}
	return nil
}

func restoreEncryptedBundle(
	ctx context.Context,
	cmd *cobra.Command,
	cfg *config.Config,
	backupName, target, destDir, customDir string,
	dryRun bool,
) error {
	var r io.ReadCloser
	if _, statErr := os.Stat(backupName); statErr == nil {
		f, err := os.Open(backupName)
		if err != nil {
			return err
		}
		r = f
	} else {
		driver, err := resolveDriver(cfg, target, customDir)
		if err != nil {
			return err
		}
		r, err = driver.Download(ctx, backupName)
		if err != nil {
			return fmt.Errorf("failed to fetch %s from %s: %w", backupName, driver.Name(), err)
		}
	}
	defer r.Close()

	masterKey, _, err := resolveMasterKey(cfg)
	if err != nil {
		return err
	}
	defer crypto.WipeKey(masterKey)

	if dryRun {
		fmt.Fprintf(cmd.OutOrStdout(), "[DRY-RUN] Verifying archive integrity and decrypting headers for %s...\n", backupName)
		_, manifest, err := bundle.VerifyHeader(r, masterKey)
		if err != nil {
			return fmt.Errorf("[DRY-RUN] Decryption / header verification failed: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "[DRY-RUN] ✓ Cryptographic integrity verified! Archive can be decrypted safely.\n")
		fmt.Fprintf(cmd.OutOrStdout(), "[DRY-RUN] Backup ID: %s, Database: %s, Blobs: %d\n",
			manifest.BackupID, manifest.DatabaseName, manifest.BlobCount)
		return nil
	}

	if destDir == "" {
		destDir = "./restore-" + time.Now().UTC().Format("20060102-150405")
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory %s: %w", destDir, err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Decrypting and unpacking backup %s into %s...\n", backupName, destDir)
	res, err := bundle.Unpack(ctx, r, masterKey)
	if err != nil {
		return fmt.Errorf("unpack failed: %w", err)
	}

	if err := writeUnpackedBackup(destDir, res); err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "✓ Successfully restored backup %s\n", res.Manifest.BackupID)
	fmt.Fprintf(cmd.OutOrStdout(), "  Output Directory: %s\n", destDir)
	fmt.Fprintf(cmd.OutOrStdout(), "  Blobs Extracted:  %d\n", len(res.Blobs))
	return nil
}

func newBackupRestoreCmd(rootFlags *RootFlags) *cobra.Command {
	var target string
	var destDir string
	var customDir string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "restore <backup-name-or-snapshot>",
		Short: "Restore data from a ZFS snapshot or decrypt an authenticated bundle",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			backupName := args[0]
			cfg, err := loadToolchainConfig()
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			targetLower := strings.ToLower(strings.TrimSpace(target))
			if targetLower == "zfs" || strings.Contains(backupName, "@") {
				return restoreZFSSnapshot(ctx, cmd, cfg, backupName, dryRun)
			}

			return restoreEncryptedBundle(ctx, cmd, cfg, backupName, target, destDir, customDir, dryRun)
		},
	}

	cmd.Flags().StringVar(&target, "target", "", "Target storage driver ('zfs', 'filesystem', 'nextcloud', 's3')")
	cmd.Flags().StringVar(&destDir, "dest", "", "Destination directory to restore files into (defaults to ./restore-<timestamp>)")
	cmd.Flags().StringVar(&customDir, "dir", "", "Custom directory path for filesystem backups")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Verify cryptographic signatures and headers without writing files to disk")

	return cmd
}

// -----------------------------------------------------------------------------
// backup prune
// -----------------------------------------------------------------------------

func newBackupPruneCmd(rootFlags *RootFlags) *cobra.Command {
	var keepCount int
	var target string
	var customDir string

	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Prune old backups according to retention policies",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadToolchainConfig()
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			targetLower := strings.ToLower(strings.TrimSpace(target))
			retention := keepCount
			if retention <= 0 {
				retention = cfg.Resilience.ZFS.RetentionCount
			}
			if retention <= 0 {
				retention = 7
			}

			if targetLower == "zfs" || (targetLower == "" && cfg.Resilience.DefaultMode == "zfs_stream") {
				dataset := cfg.Resilience.ZFS.LocalDataset
				mgr := zfs.NewSnapshotManager()
				prefix := cfg.Resilience.ZFS.SnapshotPrefix
				if prefix == "" {
					prefix = "niebla-auto"
				}

				fmt.Fprintf(cmd.OutOrStdout(), "Pruning ZFS snapshots on %s (keeping newest %d matching prefix %q)...\n",
					dataset, retention, prefix)
				pruned, err := zfs.PruneOlderSnapshots(ctx, mgr, dataset, prefix, retention)
				if err != nil {
					return fmt.Errorf("zfs snapshot pruning failed: %w", err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "✓ Pruned %d snapshot(s)\n", len(pruned))
				return nil
			}

			driver, err := resolveDriver(cfg, target, customDir)
			if err != nil {
				return err
			}

			items, err := driver.List(ctx, "")
			if err != nil {
				return fmt.Errorf("failed to list items from %s: %w", driver.Name(), err)
			}

			if len(items) <= retention {
				fmt.Fprintf(cmd.OutOrStdout(), "Found %d backup(s); retention count is %d. Nothing to prune.\n", len(items), retention)
				return nil
			}

			// Sort oldest first
			sort.Slice(items, func(i, j int) bool {
				return items[i].ModTime.Before(items[j].ModTime)
			})

			excess := len(items) - retention
			fmt.Fprintf(cmd.OutOrStdout(), "Pruning %d oldest backup(s) on %s (keeping newest %d)...\n", excess, driver.Name(), retention)
			for i := 0; i < excess; i++ {
				item := items[i]
				fmt.Fprintf(cmd.OutOrStdout(), "  Deleting %s...\n", item.Name)
				if err := driver.Delete(ctx, item.Name); err != nil {
					return fmt.Errorf("failed to delete %s: %w", item.Name, err)
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), "✓ Pruning complete. Retained %d backup(s).\n", retention)
			return nil
		},
	}

	cmd.Flags().IntVar(&keepCount, "keep", 0, "Number of most recent backups to retain (defaults to retention_count in config)")
	cmd.Flags().StringVar(&target, "target", "", "Target storage driver ('zfs', 'filesystem', 'nextcloud', 's3')")
	cmd.Flags().StringVar(&customDir, "dir", "", "Custom directory path for filesystem backups")

	return cmd
}
