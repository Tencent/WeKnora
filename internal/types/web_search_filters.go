package types

import (
	"fmt"
	"strings"
	"time"
)

// WebSearchFilters specifies optional country and freshness restrictions.
type WebSearchFilters struct {
	Country   string
	Freshness string
}

// Validate checks filter syntax before any provider request.
func (f WebSearchFilters) Validate() error {
	country := strings.ToUpper(f.Country)
	if country != "" && country != "ALL" &&
		(len(country) != 2 || country[0] < 'A' || country[0] > 'Z' || country[1] < 'A' || country[1] > 'Z') {
		return fmt.Errorf("country must be a two-letter country code or ALL")
	}
	switch f.Freshness {
	case "", "pd", "pw", "pm", "py":
		return nil
	}
	start, end, ok := strings.Cut(f.Freshness, "to")
	from, fromErr := time.Parse(time.DateOnly, start)
	to, toErr := time.Parse(time.DateOnly, end)
	if !ok || fromErr != nil || toErr != nil || from.After(to) {
		return fmt.Errorf("freshness must be pd, pw, pm, py, or YYYY-MM-DDtoYYYY-MM-DD with start <= end")
	}
	return nil
}
