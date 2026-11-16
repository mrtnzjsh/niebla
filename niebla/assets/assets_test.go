package assets

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbeddedAssets(t *testing.T) {
	// 1. Verify files exist in StaticFS
	entries, err := StaticFS.ReadDir("static")
	if err != nil {
		t.Fatalf("failed to read embedded static directory: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("embedded static directory is empty")
	}

	foundCSS := false
	foundHTMX := false
	for _, entry := range entries {
		if entry.Name() == "styles.css" {
			foundCSS = true
		}
		if entry.Name() == "htmx.min.js" {
			foundHTMX = true
		}
	}
	if !foundCSS {
		t.Errorf("styles.css not found in embedded StaticFS")
	}
	if !foundHTMX {
		t.Errorf("htmx.min.js not found in embedded StaticFS")
	}
}

func TestAssetHandlerServing(t *testing.T) {
	handler := Handler("")

	tests := []struct {
		name                string
		path                string
		expectedStatus      int
		expectedContentType string
		bodyMustContain     string
	}{
		{
			name:                "Tailwind Stylesheet",
			path:                "/static/styles.css",
			expectedStatus:      http.StatusOK,
			expectedContentType: "text/css",
			bodyMustContain:     "--tw-",
		},
		{
			name:                "HTMX Runtime",
			path:                "/static/htmx.min.js",
			expectedStatus:      http.StatusOK,
			expectedContentType: "application/javascript",
			bodyMustContain:     "htmx",
		},
		{
			name:           "Non-existent File",
			path:           "/static/does-not-exist.js",
			expectedStatus: http.StatusNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.path, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tc.expectedStatus {
				t.Fatalf("GET %s returned status %d, expected %d", tc.path, rec.Code, tc.expectedStatus)
			}

			if tc.expectedStatus == http.StatusOK {
				ct := rec.Header().Get("Content-Type")
				if !strings.Contains(ct, tc.expectedContentType) {
					t.Errorf("GET %s Content-Type = %q; expected to contain %q", tc.path, ct, tc.expectedContentType)
				}

				body := rec.Body.String()
				if len(body) == 0 {
					t.Errorf("GET %s returned empty body", tc.path)
				}
				if tc.bodyMustContain != "" && !strings.Contains(body, tc.bodyMustContain) {
					t.Errorf("GET %s body does not contain expected string %q", tc.path, tc.bodyMustContain)
				}
			}
		})
	}
}

func TestAssetHandlerOverrideDir(t *testing.T) {
	// When non-existent dir is provided, fallback to embedded assets should work
	handler := Handler("/non/existent/override/dir")
	req := httptest.NewRequest("GET", "/static/styles.css", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Fallback to embedded assets failed when override dir is missing: got status %d", rec.Code)
	}
}
