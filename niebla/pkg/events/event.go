package events

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// EventType represents the category of a CloudEvents message.
type EventType string

const (
	// CloudEvents specification version.
	SpecVersion10 = "1.0"

	// Standard Niebla event types.
	TypeCLCreated         EventType = "niebla.cl.created"
	TypeReviewersAssigned EventType = "niebla.cl.reviewers_assigned"
	TypeCommentsPublished EventType = "niebla.cl.comments_published"
	TypeCommentResolved   EventType = "niebla.cl.comment_resolved"
	TypeApprovalUpdated   EventType = "niebla.cl.approval_updated"
	TypeCLSubmitted       EventType = "niebla.cl.submitted"
	TypeCLRebased         EventType = "niebla.cl.rebased"
	TypeCLRebaseConflict  EventType = "niebla.cl.rebase_conflict"
)

// AllEventTypes lists all standard CloudEvents types.
var AllEventTypes = []EventType{
	TypeCLCreated,
	TypeReviewersAssigned,
	TypeCommentsPublished,
	TypeCommentResolved,
	TypeApprovalUpdated,
	TypeCLSubmitted,
	TypeCLRebased,
	TypeCLRebaseConflict,
}

// IsValid reports whether the event type is recognized.
func (t EventType) IsValid() bool {
	switch t {
	case TypeCLCreated, TypeReviewersAssigned, TypeCommentsPublished,
		TypeCommentResolved, TypeApprovalUpdated, TypeCLSubmitted,
		TypeCLRebased, TypeCLRebaseConflict:
		return true
	default:
		return false
	}
}

func (t EventType) String() string {
	return string(t)
}

// Event represents a standardized CloudEvents 1.0 envelope.
type Event struct {
	ID              string            `json:"id"`
	SpecVersion     string            `json:"specversion"`
	Type            EventType         `json:"type"`
	Source          string            `json:"source"`
	Subject         string            `json:"subject"`
	Time            time.Time         `json:"time"`
	DataContentType string            `json:"datacontenttype"`
	Data            any               `json:"data"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

// New creates a new Event initialized with a unique UUID and current UTC timestamp.
func New(eventType EventType, source, subject string, data any) Event {
	return Event{
		ID:              generateUUID(),
		SpecVersion:     SpecVersion10,
		Type:            eventType,
		Source:          source,
		Subject:         subject,
		Time:            time.Now().UTC(),
		DataContentType: "application/json",
		Data:            data,
	}
}

func generateUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // Version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return hex.EncodeToString(b[:])
}
