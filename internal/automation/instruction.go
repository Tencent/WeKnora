package automation

import (
	"strings"
	"time"
)

// RunVars are the values an instruction may reference.
type RunVars struct {
	Now            time.Time
	LastRunAt      *time.Time
	LastSuccessAt  *time.Time
	RunID          string
	AutomationName string
	Location       *time.Location
}

func formatTime(t *time.Time, loc *time.Location) string {
	if t == nil || t.IsZero() {
		return ""
	}
	if loc == nil {
		loc = time.UTC
	}
	return t.In(loc).Format(time.RFC3339)
}

// RenderInstruction substitutes {{now}}, {{last_run_at}},
// {{last_success_at}}, {{run_id}} and {{automation_name}}. Unknown
// placeholders are left alone so a literal "{{" in a prompt survives.
func RenderInstruction(instruction string, v RunVars) string {
	now := v.Now
	return strings.NewReplacer(
		"{{now}}", formatTime(&now, v.Location),
		"{{last_run_at}}", formatTime(v.LastRunAt, v.Location),
		"{{last_success_at}}", formatTime(v.LastSuccessAt, v.Location),
		"{{run_id}}", v.RunID,
		"{{automation_name}}", v.AutomationName,
	).Replace(instruction)
}

// maxCarriedResult bounds how much of the previous answer a per-run session
// is handed.
const maxCarriedResult = 4000

// RunQuery is the user turn of one run: the rendered instruction, preceded
// by the previous run's answer when the automation carries it over.
func RunQuery(instruction string, v RunVars, lastResult string) string {
	query := RenderInstruction(instruction, v)
	lastResult = strings.TrimSpace(lastResult)
	if lastResult == "" {
		return query
	}
	if r := []rune(lastResult); len(r) > maxCarriedResult {
		lastResult = string(r[:maxCarriedResult]) + "…"
	}
	return "<previous_run_result>\n" + lastResult + "\n</previous_run_result>\n\n" + query
}

// RunNote tells the agent it is running unattended, so it does not wait on
// a user who is not there.
func RunNote(name string, scheduledAt time.Time, loc *time.Location) string {
	at := scheduledAt
	return "This turn is a scheduled run of the automation \"" + name + "\", fired for " +
		formatTime(&at, loc) + ". No user is watching: do not ask clarifying questions; " +
		"make reasonable assumptions, state them, and finish the task. Actions that need " +
		"approval will wait for the owner."
}
