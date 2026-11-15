package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type ReleasePayload struct {
	TagName         string `json:"tag_name"`
	TargetCommitish string `json:"target_commitish"`
	Name            string `json:"name"`
	Body            string `json:"body"`
	Draft           bool   `json:"draft"`
	Prerelease      bool   `json:"prerelease"`
}

type ReleaseResponse struct {
	ID        int64  `json:"id"`
	UploadURL string `json:"upload_url"`
	HTMLURL   string `json:"html_url"`
}

func main() {
	tag := flag.String("tag", "", "Git tag (e.g. v0.1.0)")
	repo := flag.String("repo", "mrtnzjsh/niebla", "GitHub repository (owner/repo)")
	notesFile := flag.String("notes", "", "Path to release notes markdown file")
	distDir := flag.String("dist", "", "Directory containing release assets to upload")
	flag.Parse()

	token := os.Getenv("GH_RELEASE_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token == "" {
		fmt.Fprintln(os.Stderr, "Error: GH_RELEASE_TOKEN environment variable is not set")
		os.Exit(1)
	}

	if *tag == "" {
		fmt.Fprintln(os.Stderr, "Error: -tag flag is required")
		os.Exit(1)
	}

	var notes string
	if *notesFile != "" {
		content, err := os.ReadFile(*notesFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to read notes file: %v\n", err)
			notes = fmt.Sprintf("Release %s", *tag)
		} else {
			notes = string(content)
		}
	} else {
		notes = fmt.Sprintf("Release %s", *tag)
	}

	client := &http.Client{Timeout: 90 * time.Second}

	// 1. Create GitHub Release
	payload := ReleasePayload{
		TagName:         *tag,
		TargetCommitish: "main",
		Name:            *tag,
		Body:            notes,
		Draft:           false,
		Prerelease:      false,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling payload: %v\n", err)
		os.Exit(1)
	}

	createURL := fmt.Sprintf("https://api.github.com/repos/%s/releases", *repo)
	req, err := http.NewRequest("POST", createURL, bytes.NewReader(bodyBytes))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating HTTP request: %v\n", err)
		os.Exit(1)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error communicating with GitHub API: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "GitHub API returned HTTP %d: %s\n", resp.StatusCode, string(respBytes))
		os.Exit(1)
	}

	var releaseResp ReleaseResponse
	if err := json.Unmarshal(respBytes, &releaseResp); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing release response: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("[✓] Created GitHub release: %s (ID: %d)\n", releaseResp.HTMLURL, releaseResp.ID)

	// 2. Upload Release Assets
	if *distDir != "" {
		entries, err := os.ReadDir(*distDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading dist directory %s: %v\n", *distDir, err)
			os.Exit(1)
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			filename := entry.Name()
			assetPath := filepath.Join(*distDir, filename)

			fileData, err := os.ReadFile(assetPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to read asset %s: %v\n", filename, err)
				continue
			}

			uploadURL := fmt.Sprintf("https://uploads.github.com/repos/%s/releases/%d/assets?name=%s", *repo, releaseResp.ID, filename)
			upReq, err := http.NewRequest("POST", uploadURL, bytes.NewReader(fileData))
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to create upload request for %s: %v\n", filename, err)
				continue
			}
			upReq.Header.Set("Authorization", "Bearer "+token)
			upReq.Header.Set("Content-Type", "application/octet-stream")

			upResp, err := client.Do(upReq)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to upload %s: %v\n", filename, err)
				continue
			}
			upRespBody, _ := io.ReadAll(upResp.Body)
			upResp.Body.Close()

			if upResp.StatusCode < 200 || upResp.StatusCode >= 300 {
				fmt.Fprintf(os.Stderr, "Failed to upload %s (HTTP %d): %s\n", filename, upResp.StatusCode, string(upRespBody))
			} else {
				fmt.Printf("[✓] Uploaded release asset: %s (%d bytes)\n", filename, len(fileData))
			}
		}
	}
}

