package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"niebla/pkg/httpclient"
)

// Config defines the connection parameters for an OpenAI-compatible chat completion endpoint.
type Config struct {
	Endpoint       string
	APIKey         string
	APIKeyEnv      string
	Model          string
	TimeoutSeconds int
	Temperature    float64
	TLS            httpclient.TLSOptions
	HTTPClient     *http.Client
}

// HintRequest encapsulates the code review context sent to the LLM to generate a code suggestion.
type HintRequest struct {
	SystemPrompt       string `json:"system_prompt"`
	FilePath           string `json:"file_path"`
	LineStart          int    `json:"line_start"`
	LineEnd            int    `json:"line_end"`
	ReviewerComment    string `json:"reviewer_comment"`
	TargetCode         string `json:"target_code"`
	SurroundingContext string `json:"surrounding_context,omitempty"`
	ApertureLevel      string `json:"aperture_level"` // "lines", "block", or "file"
}

// HintResponse represents the structured code suggestion returned by the AI model.
type HintResponse struct {
	SufficientContext    bool   `json:"sufficient_context"`
	Explanation          string `json:"explanation"`
	ReplacementStartLine int    `json:"replacement_start_line"`
	ReplacementEndLine   int    `json:"replacement_end_line"`
	ReplacementCode      string `json:"replacement_code"`
	ApertureUsed         string `json:"aperture_used,omitempty"`
}

// chatMessage represents a single OpenAI chat message.
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatCompletionRequest is the standard OpenAI /v1/chat/completions request payload.
type chatCompletionRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
}

// chatCompletionResponse is the standard OpenAI /v1/chat/completions response payload.
type chatCompletionResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// Client interacts with an OpenAI-compatible chat completion API using the shared httpclient package.
type Client struct {
	cfg        Config
	httpClient *http.Client
}

// NewClient constructs a new OpenAI-compatible Client backed by pkg/httpclient.
func NewClient(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, fmt.Errorf("AI endpoint cannot be empty")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		cfg.Model = "gpt-4o"
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	hc := cfg.HTTPClient
	if hc == nil {
		var err error
		hc, err = httpclient.New(httpclient.ClientOptions{
			Timeout: timeout,
			Auth: httpclient.AuthConfig{
				Mode:     "bearer",
				Token:    cfg.APIKey,
				TokenEnv: cfg.APIKeyEnv,
			},
			TLS: cfg.TLS,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to initialize AI HTTP client: %w", err)
		}
	}

	return &Client{
		cfg:        cfg,
		httpClient: hc,
	}, nil
}

// CompletePrompt sends a system prompt and user prompt to the OpenAI-compatible chat completion API
// and returns the raw assistant response content (stripped of optional markdown code fences).
func (c *Client) CompletePrompt(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	sysPrompt := strings.TrimSpace(systemPrompt)
	if sysPrompt == "" {
		sysPrompt = DefaultSystemPrompt
	}

	payload := chatCompletionRequest{
		Model: c.cfg.Model,
		Messages: []chatMessage{
			{Role: "system", Content: sysPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: c.cfg.Temperature,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal chat completion request: %w", err)
	}

	url := strings.TrimRight(c.cfg.Endpoint, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("AI endpoint request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read AI endpoint response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("AI endpoint returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBytes)))
	}

	var completionResp chatCompletionResponse
	if err := json.Unmarshal(respBytes, &completionResp); err != nil {
		return "", fmt.Errorf("failed to decode chat completion response: %w", err)
	}
	if completionResp.Error != nil && completionResp.Error.Message != "" {
		return "", fmt.Errorf("AI endpoint error: %s", completionResp.Error.Message)
	}
	if len(completionResp.Choices) == 0 {
		return "", fmt.Errorf("AI endpoint returned zero choices")
	}

	return cleanJSONResponse(completionResp.Choices[0].Message.Content), nil
}

// GenerateHint queries the OpenAI-compatible endpoint with the system prompt and code context.
func (c *Client) GenerateHint(ctx context.Context, req HintRequest) (*HintResponse, error) {
	userPrompt := buildUserPrompt(req)
	rawContent, err := c.CompletePrompt(ctx, req.SystemPrompt, userPrompt)
	if err != nil {
		return nil, err
	}
	var hint HintResponse
	if err := json.Unmarshal([]byte(rawContent), &hint); err != nil {
		return nil, fmt.Errorf("failed to parse AI hint JSON (%s): %w", rawContent, err)
	}

	if hint.ReplacementStartLine <= 0 {
		hint.ReplacementStartLine = req.LineStart
	}
	if hint.ReplacementEndLine < hint.ReplacementStartLine {
		hint.ReplacementEndLine = req.LineEnd
	}
	hint.ApertureUsed = req.ApertureLevel

	return &hint, nil
}

func buildUserPrompt(req HintRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "FILE_PATH: %s\n", req.FilePath)
	fmt.Fprintf(&b, "LINE_RANGE: %d-%d\n", req.LineStart, req.LineEnd)
	fmt.Fprintf(&b, "APERTURE_LEVEL: %s\n", req.ApertureLevel)
	fmt.Fprintf(&b, "REVIEWER_COMMENT: %s\n\n", req.ReviewerComment)
	fmt.Fprintf(&b, "TARGET_CODE (Lines %d-%d):\n%s\n", req.LineStart, req.LineEnd, req.TargetCode)
	if strings.TrimSpace(req.SurroundingContext) != "" {
		fmt.Fprintf(&b, "\nSURROUNDING_CONTEXT:\n%s\n", req.SurroundingContext)
	}
	return b.String()
}

// cleanJSONResponse strips optional markdown ```json ... ``` fences around a JSON object.
func cleanJSONResponse(s string) string {
	trimmed := strings.TrimSpace(s)
	if strings.HasPrefix(trimmed, "```") {
		if idx := strings.Index(trimmed, "\n"); idx != -1 {
			trimmed = trimmed[idx+1:]
		}
		if strings.HasSuffix(trimmed, "```") {
			trimmed = strings.TrimSuffix(trimmed, "```")
		}
	}
	return strings.TrimSpace(trimmed)
}

// DefaultSystemPrompt is the fallback system prompt if a custom prompt file is not provided.
const DefaultSystemPrompt = `You are an expert code review assistant integrated into the Niebla version control system.
Given a file path, line range, reviewer comment, and code snippet, return a JSON object with:
{
  "sufficient_context": true,
  "explanation": "Brief 1-sentence explanation of the proposed change",
  "replacement_start_line": <int>,
  "replacement_end_line": <int>,
  "replacement_code": "<exact replacement code without markdown backticks>"
}
If the provided code snippet lacks required surrounding declarations to safely write the fix, set "sufficient_context": false.`

