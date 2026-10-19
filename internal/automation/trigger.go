// Package automation holds the scheduling rules of agent automations: when
// a trigger fires, and what an automation's run is told. It does no I/O.
package automation

import (
	"errors"
	"fmt"
	"strings"
	"time"

	// Timezones must resolve on hosts without a zoneinfo database (Lite).
	_ "time/tzdata"

	"github.com/robfig/cron/v3"

	"github.com/Tencent/WeKnora/internal/types"
)

// DefaultMinInterval is the shortest gap allowed between two runs.
const DefaultMinInterval = 5 * time.Minute

// ErrInvalidTrigger wraps every trigger validation failure.
var ErrInvalidTrigger = errors.New("invalid trigger")

var (
	standardParser = cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
	)
	secondsParser = cron.NewParser(
		cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
	)
)

// Schedule is a validated trigger.
type Schedule struct {
	trigger  types.AutomationTrigger
	location *time.Location
	cron     cron.Schedule
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidTrigger, fmt.Sprintf(format, args...))
}

// Parse validates t. A cron expression has five fields (minute first) or
// six (seconds first), or is a descriptor such as @daily; it is evaluated in
// t.Timezone, which a CRON_TZ= prefix in the expression may not override.
func Parse(t types.AutomationTrigger) (*Schedule, error) {
	t.Expr = strings.TrimSpace(t.Expr)
	t.Timezone = strings.TrimSpace(t.Timezone)
	loc := time.UTC
	if t.Timezone != "" {
		var err error
		if loc, err = time.LoadLocation(t.Timezone); err != nil {
			return nil, invalid("unknown timezone %q", t.Timezone)
		}
	}
	s := &Schedule{trigger: t, location: loc}
	switch t.Type {
	case types.AutomationTriggerCron:
		if t.Expr == "" {
			return nil, invalid("a cron trigger needs an expression")
		}
		if strings.HasPrefix(t.Expr, "CRON_TZ=") || strings.HasPrefix(t.Expr, "TZ=") {
			return nil, invalid("set the timezone field instead of a TZ prefix")
		}
		parser := standardParser
		if !strings.HasPrefix(t.Expr, "@") && len(strings.Fields(t.Expr)) == 6 {
			parser = secondsParser
		}
		sched, err := parser.Parse(t.Expr)
		if err != nil {
			return nil, invalid("%v", err)
		}
		s.cron = sched
	case types.AutomationTriggerOnce:
		if t.At == nil || t.At.IsZero() {
			return nil, invalid("a once trigger needs a time")
		}
		if t.Expr != "" {
			return nil, invalid("a once trigger takes no expression")
		}
	default:
		return nil, invalid("unknown trigger type %q", t.Type)
	}
	return s, nil
}

// Trigger is the normalized trigger.
func (s *Schedule) Trigger() types.AutomationTrigger { return s.trigger }

// Next is the first time the trigger fires strictly after after, or nil when
// it never fires again.
func (s *Schedule) Next(after time.Time) *time.Time {
	if s.cron == nil {
		at := s.trigger.At.UTC()
		if at.After(after) {
			return &at
		}
		return nil
	}
	next := s.cron.Next(after.In(s.location))
	if next.IsZero() {
		return nil
	}
	next = next.UTC()
	return &next
}

// Upcoming is the next n firing times after after.
func (s *Schedule) Upcoming(after time.Time, n int) []time.Time {
	var out []time.Time
	for len(out) < n {
		next := s.Next(after)
		if next == nil {
			break
		}
		out = append(out, *next)
		after = *next
	}
	return out
}

// CheckInterval rejects a cron trigger that can fire twice within min. It
// looks at the gaps over the next days, which catches expressions such as
// "* * * * *" and "*/2 9 * * *" alike.
func (s *Schedule) CheckInterval(from time.Time, minInterval time.Duration) error {
	if s.cron == nil || minInterval <= 0 {
		return nil
	}
	times := s.Upcoming(from, 64)
	for i := 1; i < len(times); i++ {
		if gap := times[i].Sub(times[i-1]); gap < minInterval {
			return invalid("runs would be %s apart; the minimum is %s", gap, minInterval)
		}
	}
	return nil
}
