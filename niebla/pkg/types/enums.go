package types

import "strings"

// CLStatus represents the lifecycle state of a Niebla changelist.
type CLStatus string

const (
	CLStatusPending   CLStatus = "pending"
	CLStatusOpen      CLStatus = "open"
	CLStatusShelved   CLStatus = "shelved"
	CLStatusSubmitted CLStatus = "submitted"
	CLStatusAbandoned CLStatus = "abandoned"
)

// AllCLStatuses lists all valid changelist status values.
var AllCLStatuses = []CLStatus{
	CLStatusPending,
	CLStatusOpen,
	CLStatusShelved,
	CLStatusSubmitted,
	CLStatusAbandoned,
}

// IsValid reports whether the changelist status is one of the recognized states.
func (s CLStatus) IsValid() bool {
	switch s {
	case CLStatusPending, CLStatusOpen, CLStatusShelved, CLStatusSubmitted, CLStatusAbandoned:
		return true
	default:
		return false
	}
}

// IsSubmitted returns true if the changelist is committed to trunk.
func (s CLStatus) IsSubmitted() bool {
	return s == CLStatusSubmitted
}

// IsOpen returns true if the changelist is still under active development or review.
func (s CLStatus) IsOpen() bool {
	return s == CLStatusOpen || s == CLStatusPending || s == CLStatusShelved
}

func (s CLStatus) String() string {
	return string(s)
}

// Verdict represents a reviewer's evaluation verdict on a changelist.
type Verdict string

const (
	VerdictLGTM             Verdict = "lgtm"
	VerdictApproved         Verdict = "approved"
	VerdictChangesRequested Verdict = "changes_requested"
	VerdictNeedsWork        Verdict = "needs_work"
	VerdictComment          Verdict = "comment"
	VerdictPending          Verdict = "pending"
)

// AllVerdicts lists all valid review verdicts.
var AllVerdicts = []Verdict{
	VerdictLGTM,
	VerdictApproved,
	VerdictChangesRequested,
	VerdictNeedsWork,
	VerdictComment,
	VerdictPending,
}

// ParseVerdict normalizes a raw verdict string into a typed Verdict.
func ParseVerdict(raw string) (Verdict, bool) {
	v := Verdict(strings.ToLower(strings.TrimSpace(raw)))
	return v, v.IsValid()
}

// IsValid reports whether the verdict is a recognized review verdict.
func (v Verdict) IsValid() bool {
	switch v {
	case VerdictLGTM, VerdictApproved, VerdictChangesRequested, VerdictNeedsWork, VerdictComment, VerdictPending:
		return true
	default:
		return false
	}
}

// IsApproval reports whether this verdict satisfies reviewer approval requirements.
func (v Verdict) IsApproval() bool {
	return v == VerdictLGTM || v == VerdictApproved
}

// IsBlocking reports whether this verdict prevents submission (requires rework).
func (v Verdict) IsBlocking() bool {
	return v == VerdictChangesRequested || v == VerdictNeedsWork
}

func (v Verdict) String() string {
	return string(v)
}

// CommentSide specifies whether a code comment attaches to the base revision or modified revision.
type CommentSide string

const (
	CommentSideBase     CommentSide = "base"
	CommentSideModified CommentSide = "modified"
)

// AllCommentSides lists all valid comment diff sides.
var AllCommentSides = []CommentSide{
	CommentSideBase,
	CommentSideModified,
}

// IsValid reports whether the comment side is valid.
func (s CommentSide) IsValid() bool {
	return s == CommentSideBase || s == CommentSideModified
}

func (s CommentSide) String() string {
	return string(s)
}

// CheckRunStatus represents the execution state of an automated CI check run.
type CheckRunStatus string

const (
	CheckRunStatusQueued    CheckRunStatus = "queued"
	CheckRunStatusPending   CheckRunStatus = "pending"
	CheckRunStatusRunning   CheckRunStatus = "running"
	CheckRunStatusPassed    CheckRunStatus = "passed"
	CheckRunStatusFailed    CheckRunStatus = "failed"
	CheckRunStatusCanceled  CheckRunStatus = "canceled"
	CheckRunStatusCancelled CheckRunStatus = "cancelled" // British spelling alias
	CheckRunStatusSkipped   CheckRunStatus = "skipped"
)

// AllCheckRunStatuses lists all valid check run statuses.
var AllCheckRunStatuses = []CheckRunStatus{
	CheckRunStatusQueued,
	CheckRunStatusPending,
	CheckRunStatusRunning,
	CheckRunStatusPassed,
	CheckRunStatusFailed,
	CheckRunStatusCanceled,
	CheckRunStatusCancelled,
	CheckRunStatusSkipped,
}

// ParseCheckRunStatus normalizes a raw status string into a typed CheckRunStatus.
func ParseCheckRunStatus(raw string) (CheckRunStatus, bool) {
	s := CheckRunStatus(strings.ToLower(strings.TrimSpace(raw)))
	return s, s.IsValid()
}

// Canonical returns the canonical form of the check run status (e.g. normalizing "cancelled" to "canceled").
func (s CheckRunStatus) Canonical() CheckRunStatus {
	if s == CheckRunStatusCancelled {
		return CheckRunStatusCanceled
	}
	return s
}

// IsValid reports whether the check run status is recognized.
func (s CheckRunStatus) IsValid() bool {
	switch s {
	case CheckRunStatusQueued, CheckRunStatusPending, CheckRunStatusRunning,
		CheckRunStatusPassed, CheckRunStatusFailed, CheckRunStatusCanceled,
		CheckRunStatusCancelled, CheckRunStatusSkipped:
		return true
	default:
		return false
	}
}

// IsTerminal reports whether the check run has finished execution.
func (s CheckRunStatus) IsTerminal() bool {
	switch s {
	case CheckRunStatusPassed, CheckRunStatusFailed, CheckRunStatusCanceled,
		CheckRunStatusCancelled, CheckRunStatusSkipped:
		return true
	default:
		return false
	}
}

// IsSuccess reports whether the check run passed cleanly.
func (s CheckRunStatus) IsSuccess() bool {
	return s == CheckRunStatusPassed
}

func (s CheckRunStatus) String() string {
	return string(s)
}

// ChangeType represents the nature of a diff modification to a path.
type ChangeType string

const (
	ChangeTypeAdded      ChangeType = "added"
	ChangeTypeModified   ChangeType = "modified"
	ChangeTypeDeleted    ChangeType = "deleted"
	ChangeTypeRenamed    ChangeType = "renamed"
	ChangeTypeUnmodified ChangeType = "unmodified"
)

// AllChangeTypes lists all valid change types.
var AllChangeTypes = []ChangeType{
	ChangeTypeAdded,
	ChangeTypeModified,
	ChangeTypeDeleted,
	ChangeTypeRenamed,
	ChangeTypeUnmodified,
}

// IsValid reports whether the change type is valid.
func (c ChangeType) IsValid() bool {
	switch c {
	case ChangeTypeAdded, ChangeTypeModified, ChangeTypeDeleted, ChangeTypeRenamed, ChangeTypeUnmodified:
		return true
	default:
		return false
	}
}

func (c ChangeType) String() string {
	return string(c)
}

