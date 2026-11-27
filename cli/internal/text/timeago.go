package text

import (
	"fmt"
	"time"
)

// FuzzyAgo formats t relative to now as a short relative-time string.
//
// Examples:
//
//	FuzzyAgo(now, now-5m)  → "about 5 minutes ago"
//	FuzzyAgo(now, now-3h)  → "about 3 hours ago"
//	FuzzyAgo(now, now-2d)  → "about 2 days ago"
//	FuzzyAgo(now, now+5m)  → "about 5 minutes from now"
func FuzzyAgo(now, t time.Time) string {
	d := now.Sub(t)
	suffix := "ago"
	if d < 0 {
		d = -d
		suffix = "from now"
	}
	switch {
	case d < time.Minute:
		return "less than a minute " + suffix
	case d < time.Hour:
		return fmt.Sprintf("about %s %s", Pluralize(int(d/time.Minute), "minute"), suffix)
	case d < 24*time.Hour:
		return fmt.Sprintf("about %s %s", Pluralize(int(d/time.Hour), "hour"), suffix)
	case d < 30*24*time.Hour:
		return fmt.Sprintf("about %s %s", Pluralize(int(d/(24*time.Hour)), "day"), suffix)
	case d < 365*24*time.Hour:
		return fmt.Sprintf("about %s %s", Pluralize(int(d/(30*24*time.Hour)), "month"), suffix)
	default:
		return fmt.Sprintf("about %s %s", Pluralize(int(d/(365*24*time.Hour)), "year"), suffix)
	}
}
