package policy

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	readOnly  = types.AssistantAccessReadOnly
	confirm   = types.AssistantAccessConfirm
	autoWrite = types.AssistantAccessAutoWrite
)

func TestDecide(t *testing.T) {
	want := map[Risk]map[types.AssistantAccessLevel]Verdict{
		RiskRead:        {readOnly: Allow, confirm: Allow, autoWrite: Allow},
		RiskProbe:       {readOnly: Allow, confirm: Allow, autoWrite: Allow},
		RiskWrite:       {readOnly: Deny, confirm: Ask, autoWrite: Allow},
		RiskDestructive: {readOnly: Deny, confirm: Ask, autoWrite: Ask},
	}
	for risk, byLevel := range want {
		for level, verdict := range byLevel {
			assert.Equal(t, verdict, Decide(Action{Risk: risk}, Rules{Level: level}), "%s at %s", risk, level)
			asked := Ask
			if verdict == Deny {
				asked = Deny
			}
			assert.Equal(t, asked, Decide(Action{Risk: risk, Always: true}, Rules{Level: level}),
				"always %s at %s", risk, level)
		}
	}
}

func TestDecideTreatsAnUnknownRiskAsDestructive(t *testing.T) {
	assert.Equal(t, Ask, Decide(Action{Risk: "unknown"}, Rules{Level: autoWrite}))
	assert.Equal(t, Deny, Decide(Action{Risk: "unknown"}, Rules{Level: readOnly}))
}

func TestRiskAbove(t *testing.T) {
	assert.True(t, RiskWrite.Above(RiskRead))
	assert.True(t, RiskRead.Above(""))
	assert.False(t, RiskRead.Above(RiskWrite))
	assert.False(t, RiskWrite.Above(RiskWrite))
}

func TestOffered(t *testing.T) {
	assert.True(t, Offered(readOnly, RiskRead))
	assert.True(t, Offered(readOnly, RiskProbe))
	assert.False(t, Offered(readOnly, RiskWrite))
	assert.False(t, Offered(readOnly, RiskDestructive))
	assert.True(t, Offered(confirm, RiskDestructive))
}

func TestPregrantableAndCovers(t *testing.T) {
	assert.True(t, Pregrantable(Action{Risk: RiskWrite}))
	assert.True(t, Pregrantable(Action{Risk: RiskDestructive}))
	assert.False(t, Pregrantable(Action{Risk: RiskDestructive, Always: true}))
	assert.False(t, Pregrantable(Action{Risk: RiskRead}))
	assert.False(t, Grantable(Action{Risk: RiskDestructive}), "a run's card never remembers a destructive call")

	assert.True(t, Covers(RiskDestructive, RiskWrite))
	assert.True(t, Covers(RiskWrite, RiskWrite))
	assert.False(t, Covers(RiskWrite, RiskDestructive))
	assert.False(t, Covers("", RiskWrite))
	assert.False(t, Covers(RiskDestructive, "bogus"))
}
