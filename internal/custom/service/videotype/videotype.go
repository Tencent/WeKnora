package videotype

import "strings"

const (
	Interview = "interview"
	Training  = "training"
	Salon     = "salon"
	General   = "general"
)

// Normalize converts legacy upload values to the single business vocabulary.
func Normalize(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case Interview:
		return Interview
	case "tutorial", Training:
		return Training
	case "lecture", Salon:
		return Salon
	case "case_analysis", "case", General, "":
		return General
	default:
		return General
	}
}
