package views

import (
	"strings"

	"niebla/internal/db"
	"niebla/internal/web/auth"
	"niebla/pkg/types"
)

// InboxCounts holds counts for each inbox partition.
type InboxCounts struct {
	MyReviews int
	Outgoing  int
	Agents    int
	Submitted int
	All       int
}

// CLRowData represents a single changelist row in the inbox list.
type CLRowData struct {
	db.Changelist
	IsAgent    bool
	IsReviewer bool
}

// InboxViewData holds the model required to render the Cotejo inbox view.
type InboxViewData struct {
	ActiveTab       string
	Counts          InboxCounts
	Changelists     []CLRowData
	CurrentUserName string
}

// ActiveUser returns the dynamic active OS username ($USER), or "reviewer" as fallback.
func ActiveUser() string {
	return auth.CurrentOSUser()
}

// ActiveUserInitial returns the single-character uppercase initial of ActiveUser().
func ActiveUserInitial() string {
	currentUser := ActiveUser()
	if len(currentUser) > 0 {
		return strings.ToUpper(string(currentUser[0]))
	}
	return "R"
}

// IsSubmitted reports whether the CL status string represents a submitted changelist.
func IsSubmitted(status string) bool {
	return types.CLStatus(status).IsSubmitted()
}

// IsApprovalVerdict reports whether the verdict satisfies approval requirements.
func IsApprovalVerdict(verdict string) bool {
	return types.Verdict(verdict).IsApproval()
}

// IsBlockingVerdict reports whether the verdict requests changes or blocks submission.
func IsBlockingVerdict(verdict string) bool {
	return types.Verdict(verdict).IsBlocking()
}
