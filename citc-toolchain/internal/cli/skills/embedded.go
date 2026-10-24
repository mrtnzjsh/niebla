package skills

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed assets/citc/*
var EmbeddedFS embed.FS

const (
	// RootAssetDir is the root path within EmbeddedFS.
	RootAssetDir = "assets/citc"
)

// ExtractEmbeddedSkills writes all embedded skill files into the target destination directory.
func ExtractEmbeddedSkills(targetDir string) error {
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create target skill directory %s: %w", targetDir, err)
	}

	entries, err := fs.ReadDir(EmbeddedFS, RootAssetDir)
	if err != nil {
		return fmt.Errorf("failed to read embedded assets: %w", err)
	}

	for _, entry := range entries {
		relPath := entry.Name()
		embeddedPath := RootAssetDir + "/" + relPath
		destPath := filepath.Join(targetDir, relPath)

		if entry.IsDir() {
			if err := extractSubdir(embeddedPath, destPath); err != nil {
				return err
			}
			continue
		}

		data, err := EmbeddedFS.ReadFile(embeddedPath)
		if err != nil {
			return fmt.Errorf("failed to read embedded file %s: %w", embeddedPath, err)
		}

		if err := os.WriteFile(destPath, data, 0644); err != nil {
			return fmt.Errorf("failed to write skill file %s: %w", destPath, err)
		}
	}

	return nil
}

func extractSubdir(srcDir, destDir string) error {
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", destDir, err)
	}

	entries, err := fs.ReadDir(EmbeddedFS, srcDir)
	if err != nil {
		return fmt.Errorf("failed to read embedded directory %s: %w", srcDir, err)
	}

	for _, entry := range entries {
		src := srcDir + "/" + entry.Name()
		dest := filepath.Join(destDir, entry.Name())

		if entry.IsDir() {
			if err := extractSubdir(src, dest); err != nil {
				return err
			}
			continue
		}

		data, err := EmbeddedFS.ReadFile(src)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", src, err)
		}
		if err := os.WriteFile(dest, data, 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", dest, err)
		}
	}

	return nil
}
