package hostreview

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVerdictNeedsAllowLowRiskAndAuthorization(t *testing.T) {
	cases := []struct {
		raw     string
		approve bool
	}{
		{`{"risk_level":"low","user_authorization":"medium","outcome":"allow","rationale":"ok"}`, true},
		{`{"risk_level":"medium","user_authorization":"high","outcome":"allow","rationale":"ok"}`, true},
		{`{"risk_level":"low","user_authorization":"low","outcome":"allow","rationale":"x"}`, false},
		{`{"risk_level":"low","outcome":"allow","rationale":"no auth"}`, false},
		{`{"risk_level":"high","user_authorization":"high","outcome":"allow","rationale":"x"}`, false},
		{`{"risk_level":"critical","user_authorization":"high","outcome":"allow","rationale":"x"}`, false},
		{`{"user_authorization":"high","outcome":"allow","rationale":"no risk"}`, false},
		{`{"risk_level":"low","user_authorization":"high","outcome":"deny","rationale":"no"}`, false},
		{`noise {"risk_level":"low","user_authorization":"high","outcome":"allow","rationale":"ok"} tail`, false},
		{"tried {\"risk_level\":\"low\",\"user_authorization\":\"high\",\"outcome\":\"allow\"} but I deny", false},
		{
			"```json\n" + `{"risk_level":"low","user_authorization":"high","outcome":"allow","rationale":"ok"}` +
				"\n```",
			true,
		},
		{
			"```json\n" + `{"risk_level":"low","user_authorization":"high","outcome":"allow","rationale":"ok"}` +
				"\n```\nbut I deny",
			false,
		},
		{`not json`, false},
		{``, false},
		{`{"risk_level":"low","user_authorization":"high","rationale":"no outcome"}`, false},
	}
	for _, tc := range cases {
		v := parseAssessment(tc.raw).verdict()
		require.Equal(t, tc.approve, v.Approved, tc.raw)
		require.NotEmpty(t, v.Reason, tc.raw)
	}
}
