package db

import (
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"niebla/internal/db/store"
)

func toChangelist(c store.Changelist) Changelist {
	var parentCL *int64
	if c.ParentCl.Valid {
		parentCL = &c.ParentCl.Int64
	}
	var trunkRev *int64
	if c.TrunkRevision.Valid {
		trunkRev = &c.TrunkRevision.Int64
	}
	var conflictDetails *string
	if c.RebaseConflictDetails.Valid {
		conflictDetails = &c.RebaseConflictDetails.String
	}
	return Changelist{
		CLID:                  c.ClID,
		Author:                c.Author,
		Description:           c.Description,
		RootTreeHash:          c.RootTreeHash,
		ParentCL:              parentCL,
		Status:                c.Status,
		TrunkRevision:         trunkRev,
		CommittedAt:           c.CommittedAt.Time,
		NeedsRebase:           c.NeedsRebase,
		RebaseConflictDetails: conflictDetails,
		BasePatchsetNumber:    int(c.BasePatchsetNumber),
	}
}

func toBlobRecord(b store.Blob) BlobRecord {
	return BlobRecord{
		SHA256:      b.Sha256,
		ByteSize:    b.ByteSize,
		StoragePath: b.StoragePath,
		CreatedAt:   b.CreatedAt.Time,
	}
}

func uuidToString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func toWorkspace(w store.Workspace) Workspace {
	var baseCL *int64
	if w.BaseCl.Valid {
		baseCL = &w.BaseCl.Int64
	}
	return Workspace{
		ID:        uuidToString(w.ID),
		Name:      w.Name,
		Owner:     w.Owner,
		BaseCL:    baseCL,
		CreatedAt: w.CreatedAt.Time,
	}
}

func toPatchset(p store.ClPatchset) Patchset {
	return Patchset{
		PatchsetID:     int64(p.PatchsetID),
		CLID:           p.ClID,
		PatchsetNumber: int(p.PatchsetNumber),
		RootTreeHash:   p.RootTreeHash,
		CreatedAt:      p.CreatedAt.Time,
		IsAutoRebase:   p.IsAutoRebase,
	}
}

func toComment(c store.ClComment) Comment {
	var parentID *int64
	if c.ParentCommentID.Valid {
		parentID = &c.ParentCommentID.Int64
	}
	return Comment{
		ID:                  c.ID,
		CLID:                c.ClID,
		PatchsetNumber:      int(c.PatchsetNumber),
		FilePath:            c.FilePath,
		LineNumber:          int(c.LineNumber),
		EndLineNumber:       int(c.EndLineNumber),
		Side:                c.Side,
		Author:              c.Author,
		Body:                c.Body,
		Resolved:            c.Resolved,
		ParentCommentID:     parentID,
		CreatedAt:           c.CreatedAt.Time,
		IsDraft:             c.IsDraft,
		HasSuggestion:       c.HasSuggestion,
		SuggestionStartLine: int(c.SuggestionStartLine),
		SuggestionEndLine:   int(c.SuggestionEndLine),
		SuggestionCode:      c.SuggestionCode,
		SuggestionAppliedPS: int(c.SuggestionAppliedPs),
	}
}

func toCheckRun(r store.ClCheckRun) CheckRun {
	var extURL string
	if r.ExternalUrl.Valid {
		extURL = r.ExternalUrl.String
	}
	var startedAt *time.Time
	if r.StartedAt.Valid {
		t := r.StartedAt.Time
		startedAt = &t
	}
	var completedAt *time.Time
	if r.CompletedAt.Valid {
		t := r.CompletedAt.Time
		completedAt = &t
	}
	return CheckRun{
		ID:             r.ID,
		CLID:           r.ClID,
		PatchsetNumber: int(r.PatchsetNumber),
		RunnerName:     r.RunnerName,
		Provider:       r.Provider,
		Status:         r.Status,
		ExternalURL:    extURL,
		LogBlobSHA256:  r.LogBlobSha256,
		StartedAt:      startedAt,
		CompletedAt:    completedAt,
		CreatedAt:      r.CreatedAt.Time,
	}
}
