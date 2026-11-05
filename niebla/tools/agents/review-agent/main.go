package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	internalagents "niebla/internal/agents"
	"niebla/internal/config"
	"niebla/pkg/agents"
	"niebla/pkg/ai"
	"niebla/pkg/types"
	"niebla/pkg/httpclient"
)

func resolveTargetFiles(filesFlag string) []string {
	rawFiles := strings.TrimSpace(filesFlag)
	if rawFiles == "" {
		rawFiles = strings.TrimSpace(os.Getenv("NIEBLA_TOUCHED_FILES"))
	}
	var targetFiles []string
	for _, file := range strings.Split(rawFiles, ",") {
		trimmed := strings.TrimSpace(file)
		if trimmed != "" {
			targetFiles = append(targetFiles, trimmed)
		}
	}
	return targetFiles
}

func resolveSystemPrompt(cfg *config.Config, promptFlag string) string {
	sysPrompt := internalagents.DefaultAIReviewSystemPrompt
	promptPath := strings.TrimSpace(promptFlag)
	if promptPath == "" {
		promptPath = strings.TrimSpace(cfg.Critique.AI.SystemPromptPath)
	}
	if promptPath != "" {
		if data, rErr := os.ReadFile(promptPath); rErr == nil && len(strings.TrimSpace(string(data))) > 0 {
			sysPrompt = string(data) + "\n\n" + internalagents.DefaultAIReviewSystemPrompt
		}
	}
	return sysPrompt
}

func buildReviewUserPrompt(targetFiles []string, maxLines int) string {
	if maxLines <= 0 {
		maxLines = 1200
	}
	var userPrompt strings.Builder
	userPrompt.WriteString("Review the following modified files and return a JSON AgentReviewReport:\n\n")
	for _, relPath := range targetFiles {
		contentBytes, readErr := os.ReadFile(filepath.FromSlash(relPath))
		if readErr != nil {
			continue
		}
		lines := strings.Split(string(contentBytes), "\n")
		if len(lines) > maxLines {
			lines = lines[:maxLines]
		}
		fmt.Fprintf(&userPrompt, "=== FILE: %s ===\n", relPath)
		for i, line := range lines {
			fmt.Fprintf(&userPrompt, "%4d | %s\n", i+1, line)
		}
		userPrompt.WriteString("\n")
	}
	return userPrompt.String()
}

func main() {
	promptFlag := flag.String("prompt", "", "Path to domain system prompt (.niebla/prompts/<domain>.md)")
	filesFlag := flag.String("files", "", "Comma-separated list of file paths to review (defaults to NIEBLA_TOUCHED_FILES)")
	configFlag := flag.String("config", "", "Optional path to config.toml or config directory")
	failOnChangesFlag := flag.Bool("fail-on-changes", true, "Exit with code 1 when verdict is changes_requested")
	flag.Parse()

	var cfg *config.Config
	var err error
	if *configFlag != "" {
		cfg, err = config.LoadFromPath(*configFlag)
	} else {
		cfg, err = config.Load()
	}
	if err != nil {
		cfg = config.DefaultConfig()
	}

	if !cfg.Critique.AI.IsConfigured() {
		report := agents.AgentReviewReport{
			Verdict: types.VerdictLGTM,
			Summary: "Skipped AI review: [critique.ai] endpoint is not configured.",
		}
		_ = json.NewEncoder(os.Stdout).Encode(report)
		os.Exit(0)
	}

	sysPrompt := resolveSystemPrompt(cfg, *promptFlag)
	targetFiles := resolveTargetFiles(*filesFlag)

	if len(targetFiles) == 0 {
		report := agents.AgentReviewReport{
			Verdict: types.VerdictLGTM,
			Summary: "No touched files to review.",
		}
		_ = json.NewEncoder(os.Stdout).Encode(report)
		os.Exit(0)
	}

	userPrompt := buildReviewUserPrompt(targetFiles, cfg.Critique.AI.MaxContextLines)

	client, err := ai.NewClient(ai.Config{
		Endpoint:       cfg.Critique.AI.Endpoint,
		APIKey:         cfg.Critique.AI.APIKey,
		APIKeyEnv:      cfg.Critique.AI.APIKeyEnv,
		Model:          cfg.Critique.AI.Model,
		TimeoutSeconds: cfg.Critique.AI.TimeoutSeconds,
		Temperature:    cfg.Critique.AI.Temperature,
		TLS: httpclient.TLSOptions{
			Enabled:    cfg.Critique.AI.TLS.Enabled,
			CAFile:     cfg.Critique.AI.TLS.CAFile,
			ServerName: cfg.Critique.AI.TLS.ServerName,
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error initializing AI client: %v\n", err)
		os.Exit(1)
	}

	timeout := time.Duration(cfg.Critique.AI.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	rawResp, err := client.CompletePrompt(ctx, sysPrompt, userPrompt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error querying AI endpoint: %v\n", err)
		os.Exit(1)
	}

	report, _ := agents.ParseReviewOutput(rawResp)
	if report == nil {
		fmt.Println(rawResp)
		os.Exit(0)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(report)

	if *failOnChangesFlag && report.Verdict.IsBlocking() {
		os.Exit(1)
	}
}

