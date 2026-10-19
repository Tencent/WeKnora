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
		RiskWrite:       {readOnly: Ask, confirm: Ask, autoWrite: Allow},
		RiskDestructive: {readOnly: Ask, confirm: Ask, autoWrite: Ask},
	}
	for risk, byLevel := range want {
		for level, verdict := range byLevel {
			assert.Equal(t, verdict, Decide(Action{Risk: risk}, Rules{Level: level}), "%s at %s", risk, level)
			assert.Equal(t, Ask, Decide(Action{Risk: risk, Always: true}, Rules{Level: level}),
				"always %s at %s", risk, level)
			assert.Equal(t, Ask, Decide(Action{Risk: risk}, Rules{Level: level, ForceAsk: true}),
				"forced %s at %s", risk, level)
		}
	}
}

func TestDecideTreatsAnUnknownRiskAsDestructive(t *testing.T) {
	assert.Equal(t, Ask, Decide(Action{Risk: "unknown"}, Rules{Level: autoWrite}))
}

func TestOffered(t *testing.T) {
	assert.True(t, Offered(readOnly, RiskRead))
	assert.True(t, Offered(readOnly, RiskProbe))
	assert.False(t, Offered(readOnly, RiskWrite))
	assert.False(t, Offered(readOnly, RiskDestructive))
	assert.True(t, Offered(confirm, RiskDestructive))
}
