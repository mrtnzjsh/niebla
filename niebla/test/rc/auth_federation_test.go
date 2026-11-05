//go:build rc

package rc_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAuthAndGroupFederation(t *testing.T) {
	cluster := SetupRCCluster(t)
	defer cluster.Cleanup()

	ctx := context.Background()

	// 1. Create a CL authored by 'charlie'
	emptyTreeHash := []byte{0xaa, 0xbb, 0xcc, 0xdd}
	clID, err := cluster.DB.CreateShelvedCL(ctx, "charlie", "feat: group routed changelist", emptyTreeHash, 0)
	if err != nil {
		t.Fatalf("failed to create CL: %v", err)
	}

	// 2. Add group '@backend-core' as reviewer (which resolves to alice and bob in cluster.Config)
	if err := cluster.DB.AddReviewers(ctx, clID, []string{"@backend-core"}); err != nil {
		t.Fatalf("failed to add @backend-core reviewer: %v", err)
	}

	baseURL := cluster.CotejoServer.URL
	client := &http.Client{}

	getWithUser := func(endpoint, user string) (*http.Response, string) {
		req, err := http.NewRequest("GET", baseURL+endpoint, nil)
		if err != nil {
			t.Fatalf("failed to build request: %v", err)
		}
		if user != "" {
			req.Header.Set("X-Remote-User", user)
			req.Header.Set("X-Remote-Email", user+"@example.com")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET %s failed: %v", endpoint, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}

	// 3. Verify Alice (member of @backend-core) sees this in 'my-reviews'
	resp, body := getWithUser("/?tab=my-reviews", "alice")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Alice GET /?tab=my-reviews returned %d", resp.StatusCode)
	}
	if !strings.Contains(body, fmt.Sprintf("CL %d", clID)) && !strings.Contains(body, "group routed changelist") {
		t.Errorf("Alice did not see group-routed CL %d in my-reviews", clID)
	}

	// 4. Verify Bob (member of @backend-core) also sees this in 'my-reviews'
	resp, body = getWithUser("/?tab=my-reviews", "bob")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Bob GET /?tab=my-reviews returned %d", resp.StatusCode)
	}
	if !strings.Contains(body, fmt.Sprintf("CL %d", clID)) && !strings.Contains(body, "group routed changelist") {
		t.Errorf("Bob did not see group-routed CL %d in my-reviews", clID)
	}

	// 5. Verify Dave (NOT in @backend-core) does NOT see this in 'my-reviews'
	resp, body = getWithUser("/?tab=my-reviews", "dave")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Dave GET /?tab=my-reviews returned %d", resp.StatusCode)
	}
	if strings.Contains(body, fmt.Sprintf("CL %d", clID)) && strings.Contains(body, "group routed changelist") {
		t.Errorf("Dave unexpectedly saw CL %d in my-reviews despite not belonging to @backend-core", clID)
	}
}
