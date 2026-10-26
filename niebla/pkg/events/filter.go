package events

import "strings"

// MatchFilter checks whether an eventType matches any pattern in filters.
// If filters is empty or contains "*", it matches all events.
// Supports suffix wildcards like "niebla.cl.*".
func MatchFilter(filters []string, eventType string) bool {
	if len(filters) == 0 {
		return true
	}
	for _, f := range filters {
		f = strings.TrimSpace(f)
		if f == "*" || f == "" {
			return true
		}
		if strings.HasSuffix(f, "*") {
			prefix := strings.TrimSuffix(f, "*")
			if strings.HasPrefix(eventType, prefix) {
				return true
			}
		} else if f == eventType {
			return true
		}
	}
	return false
}
