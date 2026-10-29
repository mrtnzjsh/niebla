package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"niebla/internal/config"
	"niebla/internal/groups"
	"niebla/internal/web/auth"
)

func TestCotejoInboxSegregation_TabsAndCounts(t *testing.T) {
	ResetDemoStoresForTest()

	cfg := config.DefaultConfig()
	clHandler := NewCLHandler(nil, nil, &auth.LocalResolver{DefaultUser: "alice"})
	clHandler.SetDependencies(cfg, groups.NewResolver(cfg.Critique.Groups))

	// 1. Test Default Index (should select my-reviews or outgoing)
	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	clHandler.HandleIndex(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	body := rr.Body.String()

	// Verify Tab headers exist
	if !strings.Contains(body, "My Review Queue") {
		t.Errorf("missing 'My Review Queue' tab in body")
	}
	if !strings.Contains(body, "Out for Review") {
		t.Errorf("missing 'Out for Review' tab in body")
	}
	if !strings.Contains(body, "Agent / Automated") {
		t.Errorf("missing 'Agent / Automated' tab in body")
	}
	if !strings.Contains(body, "Submitted / Trunk") {
		t.Errorf("missing 'Submitted / Trunk' tab in body")
	}

	// 2. Query Agents Tab specifically: ?tab=agents
	reqAgents := httptest.NewRequest("GET", "/?tab=agents", nil)
	rrAgents := httptest.NewRecorder()
	clHandler.HandleIndex(rrAgents, reqAgents)

	if rrAgents.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rrAgents.Code)
	}
	agentsBody := rrAgents.Body.String()

	// Agent CL 3 (agent/linter-bot) must be present
	if !strings.Contains(agentsBody, "agent/linter-bot") {
		t.Errorf("expected 'agent/linter-bot' in agents tab")
	}
	if !strings.Contains(agentsBody, "🤖 Agent") {
		t.Errorf("expected '🤖 Agent' badge in agents tab")
	}

	// 3. Query Outgoing Tab: ?tab=outgoing
	reqOut := httptest.NewRequest("GET", "/?tab=outgoing", nil)
	rrOut := httptest.NewRecorder()
	clHandler.HandleIndex(rrOut, reqOut)

	outBody := rrOut.Body.String()
	// In demo mode, CL 1 is authored by alice@corp
	if !strings.Contains(outBody, "CL 1") || !strings.Contains(outBody, "alice@corp") {
		t.Errorf("expected CL 1 (alice@corp) in outgoing tab")
	}
	// CL 3 (agent) should NOT be in outgoing
	if strings.Contains(outBody, "agent/linter-bot") {
		t.Errorf("agent CL should not be in outgoing tab")
	}

	// 4. Query Submitted Tab: ?tab=submitted
	reqSub := httptest.NewRequest("GET", "/?tab=submitted", nil)
	rrSub := httptest.NewRecorder()
	clHandler.HandleIndex(rrSub, reqSub)

	subBody := rrSub.Body.String()
	// CL 4 is submitted
	if !strings.Contains(subBody, "CL 4") {
		t.Errorf("expected CL 4 in submitted tab")
	}
	if !strings.Contains(subBody, "Submitted") {
		t.Errorf("expected 'Submitted' badge in submitted tab")
	}
}

func TestCotejoInboxSegregation_HTMXPartialSwap(t *testing.T) {
	ResetDemoStoresForTest()

	cfg := config.DefaultConfig()
	clHandler := NewCLHandler(nil, nil, &auth.LocalResolver{DefaultUser: "alice"})
	clHandler.SetDependencies(cfg, groups.NewResolver(cfg.Critique.Groups))

	// Request with HX-Request: true
	req := httptest.NewRequest("GET", "/?tab=agents", nil)
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()
	clHandler.HandleIndex(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	body := rr.Body.String()

	// Should NOT contain full HTML document tags
	if strings.Contains(body, "<!DOCTYPE html>") || strings.Contains(body, "<html") {
		t.Errorf("HTMX partial response should not contain <!DOCTYPE html> or <html>")
	}

	// Should contain the segregated inbox header and table
	if !strings.Contains(body, "Cotejo Review Inbox") || !strings.Contains(body, "agent/linter-bot") {
		t.Errorf("expected inbox content in HTMX response")
	}
}

func TestCotejoInboxSegregation_EmptyState(t *testing.T) {
	ResetDemoStoresForTest()

	cfg := config.DefaultConfig()
	// Bob has no outgoing CLs in demo mode
	clHandler := NewCLHandler(nil, nil, &auth.LocalResolver{DefaultUser: "bob-no-cls"})
	clHandler.SetDependencies(cfg, groups.NewResolver(cfg.Critique.Groups))

	req := httptest.NewRequest("GET", "/?tab=outgoing", nil)
	rr := httptest.NewRecorder()
	clHandler.HandleIndex(rr, req)

	body := rr.Body.String()
	if !strings.Contains(body, "No changelists out for review") {
		t.Errorf("expected custom empty state for outgoing tab, got: %s", body)
	}
}
