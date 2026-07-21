package im

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummarizeChannel_OmitsCredentials(t *testing.T) {
	ch := Channel{
		ID:          "ch-1",
		TenantID:    1,
		AgentID:     "agent-1",
		Platform:    "feishu",
		Name:        "support",
		Credentials: types.JSON(`{"app_secret":"top-secret"}`),
	}

	summary := SummarizeChannel(ch)
	body, err := json.Marshal(summary)
	require.NoError(t, err)

	assert.True(t, summary.CredentialsConfigured)
	assert.NotContains(t, string(body), "top-secret")
	assert.NotContains(t, string(body), `"credentials":`)
}

func TestSummarizeChannel_EmptyCredentialsNotConfigured(t *testing.T) {
	summary := SummarizeChannel(Channel{Credentials: types.JSON("{}")})
	assert.False(t, summary.CredentialsConfigured)
}
