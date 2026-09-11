package types

import "strings"

const MaxBrowserSearchInstructionsLength = 4000

// Shared by the settings API and the browser tool, so the editor shows the
// actual default that a new browser-enabled request will receive.
const DefaultBrowserSearchInstructions = `Default search engine: Bing.
Search URL: https://www.bing.com/search?q={query}`

func (p UserPreferences) EffectiveBrowserSearchInstructions() string {
	if p.BrowserSearchInstructions != nil {
		if value := strings.TrimSpace(*p.BrowserSearchInstructions); value != "" {
			return value
		}
	}
	return DefaultBrowserSearchInstructions
}
