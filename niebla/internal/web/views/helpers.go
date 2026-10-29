package views

import (
	"os"
	"os/user"
	"strings"

	"niebla/internal/db"
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
	if u := strings.TrimSpace(os.Getenv("USER")); u != "" {
		return u
	}
	if cur, err := user.Current(); err == nil && cur.Username != "" {
		return cur.Username
	}
	return "reviewer"
}

// ActiveUserInitial returns the single-character uppercase initial of ActiveUser().
func ActiveUserInitial() string {
	u := ActiveUser()
	if len(u) > 0 {
		return strings.ToUpper(string(u[0]))
	}
	return "R"
}
