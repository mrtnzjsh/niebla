package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClient_GenerateHint(t *testing.T) {
	t.Setenv("TEST_OPENAI_KEY", "sk-test-999")

	var gotAuth string
	var gotReq chatCompletionRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("failed to decode request body: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		respJSON := `{
			"choices": [{
				"message": {
					"role": "assistant",
					"content": "{\"sufficient_context\": true, \"explanation\": \"Use constant-time comparison\", \"replacement_start_line\": 12, \"replacement_end_line\": 14, \"replacement_code\": \"if subtle.ConstantTimeCompare(a, b) == 1 {\"}"
				}
			}]
		}`
		_, _ = w.Write([]byte(respJSON))
	}))
	defer srv.Close()

	client, err := NewClient(Config{
		Endpoint:       srv.URL + "/v1",
		APIKeyEnv:      "TEST_OPENAI_KEY",
		Model:          "gpt-4o",
		TimeoutSeconds: 10,
		Temperature:    0.2,
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	hint, err := client.GenerateHint(context.Background(), HintRequest{
		FilePath:        "pkg/auth/token.go",
		LineStart:       12,
		LineEnd:         14,
		ReviewerComment: "Avoid timing side-channel here",
		TargetCode:      "if string(a) == string(b) {",
		ApertureLevel:   ApertureLines,
	})
	if err != nil {
		t.Fatalf("GenerateHint failed: %v", err)
	}

	if gotAuth != "Bearer sk-test-999" {
		t.Errorf("expected Bearer sk-test-999, got %q", gotAuth)
	}
	if gotReq.Model != "gpt-4o" {
		t.Errorf("expected model gpt-4o, got %q", gotReq.Model)
	}
	if !hint.SufficientContext {
		t.Errorf("expected SufficientContext=true")
	}
	if hint.ReplacementCode != "if subtle.ConstantTimeCompare(a, b) == 1 {" {
		t.Errorf("unexpected ReplacementCode: %q", hint.ReplacementCode)
	}
}

func TestGenerateWithAperture_EscalatesWhenInsufficient(t *testing.T) {
	var seenApertures []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatCompletionRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		userMsg := req.Messages[1].Content

		var aperture string
		for _, line := range strings.Split(userMsg, "\n") {
			if strings.HasPrefix(line, "APERTURE_LEVEL: ") {
				aperture = strings.TrimPrefix(line, "APERTURE_LEVEL: ")
				break
			}
		}
		seenApertures = append(seenApertures, aperture)

		w.Header().Set("Content-Type", "application/json")
		if aperture == ApertureLines {
			// Signal insufficient context on Tier 1 ("lines")
			_, _ = w.Write([]byte(`{
				"choices": [{
					"message": {
						"role": "assistant",
						"content": "{\"sufficient_context\": false, \"explanation\": \"Need enclosing function signature\", \"replacement_start_line\": 5, \"replacement_end_line\": 5, \"replacement_code\": \"\"}"
					}
				}]
			}`))
			return
		}

		// Succeed on Tier 2 ("block")
		inner, _ := json.Marshal(HintResponse{
			SufficientContext:    true,
			Explanation:          "Return wrapped error",
			ReplacementStartLine: 5,
			ReplacementEndLine:   5,
			ReplacementCode:      "\treturn fmt.Errorf(\"invalid token: %w\", err)",
		})
		outer, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]string{
						"role":    "assistant",
						"content": string(inner),
					},
				},
			},
		})
		_, _ = w.Write(outer)
	}))
	defer srv.Close()

	client, err := NewClient(Config{
		Endpoint: srv.URL + "/v1",
		Model:    "gpt-4o",
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	sampleFile := strings.Join([]string{
		"package auth",
		"",
		"func Verify(tok string) error {",
		"\terr := parse(tok)",
		"\treturn err",
		"}",
	}, "\n")

	resp, err := GenerateWithAperture(context.Background(), client, sampleFile, HintRequest{
		FilePath:        "pkg/auth/token.go",
		LineStart:       5,
		LineEnd:         5,
		ReviewerComment: "Wrap the error with context before returning",
	}, 1200)
	if err != nil {
		t.Fatalf("GenerateWithAperture failed: %v", err)
	}

	if len(seenApertures) != 2 || seenApertures[0] != ApertureLines || seenApertures[1] != ApertureBlock {
		t.Errorf("expected aperture escalation [lines, block], got %v", seenApertures)
	}
	if resp.ApertureUsed != ApertureBlock {
		t.Errorf("expected ApertureUsed='block', got %q", resp.ApertureUsed)
	}
	if !strings.Contains(resp.ReplacementCode, "fmt.Errorf") {
		t.Errorf("unexpected replacement code: %q", resp.ReplacementCode)
	}
}
