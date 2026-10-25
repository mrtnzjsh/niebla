package events

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

const (
	// CloudEvents specification version.
	SpecVersion10 = "1.0"

	// Standard CitC event types.
	TypeCLCreated         = "citc.cl.created"
	TypeReviewersAssigned = "citc.cl.reviewers_assigned"
	TypeCommentsPublished = "citc.cl.comments_published"
	TypeCommentResolved   = "citc.cl.comment_resolved"
	TypeApprovalUpdated   = "citc.cl.approval_updated"
	TypeCLSubmitted       = "citc.cl.submitted"
	TypeCLRebased         = "citc.cl.rebased"
	TypeCLRebaseConflict  = "citc.cl.rebase_conflict"
)

// Event represents a standardized CloudEvents 1.0 envelope.
type Event struct {
	ID              string            `json:"id"`
	SpecVersion     string            `json:"specversion"`
	Type            string            `json:"type"`
	Source          string            `json:"source"`
	Subject         string            `json:"subject"`
	Time            time.Time         `json:"time"`
	DataContentType string            `json:"datacontenttype"`
	Data            any               `json:"data"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

// New creates a new Event initialized with a unique UUID and current UTC timestamp.
func New(eventType, source, subject string, data any) Event {
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
