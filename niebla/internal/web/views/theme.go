package views

import "sync"

var (
	themeMu        sync.RWMutex
	activeThemeCSS = ""
)

// SetActiveThemeCSS sets the CSS custom properties declarations for Cotejo views.
func SetActiveThemeCSS(css string) {
	themeMu.Lock()
	defer themeMu.Unlock()
	activeThemeCSS = css
}

// GetActiveThemeCSS returns the current CSS custom properties declarations.
func GetActiveThemeCSS() string {
	themeMu.RLock()
	defer themeMu.RUnlock()
	return activeThemeCSS
}
