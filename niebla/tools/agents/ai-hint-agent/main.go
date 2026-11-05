package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"niebla/internal/config"
	"niebla/pkg/ai"
	"niebla/pkg/httpclient"
)

func main() {
	fileFlag := flag.String("file", "", "Path to the target source file")
	stdinContentFlag := flag.Bool("stdin-content", false, "Read target file content from stdin")
	lineStartFlag := flag.Int("line-start", 1, "1-indexed start line number")
	lineEndFlag := flag.Int("line-end", 1, "1-indexed end line number")
	commentFlag := flag.String("comment", "", "Reviewer comment text")
	promptFlag := flag.String("prompt", "", "Path to system prompt file")
	configFlag := flag.String("config", "", "Optional path to config.toml")
	flag.Parse()

	if *commentFlag == "" {
		fmt.Fprintln(os.Stderr, "error: --comment is required")
		os.Exit(1)
	}

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

	if !*stdinContentFlag && *fileFlag == "" {
		fmt.Fprintln(os.Stderr, "error: either --file or --stdin-content is required")
		os.Exit(1)
	}

	var fileContent string
	if *stdinContentFlag {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading stdin: %v\n", err)
			os.Exit(1)
		}
		fileContent = string(b)
	} else {
		b, err := os.ReadFile(*fileFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading file %s: %v\n", *fileFlag, err)
			os.Exit(1)
		}
		fileContent = string(b)
	}

	promptPath := *promptFlag
	if promptPath == "" {
		promptPath = cfg.Critique.AI.SystemPromptPath
	}
	var sysPrompt string
	if promptPath != "" {
		if b, err := os.ReadFile(promptPath); err == nil {
			sysPrompt = string(b)
		}
	}

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
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	resp, err := ai.GenerateWithAperture(ctx, client, fileContent, ai.HintRequest{
		SystemPrompt:    sysPrompt,
		FilePath:        *fileFlag,
		LineStart:       *lineStartFlag,
		LineEnd:         *lineEndFlag,
		ReviewerComment: *commentFlag,
	}, cfg.Critique.AI.MaxContextLines)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error generating hint: %v\n", err)
		os.Exit(1)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(resp); err != nil {
		fmt.Fprintf(os.Stderr, "error encoding response: %v\n", err)
		os.Exit(1)
	}
}

