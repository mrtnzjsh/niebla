package views

import (
	"os"
	"os/user"
	"strings"
)

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
