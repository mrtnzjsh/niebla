package grpc

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"niebla/internal/db"
	"niebla/pkg/cas"
	pkgevents "niebla/pkg/events"
	nieblav1 "niebla/pkg/proto/nieblav1"
	"niebla/pkg/tree"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func setupEventsTestServer(t *testing.T) (*Server, *db.DB, *cas.Storage, *pkgevents.RecordingEmitter, func()) {
	t.Helper()
	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("niebla_events_test"),
		postgres.WithUsername("niebla_events_user"),
		postgres.WithPassword("niebla_events_password"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("failed to start postgres container: %v", err)
	}

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to get connection string: %v", err)
	}

	database, err := db.Connect(ctx, db.DefaultConfig(connStr))
	if err != nil {
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to connect to database: %v", err)
	}

	casDir := filepath.Join(t.TempDir(), "cas")
	casStorage, err := cas.NewStorage(casDir)
	if err != nil {
		database.Close()
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to create CAS storage: %v", err)
	}

	server, err := NewServer(database, casStorage, 1000)
	if err != nil {
		database.Close()
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to create server: %v", err)
	}

	emitter := pkgevents.NewRecordingEmitter()
	server.SetEmitter(emitter)

	cleanup := func() {
		database.Close()
		_ = pgContainer.Terminate(context.Background())
	}

	return server, database, casStorage, emitter, cleanup
}

func putTestBlob(t *testing.T, s *cas.Storage, database *db.DB, content []byte) ([]byte, int64) {
	t.Helper()
	h, _, size, path, err := s.Put(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("cas Put: %v", err)
	}
	if err := database.InsertBlob(context.Background(), h, size, path); err != nil {
		t.Fatalf("InsertBlob: %v", err)
	}
	return h, size
}

func putTestTree(t *testing.T, database *db.DB, entries []tree.Entry) []byte {
	t.Helper()
	tree.SortEntries(entries)
	h := tree.ComputeTreeHash(entries)
	if err := database.InsertTreeNodes(context.Background(), h, entries); err != nil {
		t.Fatalf("InsertTreeNodes: %v", err)
	}
	return h
}

