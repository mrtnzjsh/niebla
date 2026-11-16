package assets

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
)

//go:embed static/*
var StaticFS embed.FS

func init() {
	_ = mime.AddExtensionType(".css", "text/css; charset=utf-8")
	_ = mime.AddExtensionType(".js", "application/javascript")
}

// Handler returns an http.Handler that serves static assets under /static/.
// If overrideDir is provided and the directory exists on disk, assets are served
// from the filesystem to support live-reloading during local frontend development.
// Otherwise, it serves assets embedded directly into the binary via StaticFS.
func Handler(overrideDir string) http.Handler {
	if overrideDir != "" {
		staticDir := filepath.Join(overrideDir, "static")
		if info, err := os.Stat(staticDir); err == nil && info.IsDir() {
			return http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir)))
		}
		if info, err := os.Stat(overrideDir); err == nil && info.IsDir() {
			return http.StripPrefix("/static/", http.FileServer(http.Dir(overrideDir)))
		}
	}

	sub, err := fs.Sub(StaticFS, "static")
	if err != nil {
		panic(err)
	}
	return http.StripPrefix("/static/", http.FileServer(http.FS(sub)))
}
