package groups

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"niebla/internal/config"
)

func TestStaticResolver(t *testing.T) {
	cfg := config.CritiqueGroupConfig{
		Mode: "static",
		Static: map[string][]string{
			"backend":  {"alice", "bob"},
			"frontend": {"carol", "dave"},
		},
	}
	resolver := NewResolver(cfg)

	ctx := context.Background()

	// 1. IsGroup checks
	if !resolver.IsGroup("@backend") {
		t.Errorf("expected @backend to be recognized as group")
	}
	if !resolver.IsGroup("backend") {
		t.Errorf("expected backend to be recognized as group")
	}
	if resolver.IsGroup("@nonexistent") {
		t.Errorf("expected @nonexistent not to be a group")
	}

	// 2. ResolveGroup
	members, err := resolver.ResolveGroup(ctx, "@backend")
	if err != nil {
		t.Fatalf("ResolveGroup failed: %v", err)
	}
	if !reflect.DeepEqual(members, []string{"alice", "bob"}) {
		t.Errorf("expected [alice bob], got %v", members)
	}
}

func TestExpandReviewers(t *testing.T) {
	cfg := config.CritiqueGroupConfig{
		Mode: "static",
		Static: map[string][]string{
			"team-core": {"alice", "bob"},
		},
	}
	resolver := NewResolver(cfg)
	ctx := context.Background()

	// Comma separated with mixed groups, individual users, whitespace, and duplicates
	inputs := []string{
		"eve, @team-core, frank",
		"alice",      // Duplicate (already in @team-core)
		"  carol  ",  // Whitespace
		"@unknown",   // Unresolved group treated as single user "unknown"
	}

	expanded, err := ExpandReviewers(ctx, resolver, inputs)
	if err != nil {
		t.Fatalf("ExpandReviewers failed: %v", err)
	}

	expected := []string{"alice", "bob", "carol", "eve", "frank", "unknown"}
	if !reflect.DeepEqual(expanded, expected) {
		t.Errorf("expected %v, got %v", expected, expanded)
	}
}

func TestAPIResolver(t *testing.T) {
	// Mock HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/groups/infra/members" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string][]string{
				"members": {"sre-lead", "devops-engineer"},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	cfg := config.CritiqueGroupConfig{
		Mode: "api",
		API: config.CritiqueGroupAPI{
			URL:       server.URL + "/groups",
			TimeoutMS: 1000,
		},
	}
	resolver := NewResolver(cfg)
	ctx := context.Background()

	members, err := resolver.ResolveGroup(ctx, "@infra")
	if err != nil {
		t.Fatalf("API ResolveGroup failed: %v", err)
	}
	expected := []string{"sre-lead", "devops-engineer"}
	if !reflect.DeepEqual(members, expected) {
		t.Errorf("expected %v, got %v", expected, members)
	}
}
