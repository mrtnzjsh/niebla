package db

import (
	"strings"
	"testing"
)

func TestParseAndFormatSuggestionMarkdown(t *testing.T) {
	body := "Use constant-time comparison to prevent timing attacks.\n\n```suggestion\nif subtle.ConstantTimeCompare(a, b) == 1 {\n\treturn true\n}\n```"
	code, ok := ParseSuggestionFromMarkdown(body)
	if !ok {
		t.Fatalf("expected suggestion block to be parsed")
	}
	wantCode := "if subtle.ConstantTimeCompare(a, b) == 1 {\n\treturn true\n}"
	if code != wantCode {
		t.Errorf("got code %q, want %q", code, wantCode)
	}

	c := Comment{
		Body:           body,
		HasSuggestion:  true,
		SuggestionCode: code,
	}
	cleanBody := c.BodyWithoutSuggestionFence()
	if cleanBody != "Use constant-time comparison to prevent timing attacks." {
		t.Errorf("unexpected cleanBody: %q", cleanBody)
	}

	formatted := FormatSuggestionMarkdown("Please fix this check", "return nil")
	if !strings.Contains(formatted, "```suggestion\nreturn nil\n```") {
		t.Errorf("unexpected formatted markdown: %q", formatted)
	}
}

