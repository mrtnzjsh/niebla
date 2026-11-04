package events

import "strings"

// MatchFilter checks whether an eventType matches any pattern in filters.
// If filters is empty or contains "*", it matches all events.
// Supports suffix wildcards like "niebla.cl.*".
func MatchFilter(filters []string, eventType string) bool {
	if len(filters) == 0 {
		return true
	}
	for _, filter := range filters {
		filter = strings.TrimSpace(filter)
		if filter == "*" || filter == "" {
			return true
		}
		if strings.HasSuffix(filter, "*") {
			prefix := strings.TrimSuffix(filter, "*")
			if strings.HasPrefix(eventType, prefix) {
				return true
			}
		} else if filter == eventType {
			return true
		}
	}
	return false
}
