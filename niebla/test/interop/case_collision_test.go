package interop_test

import (
	"testing"

	"niebla/pkg/tree"
)

func TestDetectCaseCollisions(t *testing.T) {
	dummyHash := []byte("01234567890123456789012345678901")

	entries := []tree.Entry{
		{Name: "README.md", Mode: 0100644, Size: 100, BlobSHA256: dummyHash},
		{Name: "readme.md", Mode: 0100644, Size: 100, BlobSHA256: dummyHash},
		{Name: "Makefile", Mode: 0100644, Size: 50, BlobSHA256: dummyHash},
		{Name: "makefile", Mode: 0100644, Size: 50, BlobSHA256: dummyHash},
		{Name: "unique_file.go", Mode: 0100644, Size: 200, BlobSHA256: dummyHash},
	}

	collisions := tree.DetectCaseCollisions(entries)
	if len(collisions) != 2 {
		t.Fatalf("expected 2 collisions, got %d", len(collisions))
	}

	collisionMap := make(map[string][]string)
	for _, c := range collisions {
		collisionMap[c.CanonicalFold] = c.Names
	}

	if names, ok := collisionMap["readme.md"]; !ok || len(names) != 2 {
		t.Errorf("expected collision for 'readme.md', got %v", names)
	}
	if names, ok := collisionMap["makefile"]; !ok || len(names) != 2 {
		t.Errorf("expected collision for 'makefile', got %v", names)
	}
}

func TestNoCaseCollisions(t *testing.T) {
	dummyHash := []byte("01234567890123456789012345678901")

	entries := []tree.Entry{
		{Name: "README.md", Mode: 0100644, Size: 100, BlobSHA256: dummyHash},
		{Name: "Makefile", Mode: 0100644, Size: 50, BlobSHA256: dummyHash},
		{Name: "main.go", Mode: 0100644, Size: 200, BlobSHA256: dummyHash},
	}

	collisions := tree.DetectCaseCollisions(entries)
	if len(collisions) != 0 {
		t.Errorf("expected 0 collisions, got %v", collisions)
	}
}
