package automation

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

func cronTrigger(expr, tz string) types.AutomationTrigger {
	return types.AutomationTrigger{Type: types.AutomationTriggerCron, Expr: expr, Timezone: tz}
}

func TestParseRejectsBadTriggers(t *testing.T) {
	for name, tr := range map[string]types.AutomationTrigger{
		"empty type":     {},
		"no expr":        cronTrigger("", ""),
		"garbage":        cronTrigger("every day", ""),
		"bad timezone":   cronTrigger("0 9 * * *", "Mars/Olympus"),
		"tz prefix":      cronTrigger("CRON_TZ=Asia/Tokyo 0 9 * * *", ""),
		"once without":   {Type: types.AutomationTriggerOnce},
		"once with expr": {Type: types.AutomationTriggerOnce, Expr: "0 9 * * *", At: "2026-10-19T22:09:00Z"},
		"once nonsense":  {Type: types.AutomationTriggerOnce, At: "tomorrow"},
	} {
		_, err := Parse(tr)
		require.Truef(t, errors.Is(err, ErrInvalidTrigger), "%s: %v", name, err)
	}
}

func TestNextHonorsTimezone(t *testing.T) {
	s, err := Parse(cronTrigger("0 9 * * *", "Asia/Shanghai"))
	require.NoError(t, err)
	from := time.Date(2026, 10, 19, 2, 0, 0, 0, time.UTC) // 10:00 Shanghai
	next := s.Next(from)
	require.NotNil(t, next)
	require.Equal(t, time.Date(2026, 10, 20, 1, 0, 0, 0, time.UTC), *next)
	require.Equal(t, time.UTC, next.Location())
}

func TestSixFieldsAndDescriptors(t *testing.T) {
	from := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	s, err := Parse(cronTrigger("0 30 9 * * MON", ""))
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 10, 19, 9, 30, 0, 0, time.UTC), *s.Next(from))

	s, err = Parse(cronTrigger("@daily", ""))
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC), *s.Next(from))
}

func TestOnceFiresOnce(t *testing.T) {
	at := time.Date(2026, 10, 20, 7, 0, 0, 0, time.UTC)
	for _, given := range []types.AutomationTrigger{
		{Type: types.AutomationTriggerOnce, At: "2026-10-20T08:00:00+01:00"},
		{Type: types.AutomationTriggerOnce, At: "2026-10-20 15:00", Timezone: "Asia/Shanghai"},
		{Type: types.AutomationTriggerOnce, At: "2026-10-20T15:00:00", Timezone: "Asia/Shanghai"},
	} {
		s, err := Parse(given)
		require.NoError(t, err, given.At)
		require.Equal(t, "2026-10-20T07:00:00Z", s.Trigger().At)
		from := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
		require.Equal(t, at, *s.Next(from))
		require.Nil(t, s.Next(at))
		require.Len(t, s.Upcoming(from, 3), 1)
	}
}

// Normalizing a once trigger must not move the planned instant: the At
// written back re-parses to the same moment, fractional seconds included.
func TestOnceNormalizationPreservesInstant(t *testing.T) {
	at := time.Date(2026, 10, 20, 7, 1, 0, 794*int(time.Millisecond), time.UTC)
	s, err := Parse(types.AutomationTrigger{Type: types.AutomationTriggerOnce, At: at.Format(time.RFC3339Nano)})
	require.NoError(t, err)

	normalized, err := time.Parse(time.RFC3339Nano, s.Trigger().At)
	require.NoError(t, err)
	require.Equal(t, at, normalized)

	roundTrip, err := Parse(s.Trigger())
	require.NoError(t, err)
	from := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	require.Equal(t, *s.Next(from), *roundTrip.Next(from))
}

func TestCheckInterval(t *testing.T) {
	from := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	for expr, ok := range map[string]bool{
		"* * * * *":      false,
		"*/2 9 * * *":    false,
		"*/5 * * * *":    true,
		"0 9,10 * * *":   true,
		"0 0 9 * * *":    true,
		"*/30 * * * * *": false,
	} {
		s, err := Parse(cronTrigger(expr, ""))
		require.NoError(t, err, expr)
		err = s.CheckInterval(from, DefaultMinInterval)
		require.Equalf(t, ok, err == nil, "%s: %v", expr, err)
	}
}

func TestRunQuery(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Date(2026, 10, 19, 1, 0, 0, 0, time.UTC)
	v := RunVars{Now: now, RunID: "r1", AutomationName: "日报", Location: loc}
	q := RunQuery("{{automation_name}} at {{now}} since {{last_run_at}} {{unknown}}", v, "")
	require.Equal(t, "日报 at 2026-10-19T09:00:00+08:00 since  {{unknown}}", q)

	q = RunQuery("go", v, strings.Repeat("长", maxCarriedResult+10))
	require.True(t, strings.HasPrefix(q, "<previous_run_result>\n"))
	require.True(t, strings.HasSuffix(q, "…\n</previous_run_result>\n\ngo"))
}