func TestServer_CloudEventsEmission(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	server, database, casStorage, emitter, cleanup := setupEventsTestServer(t)
	defer cleanup()

	ctx := context.Background()

	// 1. Shelve a new CL -> assert TypeCLCreated event
	b1, s1 := putTestBlob(t, casStorage, database, []byte("file 1"))
	tree1 := putTestTree(t, database, []tree.Entry{
		{Name: "file1.txt", Mode: 0100644, Size: uint64(s1), BlobSHA256: b1},
	})

	shelveResp, err := server.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "alice",
		Description:  "Feature 1",
		RootTreeHash: tree1,
		ParentCl:     1,
		Reviewers:    []string{"bob"},
	})
	if err != nil {
		t.Fatalf("ShelveCL: %v", err)
	}

	events := emitter.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	createdEvt := events[0]
	if createdEvt.Type != pkgevents.TypeCLCreated {
		t.Errorf("expected type %s, got %s", pkgevents.TypeCLCreated, createdEvt.Type)
	}
	createdPayload, ok := createdEvt.Data.(pkgevents.CLCreatedPayload)
	if !ok {
		t.Fatalf("expected CLCreatedPayload, got %T", createdEvt.Data)
	}
	if createdPayload.Author != "alice" || createdPayload.CLID != "2" || createdPayload.PatchsetNumber != 1 {
		t.Errorf("unexpected created payload: %+v", createdPayload)
	}

	// 2. Submit CL 2 -> assert TypeCLSubmitted event
	emitter.Clear()
	submitResp, err := server.SubmitCL(ctx, &nieblav1.SubmitCLRequest{
		ClId:  shelveResp.ClId,
		Force: true,
	})
	if err != nil {
		t.Fatalf("SubmitCL: %v", err)
	}
	if submitResp.ClId != 2 {
		t.Errorf("expected CL 2, got %d", submitResp.ClId)
	}

	events = emitter.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event after submit, got %d", len(events))
	}
	submittedEvt := events[0]
	if submittedEvt.Type != pkgevents.TypeCLSubmitted {
		t.Errorf("expected type %s, got %s", pkgevents.TypeCLSubmitted, submittedEvt.Type)
	}
	submittedPayload, ok := submittedEvt.Data.(pkgevents.CLSubmittedPayload)
	if !ok {
		t.Fatalf("expected CLSubmittedPayload, got %T", submittedEvt.Data)
	}
	if submittedPayload.CLID != "2" || submittedPayload.TrunkRevision != 2 {
		t.Errorf("unexpected submitted payload: %+v", submittedPayload)
	}

	// 3. Test CascadeStackRebase: Create parent CL 3 and child CL 4
	emitter.Clear()
	b2, s2 := putTestBlob(t, casStorage, database, []byte("shared base"))
	baseTree := putTestTree(t, database, []tree.Entry{
		{Name: "shared.txt", Mode: 0100644, Size: uint64(s2), BlobSHA256: b2},
	})

	// Parent CL 3
	cl3Resp, err := server.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "carol",
		Description:  "Parent CL",
		RootTreeHash: baseTree,
		ParentCl:     2,
	})
	if err != nil {
		t.Fatalf("Shelve CL 3: %v", err)
	}

	// Child CL 4 adds child.txt
	bChild, sChild := putTestBlob(t, casStorage, database, []byte("child file"))
	childTree := putTestTree(t, database, []tree.Entry{
		{Name: "shared.txt", Mode: 0100644, Size: uint64(s2), BlobSHA256: b2},
		{Name: "child.txt", Mode: 0100644, Size: uint64(sChild), BlobSHA256: bChild},
	})
	cl4Resp, err := server.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "dave",
		Description:  "Child CL",
		RootTreeHash: childTree,
		ParentCl:     cl3Resp.ClId,
	})
	if err != nil {
		t.Fatalf("Shelve CL 4: %v", err)
	}

	// Now re-shelve Parent CL 3 with a disjoint update (add parent.txt)
	emitter.Clear()
	bParent, sParent := putTestBlob(t, casStorage, database, []byte("parent file"))
	newParentTree := putTestTree(t, database, []tree.Entry{
		{Name: "shared.txt", Mode: 0100644, Size: uint64(s2), BlobSHA256: b2},
		{Name: "parent.txt", Mode: 0100644, Size: uint64(sParent), BlobSHA256: bParent},
	})

	_, err = server.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		ClId:         cl3Resp.ClId,
		Author:       "carol",
		Description:  "Parent CL v2",
		RootTreeHash: newParentTree,
		ParentCl:     2,
	})
	if err != nil {
		t.Fatalf("Re-shelve CL 3: %v", err)
	}

	// Assert events contain: CLCreated (CL 3 PS 2) and CLRebased (CL 4)
	events = emitter.Events()
	var foundRebased bool
	for _, evt := range events {
		if evt.Type == pkgevents.TypeCLRebased {
			rebasedPayload, ok := evt.Data.(pkgevents.CLRebasedPayload)
			if ok && rebasedPayload.CLID == "4" && rebasedPayload.ParentCLID == "3" {
				foundRebased = true
			}
		}
	}
	if !foundRebased {
		t.Errorf("expected niebla.cl.rebased event for CL 4, got events: %+v", events)
	}

	// 4. Test CascadeStackRebase with conflict: Re-shelve Parent CL 3 modifying shared.txt
	emitter.Clear()
	// Child CL 4 was rebased onto newParentTree and had child.txt and shared.txt.
	// Let's modify shared.txt in Child CL 4 to create a conflicting branch.
	bChildShared, sChildShared := putTestBlob(t, casStorage, database, []byte("child conflicting edit"))
	cl4ConflictingTree := putTestTree(t, database, []tree.Entry{
		{Name: "child.txt", Mode: 0100644, Size: uint64(sChild), BlobSHA256: bChild},
		{Name: "shared.txt", Mode: 0100644, Size: uint64(sChildShared), BlobSHA256: bChildShared},
	})
	_, err = server.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		ClId:         cl4Resp.ClId,
		Author:       "dave",
		Description:  "Child edit shared",
		RootTreeHash: cl4ConflictingTree,
	})
	if err != nil {
		t.Fatalf("Re-shelve CL 4: %v", err)
	}

	emitter.Clear()
	// Now re-shelve Parent CL 3 with a conflicting edit to shared.txt
	bParentShared, sParentShared := putTestBlob(t, casStorage, database, []byte("parent conflicting edit"))
	parentConflictingTree := putTestTree(t, database, []tree.Entry{
		{Name: "parent.txt", Mode: 0100644, Size: uint64(sParent), BlobSHA256: bParent},
		{Name: "shared.txt", Mode: 0100644, Size: uint64(sParentShared), BlobSHA256: bParentShared},
	})
	_, err = server.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		ClId:         cl3Resp.ClId,
		Author:       "carol",
		Description:  "Parent conflict update",
		RootTreeHash: parentConflictingTree,
	})
	if err != nil {
		t.Fatalf("Re-shelve CL 3 conflict: %v", err)
	}

	events = emitter.Events()
	var foundConflict bool
	for _, evt := range events {
		if evt.Type == pkgevents.TypeCLRebaseConflict {
			conflictPayload, ok := evt.Data.(pkgevents.CLRebaseConflictPayload)
			if ok && conflictPayload.CLID == "4" && conflictPayload.ParentCLID == "3" {
				foundConflict = true
			}
		}
	}
	if !foundConflict {
		t.Errorf("expected niebla.cl.rebase_conflict event for CL 4, got events: %+v", events)
	}
}
