package events

// ReviewersAssignedPayload contains data for niebla.cl.reviewers_assigned events.
type ReviewersAssignedPayload struct {
	CLID        string   `json:"cl_id"`
	Author      string   `json:"author"`
	Reviewers   []string `json:"reviewers"`
	Description string   `json:"description,omitempty"`
	URL         string   `json:"url,omitempty"`
}

// CommentSummary represents a concise summary of a single published comment.
type CommentSummary struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Side string `json:"side,omitempty"`
	Text string `json:"text"`
}

// CommentsPublishedPayload contains data for niebla.cl.comments_published events.
type CommentsPublishedPayload struct {
	CLID         string           `json:"cl_id"`
	Author       string           `json:"author"`
	CLAuthor     string           `json:"cl_author"`
	Reviewers    []string         `json:"reviewers,omitempty"`
	CommentCount int              `json:"comment_count"`
	Comments     []CommentSummary `json:"comments,omitempty"`
	URL          string           `json:"url,omitempty"`
}

// ApprovalUpdatedPayload contains data for niebla.cl.approval_updated events.
type ApprovalUpdatedPayload struct {
	CLID     string `json:"cl_id"`
	Reviewer string `json:"reviewer"`
	Status   string `json:"status"` // "approved", "rejected", "pending"
	URL      string `json:"url,omitempty"`
}

// CLSubmittedPayload contains data for niebla.cl.submitted events.
type CLSubmittedPayload struct {
	CLID          string `json:"cl_id"`
	Author        string `json:"author"`
	URL           string `json:"url,omitempty"`
	Justification string `json:"justification,omitempty"`
}

// CLRebasedPayload contains data for niebla.cl.rebased events.
type CLRebasedPayload struct {
	CLID            string `json:"cl_id"`
	ParentCLID      string `json:"parent_cl_id"`
	NewRootTreeHash string `json:"new_root_tree_hash"`
	PatchsetNumber  int    `json:"patchset_number"`
}

// CLRebaseConflictPayload contains data for niebla.cl.rebase_conflict events.
type CLRebaseConflictPayload struct {
	CLID            string   `json:"cl_id"`
	ParentCLID      string   `json:"parent_cl_id"`
	ConflictedFiles []string `json:"conflicted_files"`
	Summary         string   `json:"summary,omitempty"`
}
